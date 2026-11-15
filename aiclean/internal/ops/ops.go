// Package ops 实现安全文件操作：隔离、恢复、永久删除、按龄清理、审计日志。
// V1.1.1 原则：清理=先隔离可回滚；绝不绕过校验直接 rm；reparse point 不跟随。
package ops

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const fileAttrReparse = 0x400

// Store 隔离区与日志的根目录。所有公共方法持 mu：
// 桥接 handler 跑在不同 goroutine，账本/偏好/日志的 read-modify-write 必须串行。
type Store struct {
	Root string // %LOCALAPPDATA%\AIClean
	mu   sync.Mutex
}

func NewStore() (*Store, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return nil, fmt.Errorf("LOCALAPPDATA 未设置")
	}
	return NewStoreAt(filepath.Join(base, "AIClean"))
}

// NewStoreAt 在指定根目录创建存储（供数据目录放到非 C 盘使用）
func NewStoreAt(root string) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("root 为空")
	}
	s := &Store{Root: root}
	for _, d := range []string{"quarantine", "logs"} {
		if err := os.MkdirAll(filepath.Join(s.Root, d), 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Meta 隔离项元数据（随目录持久化）
type Meta struct {
	ID        string `json:"id"`
	OrigPath  string `json:"origPath"`
	App       string `json:"app"`
	Size      int64  `json:"size"`
	Files     int64  `json:"files"`
	QuarAt    string `json:"quarAt"`
	RetainDay int    `json:"retainDays"`
	Note      string `json:"note"`
}

func (s *Store) quarDir() string { return filepath.Join(s.Root, "quarantine") }
func (s *Store) logPath() string { return filepath.Join(s.Root, "logs", "ops.jsonl") }

// Log 追加审计记录
func (s *Store) Log(op, path string, size int64, ok bool, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logAppL(op, "", path, size, ok, detail)
}

// LogApp 追加审计记录（带应用名）
func (s *Store) LogApp(op, app, path string, size int64, ok bool, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logAppL(op, app, path, size, ok, detail)
}

// logL/logAppL：已持锁的内部变体（供 Quarantine/Delete/Restore 等公共方法内嵌调用）
func (s *Store) logL(op, path string, size int64, ok bool, detail string) {
	s.logAppL(op, "", path, size, ok, detail)
}

var (
	logMaxBytes int64 = 2 << 20 // ops.jsonl 超过 ~2MB 触发轮转（var 供测试缩小阈值）
	logKeepN         = 5000    // 轮转后只保留最近 5000 行
)

func (s *Store) logAppL(op, app, path string, size int64, ok bool, detail string) {
	rec := map[string]any{"t": time.Now().Format("2006-01-02 15:04:05"), "op": op, "path": path, "size": size, "ok": ok}
	if app != "" {
		rec["app"] = app
	}
	if detail != "" {
		rec["detail"] = detail
	}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.Write(append(b, '\n'))
		st, _ := f.Stat()
		f.Close()
		if st != nil && st.Size() > logMaxBytes {
			s.rotateLogL()
		}
	}
}

// rotateLogL 日志轮转：只保留最近 logKeepN 行，防止 ops.jsonl 无限增长越读越慢
func (s *Store) rotateLogL() {
	b, err := os.ReadFile(s.logPath())
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) <= logKeepN {
		return
	}
	kept := strings.Join(lines[len(lines)-logKeepN:], "\n") + "\n"
	writeFileAtomic(s.logPath(), []byte(kept))
}

// Logs 读取审计日志（新→旧，限 n 条）：正序读完整体反转，不再每行前插（O(n²)→O(n)）
func (s *Store) Logs(n int) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.logPath())
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
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
		}
		if !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				size += fi.Size()
				files++
			}
		}
		return nil
	})
	return
}

// Quarantine 把目录移入隔离区：同盘 rename（原子）；跨盘 copy+校验→删源。
// 目标命名 qar_<时间戳>_<基名>，附 meta.json。
func (s *Store) Quarantine(src, app, note string, retainDays int) (*Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if retainDays <= 0 {
		retainDays = 30
	}
	st, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("不是目录: %s", src)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("拒绝隔离 reparse/symlink: %s", src)
	}
	if sys, ok := st.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
		return nil, fmt.Errorf("拒绝隔离 reparse/symlink: %s", src)
	}
	id := fmt.Sprintf("qar_%s_%06d_%s", time.Now().Format("20060102_150405"), time.Now().Nanosecond()/1000,
		strings.NewReplacer(`\`, "_", `/`, "_", `:`, "", " ", "_").Replace(filepath.Base(src)))
	dst := filepath.Join(s.quarDir(), id)
	if err := os.MkdirAll(s.quarDir(), 0o755); err != nil {
		return nil, err
	}
	size, files := dirStat(src)
	err = os.Rename(src, dst) // 同卷原子移动
	if err != nil {
		// 跨卷：复制 → 校验计数 → 删源
		if err = copyTree(src, dst); err != nil {
			os.RemoveAll(dst) // 不留无 meta 的半成品（List 看不到也清不掉）
			s.logL("quarantine", src, 0, false, err.Error())
			return nil, err
		}
		sz2, f2 := dirStat(dst)
		if sz2 != size || f2 != files {
			os.RemoveAll(dst)
			err = fmt.Errorf("跨卷复制校验不一致 (%d/%d → %d/%d)，已回滚", size, files, sz2, f2)
			s.logL("quarantine", src, size, false, err.Error())
			return nil, err
		}
		if err = os.RemoveAll(src); err != nil {
			s.logL("quarantine", src, size, false, "删除源失败: "+err.Error())
			return nil, fmt.Errorf("已复制到隔离区但删除源失败: %w", err)
		}
	}
	m := &Meta{ID: id, OrigPath: src, App: app, Size: size, Files: files, QuarAt: time.Now().Format("2006-01-02"), RetainDay: retainDays, Note: note}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if werr := os.WriteFile(filepath.Join(dst, ".aiclean.meta.json"), mb, 0o644); werr != nil {
		s.logL("quarantine", src, size, false, "元数据写入失败（隔离项将不可见）: "+werr.Error())
	}
	s.logL("quarantine", src, size, true, "→ "+dst)
	return m, nil
}

// copyTree 递归复制（不跟随 reparse；跳过非常规文件——文件级 symlink 会
// 复制到链接目标内容、云占位会触发 hydrate 下载、命名管道可能 hang）
func copyTree(src, dst string) error {
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
			return nil
		}
		if fi, e := d.Info(); e == nil {
			if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
				return nil
			}
		}
		return copyFile(p, to)
	})
}

func copyFile(src, dst string) error {
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
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	si, _ := in.Stat()
	os.Chtimes(dst, si.ModTime(), si.ModTime())
	return nil
}

// PurgeExpired 永久删除超过保留期的隔离项。启动时与自动清理都应调用——
// 不能只靠 List()（用户不打开残留页就永远不触发）。
func (s *Store) PurgeExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredL()
}

// purgeExpiredL 已持锁的内部变体
func (s *Store) purgeExpiredL() {
	ents, _ := os.ReadDir(s.quarDir())
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.quarDir(), e.Name(), ".aiclean.meta.json"))
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.RetainDay <= 0 {
			continue
		}
		t, err := time.Parse("2006-01-02", m.QuarAt)
		if err != nil || time.Since(t) <= time.Duration(m.RetainDay)*24*time.Hour {
			continue
		}
		os.RemoveAll(filepath.Join(s.quarDir(), e.Name()))
		s.logAppL("purge", m.App, m.OrigPath, m.Size, true, "超过保留期自动清理")
	}
}

// List 隔离区条目。超过保留期的条目顺手永久删除（retainDays 语义执行到位）。
func (s *Store) List() []Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredL()
	ents, _ := os.ReadDir(s.quarDir())
	var out []Meta
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.quarDir(), e.Name(), ".aiclean.meta.json"))
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		m.ID = e.Name()
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QuarAt > out[j].QuarAt })
	return out
}

// Restore 恢复隔离项到原路径（原路径已存在则拒绝，防覆盖）
func (s *Store) Restore(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dst := filepath.Join(s.quarDir(), id)
	if id == "" || dst == s.quarDir() || !strings.HasPrefix(dst, s.quarDir()+string(os.PathSeparator)) {
		return fmt.Errorf("非法路径")
	}
	var m Meta
	b, err := os.ReadFile(filepath.Join(dst, ".aiclean.meta.json"))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return fmt.Errorf("元数据缺失: %s", id)
	}
	if _, err := os.Lstat(m.OrigPath); err == nil {
		return fmt.Errorf("原路径已存在，拒绝覆盖: %s", m.OrigPath)
	}
	err = os.Rename(dst, m.OrigPath)
	if err != nil {
		os.MkdirAll(filepath.Dir(m.OrigPath), 0o755)
		if err = copyTree(dst, m.OrigPath); err != nil {
			os.RemoveAll(m.OrigPath) // 清掉半成品，否则下次还原被"已存在"挡死
			s.logL("restore", m.OrigPath, m.Size, false, err.Error())
			return err
		}
		os.Remove(filepath.Join(m.OrigPath, ".aiclean.meta.json")) // meta 不计入校验也不留在用户目录
		// 与 Quarantine 对称：校验文件数/字节一致才删隔离副本
		sz2, f2 := dirStat(m.OrigPath)
		if sz2 != m.Size || f2 != m.Files {
			os.RemoveAll(m.OrigPath)
			s.logL("restore", m.OrigPath, m.Size, false, fmt.Sprintf("跨卷复制校验不一致 (%d/%d → %d/%d)", m.Size, m.Files, sz2, f2))
			return fmt.Errorf("还原校验不一致，隔离副本已保留")
		}
		os.RemoveAll(dst)
	} else {
		os.Remove(filepath.Join(m.OrigPath, ".aiclean.meta.json"))
	}
	s.logL("restore", m.OrigPath, m.Size, true, "")
	return nil
}

// Purge 永久删除隔离项
func (s *Store) Purge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := filepath.Join(s.quarDir(), id)
	// 空 id / "." 会把 p 归一成隔离区根目录本身——前缀校验挡不住，必须单独拒绝
	if id == "" || p == s.quarDir() || !strings.HasPrefix(p, s.quarDir()+string(os.PathSeparator)) {
		return fmt.Errorf("非法路径")
	}
	// 日志记录原始路径和应用名，而不是隔离 id
	var m Meta
	origPath, app := id, ""
	if b, err := os.ReadFile(filepath.Join(p, ".aiclean.meta.json")); err == nil && json.Unmarshal(b, &m) == nil {
		origPath, app = m.OrigPath, m.App
	}
	sz, _ := dirStat(p)
	err := os.RemoveAll(p)
	s.logAppL("purge", app, origPath, sz, err == nil, "")
	return err
}

// Delete 一步永久删除目录或文件（不经过隔离区）。
// 拒绝 reparse/symlink 目录（那通常是已迁移的 junction）。
// 仅应作用于扫描结果中的已知路径 —— 由调用方校验。
func (s *Store) Delete(src, app string) (size int64, files int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := os.Lstat(src)
	if err != nil {
		s.logAppL("purge", app, src, 0, false, err.Error())
		return 0, 0, err
	}
	// reparse point（junction/symlink）在 Lstat 下 IsDir()==false，必须先于目录分支检查，
	// 否则 junction 会被当文件静默解链——已迁移数据失去入口。
	if fi, e := st.Sys().(*syscall.Win32FileAttributeData); e && fi.FileAttributes&fileAttrReparse != 0 {
		err = fmt.Errorf("拒绝删除 reparse/symlink: %s", src)
		s.logAppL("purge", app, src, 0, false, err.Error())
		return 0, 0, err
	}
	if st.IsDir() {
		if st.Mode()&os.ModeSymlink != 0 {
			err = fmt.Errorf("拒绝删除 reparse/symlink: %s", src)
			s.logAppL("purge", app, src, 0, false, err.Error())
			return 0, 0, err
		}
		if fi, e := st.Sys().(*syscall.Win32FileAttributeData); e && fi.FileAttributes&fileAttrReparse != 0 {
			err = fmt.Errorf("拒绝删除 reparse 目录: %s", src)
			s.logAppL("purge", app, src, 0, false, err.Error())
			return 0, 0, err
		}
		size, files = dirStat(src)
	} else {
		size, files = st.Size(), 1
	}
	err = os.RemoveAll(src)
	s.logAppL("purge", app, src, size, err == nil, "")
	return
}

// Prune 删除目录中早于 days 天的文件（不跟随 reparse）
func (s *Store) Prune(root string, days int) (delBytes int64, delFiles int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneAppL(root, days, "")
}

// PruneApp 同 Prune，日志带应用名
func (s *Store) PruneApp(root string, days int, app string) (delBytes int64, delFiles int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneAppL(root, days, app)
}

func (s *Store) pruneAppL(root string, days int, app string) (delBytes int64, delFiles int, err error) {
	cut := time.Now().AddDate(0, 0, -days)
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() {
			if p != root {
				if fi, er := d.Info(); er == nil {
					if sys, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && sys.FileAttributes&fileAttrReparse != 0 {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		fi, er := d.Info()
		if er == nil && fi.ModTime().Before(cut) && os.Remove(p) == nil {
			delBytes += fi.Size()
			delFiles++
		}
		return nil
	})
	s.logAppL("prune", app, root, delBytes, err == nil, fmt.Sprintf("%dd: %d 文件", days, delFiles))
	return
}

// ---------- 迁移账本：记录哪些迁移还留着 .aiclean-bak-* 备份 ----------

func (s *Store) migPath() string { return filepath.Join(s.Root, "migrations.json") }

// writeFileAtomic 先写临时文件再 Rename：写到一半崩溃不会留下半截坏文件
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// backupCorrupt JSON 解析失败的文件改名 .bad-时间戳 保留现场（不静默覆盖用户数据）
func backupCorrupt(path string) {
	bad := path + ".bad-" + time.Now().Format("20060102-150405")
	os.Rename(path, bad)
}

func (s *Store) migReadAll() []map[string]any {
	b, err := os.ReadFile(s.migPath())
	if err != nil {
		return nil
	}
	var out []map[string]any
	if len(b) > 0 && json.Unmarshal(b, &out) != nil {
		backupCorrupt(s.migPath()) // 损坏的账本先留证再重建，不能当空账本直接覆盖
		return nil
	}
	return out
}

func (s *Store) migWriteAll(list []map[string]any) {
	b, _ := json.MarshalIndent(list, "", "  ")
	writeFileAtomic(s.migPath(), b)
}

// MigAdd 记录一次成功迁移（src/dst/bak/size）
func (s *Store) MigAdd(src, dst, bak string, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.migReadAll()
	list = append(list, map[string]any{
		"src": src, "dst": dst, "bak": bak, "size": size,
		"at": time.Now().Format("2006-01-02 15:04:05"), "done": false,
	})
	s.migWriteAll(list)
}

// MigFind 查找某源路径最近一条未完成记录
func (s *Store) MigFind(src string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.migReadAll() {
		if m["src"] == src && m["done"] != true {
			return m
		}
	}
	return nil
}

// MigList 列出备份仍存在的迁移记录
func (s *Store) MigList() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	list := s.migReadAll()
	changed := false
	for _, m := range list {
		bak, _ := m["bak"].(string)
		if m["done"] == true || bak == "" {
			continue
		}
		if _, err := os.Lstat(bak); err == nil {
			out = append(out, m)
		} else {
			m["done"], changed = true, true // 备份已不在，顺手归档
		}
	}
	if changed {
		s.migWriteAll(list) // 归档标记落盘，否则每次调用都重查失效备份
	}
	return out
}

// MigListAll 全部迁移记录（含已归档 done）——用于已迁移项的体积记账
func (s *Store) MigListAll() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migReadAll()
}

// MigRemove 删除某源路径的迁移记录（迁回后：数据回 C 盘，迁移不再存在）
func (s *Store) MigRemove(src string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.migReadAll()
	out := list[:0]
	for _, m := range list {
		if m["src"] != src {
			out = append(out, m)
		}
	}
	s.migWriteAll(out)
}

// MigFinish 标记某源路径的迁移记录已清理备份
func (s *Store) MigFinish(src string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.migReadAll()
	for _, m := range list {
		if m["src"] == src && m["done"] != true {
			m["done"] = true
		}
	}
	s.migWriteAll(list)
}

// ---------- 偏好设置 ----------

func (s *Store) prefsPath() string { return filepath.Join(s.Root, "prefs.json") }

// Prefs 读取持久化偏好
func (s *Store) Prefs() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefsL()
}

func (s *Store) prefsL() map[string]any {
	b, err := os.ReadFile(s.prefsPath())
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		backupCorrupt(s.prefsPath()) // 损坏的偏好先留证再重建，不能静默覆盖
		return map[string]any{}
	}
	return m
}

// SetPrefs 合并写入偏好
func (s *Store) SetPrefs(m map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.prefsL()
	for k, v := range m {
		cur[k] = v
	}
	b, _ := json.MarshalIndent(cur, "", "  ")
	writeFileAtomic(s.prefsPath(), b)
}
