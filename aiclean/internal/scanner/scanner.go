// Package scanner 实现规则加载与真实目录扫描。
// 安全约束（V1.1.1）：跳过 reparse point 防环回与重复计数；
// 嵌套规则路径去重；进程运行中的项标记 locked 不操作。
package scanner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"aiclean/internal/junction"
	"aiclean/internal/model"
)

const fileAttrReparse = 0x400 // FILE_ATTRIBUTE_REPARSE_POINT

// LoadRules 解析 rules.json。v1 阶段未验证签名的规则全部按原 risk 处理，
// 但 P2 正式版：无签名 → 全部 review（见 V1.1.1 §5.2）。
func LoadRules(path string) ([]model.Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadRulesData(b)
}

// heur 当前规则文件的启发式配置（LoadRules/LoadRulesData 时一并载入；
// 外部 rules.json 优先的策略下始终跟实际生效的规则文件走）
var heur *model.Heuristics

// LoadRulesData 从字节解析规则（供 go:embed 内嵌规则文件使用）
func LoadRulesData(b []byte) ([]model.Rule, error) {
	var rf model.RuleFile
	if err := json.Unmarshal(b, &rf); err != nil {
		return nil, err
	}
	if len(rf.Heuristics.DeletePatterns)+len(rf.Heuristics.UserDataPatterns) > 0 {
		heur = &rf.Heuristics
	} else {
		heur = nil
	}
	return rf.Rules, nil
}

// expandEnv 展开 Windows %NAME% 形式环境变量（os.Expand 只认 $NAME）。
// 深度上限防止自引用变量（A=%A%）死循环。
func expandEnv(p string) string {
	for depth := 0; depth < 10; depth++ {
		i := strings.IndexByte(p, '%')
		if i < 0 {
			return p
		}
		j := strings.IndexByte(p[i+1:], '%')
		if j < 0 {
			return p
		}
		name := p[i+1 : i+1+j]
		p = p[:i] + os.Getenv(name) + p[i+1+j+1:]
	}
	return p
}

// isReparse 判断目录项是否为 reparse point（junction/symlink/云占位符）
func isReparse(d os.DirEntry) bool {
	if d.Type()&os.ModeSymlink != 0 {
		return true
	}
	fi, err := d.Info()
	if err != nil {
		return false
	}
	sys, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && sys.FileAttributes&fileAttrReparse != 0
}

// dirSize 递归统计大小/文件数/最新修改；不跟随 reparse point
func dirSize(root string) (size, files int64, lastMod time.Time, skipped int) {
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if Cancelled() {
			return errScanCancel
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && isReparse(d) {
				skipped++
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		size += fi.Size()
		files++
		if fi.ModTime().After(lastMod) {
			lastMod = fi.ModTime()
		}
		return nil
	})
	return
}

var (
	k32             = syscall.NewLazyDLL("kernel32.dll")
	procSnapshot    = k32.NewProc("CreateToolhelp32Snapshot")
	procFirst       = k32.NewProc("Process32FirstW")
	procNext        = k32.NewProc("Process32NextW")
	procCloseHandle = k32.NewProc("CloseHandle")
)

type processEntry32 struct {
	Size      uint32
	Usage     uint32
	ProcessID uint32
	HeapID    uintptr
	ModuleID  uint32
	Threads   uint32
	ParentPID uint32
	PriClass  int32
	Flags     uint32
	ExeFile   [260]uint16
}

// runningProcs 返回运行中进程 exe 名集合（小写）
func runningProcs() map[string]bool {
	out := map[string]bool{}
	h, _, _ := procSnapshot.Call(0x2, 0) // TH32CS_SNAPPROCESS
	if h == ^uintptr(0) {
		return out
	}
	defer procCloseHandle.Call(h)
	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	r, _, _ := procFirst.Call(h, uintptr(unsafe.Pointer(&pe)))
	for r != 0 {
		name := strings.ToLower(syscall.UTF16ToString(pe.ExeFile[:]))
		out[strings.TrimSuffix(name, ".exe")] = true
		r, _, _ = procNext.Call(h, uintptr(unsafe.Pointer(&pe)))
	}
	return out
}

// procMatch 匹配 requiresClosed 进程名：procs 已按小写+去 .exe 存储，
// 规则里写不写 ".exe" 都要能命中（曾经 .exe 后缀导致 ~20 条规则锁定失效）。
func procMatch(procs map[string]bool, name string) bool {
	return procs[strings.TrimSuffix(strings.ToLower(name), ".exe")]
}

// ProcsRunning 返回 names 里当前正在运行的进程（供操作执行前复查：
// 扫描时刻没运行的程序可能在用户删除/迁移时已经启动）。
func ProcsRunning(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	procs := runningProcs()
	var out []string
	for _, n := range names {
		if procMatch(procs, n) {
			out = append(out, n)
		}
	}
	return out
}

// Admin 当前进程是否以管理员身份运行（main 启动时赋值）
var Admin bool

// Progress 可选：每处理完一条规则回调 (done, total, app)。供前端显示真实进度。
var Progress func(done, total int, app string)

// 扫描取消：前端 cancelScan → RequestCancel；Scan 与 dirSize 检查并尽快退出
var cancelFlag atomic.Int32

func RequestCancel()  { cancelFlag.Store(1) }
func ResetCancel()    { cancelFlag.Store(0) }
func Cancelled() bool { return cancelFlag.Load() != 0 }

var errScanCancel = errors.New("scan cancelled")

// Scan 执行真实扫描：展开规则路径 → 存在性检查 → 进程检查 → 统计。
// scannedRoots 去重：父路径已扫过的子规则路径跳过（防重复计数）。
func Scan(rules []model.Rule) []model.ScanItem {
	procs := runningProcs()
	type cand struct {
		r    model.Rule
		path string
	}
	var cands []cand
	var items []model.ScanItem
	seen := map[string]bool{}
	// requiresAdmin 且未提权 → 标记 locked，让前端显示"需要管理员"
	markAdmin := func(r model.Rule, it *model.ScanItem) {
		if r.RequiresAdmin && !Admin && it.State != "locked" {
			it.NeedsAdmin = true
			it.State, it.StatusTxt = "locked", "需要管理员"
		}
	}
	for _, r := range rules {
		if r.Action == "ignore" {
			continue
		}
		if r.RegKey != "" {
			items = append(items, scanRegRule(r, procs)...)
			continue
		}
		p := expandEnv(r.Path)
		// 通配符规则（如 %LOCALAPPDATA%\JetBrains\*\caches）展开成多条候选
		paths := []string{p}
		if strings.ContainsAny(p, "*?") {
			if m, _ := filepath.Glob(p); len(m) > 0 {
				paths = m
			} else {
				continue
			}
		}
		for _, p2 := range paths {
			st, err := os.Stat(p2)
			if err != nil {
				continue
			}
			// matchFile：目录里必须存在该文件才算命中（如 WSL LocalState 必须有 *.vhdx，
			// 否则 Packages\*\LocalState 会把所有 UWP 系统组件误匹配成"WSL 发行版"）
			if r.MatchFile != "" && st.IsDir() {
				if m, _ := filepath.Glob(filepath.Join(p2, r.MatchFile)); len(m) == 0 {
					continue
				}
			}
			isFile := !st.IsDir()
			abs := filepath.Clean(p2)
			low := strings.ToLower(abs)
			if seen[low] {
				continue
			}
			seen[low] = true
			if isFile {
				it := r.ToItem(abs, shortDir(abs))
				it.Size, it.Files, it.LastMod = st.Size(), 1, st.ModTime().Format("2006-01-02")
				for _, pn := range r.RequiresClosed { // 文件项也要查进程占用（sqlite 等）
					if procMatch(procs, pn) {
						it.LockedBy = append(it.LockedBy, pn)
					}
				}
				if len(it.LockedBy) > 0 {
					it.State, it.StatusTxt, it.RiskTxt = "locked", "暂不可操作", "运行中"
				}
				markAdmin(r, &it)
				items = append(items, it)
				continue
			}
			if junction.IsJunction(abs) {
				it := r.ToItem(abs, shortDir(abs))
				it.State, it.StatusTxt = "migrated", "已迁移"
				if info, err := junction.Read(abs); err == nil {
					it.MovedTo = info.TargetWin
				}
				items = append(items, it)
				continue
			}
			cands = append(cands, cand{r, abs})
		}
	}
	// 嵌套去重：父目录已包含的子规则路径不再单独出现
	sort.Slice(cands, func(i, j int) bool { return len(cands[i].path) < len(cands[j].path) })
	var roots []string
	done := 0
	for _, c := range cands {
		if Cancelled() {
			break
		}
		dup := false
		for _, root := range roots {
			if strings.HasPrefix(strings.ToLower(c.path), strings.ToLower(root)+`\`) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		// reportOnly 汇总目录 / scanHeuristics 启发式父目录不作为去重根：
		// 其下子项（独立规则或名字命中的目录）仍单独列出，
		// 父项显示尺寸在扫描后扣除这些子项，不会重复计数。
		if !c.r.ReportOnly && !c.r.ScanHeuristics {
			roots = append(roots, c.path)
		}
		it := c.r.ToItem(c.path, shortDir(c.path))
		// 进程占用检查
		for _, pn := range c.r.RequiresClosed {
			if procMatch(procs, pn) {
				it.LockedBy = append(it.LockedBy, pn)
			}
		}
		if len(it.LockedBy) > 0 {
			it.State, it.StatusTxt, it.RiskTxt = "locked", "暂不可操作", "运行中"
		} else if c.r.RequiresAdmin && !Admin {
			it.NeedsAdmin = true
			it.State, it.StatusTxt = "locked", "需要管理员"
		}
		size, files, lm, skipped := dirSize(c.path)
		it.Size, it.Files, it.Skipped = size, files, skipped
		if !lm.IsZero() {
			it.LastMod = lm.Format("2006-01-02")
		}
		if it.Prunable {
			it.PrunableSize = agedSize(c.path, it.PruneDays)
		}
		items = append(items, it)
		done++
		if Progress != nil {
			Progress(done, len(cands), it.App)
		}
	}
	// ---------- 启发式发现：scanHeuristics 目录下按名字模式列出子目录 ----------
	// deletePatterns → 可清理缓存（应用自动重建，标注"自动发现"）
	// userDataPatterns → 待确认（名字像用户数据/会话，让用户自己判断）
	// 其余子目录留在父项尺寸里不单独列出；reparse/文件/已存在路径跳过。
	if heur != nil {
		ruleByID := map[string]model.Rule{}
		for _, r := range rules {
			ruleByID[r.ID] = r
		}
		delSet, usrSet, ignSet := nameSet(heur.DeletePatterns), nameSet(heur.UserDataPatterns), nameSet(heur.IgnoreNames)
		existing := map[string]bool{}
		for _, it := range items {
			existing[strings.ToLower(it.Path)] = true
		}
		var extra []model.ScanItem
		for _, it := range items {
			r, ok := ruleByID[it.RuleID]
			if !ok || !r.ScanHeuristics || it.State == "migrated" {
				continue
			}
			ents, err := os.ReadDir(it.Path)
			if err != nil {
				continue
			}
			for _, en := range ents {
				if !en.IsDir() || isReparse(en) {
					continue
				}
				name := strings.ToLower(en.Name())
				if ignSet[name] {
					continue
				}
				cp := filepath.Join(it.Path, en.Name())
				cl := strings.ToLower(cp)
				if existing[cl] {
					continue
				}
				var ch model.ScanItem
				switch {
				case delSet[name]:
					ch = model.ScanItem{RuleID: it.RuleID + "::h", App: it.App, Dir: en.Name(), Path: cp,
						Note: "（自动发现）按目录名判断是缓存类目录，删除后应用会重建",
						Risk: "cache", RiskTxt: "低风险", Action: "clean", State: "active", StatusTxt: "可清理",
						Category: it.Category, Heuristic: true}
				case usrSet[name]:
					ch = model.ScanItem{RuleID: it.RuleID + "::h", App: it.App, Dir: en.Name(), Path: cp,
						Note: "（自动发现）按目录名判断像用户数据/会话目录，删除前请先确认",
						Risk: "review", RiskTxt: "待确认", Action: "none", State: "active", StatusTxt: "需确认",
						Category: it.Category, Heuristic: true}
				default:
					continue
				}
				ch.LockedBy, ch.NeedsAdmin, ch.RequiresClosed = it.LockedBy, it.NeedsAdmin, it.RequiresClosed
				if len(ch.LockedBy) > 0 {
					ch.State, ch.StatusTxt, ch.RiskTxt = "locked", "暂不可操作", "运行中"
				} else if ch.NeedsAdmin {
					ch.State, ch.StatusTxt = "locked", "需要管理员"
				}
				existing[cl] = true
				extra = append(extra, ch)
			}
		}
		for i := range extra {
			if Cancelled() {
				break
			}
			size, files, lm, skipped := dirSize(extra[i].Path)
			extra[i].Size, extra[i].Files, extra[i].Skipped = size, files, skipped
			if !lm.IsZero() {
				extra[i].LastMod = lm.Format("2006-01-02")
			}
		}
		items = append(items, extra...)
	}
	// 汇总目录显示尺寸扣除已单列子项（避免重复计数）
	for i := range items {
		if !items[i].ReportOnly {
			continue
		}
		pi := strings.ToLower(items[i].Path) + `\`
		var sub, sf int64
		for j := range items {
			if i != j && strings.HasPrefix(strings.ToLower(items[j].Path), pi) {
				sub += items[j].Size
				sf += items[j].Files
			}
		}
		if sub > 0 {
			items[i].Size -= sub
			if items[i].Size < 0 {
				items[i].Size = 0
			}
			items[i].Files -= sf
			if items[i].Files < 0 {
				items[i].Files = 0
			}
			items[i].Note += "（已扣除单列子项）"
		}
	}
	// 大的在前
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	return items
}

// nameSet 小写名字集合（启发式精确匹配用）
func nameSet(list []string) map[string]bool {
	s := make(map[string]bool, len(list))
	for _, n := range list {
		s[strings.ToLower(n)] = true
	}
	return s
}

// agedSize 统计目录中早于 days 天的文件总字节（不跟随 reparse）
func agedSize(root string, days int) (sz int64) {
	if days <= 0 {
		return 0
	}
	cut := time.Now().AddDate(0, 0, -days)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && isReparse(d) {
				return filepath.SkipDir
			}
			return nil
		}
		if fi, e := d.Info(); e == nil && fi.ModTime().Before(cut) {
			sz += fi.Size()
		}
		return nil
	})
	return
}

// shortDir 从绝对路径提取 "父目录\\目录" 短形式
func shortDir(abs string) string {
	dir := filepath.Base(filepath.Dir(abs))
	base := filepath.Base(abs)
	if dir == "." || dir == `\` || dir == "" {
		return base
	}
	return dir + `\` + base
}
