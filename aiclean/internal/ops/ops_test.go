package ops

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func past() time.Time { return time.Now().AddDate(0, 0, -60) }

func tStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir()}
	for _, d := range []string{"quarantine", "logs"} {
		if err := os.MkdirAll(filepath.Join(s.Root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func mkTree(t *testing.T) (string, int64) {
	dir := t.TempDir()
	src := filepath.Join(dir, "中文目录_测试")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a.bin"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.bin"), make([]byte, 100), 0o644)
	return src, 105
}

func TestQuarantineRestorePurge(t *testing.T) {
	s := tStore(t)
	src, want := mkTree(t)
	m, err := s.Quarantine(src, "TestApp", "测试", 30)
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if m.Size != want {
		t.Fatalf("size = %d, want %d", m.Size, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("源目录应已移走")
	}
	if l := s.List(); len(l) != 1 || l[0].App != "TestApp" {
		t.Fatalf("list: %+v", l)
	}
	if err := s.Restore(m.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "sub", "b.bin")); err != nil {
		t.Fatal("恢复后文件缺失")
	}
	// 再隔离→永久删除
	m2, err := s.Quarantine(src, "TestApp", "测试", 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(m2.ID); err != nil {
		t.Fatal(err)
	}
	if l := s.List(); len(l) != 0 {
		t.Fatal("purge 后应为空")
	}
}

func TestRestoreRejectsExisting(t *testing.T) {
	s := tStore(t)
	src, _ := mkTree(t)
	m, _ := s.Quarantine(src, "A", "", 30)
	os.MkdirAll(src, 0o755) // 原路径被重建
	if err := s.Restore(m.ID); err == nil {
		t.Fatal("应拒绝覆盖已有路径")
	}
}

func TestQuarantineRejectsSymlink(t *testing.T) {
	s := tStore(t)
	dir := t.TempDir()
	tgt := filepath.Join(dir, "tgt")
	os.MkdirAll(tgt, 0o755)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(tgt, link); err != nil {
		t.Skip("无 symlink 权限")
	}
	if _, err := s.Quarantine(link, "A", "", 30); err == nil {
		t.Fatal("应拒绝隔离 symlink")
	}
}

func TestPruneByAge(t *testing.T) {
	s := tStore(t)
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	os.WriteFile(old, []byte("x"), 0o644)
	os.Chtimes(old, past(), past())
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644)
	b, n, err := s.Prune(dir, 30)
	if err != nil || n != 1 || b != 1 {
		t.Fatalf("prune: %v n=%d b=%d", err, n, b)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("旧文件应已删")
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal("新文件应保留")
	}
}
