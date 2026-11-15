// AIClean · Go + WebView2 单文件桌面壳
// P1 里程碑：窗口、导航、设计系统、JS↔Go 桥、真实磁盘数据。
package main

import (
	"crypto/md5"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"aiclean/internal/junction"
	"aiclean/internal/migrate"
	"aiclean/internal/model"
	"aiclean/internal/ops"
	"aiclean/internal/scanner"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"runtime/debug"
)

//go:embed web/index.html
var indexHTML string

//go:embed rules.json
var rulesJSON []byte

//go:embed assets/icon.ico
var iconICO []byte

// Version 构建时注入：go build -ldflags "-X main.Version=1.0.0"（见 build.ps1）
var Version = "dev"

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	user32              = syscall.NewLazyDLL("user32.dll")
	getDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
	getDriveTypeW       = kernel32.NewProc("GetDriveTypeW")
	getVolumeInfoW      = kernel32.NewProc("GetVolumeInformationW")
	getWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	setWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	setWindowPos        = user32.NewProc("SetWindowPos")
	showWindow          = user32.NewProc("ShowWindow")
	postMessageW        = user32.NewProc("PostMessageW")
	sendMessageW        = user32.NewProc("SendMessageW")
	createIconRes       = user32.NewProc("CreateIconFromResourceEx")
	isZoomed            = user32.NewProc("IsZoomed")
	getCursorPos        = user32.NewProc("GetCursorPos")
	getAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	createMutexW        = kernel32.NewProc("CreateMutexW")
	localFree           = kernel32.NewProc("LocalFree")
	isWindowW           = user32.NewProc("IsWindow")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	getWindowRect       = user32.NewProc("GetWindowRect")
	screenToClient      = user32.NewProc("ScreenToClient")
	sysParamsInfo       = user32.NewProc("SystemParametersInfoW")
	setDpiAware         = user32.NewProc("SetProcessDpiAwarenessContext")
	dwmSetAttr          = dwmapi.NewProc("DwmSetWindowAttribute")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	shellExecuteW       = shell32.NewProc("ShellExecuteW")
	shEmptyBin          = shell32.NewProc("SHEmptyRecycleBinW")
	dwmapi              = syscall.NewLazyDLL("dwmapi.dll")
	dwmExtend           = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	advapi              = syscall.NewLazyDLL("advapi32.dll")
	convertSDDL         = advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procOpenProcTok     = advapi.NewProc("OpenProcessToken")
	procGetTokInfo      = advapi.NewProc("GetTokenInformation")
	procCurrentProcess  = kernel32.NewProc("GetCurrentProcess")
	procCloseHandle     = kernel32.NewProc("CloseHandle")
	createEventW        = kernel32.NewProc("CreateEventW")
	openEventW          = kernel32.NewProc("OpenEventW")
	setEventW           = kernel32.NewProc("SetEvent")
	waitForSingleObject = kernel32.NewProc("WaitForSingleObject")
	ntdll               = syscall.NewLazyDLL("ntdll.dll")
	rtlGetVersion       = ntdll.NewProc("RtlGetVersion")
	messageBoxW         = user32.NewProc("MessageBoxW")
)

type wmargins struct{ Left, Right, Top, Bottom int32 }

const driveFixed = 3

const tokenQuery = 0x0008 // TOKEN_QUERY

type DriveInfo struct {
	Letter string `json:"letter"`
	Free   uint64 `json:"free"`
	Total  uint64 `json:"total"`
	FS     string `json:"fs"`
	Fixed  bool   `json:"fixed"`
}

func utf16(s string) (*uint16, error) { return syscall.UTF16PtrFromString(s) }

func driveFS(root string) string {
	rp, _ := utf16(root)
	var name [64]uint16
	var serial, maxLen, flags uint32
	var fs [16]uint16
	r, _, _ := getVolumeInfoW.Call(uintptr(unsafe.Pointer(rp)),
		uintptr(unsafe.Pointer(&name[0])), 64,
		uintptr(unsafe.Pointer(&serial)), uintptr(unsafe.Pointer(&maxLen)),
		uintptr(unsafe.Pointer(&flags)),
		uintptr(unsafe.Pointer(&fs[0])), 16)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(fs[:])
}

func drives() []DriveInfo {
	var out []DriveInfo
	for l := 'C'; l <= 'Z'; l++ {
		root := string(l) + `:\`
		rp, err := utf16(root)
		if err != nil {
			continue
		}
		dt, _, _ := getDriveTypeW.Call(uintptr(unsafe.Pointer(rp)))
		var free, total, avail uint64
		r, _, _ := getDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(rp)),
			uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)),
			uintptr(unsafe.Pointer(&avail)))
		if r == 0 {
			continue
		}
		out = append(out, DriveInfo{
			Letter: string(l), Free: free, Total: total,
			FS: driveFS(root), Fixed: dt == driveFixed,
		})
	}
	return out
}

// jsCall: JS 侧 `await getState()` → Go 返回 map（go-webview2 自动序列化）
func getState(_ ...any) (any, error) {
	return map[string]any{
		"version":       Version,
		"drives":        drives(),
		"elevated":      isAdmin(),
		"pinnedMissing": storePinnedMissing, // 指向盘未连接时非空，前端顶部提示
	}, nil
}

// isAdmin 进程是否以管理员身份运行：OpenProcessToken + TokenElevation 正规判定
// （原先探测 \\.\PHYSICALDRIVE0 在部分受限环境误判）
func isAdmin() bool {
	var tok syscall.Handle
	h, _, _ := procCurrentProcess.Call()
	r, _, _ := procOpenProcTok.Call(h, tokenQuery, uintptr(unsafe.Pointer(&tok)))
	if r == 0 {
		return false
	}
	defer procCloseHandle.Call(uintptr(tok))
	var elev uint32
	var retLen uint32
	r, _, _ = procGetTokInfo.Call(uintptr(tok), 20 /*TokenElevation*/, uintptr(unsafe.Pointer(&elev)), 4, uintptr(unsafe.Pointer(&retLen)))
	return r != 0 && elev != 0
}

// relaunchElevated: JS `await relaunchElevated()` → 以管理员身份重启（UAC 弹窗）
// 新实例带 --elevated 参数（单实例锁不拦）；本窗口随即退出
func relaunchElevated(_ ...any) (any, error) {
	exe, err := os.Executable()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	r, _, _ := shellExecuteW.Call(0,
		uintptr(unsafe.Pointer(must16("runas"))),
		uintptr(unsafe.Pointer(must16(exe))),
		uintptr(unsafe.Pointer(must16("--elevated"))),
		0, 1)
	if r <= 32 { // ShellExecute 返回值 ≤32 表示失败（含用户取消 UAC）
		return map[string]any{"ok": false, "error": "没有提权（可能取消了 UAC 确认）"}, nil
	}
	postMessageW.Call(appHwnd, wmClose, 0, 0)
	return map[string]any{"ok": true}, nil
}

// loadRules 定位规则：只读 exe 同目录的 rules.json（可更新），否则用内嵌副本。
// 不再查上级目录——exe 放桌面时上级就是用户主目录，可能误读别的同名文件。
func loadRules() ([]model.Rule, string, error) {
	exe, _ := os.Executable()
	for _, c := range []string{
		filepath.Join(filepath.Dir(exe), "rules.json"),
	} {
		if _, err := os.Stat(c); err == nil {
			r, err := scanner.LoadRules(c)
			if err != nil {
				// 外部规则损坏（手滑改坏/BOM 等）：回退内嵌，让应用还能用
				if s, e2 := mustStore(); e2 == nil {
					s.Log("scan", c, 0, false, "外部 rules.json 解析失败，已回退内嵌规则: "+err.Error())
				}
				break
			}
			return r, c, nil
		}
	}
	r, err := scanner.LoadRulesData(rulesJSON)
	return r, "", err // rulesPath 空串 → 前端显示"内嵌"
}

// ---------- 最近一次扫描结果（破坏性操作的路径校验依据） ----------

var (
	scanMu   sync.Mutex
	lastScan = map[string]model.ScanItem{}
	lastBig  = map[string]model.ScanItem{}
	scanning bool
)

// ignoreSet 从 prefs 读"不再提示"集合（规则 id 集合 + 路径集合）
func ignoreSet() (map[string]bool, map[string]bool) {
	rs, ps := map[string]bool{}, map[string]bool{}
	s, err := mustStore()
	if err != nil {
		return rs, ps
	}
	p := s.Prefs()
	for _, v := range anySlice(p["ignoreRules"]) {
		if str, ok := v.(string); ok {
			rs[str] = true
		}
	}
	for _, v := range anySlice(p["ignorePaths"]) {
		if str, ok := v.(string); ok {
			ps[normPath(str)] = true
		}
	}
	return rs, ps
}

func anySlice(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

// ignoreItem: JS `await ignoreItem({ruleId,path})` → 该规则/路径以后不再出现在扫描里
func ignoreItem(a ...any) (any, error) {
	m, _ := a[0].(map[string]any)
	rid, _ := m["ruleId"].(string)
	path, _ := m["path"].(string)
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	p := s.Prefs()
	rules := append(anySlice(p["ignoreRules"]), rid)
	paths := append(anySlice(p["ignorePaths"]), path)
	s.SetPrefs(map[string]any{"ignoreRules": rules, "ignorePaths": paths})
	return map[string]any{"ok": true}, nil
}

// unignoreAll: JS `await unignoreAll()` → 清空忽略名单
func unignoreAll(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	s.SetPrefs(map[string]any{"ignoreRules": []any{}, "ignorePaths": []any{}})
	return map[string]any{"ok": true}, nil
}

func normPath(p string) string { return strings.ToLower(filepath.Clean(p)) }

// guardPath 校验路径属于最近扫描结果或大文件猎手结果，且当前可操作（未锁定/未迁移）
func guardPath(path string) (model.ScanItem, error) {
	scanMu.Lock()
	it, ok := lastScan[normPath(path)]
	if !ok {
		it, ok = lastBig[normPath(path)]
	}
	scanMu.Unlock()
	if !ok {
		return it, fmt.Errorf("该路径不在最近一次扫描结果中，拒绝操作")
	}
	if it.ReportOnly {
		return it, fmt.Errorf("汇总目录仅供统计占用，不可操作")
	}
	return guardMigOK(it)
}

// guardPathMig 迁移专用校验：reportOnly 汇总目录允许整体搬家（junction 可逆），
// 仍要求在最近扫描结果中、未锁定、未迁移。
func guardPathMig(path string) (model.ScanItem, error) {
	scanMu.Lock()
	it, ok := lastScan[normPath(path)]
	scanMu.Unlock()
	if !ok {
		return it, fmt.Errorf("该路径不在最近一次扫描结果中，拒绝操作")
	}
	return guardMigOK(it)
}

func guardMigOK(it model.ScanItem) (model.ScanItem, error) {
	switch it.State {
	case "locked":
		return it, fmt.Errorf("程序正在运行，已锁定（%s）", strings.Join(it.LockedBy, "、"))
	case "migrated":
		return it, fmt.Errorf("该项已迁移，无需操作")
	}
	if it.NoMigrate {
		return it, fmt.Errorf("该项不适合迁移（云同步根/系统目录/程序本体），仅统计占用")
	}
	// 祖先已是 junction：说明父目录被迁移过，子项再迁会在链接里套链接
	for p := filepath.Dir(it.Path); ; {
		if fi, err := os.Lstat(p); err == nil {
			if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&0x400 != 0 {
				return it, fmt.Errorf("父目录 %s 已是迁移链接，请先从父级迁回", p)
			}
		}
		if parent := filepath.Dir(p); parent == p {
			break
		} else {
			p = parent
		}
	}
	// 下级目录已被本工具迁移过：父目录整体迁移/删除会丢弃子目录的 junction，
	// 已迁出的数据在新盘失去入口（孤儿数据）
	if s, err := mustStore(); err == nil {
		low := normPath(it.Path) + `\`
		for _, m := range s.MigListAll() {
			if m["done"] == true {
				continue
			}
			src, _ := m["src"].(string)
			if src != "" && strings.HasPrefix(normPath(src), low) {
				return it, fmt.Errorf("子目录 %s 已被迁移，请先将其迁回再操作", src)
			}
		}
	}
	return it, nil
}

// checkStillClosed 执行前复查 requiresClosed：扫描时刻没运行的程序，
// 在用户按下"清理/迁移"这几秒内可能已经启动了——扫描结果会过期，执行时必须重查。
func checkStillClosed(it model.ScanItem) error {
	if hit := scanner.ProcsRunning(it.RequiresClosed); len(hit) > 0 {
		return fmt.Errorf("检测到程序正在运行（%s），请先关闭再操作", strings.Join(hit, "、"))
	}
	return nil
}

// ---------- 大文件猎手：扫用户目录下 ≥300MB 的散文件（规则之外的大头） ----------

// findBigFiles: JS `await findBigFiles()` → {items:[ScanItem...]}
// 遍历 %USERPROFILE% + C:\Users\Public + C:\ 根目录散文件；
// 跳过 reparse（含 OneDrive 云占位）、页面/休眠文件、本程序数据目录。
// 同尺寸文件做快速指纹比对，标记 dup=true（"可能重复"）。
func findBigFiles(_ ...any) (any, error) {
	stop := startHeartbeat()
	defer close(stop)
	const minSize = int64(300) << 20
	var out []model.ScanItem
	storeRoot := ""
	if s, e := mustStore(); e == nil {
		storeRoot = strings.ToLower(s.Root)
	}
	skipNames := map[string]bool{
		"pagefile.sys": true, "hiberfil.sys": true, "swapfile.sys": true,
		"dumpstack.log.tmp": true, "bootstat.dat": true,
	}
	push := func(p string, fi os.FileInfo) {
		out = append(out, model.ScanItem{
			RuleID: "bigfile", App: "大文件", Dir: shortDirL(p), Path: p,
			Size: fi.Size(), Files: 1, LastMod: fi.ModTime().Format("2006-01-02"),
			Risk: "review", RiskTxt: "超大文件", Action: "clean", State: "active",
			StatusTxt: "超大文件", Big: true,
		})
	}
	isReparse := func(d os.DirEntry) bool {
		fi, err := d.Info()
		if err != nil {
			return true
		}
		if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
			return sys.FileAttributes&0x400 != 0
		}
		return false
	}
	walk := func(root string) {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			low := strings.ToLower(p)
			if d.IsDir() {
				if storeRoot != "" && strings.HasPrefix(low, storeRoot) {
					return filepath.SkipDir
				}
				if p != root && isReparse(d) {
					return filepath.SkipDir // junction/OneDrive 占位不深入
				}
				return nil
			}
			if skipNames[filepath.Base(low)] || isReparse(d) {
				return nil
			}
			fi, e := d.Info()
			if e != nil || fi.Size() < minSize {
				return nil
			}
			push(p, fi)
			return nil
		})
	}
	if u := os.Getenv("USERPROFILE"); u != "" {
		walk(u)
	}
	walk(`C:\Users\Public`)
	// C:\ 根目录散文件（不下钻系统目录）
	if ents, e := os.ReadDir(`C:\`); e == nil {
		for _, en := range ents {
			if en.IsDir() || skipNames[strings.ToLower(en.Name())] || isReparse(en) {
				continue
			}
			fi, e := en.Info()
			if e != nil || fi.Size() < minSize {
				continue
			}
			push(`C:\`+en.Name(), fi)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	if len(out) > 150 {
		out = out[:150]
	}
	markBigDups(out)
	scanMu.Lock()
	lastBig = map[string]model.ScanItem{}
	for _, it := range out {
		lastBig[normPath(it.Path)] = it
	}
	scanMu.Unlock()
	return map[string]any{"items": out, "count": len(out)}, nil
}

// shortDirL 大文件显示的短路径（去掉盘符前缀）
func shortDirL(p string) string {
	s := filepath.Dir(p)
	return strings.TrimPrefix(s, `C:\`)
}

// markBigDups 同尺寸文件按"头 4MB + 尾 4MB"快速指纹标记可能重复
func markBigDups(items []model.ScanItem) {
	bySize := map[int64][]int{}
	for i, it := range items {
		bySize[it.Size] = append(bySize[it.Size], i)
	}
	fp := func(p string, size int64) (string, bool) {
		f, err := os.Open(p)
		if err != nil {
			return "", false
		}
		defer f.Close()
		h := md5.New()
		buf := make([]byte, 4<<20)
		n, _ := f.Read(buf)
		h.Write(buf[:n])
		if size > int64(4<<20) {
			if _, err := f.Seek(size-int64(4<<20), 0); err == nil {
				n, _ = f.Read(buf)
				h.Write(buf[:n])
			}
		}
		return hex.EncodeToString(h.Sum(nil)), true
	}
	for _, idxs := range bySize {
		if len(idxs) < 2 {
			continue
		}
		seen := map[string]bool{}
		for _, i := range idxs {
			if h, ok := fp(items[i].Path, items[i].Size); ok {
				if seen[h] {
					items[i].Dup = true
				} else {
					seen[h] = true
				}
			}
		}
	}
}

// applyIgnoreFilters 应用"不再提示"名单：rules 非空按 ruleId 过滤规则，
// items 非空按归一化路径过滤结果。返回各自过滤后的切片。
func applyIgnoreFilters(rules []model.Rule, items []model.ScanItem) ([]model.Rule, []model.ScanItem) {
	ignoredR, ignoredP := ignoreSet()
	if rules != nil && len(ignoredR) > 0 {
		kept := rules[:0]
		for _, r := range rules {
			if !ignoredR[r.ID] {
				kept = append(kept, r)
			}
		}
		rules = kept
	}
	if items != nil && len(ignoredP) > 0 {
		kept := items[:0]
		for _, it := range items {
			if !ignoredP[normPath(it.Path)] {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	return rules, items
}

// scanItems: JS `await scanItems()` → 真实规则扫描结果
func scanItems(_ ...any) (any, error) {
	scanMu.Lock()
	if scanning {
		scanMu.Unlock()
		return map[string]any{"items": []any{}, "error": "扫描进行中"}, nil
	}
	scanning = true
	scanMu.Unlock()
	defer func() { scanMu.Lock(); scanning = false; scanMu.Unlock() }()

	rules, src, err := loadRules()
	if err != nil {
		return map[string]any{"items": []any{}, "error": err.Error()}, nil
	}
	scanner.Progress = func(done, total int, app string) {
		if appWin != nil {
			b, _ := json.Marshal(map[string]any{"i": done, "n": total, "app": app})
			evalJS(fmt.Sprintf("window.__aicProg && window.__aicProg('scan',%s)", string(b)))
		}
	}
	scanner.ResetCancel()
	rules, items := applyIgnoreFilters(rules, nil)
	items = scanner.Scan(rules)
	_, items = applyIgnoreFilters(nil, items)
	cancelled := scanner.Cancelled()
	scanner.Progress = nil
	scanMu.Lock()
	lastScan = map[string]model.ScanItem{}
	for _, it := range items {
		lastScan[normPath(it.Path)] = it
	}
	scanMu.Unlock()
	return map[string]any{"items": items, "rulesPath": src, "count": len(items), "cancelled": cancelled}, nil
}

// cancelScan: JS `await cancelScan()` → 让进行中的扫描尽快停下（已扫到的部分仍返回）
func cancelScan(_ ...any) (any, error) {
	scanner.RequestCancel()
	return map[string]any{"ok": true}, nil
}

var (
	store   *ops.Store
	storeMu sync.Mutex
)

// migMu 迁移操作串行：migrateItem/rollbackMigration/finishMigration 全程互斥，
// 防止并发迁移同一路径（共享 dst、copyDone 全局进度）把迁移做坏。
var migMu sync.Mutex

// isOurMovedTarget 目标路径是否在本工具命名空间 <盘>:\AIClean-Moved\ 之下
func isOurMovedTarget(t string) bool {
	c := filepath.Clean(t)
	v := filepath.VolumeName(c)
	if v == "" || len(c) <= len(v)+1 {
		return false
	}
	rest := strings.ToLower(strings.TrimPrefix(c, v+`\`))
	return strings.HasPrefix(rest, `aiclean-moved\`)
}

// 前端上报的可拖拽区域（无边框窗口的"标题栏"）：.top 主矩形 + 内部按钮排除区，
// 坐标为物理像素（前端已乘 devicePixelRatio）。WM_NCHITTEST 据此返回 HTCAPTION。
var (
	dragMu sync.RWMutex
	dragZ  struct {
		l, t, r, b int32
		exc        [][4]int32
	}
)

func setDrag(a ...any) (any, error) {
	m, _ := a[0].(map[string]any)
	f := func(k string) int32 { v, _ := m[k].(float64); return int32(v) }
	z := struct {
		l, t, r, b int32
		exc        [][4]int32
	}{f("l"), f("t"), f("r"), f("b"), nil}
	if es, ok := m["exc"].([]any); ok {
		for _, e := range es {
			if q, ok := e.([]any); ok && len(q) == 4 {
				var r [4]int32
				for i := 0; i < 4; i++ {
					v, _ := q[i].(float64)
					r[i] = int32(v)
				}
				z.exc = append(z.exc, r)
			}
		}
	}
	dragMu.Lock()
	dragZ = z
	dragMu.Unlock()
	return map[string]any{"ok": true}, nil
}

// inDrag: 客户区坐标（物理像素）是否落在可拖区且不在排除区里
func inDrag(cx, cy int32) bool {
	dragMu.RLock()
	defer dragMu.RUnlock()
	z := dragZ
	if cx < z.l || cx >= z.r || cy < z.t || cy >= z.b {
		return false
	}
	for _, e := range z.exc {
		if cx >= e[0] && cx < e[2] && cy >= e[1] && cy < e[3] {
			return false
		}
	}
	return true
}

func mustStore() (*ops.Store, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if store == nil {
		root := storeRoot()
		// 旧数据在 C 盘、新根在其他盘 → 一次性把账本/日志/残留搬过去，再启用新根
		old := filepath.Join(os.Getenv("LOCALAPPDATA"), "AIClean")
		if root != "" && !strings.EqualFold(normPath(root), normPath(old)) {
			if st, err := os.Stat(old); err == nil && st.IsDir() {
				if _, err := os.Stat(root); os.IsNotExist(err) {
					if err := copyTreeAny(old, root); err != nil {
						os.RemoveAll(root) // 清掉半成品，下次启动重试
						root = old         // 搬不动就继续用 C 盘，功能不受影响
					} else {
						os.RemoveAll(old) // 复制完整才删，失败留着 C 盘原样
					}
				} else if err := mergeTreeAny(old, root); err == nil {
					// 死盘回退期在 C 盘写过日志/残留 → 缺失项并回正式数据目录
					os.RemoveAll(old)
				}
			}
		}
		var s *ops.Store
		var err error
		if root != "" {
			s, err = ops.NewStoreAt(root)
		}
		if s == nil { // 选了新根但建不了（盘被拔了/权限不足）→ 回退 LOCALAPPDATA
			s, err = ops.NewStore()
		}
		if err != nil {
			return nil, err
		}
		store = s
	}
	return store, nil
}

// storePointerFile 数据目录指向文件：第一次选定后持久记住。
// 放在 LOCALAPPDATA 下数据目录旁边——不管数据目录搬到哪，指针都读得到。
func storePointerFile() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "AIClean-root.txt")
}

// storePinnedMissing 指向盘未连接时记下原目录（供界面提示"X 盘未连接"）。
// 盘回来后下一次启动自动恢复——指向文件在死盘期间绝不被改写。
var storePinnedMissing string

// storeRoot: 先读指向文件固定数据目录；指向盘不在 → 临时回退 LOCALAPPDATA，
// 不改写指向文件（否则原配置会被悄悄覆盖、盘回来后账本"换家"）。
// 只有首次启动（无指针）才选盘并持久化。
func storeRoot() string {
	if b, err := os.ReadFile(storePointerFile()); err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			if vol := filepath.VolumeName(p); vol != "" {
				if _, err := os.Stat(vol + `\`); err == nil {
					return p // 盘还在 → 固定用这个目录
				}
			}
			// 指向盘不在：临时回退，不改写指向文件；界面提示盘未连接
			storePinnedMissing = p
			return filepath.Join(os.Getenv("LOCALAPPDATA"), "AIClean")
		}
	}
	root := pickStoreRoot()
	if root == "" {
		root = filepath.Join(os.Getenv("LOCALAPPDATA"), "AIClean")
	}
	// 持久化选择（写失败无妨，下次启动重选）
	tmp := storePointerFile() + ".tmp"
	if os.WriteFile(tmp, []byte(root), 0o644) == nil {
		os.Rename(tmp, storePointerFile())
	}
	return root
}

// pickStoreRoot: 选"固定 NTFS 且非 C 盘"里可用空间最大的盘（X:\AIClean）。
// 没有第二块本地盘时返回空 → 回退 %LOCALAPPDATA%\AIClean。
func pickStoreRoot() string {
	best := ""
	var bestFree uint64
	for _, d := range drives() {
		if d.Letter == "C" || !d.Fixed || strings.ToUpper(d.FS) != "NTFS" {
			continue
		}
		if d.Free > bestFree {
			bestFree, best = d.Free, d.Letter
		}
	}
	if best == "" {
		return ""
	}
	return best + `:\AIClean`
}

// mergeTreeAny: 把 src 里"目标缺失"的文件并到 dst（已存在的以 dst 为准）。
// ops.jsonl 两边都有时追加而不是丢弃——回退期写的日志不丢。
func mergeTreeAny(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		fi, e := d.Info()
		if e != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if _, e := os.Lstat(to); e == nil {
			switch {
			case strings.EqualFold(filepath.Base(p), "ops.jsonl"):
				if b, e2 := os.ReadFile(p); e2 == nil && len(b) > 0 {
					if f, e3 := os.OpenFile(to, os.O_APPEND|os.O_WRONLY, 0o644); e3 == nil {
						f.Write(b)
						f.Close()
					}
				}
			case strings.EqualFold(filepath.Base(p), "migrations.json"):
				mergeMigrationsFile(p, to) // 账本并集：src+bak 去重，两边记录都不能丢
			}
			return nil
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		out, e := os.Create(to)
		if e != nil {
			in.Close()
			return e
		}
		_, err = io.Copy(out, in)
		out.Close()
		in.Close()
		return err
	})
}

// mergeMigrationsFile 两份账本并集：以 src+bak 为键去重，回退盘记录追加到目标已有记录之后。
// 断盘期间若在临时目录产生过迁移（正常会被 pinnedStoreBlocked 拦住，这里是兜底），
// 合并时任何一边的记录都不允许丢——丢了对应 C 盘备份就再也删不掉。
func mergeMigrationsFile(srcFile, dstFile string) {
	read := func(p string) []map[string]any {
		var out []map[string]any
		if b, e := os.ReadFile(p); e == nil && json.Unmarshal(b, &out) == nil {
			return out
		}
		return nil
	}
	dstRecs, srcRecs := read(dstFile), read(srcFile)
	seen := map[string]bool{}
	union := make([]map[string]any, 0, len(dstRecs)+len(srcRecs))
	key := func(m map[string]any) string {
		s, _ := m["src"].(string)
		b, _ := m["bak"].(string)
		return strings.ToLower(s) + "|" + strings.ToLower(b)
	}
	for _, m := range dstRecs {
		union = append(union, m)
		seen[key(m)] = true
	}
	for _, m := range srcRecs {
		if !seen[key(m)] {
			union = append(union, m)
		}
	}
	if b, e := json.Marshal(union); e == nil {
		tmp := dstFile + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, dstFile)
		}
	}
}

// copyTreeAny: 递归复制目录（跨盘 rename 不行，只能复制；链接/特殊文件跳过）
func copyTreeAny(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			if fi, e := d.Info(); e == nil {
				if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&0x400 != 0 {
					return filepath.SkipDir // 不复制 junction，避免把链接当实体搬
				}
			}
			return os.MkdirAll(to, 0o755)
		}
		fi, e := d.Info()
		if e != nil || !fi.Mode().IsRegular() {
			return nil
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.Create(to)
		if e != nil {
			return e
		}
		_, err = io.Copy(out, in)
		out.Close()
		return err
	})
}

// cleanItem: JS `await cleanItem({path,app,note})` → 隔离该目录
func cleanItem(a ...any) (any, error) {
	stop := startHeartbeat()
	defer close(stop)
	if len(a) < 1 {
		return nil, fmt.Errorf("missing args")
	}
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	app, _ := m["app"].(string)
	note, _ := m["note"].(string)
	if path == "" {
		return nil, fmt.Errorf("path 为空")
	}
	it, err := guardPath(path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	if it.Prunable {
		return map[string]any{"ok": false, "error": "该项只能按龄清理，拒绝整目录删除"}, nil
	}
	if err := checkStillClosed(it); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	meta, err := s.Quarantine(it.Path, app, note, retainDaysPref(s))
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true, "id": meta.ID, "size": meta.Size}, nil
}

// deleteItem: JS `await deleteItem({path,app})` → 一步永久删除（目录或文件）
func deleteItem(a ...any) (any, error) {
	stop := startHeartbeat()
	defer close(stop)
	if len(a) < 1 {
		return nil, fmt.Errorf("missing args")
	}
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	app, _ := m["app"].(string)
	if path == "" {
		return nil, fmt.Errorf("path 为空")
	}
	it, err := guardPath(path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	if it.Prunable {
		return map[string]any{"ok": false, "error": "该项只能按龄清理，拒绝整目录删除"}, nil
	}
	if app == "" {
		app = it.App
	}
	if err := checkStillClosed(it); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	if it.Special != "" {
		return specialClean(s, it, app)
	}
	size, _, err := s.Delete(it.Path, app) // 用扫描时的权威路径：前端传的 path 只用于查表
	if err != nil {
		// 没删干净（文件被占用等）：残留目录移入隔离区——可恢复、期满自动清，
		// 对应设置页"删除时没删干净的残留会进入「清理残留」"。
		if st, e2 := os.Lstat(it.Path); e2 == nil && st.IsDir() {
			if meta, qerr := s.Quarantine(it.Path, app, "删除时未删干净的残留", retainDaysPref(s)); qerr == nil {
				return map[string]any{"ok": true, "size": size, "residual": meta.ID, "note": "部分内容被占用，残留已移入「清理残留」"}, nil
			}
		}
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true, "size": size}, nil
}

// specialClean 特殊清理项：回收站 / Windows Update 缓存 / 休眠文件
func specialClean(s *ops.Store, it model.ScanItem, app string) (any, error) {
	switch it.Special {
	case "recycleBin":
		// SHERB_NOCONFIRMATION | NOPROGRESSUI | NOSOUND
		if r, _, _ := shEmptyBin.Call(0, 0, 0x7); r != 0 {
			s.LogApp("purge", app, it.Path, 0, false, fmt.Sprintf("回收站清空失败 (0x%X)", r))
			return map[string]any{"ok": false, "error": "回收站清空失败，可能有文件正被使用"}, nil
		}
		s.LogApp("purge", app, it.Path, it.Size, true, "已清空回收站")
		return map[string]any{"ok": true, "size": it.Size}, nil
	case "winUpdate":
		if !isAdmin() {
			return map[string]any{"ok": false, "error": "需要管理员权限"}, nil
		}
		exec.Command("net", "stop", "wuauserv").Run() // 停服务释放占用，失败也继续尝试
		size, _, err := s.Delete(it.Path, app)
		exec.Command("net", "start", "wuauserv").Run()
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, nil
		}
		return map[string]any{"ok": true, "size": size}, nil
	case "hiberOff":
		if !isAdmin() {
			return map[string]any{"ok": false, "error": "需要管理员权限"}, nil
		}
		out, err := exec.Command("powercfg", "/h", "off").CombinedOutput()
		if err != nil {
			s.LogApp("purge", app, it.Path, 0, false, "powercfg /h off: "+strings.TrimSpace(string(out)))
			return map[string]any{"ok": false, "error": "关闭休眠失败: " + strings.TrimSpace(string(out))}, nil
		}
		s.LogApp("purge", app, it.Path, it.Size, true, "已关闭休眠，hiberfil.sys 已释放")
		return map[string]any{"ok": true, "size": it.Size}, nil
	}
	return map[string]any{"ok": false, "error": "未知特殊操作"}, nil
}

// pruneItem: JS `await pruneItem({path,days})` → 只删 N 天前的旧文件
func pruneItem(a ...any) (any, error) {
	stop := startHeartbeat()
	defer close(stop)
	if len(a) < 1 {
		return nil, fmt.Errorf("missing args")
	}
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	days, _ := m["days"].(float64)
	if path == "" {
		return nil, fmt.Errorf("path 为空")
	}
	it, err := guardPath(path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	if !it.Prunable {
		return map[string]any{"ok": false, "error": "该项不支持按龄清理"}, nil
	}
	d := it.PruneDays
	if days > 0 {
		d = int(days)
	}
	if d <= 0 || d > 3650 {
		return map[string]any{"ok": false, "error": "该项不支持按龄清理"}, nil
	}
	if err := checkStillClosed(it); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	bytes, files, err := s.PruneApp(it.Path, d, it.App)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true, "bytes": bytes, "files": files, "days": d}, nil
}

// quarList: JS `await quarList()` → 隔离区条目
func quarList(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": s.List()}, nil
}

// restoreItem: JS `await restoreItem(id)`
func restoreItem(a ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	id, _ := a[0].(string)
	if err := s.Restore(id); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// purgeItem: JS `await purgeItem(id)`
func purgeItem(a ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	id, _ := a[0].(string)
	if err := s.Purge(id); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// migDst 目标路径：<盘>:\AIClean-Moved\<ruleId>\<目录名> —— 带 ruleId 防同名目录撞车。
// ruleId 消毒：启发式项的 "id::h" 含 ":"（Windows 非法字符），恶意规则还可能带 ".."。
func migDst(path, drive string) string {
	scanMu.Lock()
	it, ok := lastScan[normPath(path)]
	scanMu.Unlock()
	key := it.RuleID
	if !ok || key == "" {
		key = "misc"
	}
	key = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|.`, r) {
			return '_'
		}
		return r
	}, key)
	if key == "" {
		key = "misc"
	}
	return filepath.Join(drive+`:\`, "AIClean-Moved", key, filepath.Base(path))
}

// migPreflight: JS `await migPreflight({path, drive})` → 检查列表
func migPreflight(a ...any) (any, error) {
	if r, blocked := pinnedStoreBlocked(); blocked {
		return r, nil
	}
	stop := startHeartbeat()
	defer close(stop)
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	drive, _ := m["drive"].(string)
	if path == "" || len(drive) != 1 {
		return map[string]any{"ok": false, "error": "参数无效"}, nil
	}
	it, err := guardPathMig(path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	dst := migDst(path, drive)
	checks := migrate.Preflight(path, dst)
	if hit := scanner.ProcsRunning(it.RequiresClosed); len(hit) > 0 {
		checks = append(checks, migrate.Check{Name: "相关程序已关闭", OK: false, Msg: strings.Join(hit, "、") + " 正在运行"})
	}
	if len(path) >= 2 && strings.EqualFold(path[:2], drive+":") {
		checks = append(checks, migrate.Check{Name: "same-drive", OK: false, Msg: "目标盘和源在同一个盘，迁移不释放空间"})
	}
	return map[string]any{"checks": checks, "dst": dst}, nil
}

// migrateItem: JS `await migrateItem({path, drive})` → 迁移+联结+回滚保护
// pinnedStoreBlocked 数据盘未连接时拦截迁移系操作——临时目录里做迁移，
// 盘回来后账本合并会丢记录（mergeTreeAny 对 migrations.json 做并集兜底，
// 但直接在断盘期禁止才是正确解）。
func pinnedStoreBlocked() (map[string]any, bool) {
	if storePinnedMissing == "" {
		return nil, false
	}
	vol := strings.TrimSuffix(filepath.VolumeName(storePinnedMissing), ":")
	return map[string]any{"ok": false, "error": fmt.Sprintf("数据目录所在的 %s 盘未连接，请连接后再进行迁移操作", vol)}, true
}

func migrateItem(a ...any) (any, error) {
	if r, blocked := pinnedStoreBlocked(); blocked {
		return r, nil
	}
	stop := startHeartbeat()
	defer close(stop)
	migMu.Lock()
	defer migMu.Unlock()
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	drive, _ := m["drive"].(string)
	if path == "" || len(drive) != 1 {
		return map[string]any{"ok": false, "error": "参数无效"}, nil
	}
	it, err := guardPathMig(path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	if err := checkStillClosed(it); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	if len(path) >= 2 && strings.EqualFold(path[:2], drive+":") {
		return map[string]any{"ok": false, "error": "目标盘和源在同一个盘，迁移不释放空间"}, nil
	}
	dst := migDst(path, drive)
	// 字节级进度（节流 150ms）→ __aicProg('migrate', {path, done, total})
	var lastEmit time.Time
	migrate.Progress = func(done, total int64) {
		if appWin == nil || (time.Since(lastEmit) < 150*time.Millisecond && done < total) {
			return
		}
		lastEmit = time.Now()
		b, _ := json.Marshal(map[string]any{"path": path, "done": done, "total": total})
		evalJS(fmt.Sprintf("window.__aicProg && window.__aicProg('migrate',%s)", string(b)))
	}
	rec, err := migrate.Migrate(path, dst)
	migrate.Progress = nil
	s, e := mustStore()
	if e == nil {
		s.Log("migrate", path, rec.Size, err == nil, "→ "+dst)
		if err == nil {
			s.MigAdd(path, dst, rec.Bak, rec.Size)
		}
	}
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "rolledBack": rec.RolledB}, nil
	}
	return map[string]any{"ok": true, "dst": dst, "bak": rec.Bak, "size": rec.Size}, nil
}

// listMigrations: JS `await listMigrations()` → 全部迁移记录
// done=false 的表示源盘备份还在（可删/可迁回）；done=true 已归档（仅记账用）
func listMigrations(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	// 先跑一遍 MigList 让"备份已被外部删掉"的记录顺手归档
	s.MigList()
	return map[string]any{"items": s.MigListAll()}, nil
}

// finishMigration: JS `await finishMigration({bak})` → 确认无误后删除源盘备份，真正腾出空间
// bak 严格校验：必须是账本里 done!=true 记录指向的备份（结构：src + ".aiclean-bak-" + 纯数字后缀，
// 不含任何路径分隔符），且 src 处的 junction 仍指向账本 dst——防止构造路径删任意目录。
func finishMigration(a ...any) (any, error) {
	if r, blocked := pinnedStoreBlocked(); blocked {
		return r, nil
	}
	stop := startHeartbeat()
	defer close(stop)
	migMu.Lock()
	defer migMu.Unlock()
	m, _ := a[0].(map[string]any)
	bak, _ := m["bak"].(string)
	if bak == "" {
		if p, _ := m["path"].(string); p != "" {
			bak = p
		}
	}
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	src, rec, err := checkBakDeletable(s, bak)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	dst, _ := rec["dst"].(string)
	size, _ := dirOf(bak)
	if err := os.RemoveAll(bak); err != nil {
		s.Log("migrate", src, 0, false, "删除备份失败: "+err.Error())
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	s.MigFinish(src)
	s.Log("migrate", src, size, true, "已清理备份（原 → "+dst+"）")
	return map[string]any{"ok": true, "freed": size}, nil
}

// validateBak 校验 bak 是本工具迁移留下的备份目录：结构 src+".aiclean-bak-"+纯数字，
// 且能在账本找到 done!=true 的对应记录。返回 (src, 账本记录, err)。
func validateBak(s *ops.Store, bak string) (string, map[string]any, error) {
	idx := strings.LastIndex(bak, ".aiclean-bak-")
	if idx < 0 {
		return "", nil, fmt.Errorf("参数无效")
	}
	suffix := bak[idx+len(".aiclean-bak-"):]
	if suffix == "" || strings.ContainsAny(suffix, `/\`) {
		return "", nil, fmt.Errorf("参数无效")
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return "", nil, fmt.Errorf("参数无效")
		}
	}
	src := bak[:idx]
	if filepath.Dir(bak) != filepath.Dir(src) {
		return "", nil, fmt.Errorf("参数无效")
	}
	for _, m := range s.MigListAll() {
		if b, _ := m["bak"].(string); b != "" && normPath(b) == normPath(bak) && m["done"] != true {
			return src, m, nil
		}
	}
	return "", nil, fmt.Errorf("没有该备份的迁移记录（可能已清理过）")
}

// checkBakDeletable finishMigration 与彻底移除共用的三重校验：
// 账本记录有效 + 原位 junction 存在且指向账本 dst + dst 目录真实存在。
// 缺任何一项都不能删备份——dst 没了备份就是唯一副本，删了永久丢数据。
func checkBakDeletable(s *ops.Store, bak string) (src string, rec map[string]any, err error) {
	src, rec, err = validateBak(s, bak)
	if err != nil {
		return "", nil, err
	}
	dst, _ := rec["dst"].(string)
	info, jerr := junction.Read(src)
	if jerr != nil || !strings.EqualFold(info.TargetWin, filepath.Clean(dst)) {
		return "", nil, fmt.Errorf("原位置的联结已不存在或目标已变更，请先用“迁回”恢复数据")
	}
	if _, e := os.Stat(dst); e != nil {
		return "", nil, fmt.Errorf("迁移目标 %s 不可达（数据可能已丢失，这个备份是唯一副本）", dst)
	}
	return src, rec, nil
}

// rollbackMigration: JS `await rollbackMigration({path})` → 迁回原位
// 必须有账本记录；账本缺失时只允许"扫描确认已迁移 + junction 目标在 X:\AIClean-Moved\ 下"
// 的项——防止对系统自带 junction（如 My Documents）或其他程序的链接执行破坏性改名。
func rollbackMigration(a ...any) (any, error) {
	if r, blocked := pinnedStoreBlocked(); blocked {
		return r, nil
	}
	stop := startHeartbeat()
	defer close(stop)
	migMu.Lock()
	defer migMu.Unlock()
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	if path == "" {
		return map[string]any{"ok": false, "error": "参数无效"}, nil
	}
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	mr := &migrate.Record{Src: path, Link: path}
	if rec := s.MigFind(path); rec != nil {
		mr.Dst, _ = rec["dst"].(string)
		mr.Bak, _ = rec["bak"].(string)
	} else {
		// 无账本：要求扫描确认是本工具迁移的（state=migrated）且目标在本工具命名空间下
		scanMu.Lock()
		it, ok := lastScan[normPath(path)]
		scanMu.Unlock()
		if !ok || it.State != "migrated" {
			return map[string]any{"ok": false, "error": "没有该路径的迁移记录"}, nil
		}
		info, jerr := junction.Read(path)
		if jerr != nil || info == nil || !isOurMovedTarget(info.TargetWin) {
			return map[string]any{"ok": false, "error": "没有该路径的迁移记录"}, nil
		}
		mr.Dst = info.TargetWin
	}
	if ok, _ := m["checkOnly"].(bool); ok {
		need, free, fast := migrate.RollbackPreflight(mr)
		return map[string]any{"ok": true, "need": need, "free": free, "fast": fast}, nil
	}
	// 迁回复制同样接字节进度（节流 150ms）→ __aicProg('migrate')
	var lastEmit time.Time
	migrate.Progress = func(done, total int64) {
		if appWin == nil || (time.Since(lastEmit) < 150*time.Millisecond && done < total) {
			return
		}
		lastEmit = time.Now()
		b, _ := json.Marshal(map[string]any{"path": path, "done": done, "total": total})
		evalJS(fmt.Sprintf("window.__aicProg && window.__aicProg('migrate',%s)", string(b)))
	}
	err = migrate.Rollback(mr)
	migrate.Progress = nil
	if err != nil {
		s.Log("migrate", path, 0, false, "迁回失败: "+err.Error())
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	s.MigRemove(path) // 数据已回 C 盘，迁移记录删掉（保留会影响已迁移记账）
	s.Log("migrate", path, 0, true, "已迁回原位")
	return map[string]any{"ok": true}, nil
}

func dirOf(p string) (int64, int64) {
	var sz, n int64
	st, err := os.Lstat(p)
	if err != nil {
		return 0, 0
	}
	if !st.IsDir() {
		return st.Size(), 1
	}
	filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				sz += fi.Size()
				n++
			}
		}
		return nil
	})
	return sz, n
}

// retainDaysPref: 隔离区保留天数（设置页可改），默认 30
func retainDaysPref(s *ops.Store) int {
	v, ok := s.Prefs()["quarRetainDays"]
	if !ok {
		return 30
	}
	var n int
	switch t := v.(type) {
	case float64:
		n = int(t)
	case string:
		if x, err := strconv.Atoi(t); err == nil {
			n = x
		}
	}
	if n != 7 && n != 30 && n != 90 {
		return 30
	}
	return n
}

// getPrefs / setPrefs: 偏好持久化（localStorage 在 data: URL 下不可用）
func getPrefs(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	return s.Prefs(), nil
}

func setPrefs(a ...any) (any, error) {
	if len(a) < 1 {
		return nil, fmt.Errorf("missing args")
	}
	m, _ := a[0].(map[string]any)
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	s.SetPrefs(m)
	return map[string]any{"ok": true}, nil
}

// getLogs: JS `await getLogs()`
func getLogs(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": s.Logs(200)}, nil
}

// ---------- 自动清理（无头模式 + 立即执行） ----------
// 规则：只动 active 状态项 —— 缓存类(action=clean)整删，prunable 按龄清；
// 锁定、待确认、用户数据一律不碰。结果写入 ops.jsonl，记录页可见。

func runAutoClean() (freed int64, cleaned int) {
	// 与手动扫描互斥：共享 scanner.Progress/cancelFlag 全局态
	scanMu.Lock()
	if scanning {
		scanMu.Unlock()
		return 0, 0
	}
	scanning = true
	scanMu.Unlock()
	defer func() { scanMu.Lock(); scanning = false; scanMu.Unlock() }()
	scanner.ResetCancel()

	s, err := mustStore()
	if err != nil {
		return 0, 0
	}
	s.PurgeExpired() // 残留保留期到点就清——不能只靠打开残留页才触发
	rules, _, err := loadRules()
	if err != nil {
		return 0, 0
	}
	_, items := applyIgnoreFilters(nil, scanner.Scan(rules)) // "不再提示"名单对自动清理同样生效
	for _, it := range autoCleanCandidates(items) {
		if it.Prunable {
			if b, _, e := s.PruneApp(it.Path, it.PruneDays, it.App); e == nil {
				freed += b
				if b > 0 {
					cleaned++
				}
			}
		} else {
			if sz, _, e := s.Delete(it.Path, it.App); e == nil {
				freed += sz
				cleaned++
			}
		}
	}
	s.LogApp("autoclean", "AIClean", "计划任务", freed, true,
		fmt.Sprintf("自动清理 %d 项，释放 %.1f GB", cleaned, float64(freed)/1e9))
	return
}

// autoCleanCandidates 静默清理准入：只收 active 的按龄项和低风险整删项——
// 仅统计、特殊项（回收站/休眠/更新缓存）、锁定、启发式、中风险一律不碰。
func autoCleanCandidates(items []model.ScanItem) []model.ScanItem {
	var out []model.ScanItem
	for _, it := range items {
		if it.State != "active" || it.ReportOnly || it.Special != "" || it.NeedsAdmin {
			continue
		}
		if it.Prunable && it.PruneDays > 0 {
			out = append(out, it)
		} else if it.Action == "clean" && !it.Heuristic && it.Risk == "cache" {
			out = append(out, it)
		}
	}
	return out
}

// autoCleanNow: JS `await autoCleanNow()` → 界面里"立即执行一次"
func autoCleanNow(_ ...any) (any, error) {
	stop := startHeartbeat()
	defer close(stop)
	freed, n := runAutoClean()
	return map[string]any{"ok": true, "freed": freed, "count": n}, nil
}

// ---------- 长操作心跳：后端执行期间每 2s 推 __aicProg('busy') ----------
// 前端按"闲置时间"判超时——没进度回调的操作（删除/迁回/猎大文件）
// 以及迁移的预检/改名阶段，都靠心跳续命，不再被硬超时误杀。

func startHeartbeat() chan struct{} {
	done := make(chan struct{})
	go func() {
		defer crashNote("heartbeat")
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				evalJS(`window.__aicProg && window.__aicProg('busy',{})`)
			}
		}
	}()
	return done
}

// ---------- 计划任务→界面 通知：命名事件 ----------
// --auto-clean 发现界面在运行时，SetEvent 通知界面代为执行
// （用户常开界面，否则定时清理永远不会跑）
const autoCleanEventName = `Global\AIClean-AutoClean`

// securityAttributes SECURITY_ATTRIBUTES（Go 侧最小布局）
type securityAttributes struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      int32
}

// lowIntegritySA 低完整性安全描述符：DACL 给 Everyone 全部访问，
// SACL 把完整性标签设为 Low（NW=No-Write-Up）。
// 界面若以管理员运行，普通权限的计划任务连"打开事件改状态"都会被拒——
// 命名对象挂这个 SD 后跨完整性可用（互斥锁同理，否则探测不到已存在实例）。
// 返回的 SA 仅本次调用有效；使用后调 freeSA 释放 SD。
func lowIntegritySA() *securityAttributes {
	var sd uintptr
	r, _, _ := convertSDDL.Call(
		uintptr(unsafe.Pointer(must16(`D:(A;;0x1F0003;;;WD)S:(ML;;NW;;;LW)`))),
		1, // SDDL_REVISION_1
		uintptr(unsafe.Pointer(&sd)), 0)
	if r == 0 || sd == 0 {
		return nil // 转换失败回退默认 SD（普通权限创建本无此问题）
	}
	return &securityAttributes{Length: uint32(unsafe.Sizeof(securityAttributes{})), SecurityDescriptor: sd, InheritHandle: 0}
}

func freeSA(sa *securityAttributes) {
	if sa != nil && sa.SecurityDescriptor != 0 {
		localFree.Call(sa.SecurityDescriptor)
	}
}

// watchAutoCleanEvent 常驻等待：计划任务 SetEvent → 排队执行自动清理
func watchAutoCleanEvent() {
	defer crashNote("watchAutoCleanEvent")
	sa := lowIntegritySA()
	var sap uintptr
	if sa != nil {
		sap = uintptr(unsafe.Pointer(sa))
	}
	h, _, _ := createEventW.Call(sap, 0, 0, uintptr(unsafe.Pointer(must16(autoCleanEventName))))
	freeSA(sa)
	if h == 0 {
		return
	}
	for {
		r, _, _ := waitForSingleObject.Call(h, 0xFFFFFFFF /*INFINITE*/)
		if r != 0 {
			return // WAIT_FAILED
		}
		go queuedAutoClean()
	}
}

// queuedAutoClean 界面代执行：扫描/迁移进行中排队等它结束（上限 30 分钟）
func queuedAutoClean() {
	defer crashNote("queuedAutoClean")
	deadline := time.Now().Add(30 * time.Minute)
	for {
		scanMu.Lock()
		busy := scanning
		scanMu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(3 * time.Second)
	}
	migMu.Lock() // 迁移进行中必须等它结束——清理和迁移不能交错
	defer migMu.Unlock()
	freed, n := runAutoClean()
	if s, e := mustStore(); e == nil {
		s.LogApp("autoclean", "AIClean", "计划任务", freed, true,
			fmt.Sprintf("由界面代为执行：自动清理 %d 项，释放 %.1f GB", n, float64(freed)/1e9))
	}
	evalJS(`window.__aicAutoDone && window.__aicAutoDone()`)
}

// notifyAutoClean 无头 --auto-clean：界面在运行 → 通知它代为执行
func notifyAutoClean() {
	ok, msg := false, "界面正在运行，本次自动清理跳过"
	evt, _, _ := openEventW.Call(0x0002 /*EVENT_MODIFY_STATE*/, 0, uintptr(unsafe.Pointer(must16(autoCleanEventName))))
	if evt != 0 {
		if r, _, _ := setEventW.Call(evt); r != 0 {
			ok, msg = true, "已通知界面代为执行自动清理"
		}
		procCloseHandle.Call(evt)
	}
	if s, e := mustStore(); e == nil {
		s.LogApp("autoclean", "AIClean", "计划任务", 0, ok, msg)
	}
}

// ---------- 计划任务：schtasks 注册/查询/删除 AIClean-AutoClean ----------
const schedTaskName = "AIClean-AutoClean"

// schedArgs: 规范化排程参数（可单测）：freq→/SC，非法时间→03:00
func schedArgs(freq, tm string) (sc, normT string) {
	dig := func(s string) bool {
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	if len(tm) != 5 || tm[2] != ':' || !dig(tm[:2]) || !dig(tm[3:]) || tm[:2] > "23" || tm[3:] > "59" {
		tm = "03:00"
	}
	sc = "DAILY"
	if freq == "weekly" {
		sc = "WEEKLY"
	}
	return sc, tm
}

// schedSet: JS `await schedSet({freq:'off|daily|weekly', time:'HH:MM'})`
func schedSet(a ...any) (any, error) {
	m, _ := a[0].(map[string]any)
	freq, _ := m["freq"].(string)
	tm, _ := m["time"].(string)
	if freq == "off" || freq == "" {
		exec.Command("schtasks", "/Delete", "/TN", schedTaskName, "/F").Run()
		if s, e := mustStore(); e == nil {
			s.SetPrefs(map[string]any{"schedExe": ""}) // 任务没了，登记路径一并清
		}
		return map[string]any{"ok": true, "on": false}, nil
	}
	sc, tm := schedArgs(freq, tm)
	exe, err := os.Executable()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	out, err := exec.Command("schtasks", "/Create", "/TN", schedTaskName,
		"/SC", sc, "/ST", tm, "/TR", `"`+exe+`" --auto-clean`, "/F").CombinedOutput()
	if err != nil {
		return map[string]any{"ok": false, "error": strings.TrimSpace(string(out))}, nil
	}
	// 登记 exe 路径：启动自检只和这里比，不解析 schtasks 输出（GBK 编码必误判）
	if s, e := mustStore(); e == nil {
		s.SetPrefs(map[string]any{"schedExe": filepath.Clean(exe)})
	}
	return map[string]any{"ok": true, "on": true, "freq": freq, "time": tm}, nil
}

// schedStatus: JS `await schedStatus()` → {on, next, freq, time}
func schedStatus(_ ...any) (any, error) {
	out, err := exec.Command("schtasks", "/Query", "/TN", schedTaskName, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return map[string]any{"on": false}, nil
	}
	// CSV: "\\AIClean-AutoClean","下次运行","状态"
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	f := strings.Split(line, `","`)
	trim := func(s string) string { return strings.Trim(s, `" `) }
	res := map[string]any{"on": true}
	if len(f) >= 3 {
		res["next"], res["status"] = trim(f[1]), trim(f[2])
	}
	return res, nil
}

// ---------- 开机自启：HKCU\...\Run\AIClean ----------
const runKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// exePathDrift: 登记的 exe 路径和当前运行路径不一致（用户移动过 exe）
func exePathDrift(registered, current string) bool {
	reg := strings.Trim(strings.TrimSpace(registered), `"`)
	if reg == "" {
		return false // 没登记过的不算漂移（首次创建由设置页负责）
	}
	return !strings.EqualFold(filepath.Clean(reg), filepath.Clean(current))
}

// schedNeedsRebuild 计划任务是否要重建（纯函数，可测）：
// 任务存在 + (prefs 没记过 schedExe[旧版本建的，补一次] 或 登记路径和当前不符)
func schedNeedsRebuild(taskExists bool, schedExe, cur string) bool {
	if !taskExists {
		return false
	}
	if schedExe == "" {
		return true // 老版本建的任务没有登记——补建一次并写入 schedExe
	}
	return exePathDrift(schedExe, cur)
}

// fixStartupDrift 启动自检：自启项/计划任务里登记的 exe 路径和实际不符时
// （用户移动过 exe）按当前路径重建并记日志。
// 自启走注册表 API（reg.exe 输出是 ANSI/GBK，中文路径解析必误判）；
// 计划任务和自己 prefs 里的 schedExe 比，不碰 schtasks 输出编码。
func fixStartupDrift() {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return
	}
	exe = filepath.Clean(exe)
	logSys := func(msg string) {
		if s, e := mustStore(); e == nil {
			s.LogApp("system", "AIClean", "启动自检", 0, true, msg)
		}
	}
	// 开机自启：注册表 API 读（Unicode，中文路径不炸）
	if old := runKeyRead(); exePathDrift(old, exe) {
		if runKeyWrite(exe) == nil {
			logSys(fmt.Sprintf("开机自启路径已更新：%s → %s", old, exe))
		}
	}
	// 计划任务：和 prefs 的 schedExe 比
	if _, e := exec.Command("schtasks", "/Query", "/TN", schedTaskName).Output(); e == nil {
		if s, e2 := mustStore(); e2 == nil {
			reg, _ := s.Prefs()["schedExe"].(string)
			if schedNeedsRebuild(true, reg, exe) {
				freq, _ := s.Prefs()["schedFreq"].(string)
				tm, _ := s.Prefs()["schedTime"].(string)
				if freq == "" {
					freq = "daily"
				}
				sc, ntm := schedArgs(freq, tm)
				if _, e3 := exec.Command("schtasks", "/Create", "/TN", schedTaskName,
					"/SC", sc, "/ST", ntm, "/TR", `"`+exe+`" --auto-clean`, "/F").CombinedOutput(); e3 == nil {
					s.SetPrefs(map[string]any{"schedExe": exe})
					if reg == "" {
						logSys(fmt.Sprintf("计划任务补充登记路径：%s", exe))
					} else {
						logSys(fmt.Sprintf("计划任务路径已更新：%s → %s", reg, exe))
					}
				}
			}
		}
	}
}

const runKeySub = `Software\Microsoft\Windows\CurrentVersion\Run`

// runKeyRead: HKCU Run 里的 AIClean 值；未设置时返回 ""
func runKeyRead() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeySub, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("AIClean")
	if err != nil {
		return ""
	}
	return v
}

func runKeyWrite(exe string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeySub, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("AIClean", `"`+exe+`"`)
}

func runKeyDelete() {
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKeySub, registry.SET_VALUE); err == nil {
		k.DeleteValue("AIClean")
		k.Close()
	}
}

func autostartGet(_ ...any) (any, error) {
	return map[string]any{"on": runKeyRead() != ""}, nil
}

// autostartSet: JS `await autostartSet({on:true})`
func autostartSet(a ...any) (any, error) {
	m, _ := a[0].(map[string]any)
	on, _ := m["on"].(bool)
	if !on {
		runKeyDelete()
		return map[string]any{"ok": true, "on": false}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	if err := runKeyWrite(exe); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true, "on": true}, nil
}

func main() {
	// 主流程 panic：写崩溃日志 + 弹窗告知日志位置，不静默闪退
	defer func() {
		if r := recover(); r != nil {
			p := crashLogPath()
			writeCrash(p, "main", r, debug.Stack())
			messageBoxW.Call(0,
				uintptr(unsafe.Pointer(must16("AIClean 遇到错误已退出，日志位于 "+p))),
				uintptr(unsafe.Pointer(must16("AIClean"))), 0x10 /*MB_ICONERROR*/)
			os.Exit(2)
		}
	}()
	elevatedLaunch := false
	autoClean := false
	for _, a := range os.Args[1:] {
		if a == "--auto-clean" {
			autoClean = true
		}
		if a == "--elevated" {
			elevatedLaunch = true
		}
	}
	// 高 DPI：PerMonitorV2，125%/150% 缩放下文字不糊；旧系统调用失败无妨
	setDpiAware.Call(^uintptr(0) - 3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	scanner.Admin = isAdmin()
	// 单实例：普通/提权/无头模式共用一把锁——任何两个实例都不应同时运行。
	// 提权重启时普通实例正在关窗，给它留时间放锁（最多 ~8s）。
	const muxName = `Global\AIClean-SingleInstance`
	sa := lowIntegritySA()
	var sap uintptr
	if sa != nil {
		sap = uintptr(unsafe.Pointer(sa))
	}
	var mux uintptr
	for i := 0; ; i++ {
		var e error
		mux, _, e = createMutexW.Call(sap, 0, uintptr(unsafe.Pointer(must16(muxName))))
		// ERROR_ACCESS_DENIED(5)：旧构建的提权进程占着无 SD 的锁——同样视为"已存在"
		if e != syscall.Errno(183) /*ERROR_ALREADY_EXISTS*/ && e != syscall.Errno(5) {
			break
		}
		if mux != 0 {
			procCloseHandle.Call(mux) // 打开的是别人的锁，句柄照样要关
		}
		if autoClean {
			notifyAutoClean() // 界面在运行 → 通知它代执行（用户常开界面，直接跳过等于永不清理）
			return
		}
		if !(elevatedLaunch && i < 40) { // 非提权重启 → 立即唤起已有窗口退出
			focusExistingWindow()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	freeSA(sa)
	defer procCloseHandle.Call(mux) // 持有到进程退出，系统兜底放锁
	if autoClean {
		// 无头模式：计划任务调用 —— 自动清理后退出，不开窗口
		runAutoClean()
		return
	}
	// 启动时清一次超期残留（不能只靠打开残留页才触发）
	if s, e := mustStore(); e == nil {
		s.PurgeExpired()
		s.LogApp("system", "AIClean", "启动", 0, true, "v"+Version)
	}
	fixStartupDrift() // exe 被移动过时，计划任务/自启里的旧路径按当前位置重建
	dataPath := ""
	if s, e := mustStore(); e == nil && s != nil {
		dataPath = filepath.Join(s.Root, "webview")
		cleanupOldWebViewData()
	}
	go watchAutoCleanEvent() // 等计划任务的 SetEvent → 界面代执行自动清理
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath, // WebView2 缓存（~234MB）跟数据目录走，不占 C 盘
		WindowOptions: webview2.WindowOptions{
			Title:  "AIClean",
			Width:  1226,
			Height: 813,
			Center: true,
		},
	})
	if w == nil {
		// WebView2 Runtime 缺失：询问是否打开微软官方下载页
		r, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0,
			uintptr(unsafe.Pointer(must16("AIClean 需要 WebView2 运行时才能显示界面。\n\nWindows 11 通常已自带；Windows 10 可能需要单独安装（约 2 MB 在线安装包）。\n\n是否现在打开微软官方下载页面？"))),
			uintptr(unsafe.Pointer(must16("AIClean"))),
			0x24) // MB_YESNO | MB_ICONQUESTION
		if r == 6 { // IDYES
			exec.Command("rundll32", "url.dll,FileProtocolHandler",
				"https://developer.microsoft.com/microsoft-edge/webview2/consumer/").Start()
		}
		return
	}
	defer w.Destroy()
	appWin = w

	bind(w, "getState", getState)
	bind(w, "scanItems", scanItems)
	bind(w, "cleanItem", cleanItem)
	bind(w, "deleteItem", deleteItem)
	bind(w, "pruneItem", pruneItem)
	bind(w, "quarList", quarList)
	bind(w, "restoreItem", restoreItem)
	bind(w, "purgeItem", purgeItem)
	bind(w, "getLogs", getLogs)
	bind(w, "migrateItem", migrateItem)
	bind(w, "migPreflight", migPreflight)
	bind(w, "listMigrations", listMigrations)
	bind(w, "finishMigration", finishMigration)
	bind(w, "rollbackMigration", rollbackMigration)
	bind(w, "getPrefs", getPrefs)
	bind(w, "setPrefs", setPrefs)
	bind(w, "winMin", winMin)
	bind(w, "winMax", winMax)
	bind(w, "winClose", winClose)
	bind(w, "winDrag", winDrag)
	bind(w, "setDrag", setDrag)
	bind(w, "openPath", openPath)
	bind(w, "openUrl", openUrl)
	bind(w, "openDataDir", openDataDir)
	bind(w, "dataDir", dataDir)
	bind(w, "openLogsDir", openLogsDir)
	bind(w, "uninstallCheck", uninstallCheck)
	bind(w, "uninstallRun", uninstallRun)
	bind(w, "quitApp", quitApp)
	bind(w, "cancelScan", cancelScan)
	bind(w, "autoCleanNow", autoCleanNow)
	bind(w, "schedSet", schedSet)
	bind(w, "schedStatus", schedStatus)
	bind(w, "autostartGet", autostartGet)
	bind(w, "autostartSet", autostartSet)
	bind(w, "winBorder", winBorder)
	bind(w, "relaunchElevated", relaunchElevated)
	bind(w, "findBigFiles", findBigFiles)
	bind(w, "ignoreItem", ignoreItem)
	bind(w, "unignoreAll", unignoreAll)
	makeFrameless(w)
	start := "home"
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "-") {
			start = a
		}
	}
	w.Navigate("data:text/html;charset=utf-8," + url.PathEscape(indexHTML) + "#" + start)
	w.Run()
}

// ---------- 无边框窗口：去掉系统标题栏，窗口控制由前端顶栏接管 ----------

var gwlStyle int32 = -16

const (
	wsPopup       = 0x80000000
	wsCaption     = 0x00C00000
	wsThickFrame  = 0x00040000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000
	wsSysMenu     = 0x00080000
	wsVisible     = 0x10000000
	// WS_OVERLAPPEDWINDOW = CAPTION|SYSMENU|THICKFRAME|MIN|MAX：标准重叠窗口，
	// 由 subclass 拦截 WM_NCCALCSIZE 消掉全部非客户区 —— 无标题栏可画，
	// 最大化/贴边/任务栏/阴影/圆角全部走系统原生路径。
	wsOverlappedWin = wsCaption | wsSysMenu | wsThickFrame | wsMinimizeBox | wsMaximizeBox
	swpNoMove       = 0x2
	swpNoSize       = 0x1
	swpNoZorder     = 0x4
	swpFrameChg     = 0x20
	swpNoActivate   = 0x10
	swMinimize      = 6
	swMaximize      = 3
	swRestore       = 9
	wmNCCalcSize    = 0x0083
	wmNCHitTest     = 0x0084
	wmNCPaint       = 0x0085
	wmNCActivate    = 0x0086
	wmGetMinMax     = 0x0024
	wmClose         = 0x10
	htClient        = 1
	htCaption       = 2
	htLeft          = 10
	htRight         = 11
	htTop           = 12
	htTopLeft       = 13
	htTopRight      = 14
	htBottom        = 15
	htBottomLeft    = 16
	htBottomRight   = 17
)

var (
	comctl32          = syscall.NewLazyDLL("comctl32.dll")
	setWindowSubclass = comctl32.NewProc("SetWindowSubclass")
	defSubclassProc   = comctl32.NewProc("DefSubclassProc")
	getDpiForWindow   = user32.NewProc("GetDpiForWindow")
	getSystemMetrics  = user32.NewProc("GetSystemMetrics")
)

// MINMAXINFO（只用到 ptMinTrackSize 字段）
type wPoint struct{ X, Y int32 }
type minMaxInfo struct {
	ptReserved, ptMaxSize, ptMaxPosition, ptMinTrackSize, ptMaxTrackSize wPoint
}

// NCCALCSIZE_PARAMS（wParam=TRUE 时 lParam 指向它；只用 rgrc[0]）
type ncCalcParams struct{ rgrc [3]wrect }

const (
	smCxSizeFrame    = 32
	smCySizeFrame    = 33
	smCxPaddedBorder = 92
)

// framelessProc 无边框窗口子类过程：
//
//	WM_NCCALCSIZE → 返回 0，客户区 = 整个窗口矩形（标题栏/边框 NC 全部消失，
//	  这就是顶部白边的根因修复 —— 没有 NC 就没有东西被刷成类背景色）
//	WM_NCHITTEST  → 先交默认处理；落在客户区且贴边 6px 时改报边缘命中，保住拖拽缩放
//	WM_NCPAINT / WM_NCACTIVATE → 吞掉，防止系统重绘根本不存在的 NC
//	WM_GETMINMAXINFO → 最小尺寸 480×320，防止缩成一个小条
var framelessProc = syscall.NewCallback(
	func(hwnd, msg, wParam, lParam, _id, _ref uintptr) uintptr {
		switch msg {
		case wmNCCalcSize:
			if wParam != 0 {
				// 最大化时 Windows 把窗口放到屏幕外 8px（藏边框用）；NC 已清零，
				// 不内缩的话客户区会出血到屏幕外，右上角按钮贴不到边。
				z, _, _ := isZoomed.Call(hwnd)
				if z != 0 {
					cx, _, _ := getSystemMetrics.Call(smCxSizeFrame)
					cy, _, _ := getSystemMetrics.Call(smCySizeFrame)
					pad, _, _ := getSystemMetrics.Call(smCxPaddedBorder)
					p := (*ncCalcParams)(unsafe.Pointer(lParam))
					p.rgrc[0].Left += int32(cx + pad)
					p.rgrc[0].Right -= int32(cx + pad)
					p.rgrc[0].Top += int32(cy + pad)
					p.rgrc[0].Bottom -= int32(cy + pad)
				}
			}
			return 0
		case wmNCPaint:
			return 0
		case wmNCActivate:
			return 1
		case wmGetMinMax:
			mm := (*minMaxInfo)(unsafe.Pointer(lParam))
			mm.ptMinTrackSize.X = 480
			mm.ptMinTrackSize.Y = 320
			return 0
		case wmNCHitTest:
			ret, _, _ := defSubclassProc.Call(hwnd, wmNCHitTest, wParam, lParam)
			if ret != htClient {
				return ret
			}
			x := int32(int16(lParam & 0xffff))
			y := int32(int16((lParam >> 16) & 0xffff))
			z, _, _ := isZoomed.Call(hwnd)
			if z == 0 { // 最大化时不可缩放，但顶栏仍可拖动还原
				dpi, _, _ := getDpiForWindow.Call(hwnd)
				bw := int32(6)
				if dpi > 0 {
					bw = int32(dpi) * 6 / 96
				}
				var r wrect
				getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
				l := x-r.Left < bw
				ri := r.Right-x <= bw
				t := y-r.Top < bw
				b := r.Bottom-y <= bw
				switch {
				case l && t:
					return htTopLeft
				case ri && t:
					return htTopRight
				case l && b:
					return htBottomLeft
				case ri && b:
					return htBottomRight
				case l:
					return htLeft
				case ri:
					return htRight
				case t:
					return htTop
				case b:
					return htBottom
				}
			}
			// 前端上报的顶栏拖放区：落在里面按标题栏处理，可拖动/双击最大化
			pt := wPoint{x, y}
			screenToClient.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
			if inDrag(pt.X, pt.Y) {
				return htCaption
			}
			return htClient
		}
		ret, _, _ := defSubclassProc.Call(hwnd, msg, wParam, lParam)
		return ret
	})

var appHwnd uintptr
var appWin webview2.WebView

type wrect struct{ Left, Top, Right, Bottom int32 }

// makeFrameless 标准无边框方案：保留 WS_OVERLAPPEDWINDOW（标题栏/边框/缩放/贴边
// 全部系统原生），再用 SetWindowSubclass 拦截 WM_NCCALCSIZE 把非客户区清零。
// 这消灭了 WS_POPUP|WS_THICKFRAME 会留下的顶部白色 NC 残留边 ——
// 那条白边是 THICKFRAME 的上边框被按类背景色绘制，融框/边框色都盖不住，
// 只有"根本没有 NC 区"才可靠。DPI、圆角、阴影、贴边、任务栏最大化全部正确。
// hwndFile 主窗口句柄手交文件：第二实例据此精确唤起窗口。
// 之前按标题 "AIClean" FindWindow——资源管理器打开同名文件夹就会被误唤起。
func hwndFile() string {
	return filepath.Join(os.TempDir(), "AIClean-hwnd.txt")
}

// focusExistingWindow 第二实例把已有主窗口提到前台（hwnd 文件 → IsWindow 校验）
func focusExistingWindow() {
	b, err := os.ReadFile(hwndFile())
	if err != nil {
		return
	}
	var h uintptr
	if _, e := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &h); e != nil || h == 0 {
		return
	}
	if r, _, _ := isWindowW.Call(h); r == 0 {
		return // 句柄已失效（窗口早关了但文件还在）
	}
	showWindow.Call(h, 9 /*SW_RESTORE*/)
	setForegroundWindow.Call(h)
}

// cleanupOldWebViewData 清掉旧版默认位置的 WebView2 缓存（%APPDATA%\<exe>）。
// DataPath 已搬到数据目录；旧目录只是浏览器缓存，删掉自动重建、不占 C 盘。
// 只在看到 EBWebView 标记目录时才删，防止误删同名文件夹。
func cleanupOldWebViewData() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := filepath.Join(os.Getenv("APPDATA"), filepath.Base(exe))
	if _, err := os.Stat(filepath.Join(old, "EBWebView")); err == nil {
		os.RemoveAll(old)
	}
}

func makeFrameless(w webview2.WebView) {
	appHwnd = uintptr(w.Window())
	os.WriteFile(hwndFile(), []byte(fmt.Sprintf("%d", appHwnd)), 0o644)
	style := uintptr(wsOverlappedWin | wsVisible)
	setWindowLongPtrW.Call(appHwnd, uintptr(uint32(gwlStyle)), style)
	// 安装子类过程（dwRefData=0，uIdSubclass=1）
	setWindowSubclass.Call(appHwnd, framelessProc, 1, 0)
	setAppIcon()
	setWinBorderHex(0xECEEEA) // 兜底：任何残留边线都与背景同色
	setWindowPos.Call(appHwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZorder|swpFrameChg)
	clampToWorkArea()
}

// setAppIcon 把内嵌的 assets/icon.ico 设成窗口图标（任务栏/Alt+Tab/任务管理器显示）。
// 运行时从 ICONDIR 里挑最大的一张 PNG 图给 CreateIconFromResourceEx——
// 不需要 .syso 资源，ico 数据随 exe 走，挪到任何目录图标都在。
func setAppIcon() {
	const wmSetIcon, iconBig, iconSmall = 0x80, 1, 0
	if len(iconICO) < 6 {
		return
	}
	n := int(binary.LittleEndian.Uint16(iconICO[4:6]))
	best, bestSz := -1, 0
	for i := 0; i < n; i++ {
		o := 6 + i*16
		if o+16 > len(iconICO) {
			break
		}
		sz := int(iconICO[o])
		if sz == 0 {
			sz = 256
		}
		if sz > bestSz {
			bestSz, best = sz, i
		}
	}
	if best < 0 {
		return
	}
	e := iconICO[6+best*16:]
	size := binary.LittleEndian.Uint32(e[8:12])
	off := binary.LittleEndian.Uint32(e[12:16])
	if int(off+size) > len(iconICO) {
		return
	}
	bits := iconICO[off : off+size]
	big, _, _ := createIconRes.Call(uintptr(unsafe.Pointer(&bits[0])), uintptr(size), 1, 0x30000, 48, 48, 0)
	small, _, _ := createIconRes.Call(uintptr(unsafe.Pointer(&bits[0])), uintptr(size), 1, 0x30000, 16, 16, 0)
	if big != 0 {
		sendMessageW.Call(appHwnd, wmSetIcon, iconBig, big)
	}
	if small != 0 {
		sendMessageW.Call(appHwnd, wmSetIcon, iconSmall, small)
	}
}

// setWinBorderHex 把窗口边框颜色设为 #RRGGBB（DWMWA_BORDER_COLOR，Win11+，旧系统忽略不报错）
func setWinBorderHex(rgb int64) {
	r := (rgb >> 16) & 0xFF
	g := (rgb >> 8) & 0xFF
	b := rgb & 0xFF
	v := uint32(b<<16 | g<<8 | r) // COLORREF = 0x00BBGGRR
	dwmSetAttr.Call(appHwnd, 34 /*DWMWA_BORDER_COLOR*/, uintptr(unsafe.Pointer(&v)), 4)
}

// clampToWorkArea 小屏/高 DPI 下 1226x813 可能超出工作区：钳制到屏幕内并居中
func clampToWorkArea() {
	var wa, r wrect
	sysParamsInfo.Call(0x30 /*SPI_GETWORKAREA*/, 0, uintptr(unsafe.Pointer(&wa)), 0)
	getWindowRect.Call(appHwnd, uintptr(unsafe.Pointer(&r)))
	w, h := r.Right-r.Left, r.Bottom-r.Top
	ww, wh := wa.Right-wa.Left, wa.Bottom-wa.Top
	if w > ww || h > wh {
		nw, nh := w, h
		if nw > ww {
			nw = ww - 16
		}
		if nh > wh {
			nh = wh - 16
		}
		setWindowPos.Call(appHwnd, 0,
			uintptr(wa.Left+(ww-nw)/2), uintptr(wa.Top+(wh-nh)/2),
			uintptr(nw), uintptr(nh), swpNoZorder)
	}
}

// winBorder: JS `await winBorder({rgb:'#RRGGBB'})` → 主题切换时同步窗口边框色
func winBorder(a ...any) (any, error) {
	m, _ := a[0].(map[string]any)
	s, _ := m["rgb"].(string)
	var r, g, b int64
	if n, _ := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); n != 3 {
		return map[string]any{"ok": false, "error": "颜色格式无效"}, nil
	}
	setWinBorderHex(r<<16 | g<<8 | b)
	return map[string]any{"ok": true}, nil
}

// 前端顶栏按钮绑定（同步即走，不做耗时事）
func winMin(_ ...any) (any, error) {
	showWindow.Call(appHwnd, swMinimize)
	return map[string]any{"ok": true}, nil
}

func winMax(_ ...any) (any, error) {
	// 用 ShowWindow 最大化/还原：系统自己管还原矩形，拖到屏幕顶贴边最大化也能正确还原
	z, _, _ := isZoomed.Call(appHwnd)
	if z != 0 {
		showWindow.Call(appHwnd, swRestore)
		return map[string]any{"ok": true, "max": false}, nil
	}
	showWindow.Call(appHwnd, swMaximize)
	return map[string]any{"ok": true, "max": true}, nil
}

func winClose(_ ...any) (any, error) {
	postMessageW.Call(appHwnd, wmClose, 0, 0)
	return map[string]any{"ok": true}, nil
}

// winDrag 前端在拖拽区 pointerdown 时调用 → 手动移动循环。
// WebView 的输入窗口 Chrome_RenderWidgetHostHWND 属于 msedgewebview2.exe
// 外部进程——跨进程不能子类化，鼠标命中永远到不了我们的 HTCAPTION 判定。
// 所以不依赖任何窗口消息：Go 侧直接轮询 GetCursorPos + SetWindowPos
// 跟随光标搬窗口，直到左键松开（GetAsyncKeyState）。
// 前端在调之前已确认按下点在 [data-drag] 且不在按钮/链接上。
var dragging atomic.Bool

func winDrag(_ ...any) (any, error) {
	if appHwnd == 0 {
		return map[string]any{"ok": false, "error": "window not ready"}, nil
	}
	if !dragging.CompareAndSwap(false, true) {
		return map[string]any{"ok": true}, nil // 已有移动循环在跑
	}
	go func() {
		defer dragging.Store(false)
		defer crashNote("winDrag")
		var p0 wPoint
		getCursorPos.Call(uintptr(unsafe.Pointer(&p0)))
		// 最大化状态拖动 = 还原后继续跟手（光标保持在标题栏的相对位置）
		if z, _, _ := isZoomed.Call(appHwnd); z != 0 {
			var mr wrect
			getWindowRect.Call(appHwnd, uintptr(unsafe.Pointer(&mr)))
			mw := mr.Right - mr.Left
			fx := float64(p0.X-mr.Left) / float64(mw)
			showWindow.Call(appHwnd, 9 /*SW_RESTORE*/)
			var nr wrect
			getWindowRect.Call(appHwnd, uintptr(unsafe.Pointer(&nr)))
			nw, nh := nr.Right-nr.Left, nr.Bottom-nr.Top
			nl := int32(float64(p0.X) - fx*float64(nw))
			nt := p0.Y - 16
			setWindowPos.Call(appHwnd, 0, uintptr(nl), uintptr(nt), 0, 0,
				swpNoSize|swpNoZorder|swpNoActivate)
			_ = nw
			_ = nh
		}
		var r0 wrect
		getWindowRect.Call(appHwnd, uintptr(unsafe.Pointer(&r0)))
		for {
			var p wPoint
			getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
			down, _, _ := getAsyncKeyState.Call(0x01 /*VK_LBUTTON*/)
			if down&0x8000 == 0 {
				break
			}
			setWindowPos.Call(appHwnd, 0,
				uintptr(r0.Left+p.X-p0.X), uintptr(r0.Top+p.Y-p0.Y),
				0, 0, swpNoSize|swpNoZorder|swpNoActivate)
			time.Sleep(7 * time.Millisecond)
		}
	}()
	return map[string]any{"ok": true}, nil
}

// openDataDir: JS `await openDataDir()` → 打开应用数据目录（记录/迁移账本/偏好所在）
func openDataDir(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	if err := exec.Command("explorer", s.Root).Start(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// dataDir: JS `await dataDir()` → 数据目录路径（设置页展示）
func dataDir(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": s.Root}, nil
}

// ---------- 彻底移除：清掉 AIClean 留在系统里的所有痕迹 ----------
// 计划任务 / 开机自启 / 残留区 / 数据目录 / 指向文件 / hwnd 文件。

// splitUninstallState 卸载前置检查（纯函数，可测）：
// 账本里 done!=true 的记录 = 已迁移且备份还在；bak 在盘上真实存在 → 需要用户决定去留。
func splitUninstallState(recs []map[string]any, exists func(string) bool) (migrated, baks []map[string]any) {
	for _, m := range recs {
		if m["done"] == true {
			continue
		}
		src, _ := m["src"].(string)
		migrated = append(migrated, map[string]any{"src": src, "dst": m["dst"], "size": m["size"]})
		if bak, _ := m["bak"].(string); bak != "" && exists(bak) {
			baks = append(baks, map[string]any{"path": bak, "src": src, "size": m["size"]})
		}
	}
	return
}

// uninstallCheck: JS `await uninstallCheck()` → {migrated:[...], baks:[...]}
func uninstallCheck(_ ...any) (any, error) {
	if storePinnedMissing != "" {
		vol := strings.TrimSuffix(filepath.VolumeName(storePinnedMissing), ":")
		return map[string]any{"ok": false, "error": fmt.Sprintf("数据目录所在的 %s 盘未连接，请连接后再移除，否则迁移记录无法检查", vol)}, nil
	}
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	migrated, baks := splitUninstallState(s.MigListAll(), func(p string) bool {
		_, e := os.Lstat(p)
		return e == nil
	})
	return map[string]any{"ok": true, "migrated": migrated, "baks": baks}, nil
}

// planBakDeletion 只校验不删除：账本未完成记录逐个过 checkBakDeletable，
// 分出"可删"和"必须留下（原因）"两组——卸载流程要先算清谁保留，再写清单、再动手。
func planBakDeletion(s *ops.Store) (toDelete []string, skipped []map[string]any) {
	for _, r := range s.MigListAll() {
		if r["done"] == true {
			continue
		}
		bak, _ := r["bak"].(string)
		if bak == "" {
			continue
		}
		if _, _, err := checkBakDeletable(s, bak); err != nil {
			skipped = append(skipped, map[string]any{"path": bak, "reason": err.Error(), "src": r["src"], "size": r["size"]})
			continue
		}
		toDelete = append(toDelete, bak)
	}
	return
}

// uninstallDeleteBaks 逐个校验后删迁移备份（与 finishMigration 同一套 checkBakDeletable）：
// dst 不可达 / junction 不在 / 账本无记录 → 跳过并记下原因，绝不盲删唯一副本。
func uninstallDeleteBaks(s *ops.Store) (skipped []map[string]any) {
	toDelete, skipped := planBakDeletion(s)
	for _, bak := range toDelete {
		os.RemoveAll(bak)
	}
	return skipped
}

// delayedRmDir 进程退出后删目录：SysProcAttr.CmdLine 整串传 cmd（Go 参数转义会把
// 内嵌引号变成 \"，cmd 不认——实测普通和带空格路径都删不掉）。emptyParent 非空时
// 追加 `rd "父目录"`（不带 /s，仅在父目录已空时生效）。
func delayedRmDir(dir, emptyParent string) {
	line := `cmd /C ping 127.0.0.1 -n 4 >nul & rd /s /q "` + dir + `"`
	if emptyParent != "" {
		line += ` & rd "` + emptyParent + `"`
	}
	c := exec.Command("cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true}
	_ = c.Start()
}

// 可注入的目录解析：KnownFolderPath（系统真实位置，OneDrive 接管/改位置都准）
// 和 exe 所在目录——测试时换掉它们来模拟写不进去的场景。
var knownFolderPath = func(id *windows.KNOWNFOLDERID) (string, error) {
	return windows.KnownFolderPath(id, 0)
}
var exeParentDir = func() (string, error) {
	p, e := os.Executable()
	if e != nil {
		return "", e
	}
	return filepath.Dir(p), nil
}

// keptListDirs 清单候选目录：桌面 → 文档 → exe 所在目录（依次尝试第一个能写的）
func keptListDirs() []string {
	var out []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Desktop, windows.FOLDERID_Documents} {
		if d, e := knownFolderPath(id); e == nil && d != "" {
			out = append(out, d)
		}
	}
	if d, e := exeParentDir(); e == nil && d != "" {
		out = append(out, d)
	}
	return out
}

// writeKeptDataList 用户保留的迁移/备份写一份清单（桌面→文档→exe 目录依次尝试），
// 避免卸载后忘了哪些数据在哪。三处都写不进去返回 error，调用方必须中止卸载。
func writeKeptDataList(migrated, baks []map[string]any) (string, error) {
	if len(migrated) == 0 && len(baks) == 0 {
		return "", nil
	}
	var sb strings.Builder
	sb.WriteString("AIClean 卸载时你选择保留的内容\n")
	sb.WriteString("生成时间：" + time.Now().Format("2006-01-02 15:04:05") + "\n\n")
	if len(migrated) > 0 {
		sb.WriteString("【已迁移的数据】这些目录的真实数据在目标位置，原位置是链接，软件照常使用。\n")
		sb.WriteString("注意：不要手动删除 目标盘:\\AIClean-Moved 目录！\n")
		for _, m := range migrated {
			fmt.Fprintf(&sb, "  原位置: %v\n  数据在: %v\n  大小:   %v\n\n", m["src"], m["dst"], m["size"])
		}
	}
	if len(baks) > 0 {
		sb.WriteString("【保留的迁移备份】这些是 C 盘上的完整副本，确认对应软件没问题后可以手动删除。\n")
		for _, b := range baks {
			fmt.Fprintf(&sb, "  备份: %v（原属于 %v，%v）\n", b["path"], b["src"], b["size"])
		}
		sb.WriteString("\n")
	}
	sb.WriteString("如果以后想迁回：把“数据在”列的目录整个复制回原位置（先删掉原位置的链接）。\n")
	var lastErr error
	for _, dir := range keptListDirs() {
		p := filepath.Join(dir, "AIClean-保留的迁移数据.txt")
		if e := os.WriteFile(p, []byte(sb.String()), 0o644); e == nil {
			return p, nil
		} else {
			lastErr = e
		}
	}
	return "", fmt.Errorf("所有候选位置都写不进去: %w", lastErr)
}

// uninstallRun: JS `await uninstallRun({deleteBaks:bool})`
// 顺序：守卫 → 算保留项 → 写桌面清单（写不出→整个中止，先删账本再写不出清单绝不允许）→
// 删计划任务/自启 → 安全删备份(可选) → 清残留区 → 删数据目录 → 删指向/hwnd 文件。
func uninstallRun(a ...any) (any, error) {
	if storePinnedMissing != "" {
		vol := strings.TrimSuffix(filepath.VolumeName(storePinnedMissing), ":")
		return map[string]any{"ok": false, "error": fmt.Sprintf("数据目录所在的 %s 盘未连接，请连接后再移除，否则迁移记录无法检查", vol)}, nil
	}
	m, _ := a[0].(map[string]any)
	deleteBaks, _ := m["deleteBaks"].(bool)
	res := map[string]any{"ok": true}
	s, serr := mustStore()
	var toDelete []string
	if serr == nil {
		// 保留项 = 用户选择留下的（仍迁移的数据全部保留；备份按 deleteBaks 决定）
		keptMig, keptBaks := splitUninstallState(s.MigListAll(), func(p string) bool {
			_, e := os.Lstat(p)
			return e == nil
		})
		if deleteBaks {
			var skipped []map[string]any
			toDelete, skipped = planBakDeletion(s) // 先算谁能删谁会留，清单按这个结果写
			keptBaks = skipped
			if len(skipped) > 0 {
				res["skipped"] = skipped
			}
		}
		// 有保留项就必须先落清单——写不出来宁可中止，不留"账本没了清单也没了"的局面
		if len(keptMig)+len(keptBaks) > 0 {
			lp, e := writeKeptDataList(keptMig, keptBaks)
			if e != nil {
				return map[string]any{"ok": false, "error": "无法生成保留数据清单，已取消移除"}, nil
			}
			if lp != "" {
				res["keptList"] = lp
			}
		}
	}
	// ↓ 从这里开始才是破坏动作
	exec.Command("schtasks", "/Delete", "/TN", schedTaskName, "/F").Run()
	runKeyDelete()
	for _, bak := range toDelete {
		os.RemoveAll(bak)
	}
	if serr == nil {
		os.RemoveAll(filepath.Join(s.Root, "quarantine")) // 残留区清空
		s.LogApp("system", "AIClean", "彻底移除", 0, true, "计划任务/自启/残留已删，数据目录随后清空")
		// webview 目录被本进程占用：退出后由延迟 cmd 删；父目录空了顺手删
		delayedRmDir(filepath.Join(s.Root, "webview"), s.Root)
		if ents, e := os.ReadDir(s.Root); e == nil {
			for _, en := range ents {
				if en.Name() != "webview" {
					os.RemoveAll(filepath.Join(s.Root, en.Name()))
				}
			}
		}
	}
	os.Remove(storePointerFile())
	os.Remove(hwndFile())
	if fb := filepath.Join(os.Getenv("LOCALAPPDATA"), "AIClean"); fb != "" {
		delayedRmDir(filepath.Join(fb, "webview"), fb)
		if ents, e := os.ReadDir(fb); e == nil {
			for _, en := range ents {
				if en.Name() != "webview" {
					os.RemoveAll(filepath.Join(fb, en.Name()))
				}
			}
		}
	}
	return res, nil
}

// openLogsDir: JS `await openLogsDir()` → 打开日志目录（ops.jsonl + crash.log）
func openLogsDir(_ ...any) (any, error) {
	s, err := mustStore()
	if err != nil {
		return nil, err
	}
	d := filepath.Join(s.Root, "logs")
	os.MkdirAll(d, 0o755)
	if err := exec.Command("explorer", d).Start(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// quitApp: JS `await quitApp()` → 退出（彻底移除流程的最后一步）
func quitApp(_ ...any) (any, error) {
	go func() {
		defer crashNote("quitApp")
		time.Sleep(400 * time.Millisecond)
		if appWin != nil {
			appWin.Terminate()
		}
		os.Exit(0)
	}()
	return map[string]any{"ok": true}, nil
}

// openPath: JS `await openPath({path})` → 资源管理器打开并选中该路径
func openPath(a ...any) (any, error) {
	if len(a) < 1 {
		return nil, fmt.Errorf("missing args")
	}
	m, _ := a[0].(map[string]any)
	path, _ := m["path"].(string)
	if path == "" {
		return map[string]any{"ok": false, "error": "path 为空"}, nil
	}
	if err := exec.Command("explorer", "/select,", filepath.Clean(path)).Start(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// openUrl: JS `await openUrl({url})` → 系统默认浏览器打开（仅允许 http/https）
func openUrl(a ...any) (any, error) {
	m, _ := a[0].(map[string]any)
	u, _ := m["url"].(string)
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return map[string]any{"ok": false, "error": "只允许 http(s) 链接"}, nil
	}
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// bind 把 Go 函数绑成异步桥接：JS 调用时最后一个参数是请求 id。
// 绑定立即返回（不阻塞 WebView2 UI 线程），真正的结果在 goroutine 完成后
// 通过 w.Dispatch → window.__aicDone(id, {v|e}) 送回 JS。
func bind(w webview2.WebView, name string, fn func(...any) (any, error)) {
	_ = w.Bind(name, func(a ...any) (any, error) {
		var id string
		if n := len(a); n > 0 {
			if s, ok := a[n-1].(string); ok && strings.HasPrefix(s, "__aic:") {
				id, a = s, a[:n-1]
			}
		}
		if id == "" {
			// 非 __aicDone 协议的调用（理论上前端总会带 id）：同步执行兜底
			return fn(a...)
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					writeCrash(crashLogPath(), "bind.dispatch", r, debug.Stack())
					b, _ := json.Marshal(map[string]any{"e": fmt.Sprintf("内部错误: %v", r)})
					evalJS(fmt.Sprintf("window.__aicDone && window.__aicDone(%s,%s)", mustJS(id), string(b)))
				}
			}()
			v, err := fn(a...)
			payload := map[string]any{"v": v}
			if err != nil {
				payload["e"] = err.Error()
			}
			b, _ := json.Marshal(payload)
			evalJS(fmt.Sprintf("window.__aicDone && window.__aicDone(%s,%s)", mustJS(id), string(b)))
		}()
		return nil, nil
	})
}

func mustJS(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// evalJS 在 UI 线程安全执行一段 JS；窗口已销毁/不可用时静默忽略（panic-safe）
func evalJS(js string) {
	if appWin == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			writeCrash(crashLogPath(), "evalJS.dispatch", r, debug.Stack())
		}
	}()
	appWin.Dispatch(func() {
		defer func() {
			if r := recover(); r != nil {
				writeCrash(crashLogPath(), "evalJS.eval", r, debug.Stack())
			}
		}()
		appWin.Eval(js)
	})
}

func must16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// ---------- 崩溃日志：panic 不落空 ----------
// 数据目录可用 → <root>\logs\crash.log；不可用 → %TEMP%\AIClean-crash.log。
// 超过 1MB 只保留最后 200KB（老的截掉）。

type osVersionInfoEx struct {
	dwOSVersionInfoSize                           uint32
	dwMajorVersion, dwMinorVersion, dwBuildNumber uint32
	dwPlatformID                                  uint32
	szCSDVersion                                  [128]uint16
	wServicePackMajor, wServicePackMinor          uint16
	wSuiteMask                                    uint16
	wProductType                                  byte
	wReserved                                     byte
}

func winVersion() string {
	var v osVersionInfoEx
	v.dwOSVersionInfoSize = uint32(unsafe.Sizeof(v))
	if r, _, _ := rtlGetVersion.Call(uintptr(unsafe.Pointer(&v))); r != 0 {
		return "未知"
	}
	return fmt.Sprintf("%d.%d.%d", v.dwMajorVersion, v.dwMinorVersion, v.dwBuildNumber)
}

func crashLogPath() string {
	if s, e := mustStore(); e == nil && s != nil {
		return filepath.Join(s.Root, "logs", "crash.log")
	}
	return filepath.Join(os.Getenv("TEMP"), "AIClean-crash.log")
}

// writeCrash 追加一条崩溃记录；path 由调用方给出（可测）。
func writeCrash(path, source string, r any, stack []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
		return e
	}
	var old []byte
	if b, e := os.ReadFile(path); e == nil {
		old = b
	}
	block := fmt.Sprintf("\n===== %s | AIClean %s | Windows %s | admin=%v | args=%q\nsource: %s\npanic: %v\n%s",
		time.Now().Format("2006-01-02 15:04:05"), Version, winVersion(), isAdmin(), os.Args, source, r, stack)
	buf := append(old, []byte(block)...)
	const maxB, keep = 1 << 20, 200 << 10 // 1MB 上限 → 留尾部 200KB
	if len(buf) > maxB {
		buf = append([]byte("……（更早的崩溃记录已截断）……\n"), buf[len(buf)-keep:]...)
	}
	return os.WriteFile(path, buf, 0o644)
}

// crashNote：defer 包一层——goroutine panic 写日志后按原流程结束（不再静默闪退）
func crashNote(source string) {
	if r := recover(); r != nil {
		writeCrash(crashLogPath(), source, r, debug.Stack())
	}
}
