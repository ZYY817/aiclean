// Package migrate 实现可靠迁移：复制校验 → 源改名备份 → 建 junction → 验证，
// 任一步失败自动回滚；bak 保留供用户确认后清理（V1.1.1 §8 决策表）。
package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"aiclean/internal/junction"
)

var (
	k32             = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFree = k32.NewProc("GetDiskFreeSpaceExW")
	procGetVolInfo  = k32.NewProc("GetVolumeInformationW")
)

const fileAttrReparse = 0x400

// Record 一次迁移的完整记录（供回滚与报告）
type Record struct {
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	Bak     string `json:"bak"`
	Link    string `json:"link"`
	Size    int64  `json:"size"`
	Files   int64  `json:"files"`
	At      string `json:"at"`
	RolledB bool   `json:"rolledBack"`
}

// Preflight 迁移前检查：返回可执行性结论
func Preflight(src, dstRoot string) []Check {
	var out []Check
	st, err := os.Lstat(src)
	out = append(out, Check{"源目录存在", err == nil && st.IsDir(), ""})
	isRp := err == nil && (st.Mode()&os.ModeSymlink != 0 ||
		func() bool {
			s, ok := st.Sys().(*syscall.Win32FileAttributeData)
			return ok && s.FileAttributes&fileAttrReparse != 0
		}())
	if isRp {
		out = append(out, Check{"源不是 junction/symlink", false, "已是链接/云占位，拒绝迁移"})
	} else {
		out = append(out, Check{"源不是 junction/symlink", err == nil, ""})
	}
	sz, _ := dirStat(src)
	free := freeBytes(dstRoot)
	out = append(out, Check{fmt.Sprintf("目标盘可用 %.1f GB ≥ 所需 %.1f GB", float64(free)/1e9, float64(sz)/1e9), free >= uint64(sz), ""})
	out = append(out, Check{"目标盘为 NTFS", isNTFS(dstRoot), "仅 NTFS 支持目录联结"})
	out = append(out, Check{"目标位置未被占用", !exists(dstRoot), ""})
	// 源目录内部的 junction/symlink/云占位：复制与校验都会跳过它们，
	// 删备份后这些链接永久丢失（如 pnpm node_modules）——必须整体拒绝
	if links := findLinks(src, 4); len(links) > 0 {
		out = append(out, Check{"源内无链接/云占位文件", false,
			fmt.Sprintf("含 %d+ 个链接（如 %s），迁移会丢失它们，请先解除链接", len(links), links[0])})
	} else {
		out = append(out, Check{"源内无链接/云占位文件", true, ""})
	}
	return out
}

// findLinks 列出 src 内部的 junction/symlink/reparse 项（不含 src 自身），最多 n 个
func findLinks(src string, n int) []string {
	var out []string
	filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == src || len(out) >= n {
			if len(out) >= n {
				return filepath.SkipAll
			}
			return nil
		}
		fi, e := d.Info()
		if e != nil {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			out = append(out, p)
			return nil
		}
		if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
			out = append(out, p)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return out
}

type Check struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Msg  string `json:"msg,omitempty"`
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func freeBytes(root string) uint64 {
	var free uint64
	p, _ := syscall.UTF16PtrFromString(volumeRoot(root))
	procGetDiskFree.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), 0, 0)
	return free
}

func volumeRoot(p string) string {
	v := filepath.VolumeName(p)
	if v == "" {
		return p
	}
	return v + `\`
}

func isNTFS(p string) bool {
	root, _ := syscall.UTF16PtrFromString(volumeRoot(p))
	var fs [64]uint16
	r, _, _ := procGetVolInfo.Call(
		uintptr(unsafe.Pointer(root)), 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&fs[0])), uintptr(len(fs)))
	return r != 0 && strings.EqualFold(syscall.UTF16ToString(fs[:]), "NTFS")
}

func dirStat(root string) (size, files int64) {
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root {
			if fi, e := d.Info(); e == nil {
				if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if fi, e := d.Info(); e == nil && !d.IsDir() {
			size += fi.Size()
			files++
		}
		return nil
	})
	return
}

// Progress 可选：复制阶段回调（已复制字节, 总字节）。供前端显示百分比。
var Progress func(done, total int64)

var copyDone, copyTotal int64

// Migrate 执行带验证与回滚的迁移。
// 步骤：preflight → 复制 → 逐数校验 → src→bak → 建 junction → 验证 → 成功。
// 失败路径：① 复制/校验失败 → 删目标残余，源不动；② junction 失败 → bak 改回 src，删目标。
func Migrate(src, dstDir string) (*Record, error) {
	rec := &Record{Src: src, Dst: dstDir, At: time.Now().Format("2006-01-02 15:04:05")}
	for _, c := range Preflight(src, dstDir) {
		if !c.OK {
			return rec, fmt.Errorf("预检失败: %s %s", c.Name, c.Msg)
		}
	}
	size, files := dirStat(src)
	rec.Size, rec.Files = size, files
	copyDone, copyTotal = 0, size
	// 1. 复制（不跟随 reparse）
	if err := copyTree(src, dstDir); err != nil {
		os.RemoveAll(dstDir)
		return rec, fmt.Errorf("复制失败: %w", err)
	}
	// 2. 校验计数+字节
	sz2, f2 := dirStat(dstDir)
	if sz2 != size || f2 != files {
		os.RemoveAll(dstDir)
		return rec, fmt.Errorf("校验不一致 (%d/%d → %d/%d)，已回滚", size, files, sz2, f2)
	}
	// 3. src → bak
	bak := src + ".aiclean-bak-" + time.Now().Format("20060102150405")
	rec.Bak = bak
	if err := os.Rename(src, bak); err != nil {
		os.RemoveAll(dstDir)
		return rec, fmt.Errorf("源改名失败: %w", err)
	}
	rollback := func(e error) (*Record, error) {
		rec.RolledB = true
		// 切换窗口期内应用可能往空载体写了新文件——并入备份，不能直接删
		if ents, rerr := os.ReadDir(src); rerr == nil {
			for _, en := range ents {
				to := filepath.Join(bak, en.Name())
				if _, x := os.Lstat(to); x == nil {
					to += ".merged" // 与备份重名：两份都留
				}
				os.Rename(filepath.Join(src, en.Name()), to)
			}
		}
		os.RemoveAll(src)
		os.Rename(bak, src)
		return rec, e
	}
	// 4. 建空载体 + junction
	if err := os.MkdirAll(src, 0o755); err != nil {
		return rollback(fmt.Errorf("创建载体失败: %w", err))
	}
	if _, err := junction.Create(src, dstDir); err != nil {
		return rollback(fmt.Errorf("junction 创建失败: %w", err))
	}
	// 5. 验证 junction 指向目标
	info, err := junction.Read(src)
	if err != nil || !strings.EqualFold(info.TargetWin, filepath.Clean(dstDir)) {
		junction.Remove(src, dstDir)
		return rollback(fmt.Errorf("junction 验证失败"))
	}
	rec.Link = src
	return rec, nil
}

// copyTree 与 ops.copyTree 同约束：不跟随 reparse；跳过非常规文件
// （文件级 symlink/云占位/命名管道等——跟随会复制链接目标内容或触发云端下载）
func copyTree(src, dst string) error {
	buf := make([]byte, copyChunk)
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			if p != src {
				if fi, e := d.Info(); e == nil {
					if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
						return filepath.SkipDir
					}
				}
			}
			return os.MkdirAll(to, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // symlink/管道/设备等不复制
		}
		if fi, e := d.Info(); e == nil {
			if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
				return nil // OneDrive 云占位等：不触发 hydrate
			}
		}
		if err := copyFile(p, to, buf); err != nil {
			return err
		}
		return nil
	})
}

const copyChunk = 8 << 20 // 8MB：大文件按块累计进度，几十 GB 的 vhdx 复制全程有上报

func copyFile(src, dst string, buf []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	for {
		nr, er := in.Read(buf)
		if nr > 0 {
			if _, ew := out.Write(buf[:nr]); ew != nil {
				out.Close()
				return ew
			}
			copyDone += int64(nr)
			if Progress != nil {
				Progress(copyDone, copyTotal)
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			out.Close()
			return er
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	si, _ := in.Stat()
	os.Chtimes(dst, si.ModTime(), si.ModTime())
	return nil
}

// dirManifest 逐文件清单：相对路径 → [大小, mtime]。copyFile 会保留 mtime，
// 没被改写过的文件 mtime 必然相同——同尺寸改写（SQLite/聊天库原地更新）靠它抓住。
func dirManifest(root string) map[string][2]int64 {
	out := map[string][2]int64{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, e := d.Info()
		if e != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return nil
		}
		out[strings.ToLower(rel)] = [2]int64{fi.Size(), fi.ModTime().UnixNano()}
		return nil
	})
	return out
}

// sameContent 两棵目录树逐文件一致（相对路径+大小+mtime 全等）。
// 总量相等不够——同长度覆盖写会骗过 dirStat 比对。
func sameContent(a, b string) bool {
	ma, mb := dirManifest(a), dirManifest(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if w, ok := mb[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// RollbackPreflight 迁回前评估：是否需要跨盘复制、需要多少空间、目标盘可用空间。
// fastPath=true 表示备份还在且逐文件未被改动 → 直接改名回位，不需要额外空间。
func RollbackPreflight(rec *Record) (need, free int64, fastPath bool) {
	dstOK, bakOK := exists(rec.Dst), exists(rec.Bak)
	if bakOK && dstOK && sameContent(rec.Bak, rec.Dst) {
		return 0, 0, true
	}
	src := rec.Link
	dataFrom := rec.Dst
	if !dstOK {
		dataFrom = rec.Bak
	}
	if !dstOK && !bakOK {
		return 0, 0, false
	}
	if strings.EqualFold(filepath.VolumeName(dataFrom), filepath.VolumeName(src)) {
		return 0, 0, true // 同卷改名，不占额外空间
	}
	need, _ = dirStat(dataFrom)
	free = int64(freeBytes(src))
	return need, free, false
}

// Rollback 撤销已完成的迁移：数据移回原位 → 删 junction → 删 bak。
// 目标已被外部删除时改从 bak 还原；两边都没数据时拒绝（不能删 bak 造成全丢）。
// 跨盘场景 os.Rename(目录) 必败（MoveFileEx 限制），走复制→校验→删源。
// 顺序保证任何一步失败都可无损还原：junction 先改名暂存而不是先拆。
func Rollback(rec *Record) error {
	if !junction.IsJunction(rec.Link) {
		return fmt.Errorf("联结不存在或已被替换: %s", rec.Link)
	}
	dstOK, bakOK := exists(rec.Dst), exists(rec.Bak)
	if !dstOK && !bakOK {
		return fmt.Errorf("目标与备份都不存在，无法迁回（数据仍在 %s 的链接后，请检查目标盘）", rec.Dst)
	}
	src := rec.Link
	// junction 改名暂存：数据落位前随时能挪回来，失败不留断口
	tmp := src + ".aitmp-junc"
	if exists(tmp) {
		if err := junction.Remove(tmp, rec.Dst); err != nil {
			if rerr := os.Remove(tmp); rerr != nil {
				return fmt.Errorf("上次残留的临时联结无法清理: %s", tmp)
			}
		}
	}
	if err := os.Rename(src, tmp); err != nil {
		return fmt.Errorf("暂存联结失败: %w", err)
	}
	restoreLink := func() {
		os.RemoveAll(src) // src 只可能是空载体或半成品副本（原件仍在 dataFrom）
		os.Rename(tmp, src)
	}
	dataFrom := rec.Dst
	if !dstOK {
		dataFrom = rec.Bak
	}
	// 快速路径：备份还在且与目标盘逐文件一致（路径+大小+mtime）→
	// 直接把备份改回原名。同尺寸改写过的文件不能走这条路——会丢迁移后写入的数据
	if bakOK && dstOK && sameContent(rec.Bak, rec.Dst) {
		if err := os.Rename(rec.Bak, src); err != nil {
			restoreLink()
			return fmt.Errorf("备份回位失败（数据仍在 %s）: %w", rec.Bak, err)
		}
		dataFrom = ""
	}
	if dataFrom != "" {
		if strings.EqualFold(filepath.VolumeName(dataFrom), filepath.VolumeName(src)) {
			if err := os.Rename(dataFrom, src); err != nil {
				restoreLink()
				return fmt.Errorf("回移失败（数据仍在 %s）: %w", dataFrom, err)
			}
		} else {
			// 跨盘复制需要 C 盘留出一整份空间——C 盘快满时提前拒绝，不做半截迁移
			need, _ := dirStat(dataFrom)
			if free := freeBytes(src); free < uint64(need) {
				restoreLink()
				return fmt.Errorf("C 盘空间不足：迁回需要 %.1f GB 可用，当前只有 %.1f GB（数据仍在 %s）",
					float64(need)/1e9, float64(free)/1e9, dataFrom)
			}
			copyDone, copyTotal = 0, need // 进度计数归零：否则带上次迁移的余量
			if err := copyTree(dataFrom, src); err != nil {
				restoreLink()
				return fmt.Errorf("跨盘复制失败（数据仍在 %s）: %w", dataFrom, err)
			}
			sz1, f1 := dirStat(dataFrom)
			sz2, f2 := dirStat(src)
			if sz1 != sz2 || f1 != f2 {
				restoreLink()
				return fmt.Errorf("跨盘校验不一致 (%d/%d → %d/%d)，数据仍在 %s", sz1, f1, sz2, f2, dataFrom)
			}
			os.RemoveAll(dataFrom)
		}
	}
	// 数据已回 src：删掉暂存的 junction 载体（只验目标字符串，dst 不在也能删）
	if err := junction.Remove(tmp, rec.Dst); err != nil {
		os.RemoveAll(tmp) // 极端兜底：验证不过也只删载体，不递归
	}
	if dstOK {
		os.RemoveAll(rec.Dst) // 目标盘副本在迁回后必然冗余（含快速路径跳过的复制）
	}
	if bakOK {
		os.RemoveAll(rec.Bak)
	}
	return nil
}
