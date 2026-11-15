package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"aiclean/internal/junction"
	"aiclean/internal/model"
	"aiclean/internal/ops"
	"aiclean/internal/scanner"
)

// ---------- rules.json 全量校验（内嵌 + 外部文件一致跑一遍） ----------

func validRisk(a string) bool {
	switch a {
	case "safe", "caution", "user-data", "review":
		return true
	}
	return false
}
func validAction(a string) bool {
	switch a {
	case "delete", "migrate", "review", "ignore", "":
		return true
	}
	return false
}
func validSpecial(s string) bool {
	switch s {
	case "", "recycleBin", "winUpdate", "hiberOff":
		return true
	}
	return false
}

func checkRules(t *testing.T, rules []model.Rule, src string) {
	ids := map[string]bool{}
	for i, r := range rules {
		if r.ID == "" {
			t.Fatalf("%s rule[%d] empty id", src, i)
		}
		if ids[r.ID] {
			t.Fatalf("%s dup rule id %q", src, r.ID)
		}
		ids[r.ID] = true
		if r.Path == "" {
			t.Fatalf("%s rule %s empty path", src, r.ID)
		}
		if !validRisk(r.Risk) {
			t.Fatalf("%s rule %s bad risk %q", src, r.ID, r.Risk)
		}
		if !validAction(r.Action) {
			t.Fatalf("%s rule %s bad action %q", src, r.ID, r.Action)
		}
		if !validSpecial(r.Special) {
			t.Fatalf("%s rule %s bad special %q", src, r.ID, r.Special)
		}
		// special 项由特殊调度器接管，action 只是 UI 提示，不做限制
		// prunable+migrate 是合法组合：既可"清理旧文件"也可整体迁移
	}
	// 路径重叠审计（scanner 按规则文件序去重，同路径后者=死规则；
	// 父目录规则不标 reportOnly/scanHeuristics 会吞掉子规则）
	seen := map[string]string{}
	for _, r := range rules {
		p := strings.ToLower(strings.ReplaceAll(r.Path, "/", `\`))
		if prev, dup := seen[p]; dup {
			t.Fatalf("%s rules %q and %q point to same path %s", src, prev, r.ID, p)
		}
		seen[p] = r.ID
	}
	for _, parent := range rules {
		if parent.ReportOnly || parent.ScanHeuristics {
			continue
		}
		pp := strings.ToLower(strings.TrimSuffix(parent.Path, `\`)) + `\`
		for _, child := range rules {
			if child.ID == parent.ID {
				continue
			}
			cp := strings.ToLower(child.Path)
			if strings.HasPrefix(cp, pp) {
				t.Fatalf("%s rule %q (%s) 是 %q (%s) 的父目录且未标 reportOnly/scanHeuristics —— 子规则会被吞掉",
					src, parent.ID, parent.Path, child.ID, child.Path)
			}
		}
	}
}

func TestEmbeddedRulesValid(t *testing.T) {
	rules, err := scanner.LoadRulesData(rulesJSON)
	if err != nil {
		t.Fatalf("embedded rules parse: %v", err)
	}
	if len(rules) < 100 {
		t.Fatalf("embedded rules only %d — 被削减了？", len(rules))
	}
	checkRules(t, rules, "embedded")
}

func TestExternalRulesValid(t *testing.T) {
	for _, c := range []string{`..\rules.json`, `..\release\rules.json`} {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		rules, err := scanner.LoadRules(c)
		if err != nil {
			t.Fatalf("%s parse: %v", c, err)
		}
		checkRules(t, rules, c)
		// 外部副本必须与内嵌副本一致——加载顺序外部优先，漂移会让发布版回退安全修复
		ext, _ := os.ReadFile(c)
		if !bytes.Equal(ext, rulesJSON) {
			t.Fatalf("%s 与内嵌 rules.json 不一致（外部副本优先加载，必须逐字节相同）", c)
		}
	}
}

// ---------- 忽略名单 ----------

func tMainStore(t *testing.T) {
	t.Helper()
	s, err := ops.NewStoreAt(t.TempDir()) // 只用临时目录，绝不碰真实 %LOCALAPPDATA%\AIClean
	if err != nil {
		t.Skipf("ops.NewStoreAt: %v", err)
	}
	old := store
	store = s
	t.Cleanup(func() { store = old })
}

func TestIgnoreAndUnignore(t *testing.T) {
	tMainStore(t)
	if _, err := ignoreItem(map[string]any{"ruleId": "r-x", "path": `C:\Foo\Bar`}); err != nil {
		t.Fatal(err)
	}
	rs, ps := ignoreSet()
	if !rs["r-x"] || !ps[normPath(`C:\Foo\Bar`)] {
		t.Fatalf("ignoreSet: r=%v p=%v", rs, ps)
	}
	if _, err := unignoreAll(); err != nil {
		t.Fatal(err)
	}
	rs, ps = ignoreSet()
	if len(rs) != 0 || len(ps) != 0 {
		t.Fatal("unignoreAll did not clear")
	}
}

func TestApplyIgnoreFilters(t *testing.T) {
	tMainStore(t)
	store.SetPrefs(map[string]any{
		"ignoreRules": []any{"r2"},
		"ignorePaths": []any{`C:\A`},
	})
	rules := []model.Rule{{ID: "r1"}, {ID: "r2"}, {ID: "r3"}}
	items := []model.ScanItem{
		{RuleID: "r1", Path: `C:\A`},
		{RuleID: "r2", Path: `C:\B`},
		{RuleID: "r3", Path: `C:\C`},
	}
	rules, items = applyIgnoreFilters(rules, items)
	if len(rules) != 2 || rules[0].ID != "r1" || rules[1].ID != "r3" {
		t.Fatalf("rules filter: %v", rules)
	}
	if len(items) != 2 || items[0].Path != `C:\B` {
		t.Fatalf("items filter: %v", items)
	}
}

// ---------- guardPath 校验 ----------

func TestGuardPath(t *testing.T) {
	scanMu.Lock()
	lastScan = map[string]model.ScanItem{
		normPath(`C:\ok`):     {Path: `C:\ok`, State: "active"},
		normPath(`C:\locked`): {Path: `C:\locked`, State: "locked", LockedBy: []string{"x.exe"}},
		normPath(`C:\mig`):    {Path: `C:\mig`, State: "migrated"},
		normPath(`C:\docs`):   {Path: `C:\docs`, State: "active", ReportOnly: true},
	}
	lastBig = map[string]model.ScanItem{
		normPath(`C:\big.bin`): {Path: `C:\big.bin`, State: "active", Big: true},
	}
	scanMu.Unlock()
	defer func() {
		scanMu.Lock()
		lastScan, lastBig = map[string]model.ScanItem{}, map[string]model.ScanItem{}
		scanMu.Unlock()
	}()

	if _, err := guardPath(`C:\ok`); err != nil {
		t.Fatalf("ok path rejected: %v", err)
	}
	if _, err := guardPath(`c:\ok`); err != nil { // 大小写不敏感
		t.Fatalf("case-insensitive path rejected: %v", err)
	}
	if _, err := guardPath(`C:\locked`); err == nil {
		t.Fatal("locked path should be rejected")
	}
	if _, err := guardPath(`C:\mig`); err == nil {
		t.Fatal("migrated path should be rejected")
	}
	if _, err := guardPath(`C:\nope`); err == nil {
		t.Fatal("unknown path should be rejected")
	}
	if _, err := guardPath(`C:\docs`); err == nil {
		t.Fatal("reportOnly aggregate path should be rejected (会删用户文档)")
	}
	if it, err := guardPath(`C:\big.bin`); err != nil || !it.Big {
		t.Fatalf("bigfile path should pass: %v %v", it, err)
	}
}

func TestGuardPathMig(t *testing.T) {
	scanMu.Lock()
	lastScan = map[string]model.ScanItem{
		normPath(`C:\ok`):     {Path: `C:\ok`, State: "active"},
		normPath(`C:\locked`): {Path: `C:\locked`, State: "locked", LockedBy: []string{"x.exe"}},
		normPath(`C:\mig`):    {Path: `C:\mig`, State: "migrated"},
		normPath(`C:\docs`):   {Path: `C:\docs`, State: "active", ReportOnly: true},
	}
	scanMu.Unlock()
	defer func() {
		scanMu.Lock()
		lastScan = map[string]model.ScanItem{}
		scanMu.Unlock()
	}()

	if _, err := guardPathMig(`C:\docs`); err != nil {
		t.Fatalf("reportOnly path should be migratable (搬家): %v", err)
	}
	if _, err := guardPathMig(`C:\ok`); err != nil {
		t.Fatalf("normal path rejected: %v", err)
	}
	if _, err := guardPathMig(`C:\locked`); err == nil {
		t.Fatal("locked path should be rejected for migration")
	}
	if _, err := guardPathMig(`C:\mig`); err == nil {
		t.Fatal("already-migrated path should be rejected")
	}
	if _, err := guardPathMig(`C:\nope`); err == nil {
		t.Fatal("unknown path should be rejected")
	}
}

// ---------- 大文件重复标记 ----------

func TestMarkBigDups(t *testing.T) {
	d := t.TempDir()
	content := []byte(strings.Repeat("abcd", 1<<20)) // 4MB
	f1 := filepath.Join(d, "a.bin")
	f2 := filepath.Join(d, "b.bin")
	f3 := filepath.Join(d, "c.bin")
	os.WriteFile(f1, content, 0o644)
	os.WriteFile(f2, content, 0o644)
	os.WriteFile(f3, append([]byte("Z"), content[1:]...), 0o644)
	items := []model.ScanItem{
		{Path: f1, Size: int64(len(content))},
		{Path: f2, Size: int64(len(content))},
		{Path: f3, Size: int64(len(content))},
	}
	markBigDups(items)
	if !items[1].Dup {
		t.Fatal("identical second copy not marked dup")
	}
	if items[0].Dup {
		t.Fatal("first copy should not be marked (保留原件)")
	}
	if items[2].Dup {
		t.Fatal("different content wrongly marked dup")
	}
}

// ---------- 排程参数规范化 ----------

func TestSchedArgs(t *testing.T) {
	cases := []struct{ freq, tm, wantSc, wantT string }{
		{"daily", "03:30", "DAILY", "03:30"},
		{"weekly", "12:00", "WEEKLY", "12:00"},
		{"daily", "", "DAILY", "03:00"},      // 空时间 → 默认 03:00
		{"daily", "99:99", "DAILY", "03:00"}, // 非法时间 → 默认
		{"daily", "3:00", "DAILY", "03:00"},  // 格式错 → 默认
		{"weekly", "25:00", "WEEKLY", "03:00"},
		{"foo", "01:00", "DAILY", "01:00"}, // 未知 freq → DAILY
	}
	for _, c := range cases {
		sc, ntm := schedArgs(c.freq, c.tm)
		if sc != c.wantSc || ntm != c.wantT {
			t.Fatalf("schedArgs(%q,%q) = %s,%s want %s,%s", c.freq, c.tm, sc, ntm, c.wantSc, c.wantT)
		}
	}
}

// ---------- 前端纯逻辑：调 node 跑 web/test/logic.test.js ----------

func TestFrontendLogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found, 跳过前端逻辑测试")
	}
	out, err := exec.Command(node, filepath.Join("web", "test", "logic.test.js")).CombinedOutput()
	t.Log(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("frontend logic test failed: %v", err)
	}
}

// ---------- 发布审计回归 ----------

// TestStoreRootPointer 数据目录指向文件：写过的选择必须固定复用；
// 指向已消失的盘才重新挑（否则迁移/拔盘后设置账本"丢失"）
func TestStoreRootPointer(t *testing.T) {
	lad := t.TempDir()
	t.Setenv("LOCALAPPDATA", lad)
	r1 := storeRoot()
	if r1 == "" {
		t.Fatal("storeRoot empty")
	}
	if _, err := os.Stat(storePointerFile()); err != nil {
		t.Fatalf("pointer file not written: %v", err)
	}
	// 指针写存在的盘 → 固定返回它，不再随剩余空间重新挑
	exe, _ := os.Executable()
	pinned := filepath.VolumeName(exe) + `\AIClean-Pinned`
	os.WriteFile(storePointerFile(), []byte(pinned), 0o644)
	if got := storeRoot(); got != pinned {
		t.Fatalf("pinned root ignored: got %q want %q", got, pinned)
	}
	// 指针写不存在的盘 → 重新挑而不是硬用死路径
	if _, err := os.Stat(`Z:\`); err == nil {
		t.Skip("Z: 盘存在，无法测试失效指针")
	}
	os.WriteFile(storePointerFile(), []byte(`Z:\AIClean`), 0o644)
	if got := storeRoot(); got == `Z:\AIClean` {
		t.Fatal("dead drive pointer should trigger re-pick")
	}
}

// TestLoadRulesEmbeddedFallback 测试二进制旁边没有 rules.json → 必须回退内嵌，
// 且 src 为空串（不是字面 "embedded"）
func TestLoadRulesEmbeddedFallback(t *testing.T) {
	rules, src, err := loadRules()
	if err != nil {
		t.Fatalf("loadRules: %v", err)
	}
	if len(rules) < 100 {
		t.Fatalf("embedded rules only %d", len(rules))
	}
	if src != "" {
		t.Fatalf("embedded fallback should return empty rulesPath, got %q", src)
	}
}

// TestAutoCleanCandidates 静默清理准入：special/reportOnly/locked/heuristic/中风险 全排除
func TestAutoCleanCandidates(t *testing.T) {
	items := []model.ScanItem{
		{Path: `C:\a`, State: "active", Action: "clean", Risk: "cache"},                                 // 收
		{Path: `C:\b`, State: "active", Action: "clean", Risk: "cache", Special: "recycleBin"},          // special 拒
		{Path: `C:\c`, State: "active", Action: "clean", Risk: "cache", ReportOnly: true},               // 统计 拒
		{Path: `C:\d`, State: "locked", Action: "clean", Risk: "cache"},                                 // 锁定 拒
		{Path: `C:\e`, State: "active", Action: "clean", Risk: "cache", Heuristic: true},                // 启发式 拒
		{Path: `C:\f`, State: "active", Action: "clean", Risk: "userData"},                              // 中风险 拒
		{Path: `C:\g`, State: "active", Action: "migrate", Risk: "userData"},                            // 迁移项 拒
		{Path: `C:\h`, State: "active", Action: "clean", Risk: "review", Prunable: true, PruneDays: 30}, // 按龄 收
		{Path: `C:\i`, State: "active", Action: "clean", Risk: "cache", NeedsAdmin: true},               // 需管理员 拒
	}
	got := autoCleanCandidates(items)
	paths := map[string]bool{}
	for _, it := range got {
		paths[it.Path] = true
	}
	if !paths[`C:\a`] || !paths[`C:\h`] || len(got) != 2 {
		t.Fatalf("candidates = %v", paths)
	}
}

// TestGuardMigDescendant 下级目录已被迁移 → 父目录迁移/删除必须拒绝（会丢子链接）
func TestGuardMigDescendant(t *testing.T) {
	tMainStore(t)
	parent := filepath.Join(t.TempDir(), "parent")
	child := filepath.Join(parent, "sub")
	os.MkdirAll(child, 0o755)
	store.MigAdd(child, `D:\AIClean-Moved\r\sub`, child+`.aiclean-bak-1`, 1)
	scanMu.Lock()
	lastScan = map[string]model.ScanItem{normPath(parent): {Path: parent, State: "active"}}
	scanMu.Unlock()
	defer func() {
		scanMu.Lock()
		lastScan = map[string]model.ScanItem{}
		scanMu.Unlock()
	}()
	if _, err := guardPathMig(parent); err == nil {
		t.Fatal("parent with migrated child should be rejected")
	}
}

// TestCheckStillClosed 执行前复查进程：测试二进制自身正在运行必命中
func TestCheckStillClosed(t *testing.T) {
	me := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	it := model.ScanItem{RequiresClosed: []string{me}}
	if err := checkStillClosed(it); err == nil {
		t.Skip("test binary process not visible to snapshot")
	}
	if hit := scanner.ProcsRunning([]string{"definitely-not-running-zz99"}); len(hit) != 0 {
		t.Fatal("fake process should not match")
	}
}

// TestStoreRootDeadDriveKeepsPointer 指向盘不在：临时回退 LOCALAPPDATA，
// 指向文件绝不能被改写——盘回来后必须恢复原目录。
func TestStoreRootDeadDriveKeepsPointer(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	// 找一个确定不存在的盘符
	used := map[string]bool{}
	for _, d := range drives() {
		used[d.Letter] = true
	}
	dead := ""
	for l := 'Z'; l >= 'A'; l-- {
		if !used[string(l)] {
			dead = string(l)
			break
		}
	}
	if dead == "" {
		t.Skip("all drive letters in use")
	}
	pinned := dead + `:\AIClean`
	if err := os.WriteFile(storePointerFile(), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	got := storeRoot()
	want := filepath.Join(os.Getenv("LOCALAPPDATA"), "AIClean")
	if got != want {
		t.Fatalf("fallback root = %q want %q", got, want)
	}
	if storePinnedMissing != pinned {
		t.Fatalf("pinnedMissing = %q want %q", storePinnedMissing, pinned)
	}
	// 指向文件内容必须原样保留
	b, err := os.ReadFile(storePointerFile())
	if err != nil || strings.TrimSpace(string(b)) != pinned {
		t.Fatalf("pointer rewritten: %q err %v", string(b), err)
	}
	storePinnedMissing = "" // 清全局状态，防污染后续测试
}

// TestStoreRootLiveDriveUsesPinned 盘还在时固定用指向目录，不再重选。
func TestStoreRootLiveDriveUsesPinned(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	storePinnedMissing = ""
	pinned := `C:\AIClean-test-pinned`
	if err := os.WriteFile(storePointerFile(), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := storeRoot(); got != pinned {
		t.Fatalf("root = %q want pinned %q", got, pinned)
	}
	if storePinnedMissing != "" {
		t.Fatalf("pinnedMissing should be empty, got %q", storePinnedMissing)
	}
}

// TestMergeTreeAnyOpsAppend 回退目录并回：已有文件以目标为准，
// ops.jsonl 追加不丢日志，缺失文件补上。
func TestMergeTreeAnyOpsAppend(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "prefs.json"), []byte(`{"a":1}`), 0o644)
	os.WriteFile(filepath.Join(src, "ops.jsonl"), []byte("src-line\n"), 0o644)
	os.MkdirAll(filepath.Join(src, "logs"), 0o755)
	os.WriteFile(filepath.Join(src, "logs", "x.log"), []byte("L"), 0o644)
	os.WriteFile(filepath.Join(dst, "prefs.json"), []byte(`{"b":2}`), 0o644)
	os.WriteFile(filepath.Join(dst, "ops.jsonl"), []byte("dst-line\n"), 0o644)
	if err := mergeTreeAny(src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "prefs.json"))
	if string(b) != `{"b":2}` {
		t.Fatalf("existing file overwritten: %q", b)
	}
	b, _ = os.ReadFile(filepath.Join(dst, "ops.jsonl"))
	if string(b) != "dst-line\nsrc-line\n" {
		t.Fatalf("ops.jsonl not appended: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "logs", "x.log")); err != nil {
		t.Fatal("missing file not merged")
	}
}

// TestMergeMigrationsUnion 断盘期间两边账本合并必须并集保留——
// 丢记录意味着对应 C 盘备份永远删不掉。
func TestMergeMigrationsUnion(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	rec := func(sfx string) map[string]any {
		return map[string]any{"src": `C:\Users\u\` + sfx, "dst": `F:\AIClean-Moved\` + sfx,
			"bak": `C:\Users\u\` + sfx + ".aiclean-bak-1", "size": 100, "at": "2025-01-01 00:00:00", "done": false}
	}
	wr := func(dir string, recs []map[string]any) {
		b, _ := json.Marshal(recs)
		os.WriteFile(filepath.Join(dir, "migrations.json"), b, 0o644)
	}
	wr(src, []map[string]any{rec("fallback-1"), rec("shared")})
	wr(dst, []map[string]any{rec("main-1"), rec("shared")}) // shared 重复 → 去重
	if err := mergeTreeAny(src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "migrations.json"))
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("merged ledger not valid json: %v", err)
	}
	srcs := map[string]bool{}
	for _, m := range out {
		s, _ := m["src"].(string)
		srcs[s] = true
	}
	for _, want := range []string{`C:\Users\u\fallback-1`, `C:\Users\u\main-1`, `C:\Users\u\shared`} {
		if !srcs[want] {
			t.Fatalf("record %s lost in merge (got %v)", want, srcs)
		}
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 union records, got %d", len(out))
	}
}

// TestMigrateBlockedWhenPinnedMissing 数据盘未连接时所有迁移系操作直接拒绝——
// 在临时目录做迁移，盘回来后账本合并/指针恢复都会出岔子。
func TestMigrateBlockedWhenPinnedMissing(t *testing.T) {
	storePinnedMissing = `Z:\AIClean`
	defer func() { storePinnedMissing = "" }()
	want := "数据目录所在的 Z 盘未连接"
	cases := map[string]func(...any) (any, error){
		"migrateItem":       migrateItem,
		"finishMigration":   finishMigration,
		"rollbackMigration": rollbackMigration,
		"migPreflight":      migPreflight,
	}
	for name, fn := range cases {
		r, err := fn(map[string]any{"path": `C:\x`, "drive": "F"})
		if err != nil {
			t.Fatalf("%s returned error: %v", name, err)
		}
		m, _ := r.(map[string]any)
		if ok, _ := m["ok"].(bool); ok {
			t.Fatalf("%s not blocked when pinned missing", name)
		}
		if e, _ := m["error"].(string); !strings.Contains(e, want) {
			t.Fatalf("%s error %q want contains %q", name, e, want)
		}
	}
}

// TestLowIntegritySA 低完整性 SD 可转换（提权界面 + 普通计划任务跨完整性通信）
func TestLowIntegritySA(t *testing.T) {
	sa := lowIntegritySA()
	if sa == nil {
		t.Skip("ConvertStringSecurityDescriptorToSecurityDescriptorW unavailable")
	}
	defer freeSA(sa)
	if sa.SecurityDescriptor == 0 {
		t.Fatal("security descriptor is null")
	}
	if sa.Length != uint32(unsafe.Sizeof(securityAttributes{})) {
		t.Fatalf("SA.Length = %d want %d (64-bit layout)", sa.Length, unsafe.Sizeof(securityAttributes{}))
	}
}

// TestLowIntegrityNamedObjects 真实链路：带低完整性 SD 的命名事件能被
// 另一进程以 EVENT_MODIFY_STATE 打开并 SetEvent；互斥锁同 SA 可创建。
// 这是"提权界面 + 普通计划任务代执行自动清理"通信成立的前提。
func TestLowIntegrityNamedObjects(t *testing.T) {
	sa := lowIntegritySA()
	if sa == nil {
		t.Skip("SDDL conversion unavailable")
	}
	defer freeSA(sa)
	name := fmt.Sprintf(`Local\AIClean-test-%d`, time.Now().UnixNano())

	// 事件：Create(带SA) → Open(MODIFY_STATE) → SetEvent → Wait 命中
	h, _, ce := createEventW.Call(uintptr(unsafe.Pointer(sa)), 0, 0, uintptr(unsafe.Pointer(must16(name))))
	if h == 0 {
		t.Fatalf("CreateEventW with low-integrity SA failed: %v", ce)
	}
	defer procCloseHandle.Call(h)
	ev2, _, oe := openEventW.Call(0x0002 /*EVENT_MODIFY_STATE*/, 0, uintptr(unsafe.Pointer(must16(name))))
	if ev2 == 0 {
		t.Fatalf("OpenEventW(EVENT_MODIFY_STATE) failed: %v", oe)
	}
	defer procCloseHandle.Call(ev2)
	if r, _, se := setEventW.Call(ev2); r == 0 {
		t.Fatalf("SetEvent failed: %v", se)
	}
	if w, _, _ := waitForSingleObject.Call(h, 2000); w != 0 {
		t.Fatalf("WaitForSingleObject = %d, want 0 (WAIT_OBJECT_0)", w)
	}

	// 互斥锁：同 SA 创建（提权进程占锁时普通进程必须能探测到"已存在"）
	mux, _, me := createMutexW.Call(uintptr(unsafe.Pointer(sa)), 0, uintptr(unsafe.Pointer(must16(name+`-m`))))
	if mux == 0 {
		t.Fatalf("CreateMutexW with low-integrity SA failed: %v", me)
	}
	procCloseHandle.Call(mux)
}

// ---------- 崩溃日志 ----------
func panicker() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = writeCrash(filepath.Join(t2dir(), "crash.log"), "test", r, debug.Stack())
		}
	}()
	panic("故意的 panic")
}
func t2dir() string { d, _ := os.MkdirTemp("", "ac"); return d }

// TestWriteCrash panic → 日志含版本号、来源、堆栈
func TestWriteCrash(t *testing.T) {
	old := Version
	Version = "9.9.9-test"
	defer func() { Version = old }()
	if err := panicker(); err != nil {
		t.Fatal(err)
	}
	// panicker 写到了自己的临时目录，直接断言函数本身产出的文件
	d := t2dir()
	p := filepath.Join(d, "crash.log")
	func() {
		defer func() {
			if r := recover(); r != nil {
				writeCrash(p, "testSource", r, debug.Stack())
			}
		}()
		panic("boom")
	}()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"AIClean 9.9.9-test", "panic: boom", "source: testSource", "goroutine", "Windows"} {
		if !strings.Contains(s, want) {
			t.Fatalf("crash log missing %q\n%s", want, s[:min(400, len(s))])
		}
	}
}

// TestWriteCrashTruncation 超 1MB 只留尾部 200KB
func TestWriteCrashTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crash.log")
	os.WriteFile(p, bytes.Repeat([]byte("x"), 1<<20), 0o644)
	func() {
		defer func() {
			if r := recover(); r != nil {
				writeCrash(p, "test", r, debug.Stack())
			}
		}()
		panic("tail")
	}()
	fi, _ := os.Stat(p)
	if fi.Size() > (1<<20)+64<<10 {
		t.Fatalf("crash log not truncated: %d bytes", fi.Size())
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "panic: tail") {
		t.Fatal("latest crash lost after truncation")
	}
}

// ---------- 卸载前置检查 ----------
// TestSplitUninstallState 有迁移记录 → migrated 列出；bak 在盘上 → baks 列出
func TestSplitUninstallState(t *testing.T) {
	recs := []map[string]any{
		{"src": `C:\a`, "dst": `F:\moved\a`, "bak": `C:\a.aiclean-bak-1`, "done": false, "size": 10},
		{"src": `C:\b`, "dst": `F:\moved\b`, "bak": `C:\b.aiclean-bak-2`, "done": true, "size": 20},  // 已归档 → 不出现
		{"src": `C:\c`, "dst": `F:\moved\c`, "bak": `C:\c.aiclean-bak-3`, "done": false, "size": 30}, // bak 已被外部删
	}
	exists := func(p string) bool { return p == `C:\a.aiclean-bak-1` }
	mig, baks := splitUninstallState(recs, exists)
	if len(mig) != 2 {
		t.Fatalf("migrated = %d want 2", len(mig))
	}
	if len(baks) != 1 || baks[0]["path"] != `C:\a.aiclean-bak-1` {
		t.Fatalf("baks = %v", baks)
	}
	// 无任何记录 → 两个列表都空
	m2, b2 := splitUninstallState(nil, exists)
	if len(m2) != 0 || len(b2) != 0 {
		t.Fatal("empty ledger should give empty lists")
	}
}

// ---------- 启动自检 ----------
func TestExePathDrift(t *testing.T) {
	for _, c := range []struct {
		reg, cur string
		want     bool
	}{
		{`"C:\Tools\AIClean.exe"`, `C:\Tools\AIClean.exe`, false},
		{`C:\Old\AIClean.exe`, `C:\New\AIClean.exe`, true},
		{`d:\x\aiclean.exe`, `D:\x\AIClean.exe`, false},               // 大小写不敏感（raw 串内 \x 非转义）
		{``, `C:\x\AIClean.exe`, false},                               // 未登记不算漂移
		{`"E:\C盘清理 工具\AIClean.exe"`, `E:\C盘清理 工具\AIClean.exe`, false}, // 中文+空格+引号一致
		{`E:\C盘清理\old\AIClean.exe`, `E:\C盘清理\new\AIClean.exe`, true},  // 中文路径真漂移
	} {
		if got := exePathDrift(c.reg, c.cur); got != c.want {
			t.Fatalf("exePathDrift(%q,%q)=%v want %v", c.reg, c.cur, got, c.want)
		}
	}
}

// TestSchedNeedsRebuild 计划任务重建判定：不存在不建/登记空补建/漂移才重建一次
func TestSchedNeedsRebuild(t *testing.T) {
	for _, c := range []struct {
		task bool
		reg  string
		cur  string
		want bool
	}{
		{false, `C:\a\AIClean.exe`, `C:\a\AIClean.exe`, false},                      // 任务不存在
		{true, ``, `C:\a\AIClean.exe`, true},                                        // 老版本建的任务没登记 → 补建
		{true, `C:\a\AIClean.exe`, `C:\a\AIClean.exe`, false},                       // 路径一致
		{true, `E:\C盘清理\release\AIClean.exe`, `E:\C盘清理\release\AIClean.exe`, false}, // 中文路径一致
		{true, `C:\old\AIClean.exe`, `C:\new\AIClean.exe`, true},                    // 漂移
	} {
		if got := schedNeedsRebuild(c.task, c.reg, c.cur); got != c.want {
			t.Fatalf("schedNeedsRebuild(%v,%q,%q)=%v want %v", c.task, c.reg, c.cur, got, c.want)
		}
	}
}

// TestVersionFilesAligned VERSION 文件与 versioninfo.json 保持一致（防发版漏改）
func TestVersionFilesAligned(t *testing.T) {
	ver := strings.TrimSpace(string(must(os.ReadFile("VERSION"))))
	var vij struct {
		StringFileInfo struct {
			FileVersion    string `json:"FileVersion"`
			ProductVersion string `json:"ProductVersion"`
		}
	}
	if err := json.Unmarshal(must(os.ReadFile("versioninfo.json")), &vij); err != nil {
		t.Fatal(err)
	}
	if vij.StringFileInfo.FileVersion != ver+".0" {
		t.Fatalf("versioninfo FileVersion=%q want %q", vij.StringFileInfo.FileVersion, ver+".0")
	}
}
func must(b []byte, e error) []byte {
	if e != nil {
		panic(e)
	}
	return b
}

// ---------- 彻底移除的安全校验 ----------

// mkMigFixture 造一条可删的迁移记录：bak/dst/src 三件套 + 账本记录 + junction
func mkMigFixture(t *testing.T) (*ops.Store, string, string, string) {
	t.Helper()
	base := t.TempDir()
	s, err := ops.NewStoreAt(filepath.Join(base, "store"))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(base, "src 目录")
	bak := src + ".aiclean-bak-1"
	dst := filepath.Join(base, "moved", "目标 目录")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "f.bin"), []byte("x"), 0o644)
	os.MkdirAll(bak, 0o755)
	os.WriteFile(filepath.Join(bak, "f.bin"), []byte("x"), 0o644)
	os.MkdirAll(src, 0o755) // junction 载体：必须是存在的空目录
	if _, err := junction.Create(src, dst); err != nil {
		t.Skipf("junction 不可用: %v", err)
	}
	s.MigAdd(src, dst, bak, 1)
	return s, src, dst, bak
}

// TestUninstallKeepsBakWhenDstMissing dst 不存在 → 校验不过 → 备份必须留下并进 skipped
func TestUninstallKeepsBakWhenDstMissing(t *testing.T) {
	s, _, dst, bak := mkMigFixture(t)
	os.RemoveAll(dst) // 目标数据没了——备份是唯一副本
	skipped := uninstallDeleteBaks(s)
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v want 1", skipped)
	}
	if _, err := os.Stat(bak); err != nil {
		t.Fatal("backup deleted although dst missing")
	}
	if !strings.Contains(skipped[0]["reason"].(string), "不可达") {
		t.Fatalf("reason = %v", skipped[0]["reason"])
	}
}

// TestUninstallDeletesValidBak 三件套齐全 → 备份正常删除
func TestUninstallDeletesValidBak(t *testing.T) {
	s, _, _, bak := mkMigFixture(t)
	skipped := uninstallDeleteBaks(s)
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v want empty", skipped)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatal("valid bak not deleted")
	}
}

// TestUninstallBlockedWhenPinnedMissing 数据盘未连接时卸载双入口都拒绝
func TestUninstallBlockedWhenPinnedMissing(t *testing.T) {
	storePinnedMissing = `Z:\AIClean`
	defer func() { storePinnedMissing = "" }()
	for name, fn := range map[string]func(...any) (any, error){
		"uninstallCheck": uninstallCheck,
		"uninstallRun":   uninstallRun,
	} {
		r, err := fn(map[string]any{})
		if err != nil {
			t.Fatalf("%s err: %v", name, err)
		}
		m := r.(map[string]any)
		if ok, _ := m["ok"].(bool); ok {
			t.Fatalf("%s not blocked", name)
		}
		if e, _ := m["error"].(string); !strings.Contains(e, "Z 盘未连接") {
			t.Fatalf("%s error %q", name, e)
		}
	}
}

// TestDelayedRmDir 延迟删除：中文+空格路径的目录几秒后消失（SysProcAttr.CmdLine 写法）
func TestDelayedRmDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "测试 dir 名")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	delayedRmDir(dir, "")
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return // 删掉了
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("dir still exists after delayed rm: %s", dir)
}

// TestWriteKeptDataList 保留清单：桌面写不进 → 落到文档；内容覆盖迁移+备份
func TestWriteKeptDataList(t *testing.T) {
	desktop := t.TempDir()
	docs := t.TempDir()
	oldKF, oldED := knownFolderPath, exeParentDir
	defer func() { knownFolderPath, exeParentDir = oldKF, oldED }()

	// 桌面失败 → 走文档
	knownFolderPath = func(id *windows.KNOWNFOLDERID) (string, error) {
		if id == windows.FOLDERID_Desktop {
			return "", fmt.Errorf("desktop unavailable")
		}
		return docs, nil
	}
	exeParentDir = func() (string, error) { return t.TempDir(), nil }
	mig := []map[string]any{{"src": `C:\a`, "dst": `F:\AIClean-Moved\x`, "size": int64(100)}}
	baks := []map[string]any{{"path": `C:\a.aiclean-bak-1`, "src": `C:\a`, "size": int64(100)}}
	p, err := writeKeptDataList(mig, baks)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != docs {
		t.Fatalf("fell back to %q want docs %q", p, docs)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{`C:\a`, `F:\AIClean-Moved\x`} {
		if !strings.Contains(s, want) {
			t.Fatalf("kept list missing %q", want)
		}
	}
	// 桌面正常 → 首选桌面
	knownFolderPath = func(id *windows.KNOWNFOLDERID) (string, error) {
		if id == windows.FOLDERID_Desktop {
			return desktop, nil
		}
		return docs, nil
	}
	p2, err := writeKeptDataList(mig, nil)
	if err != nil || filepath.Dir(p2) != desktop {
		t.Fatalf("desktop preferred: p=%q err=%v", p2, err)
	}
	// 全失败 → error
	knownFolderPath = func(*windows.KNOWNFOLDERID) (string, error) { return "", fmt.Errorf("nope") }
	exeParentDir = func() (string, error) { return "", fmt.Errorf("nope") }
	if _, err := writeKeptDataList(mig, nil); err == nil {
		t.Fatal("all-fail should return error")
	}
	// 两边都空 → 不生成文件
	if p3, e := writeKeptDataList(nil, nil); p3 != "" || e != nil {
		t.Fatalf("empty kept lists should not create file: %q %v", p3, e)
	}
}

// TestUninstallAbortsWhenListWriteFails 保留项存在 + 清单写不出 →
// 中止卸载且账本原样还在（绝不允许"账本删了清单没写"）
func TestUninstallAbortsWhenListWriteFails(t *testing.T) {
	// 造一条未完成迁移记录 → 卸载时会保留（需要清单）
	base := t.TempDir()
	s, err := ops.NewStoreAt(filepath.Join(base, "store"))
	if err != nil {
		t.Fatal(err)
	}
	s.MigAdd(`C:\a`, `F:\moved\a`, `C:\a.aiclean-bak-1`, 100)
	ledger := filepath.Join(s.Root, "migrations.json")

	oldStore, oldKF, oldED := store, knownFolderPath, exeParentDir
	store = s
	knownFolderPath = func(*windows.KNOWNFOLDERID) (string, error) { return "", fmt.Errorf("nope") }
	exeParentDir = func() (string, error) { return "", fmt.Errorf("nope") }
	defer func() { store, knownFolderPath, exeParentDir = oldStore, oldKF, oldED }()

	r, err := uninstallRun(map[string]any{"deleteBaks": true})
	if err != nil {
		t.Fatal(err)
	}
	m := r.(map[string]any)
	if ok, _ := m["ok"].(bool); ok {
		t.Fatal("uninstallRun should abort when kept-list cannot be written")
	}
	if e, _ := m["error"].(string); !strings.Contains(e, "无法生成保留数据清单") {
		t.Fatalf("error = %q", e)
	}
	if _, e := os.Stat(ledger); e != nil {
		t.Fatal("ledger deleted despite aborted uninstall")
	}
}
