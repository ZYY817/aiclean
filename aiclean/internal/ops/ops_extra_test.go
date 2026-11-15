package ops

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMigLedgerLifecycle(t *testing.T) {
	s := tStore(t)
	src := `C:\x\data`
	s.MigAdd(src, `D:\AIClean-Moved\r\data`, `C:\x\data.aiclean-bak-1`, 1234)

	if m := s.MigFind(src); m == nil || m["size"].(float64) != 1234 {
		t.Fatalf("MigFind: %v", m)
	}
	// MigList 只认备份真实存在 —— 测试里备份不存在会被顺手归档，
	// 所以先建个真备份目录再测
	bak := `C:\x\data.aiclean-bak-1`
	// bak 是绝对路径，临时建到测试目录里
	tb := filepath.Join(t.TempDir(), "data.aiclean-bak-1")
	os.MkdirAll(tb, 0o755)
	s2 := tStore(t)
	s2.MigAdd(src, `D:\dst`, tb, 99)
	if l := s2.MigList(); len(l) != 1 {
		t.Fatalf("MigList = %d, want 1", len(l))
	}
	if l := s2.MigListAll(); len(l) != 1 {
		t.Fatalf("MigListAll = %d", len(l))
	}
	s2.MigFinish(src)
	if l := s2.MigList(); len(l) != 0 {
		t.Fatalf("MigList after finish = %d", len(l))
	}
	if l := s2.MigListAll(); len(l) != 1 || l[0]["done"] != true {
		t.Fatalf("MigListAll after finish: %v", l)
	}
	s2.MigRemove(src)
	if l := s2.MigListAll(); len(l) != 0 {
		t.Fatalf("MigRemove left %d records", len(l))
	}
	_ = bak
}

func TestPrefsMerge(t *testing.T) {
	s := tStore(t)
	s.SetPrefs(map[string]any{"theme": "ink", "motion": "calm"})
	s.SetPrefs(map[string]any{"theme": "fog"})
	p := s.Prefs()
	if p["theme"] != "fog" || p["motion"] != "calm" {
		t.Fatalf("prefs merge wrong: %v", p)
	}
	s.SetPrefs(map[string]any{"ignoreRules": []any{"r1"}, "ignorePaths": []any{`C:\x`}})
	p = s.Prefs()
	if len(p["ignoreRules"].([]any)) != 1 {
		t.Fatalf("ignoreRules not stored: %v", p)
	}
}

func TestRetainExpiry(t *testing.T) {
	s := tStore(t)
	src, _ := mkTree(t)
	m, err := s.Quarantine(src, "App", "n", 1)
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	// 手工把隔离日期改成 40 天前 → List 应自动清理
	mpath := filepath.Join(s.quarDir(), m.ID, ".aiclean.meta.json")
	b, _ := os.ReadFile(mpath)
	var meta map[string]any
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	meta["quarAt"] = time.Now().AddDate(0, 0, -40).Format("2006-01-02")
	nb, _ := json.Marshal(meta)
	os.WriteFile(mpath, nb, 0o644)
	if l := s.List(); len(l) != 0 {
		t.Fatalf("expired item not purged: %d left", len(l))
	}
	if _, err := os.Stat(filepath.Join(s.quarDir(), m.ID)); !os.IsNotExist(err) {
		t.Fatal("expired dir still on disk")
	}
}

func TestDeleteFileAndDir(t *testing.T) {
	s := tStore(t)
	d := t.TempDir()
	dir := filepath.Join(d, "xdir")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o644)
	sz, n, err := s.Delete(dir, "App")
	if err != nil || sz != 1 || n != 1 {
		t.Fatalf("Delete dir: sz=%d n=%d err=%v", sz, n, err)
	}
	f := filepath.Join(d, "f.bin")
	os.WriteFile(f, []byte("abc"), 0o644)
	sz, n, err = s.Delete(f, "App")
	if err != nil || sz != 3 || n != 1 {
		t.Fatalf("Delete file: sz=%d n=%d err=%v", sz, n, err)
	}
}

// Delete 必须拒绝 reparse 目录（通常是已迁移的 junction——删 junction 路径本身会把目标数据连锅端）
func TestDeleteRejectsJunction(t *testing.T) {
	s := tStore(t)
	d := t.TempDir()
	target := filepath.Join(d, "target")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "keep"), []byte("data"), 0o644)
	link := filepath.Join(d, "link")
	// cmd /c mklink /j —— junction 无需管理员
	if out, err := exec.Command("cmd", "/c", "mklink", "/j", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink 不可用: %v %s", err, out)
	}
	if _, _, err := s.Delete(link, "App"); err == nil {
		t.Fatal("Delete 未拒绝 junction 路径")
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Fatalf("目标数据被破坏: %v", err)
	}
	// 空 id 不得清空整个隔离区
	if err := s.Purge(""); err == nil {
		t.Fatal("Purge(\"\") 未拒绝")
	}
}

// 并发冒烟：桥接 handler 跑在不同 goroutine，账本/偏好/日志的读写必须串行。
// 没加锁时该测试会触发 concurrent map write panic 或账本损坏。
func TestConcurrentStoreAccess(t *testing.T) {
	s := tStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.MigAdd(`C:\a`, `D:\b`, `C:\a.aiclean-bak-1`, int64(j))
				s.MigFind(`C:\a`)
				s.MigRemove(`C:\a`)
				s.SetPrefs(map[string]any{"k": j})
				s.Prefs()
				s.Log("test", `C:\x`, 1, true, "")
				s.Logs(10)
				s.List()
			}
		}(i)
	}
	wg.Wait()
	if n := len(s.Logs(1000)); n == 0 {
		t.Fatal("expected log entries")
	}
}

// ---------- 发布审计回归：损坏备份 + 原子写 + 日志轮转 + 启动清残留 ----------

// TestCorruptFileBackedUp 损坏的 prefs/migrations 不能被静默覆盖——
// 必须改名 .bad-* 留证再重建（旧版直接返回空内容，下一次写入就把原文件清掉）
func TestCorruptFileBackedUp(t *testing.T) {
	s := tStore(t)
	for _, p := range []string{s.prefsPath(), s.migPath()} {
		os.WriteFile(p, []byte("{corrupted!!"), 0o644)
	}
	// 读 prefs → 返回空 + 原文件已改名
	if m := s.Prefs(); len(m) != 0 {
		t.Fatalf("corrupt prefs should return empty, got %v", m)
	}
	if m := s.migReadAll(); m != nil {
		t.Fatalf("corrupt migrations should return nil, got %v", m)
	}
	bads, _ := filepath.Glob(filepath.Join(s.Root, "*.bad-*"))
	if len(bads) != 2 {
		t.Fatalf("expected 2 .bad-* backups, got %v", bads)
	}
	// 重建写入后文件应恢复为合法 JSON
	s.SetPrefs(map[string]any{"k": 1})
	s.migWriteAll([]map[string]any{{"src": "x"}})
	var m map[string]any
	if b, _ := os.ReadFile(s.prefsPath()); json.Unmarshal(b, &m) != nil || m["k"].(float64) != 1 {
		t.Fatal("prefs not rebuilt correctly")
	}
}

// TestLogRotation ops.jsonl 超阈值只留最近 logKeepN 行，读出的顺序仍是新→旧
func TestLogRotation(t *testing.T) {
	s := tStore(t)
	oldMax, oldKeep := logMaxBytes, logKeepN
	logMaxBytes, logKeepN = 200, 10 // 缩阈值让测试快
	defer func() { logMaxBytes, logKeepN = oldMax, oldKeep }()
	for i := 0; i < 30; i++ {
		s.Log("op", "p", 1, true, "entry")
	}
	b, _ := os.ReadFile(s.logPath())
	lines := 0
	for _, l := range splitLines(string(b)) {
		if l != "" {
			lines++
		}
	}
	if lines > logKeepN {
		t.Fatalf("log not rotated: %d lines > %d", lines, logKeepN)
	}
	got := s.Logs(100)
	if len(got) == 0 || got[0]["t"].(string) < got[len(got)-1]["t"].(string) {
		t.Fatal("Logs order should be newest-first")
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// TestPurgeExpiredDirect PurgeExpired 可独立调用（启动/自动清理路径），
// 不必经过 List() —— 旧版只在打开残留页时才清超期项
func TestPurgeExpiredDirect(t *testing.T) {
	s := tStore(t)
	src := filepath.Join(t.TempDir(), "oldcache")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)
	m, err := s.Quarantine(src, "app", "", 7)
	if err != nil {
		t.Skipf("quarantine: %v", err)
	}
	// 把 QuarAt 改到 100 天前
	mp := filepath.Join(s.quarDir(), m.ID, ".aiclean.meta.json")
	b, _ := os.ReadFile(mp)
	var mm Meta
	json.Unmarshal(b, &mm)
	mm.QuarAt = time.Now().Add(-100 * 24 * time.Hour).Format("2006-01-02")
	mb, _ := json.Marshal(mm)
	os.WriteFile(mp, mb, 0o644)
	s.PurgeExpired()
	if _, err := os.Lstat(filepath.Join(s.quarDir(), m.ID)); !os.IsNotExist(err) {
		t.Fatal("expired quarantine entry should be deleted by PurgeExpired")
	}
}
