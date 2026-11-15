package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"aiclean/internal/junction"
	"aiclean/internal/model"
)

func mkDir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func mkFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findItem(items []model.ScanItem, path string) *model.ScanItem {
	for i := range items {
		if items[i].Path == path {
			return &items[i]
		}
	}
	return nil
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("AICLEAN_TEST_ENV", `C:\foo\bar`)
	got := expandEnv(`%AICLEAN_TEST_ENV%\sub\x`)
	if got != `C:\foo\bar\sub\x` {
		t.Fatalf("expandEnv = %q", got)
	}
	if expandEnv(`C:\plain`) != `C:\plain` {
		t.Fatal("plain path mangled")
	}
}

func TestScanBasicDirs(t *testing.T) {
	root := t.TempDir()
	d1 := mkDir(t, root, "app1", "cache")
	d2 := mkDir(t, root, "app2")
	mkFile(t, filepath.Join(d1, "a.bin"), 100)
	mkFile(t, filepath.Join(d1, "b.bin"), 50)
	mkFile(t, filepath.Join(d2, "c.bin"), 200)
	rules := []model.Rule{
		{ID: "r1", Path: d1, Tool: "A1", Risk: "safe", Action: "delete"},
		{ID: "r2", Path: d2, Tool: "A2", Risk: "safe", Action: "delete"},
	}
	items := Scan(rules)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if it := findItem(items, d1); it == nil || it.Size != 150 || it.Files != 2 {
		t.Fatalf("d1 item wrong: %+v", it)
	}
	if it := findItem(items, d2); it == nil || it.Size != 200 || it.Files != 1 {
		t.Fatalf("d2 item wrong: %+v", it)
	}
}

func TestScanWildcard(t *testing.T) {
	root := t.TempDir()
	mkDir(t, root, "JetBrains", "Idea", "caches")
	mkDir(t, root, "JetBrains", "GoLand", "caches")
	mkFile(t, filepath.Join(root, "JetBrains", "Idea", "caches", "x"), 10)
	mkFile(t, filepath.Join(root, "JetBrains", "GoLand", "caches", "y"), 20)
	rules := []model.Rule{{ID: "jb", Path: filepath.Join(root, "JetBrains", "*", "caches"), Tool: "JB", Risk: "safe", Action: "delete"}}
	items := Scan(rules)
	if len(items) != 2 {
		t.Fatalf("wildcard items = %d, want 2", len(items))
	}
	if findItem(items, filepath.Join(root, "JetBrains", "Idea", "caches")) == nil {
		t.Fatal("Idea caches missing")
	}
}

func TestScanSingleFileRule(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "big.bin")
	mkFile(t, f, 4096)
	items := Scan([]model.Rule{{ID: "f", Path: f, Tool: "F", Risk: "safe", Action: "delete"}})
	if len(items) != 1 || items[0].Size != 4096 || items[0].Files != 1 {
		t.Fatalf("file rule item: %+v", items)
	}
}

func TestNormalDedupSwallowsChild(t *testing.T) {
	root := t.TempDir()
	parent := mkDir(t, root, "app")
	child := mkDir(t, parent, "cache")
	mkFile(t, filepath.Join(child, "x"), 100)
	rules := []model.Rule{
		{ID: "p", Path: parent, Tool: "P", Risk: "review", Action: "review"},
		{ID: "c", Path: child, Tool: "C", Risk: "safe", Action: "delete"},
	}
	items := Scan(rules)
	if len(items) != 1 || items[0].Path != parent {
		t.Fatalf("dedup failed, items=%v", items)
	}
}

func TestReportOnlyKeepsChildrenAndSubtracts(t *testing.T) {
	root := t.TempDir()
	parent := mkDir(t, root, "docs")
	child := mkDir(t, parent, "wechat")
	mkFile(t, filepath.Join(parent, "own.txt"), 30)
	mkFile(t, filepath.Join(child, "big.dat"), 200)
	rules := []model.Rule{
		{ID: "p", Path: parent, Tool: "文档", Risk: "review", Action: "review", ReportOnly: true},
		{ID: "c", Path: child, Tool: "微信文件", Risk: "user-data", Action: "migrate"},
	}
	items := Scan(rules)
	if len(items) != 2 {
		t.Fatalf("items=%d want 2", len(items))
	}
	pi, ci := findItem(items, parent), findItem(items, child)
	if ci == nil || ci.Size != 200 {
		t.Fatalf("child missing/wrong: %+v", ci)
	}
	if pi == nil || pi.Size != 30 {
		t.Fatalf("parent should be 30 after subtraction, got %+v", pi)
	}
}

func TestScanHeuristics(t *testing.T) {
	root := t.TempDir()
	parent := mkDir(t, root, "app")
	mkDir(t, parent, "Cache")
	mkDir(t, parent, "Code Cache")
	mkDir(t, parent, "data")
	mkDir(t, parent, "keepme")
	mkFile(t, filepath.Join(parent, "Cache", "c"), 10)
	mkFile(t, filepath.Join(parent, "data", "d"), 20)
	mkFile(t, filepath.Join(parent, "keepme", "k"), 40)
	heur = &model.Heuristics{
		DeletePatterns:   []string{"cache", "code cache"},
		UserDataPatterns: []string{"data"},
	}
	defer func() { heur = nil }()
	rules := []model.Rule{{ID: "p", Path: parent, Tool: "App", Risk: "user-data", Action: "review", ScanHeuristics: true}}
	items := Scan(rules)

	cache := findItem(items, filepath.Join(parent, "Cache"))
	if cache == nil || cache.Action != "clean" || !cache.Heuristic || cache.Size != 10 {
		t.Fatalf("heuristic cache wrong: %+v", cache)
	}
	if findItem(items, filepath.Join(parent, "Code Cache")) == nil {
		t.Fatal("Code Cache not discovered")
	}
	data := findItem(items, filepath.Join(parent, "data"))
	if data == nil || data.Action != "none" || !data.Heuristic || data.Risk != "review" {
		t.Fatalf("heuristic data wrong: %+v", data)
	}
	if findItem(items, filepath.Join(parent, "keepme")) != nil {
		t.Fatal("keepme should stay inside parent")
	}
	pi := findItem(items, parent)
	if pi == nil || !pi.ReportOnly {
		t.Fatal("parent should be reportOnly via scanHeuristics")
	}
	// 父项尺寸 = 自身 - 已列子项（keepme 的 40 留在里面）
	if pi.Size != 40 {
		t.Fatalf("parent net size = %d, want 40 (keepme only)", pi.Size)
	}
}

func TestHeuristicSkipsExistingRulePath(t *testing.T) {
	root := t.TempDir()
	parent := mkDir(t, root, "app")
	cacheDir := mkDir(t, parent, "cache")
	mkFile(t, filepath.Join(cacheDir, "c"), 10)
	heur = &model.Heuristics{DeletePatterns: []string{"cache"}}
	defer func() { heur = nil }()
	rules := []model.Rule{
		{ID: "p", Path: parent, Tool: "App", Risk: "review", Action: "review", ScanHeuristics: true},
		{ID: "c", Path: cacheDir, Tool: "AppCache", Risk: "safe", Action: "delete"},
	}
	items := Scan(rules)
	n := 0
	for _, it := range items {
		if it.Path == cacheDir {
			n++
			if it.Heuristic {
				t.Fatal("existing rule path emitted as heuristic dup")
			}
		}
	}
	if n != 1 {
		t.Fatalf("cache path emitted %d times, want 1", n)
	}
}

func TestDirSizeNotFollowJunction(t *testing.T) {
	root := t.TempDir()
	target := mkDir(t, root, "realdata")
	host := mkDir(t, root, "host")
	mkFile(t, filepath.Join(target, "big"), 500)
	mkFile(t, filepath.Join(host, "own"), 10)
	// 创建真 junction；环境不允许（权限/文件系统）时跳过
	link := filepath.Join(host, "link")
	if _, err := junction.Create(link, target); err != nil {
		t.Skipf("cannot create junction in test env: %v", err)
	}
	size, files, _, skipped := dirSize(host)
	if files != 1 || size != 10 {
		t.Fatalf("dirSize followed junction: size=%d files=%d", size, files)
	}
	if skipped < 1 {
		t.Fatalf("junction not counted as skipped: %d", skipped)
	}
}

func TestAgedSize(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	newf := filepath.Join(root, "new")
	mkFile(t, old, 100)
	mkFile(t, newf, 50)
	oldTime := time.Now().AddDate(0, 0, -30)
	os.Chtimes(old, oldTime, oldTime)
	if got := agedSize(root, 7); got != 100 {
		t.Fatalf("agedSize = %d, want 100", got)
	}
}

func TestCancelStopsSizing(t *testing.T) {
	root := t.TempDir()
	d := mkDir(t, root, "d")
	mkFile(t, filepath.Join(d, "x"), 100)
	RequestCancel()
	defer ResetCancel()
	items := Scan([]model.Rule{{ID: "r", Path: d, Tool: "R", Risk: "safe", Action: "delete"}})
	// 已取消标志置位；立即取消时可为空或部分（不阻塞即达标）
	if !Cancelled() {
		t.Fatal("Cancelled flag not set")
	}
	for _, it := range items {
		if it.Size != 0 {
			t.Fatalf("cancelled scan still sized dirs: %+v", it)
		}
	}
}

func TestNeedsAdminMark(t *testing.T) {
	root := t.TempDir()
	d := mkDir(t, root, "sysdir")
	mkFile(t, filepath.Join(d, "x"), 10)
	old := Admin
	Admin = false
	defer func() { Admin = old }()
	items := Scan([]model.Rule{{ID: "s", Path: d, Tool: "S", Risk: "safe", Action: "delete", RequiresAdmin: true}})
	if len(items) != 1 || !items[0].NeedsAdmin || items[0].State != "locked" {
		t.Fatalf("admin mark wrong: %+v", items)
	}
}

// requiresClosed 匹配：规则写 "steam.exe" / "steam" 都应命中（procs 去后缀存储）
func TestProcMatch(t *testing.T) {
	procs := map[string]bool{"steam": true, "code": true, "微信": true}
	for _, n := range []string{"steam.exe", "Steam.EXE", "steam", "code.exe", "微信.exe"} {
		if !procMatch(procs, n) {
			t.Fatalf("procMatch(%q) should hit", n)
		}
	}
	for _, n := range []string{"notepad", "steam2.exe", "codex.exe"} {
		if procMatch(procs, n) {
			t.Fatalf("procMatch(%q) should miss", n)
		}
	}
}
