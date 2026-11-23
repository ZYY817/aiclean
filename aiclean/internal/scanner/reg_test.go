package scanner

// 注册表扫描测试：在 HKCU\Software\AIClean-Test 下种真键再扫，覆盖五种 regMode。
// 不碰系统键，测试结束整棵测试树删除。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aiclean/internal/model"
	"golang.org/x/sys/windows/registry"
)

const testRegRoot = `Software\AIClean-Test`

func regTestCleanup(t *testing.T) {
	t.Helper()
	// 递归删测试树（无视错误）
	var del func(sub string)
	del = func(sub string) {
		k, err := registry.OpenKey(registry.CURRENT_USER, sub, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			registry.DeleteKey(registry.CURRENT_USER, sub)
			return
		}
		subs, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, s := range subs {
			del(sub + `\` + s)
		}
		registry.DeleteKey(registry.CURRENT_USER, sub)
	}
	del(testRegRoot)
}

func regMkKey(t *testing.T, sub string) registry.Key {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, testRegRoot+`\`+sub,
		registry.SET_VALUE|registry.CREATE_SUB_KEY|registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		t.Fatalf("create key %s: %v", sub, err)
	}
	return k
}

func regFind(items []model.ScanItem, want string) *model.ScanItem {
	for i := range items {
		if strings.Contains(items[i].Path, want) {
			return &items[i]
		}
	}
	return nil
}

// exeFromCmdline 提取路径的各种形态
func TestExeFromCmdline(t *testing.T) {
	cases := map[string]string{
		`"C:\Program Files\App\app.exe" -arg`:   `C:\Program Files\App\app.exe`,
		`C:\Tools\tool.exe --flag`:             `C:\Tools\tool.exe`,
		`C:\Icons\app.exe,0`:                   `C:\Icons\app.exe`,
		`"C:\app.exe"`:                         `C:\app.exe`,
		`C:\app.exe`:                           `C:\app.exe`,
		``:                                     ``,
		`  `:                                   ``,
		`C:\dir\noext args`:                    `C:\dir\noext`,
	}
	for in, want := range cases {
		if got := exeFromCmdline(in); got != want {
			t.Errorf("exeFromCmdline(%q) = %q, want %q", in, got, want)
		}
	}
}

// pathFromValueName 剥后缀+扩展名截断
func TestPathFromValueName(t *testing.T) {
	cases := map[string]string{
		`C:\a\b\app.exe.FriendlyAppName`:      `C:\a\b\app.exe`,
		`C:\a\b\lib.dll.ApplicationCompany`:   `C:\a\b\lib.dll`,
		`C:\a\b\tool.bat.FriendlyAppName`:     `C:\a\b\tool.bat`,
		`C:\a\b\app.exe`:                      `C:\a\b\app.exe`, // Store 纯路径
		`C:\a\b\PBZ.part01.exe.FriendlyAppName`: `C:\a\b\PBZ.part01.exe`,
		`SomeRandomValueName`:                 ``, // 无法判定 → 跳过
		`C:\a\b\noext`:                        ``, // 无已知扩展名 → 跳过
	}
	for in, want := range cases {
		if got := pathFromValueName(in); got != want {
			t.Errorf("pathFromValueName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 断盘保护：路径所在盘不存在 → 按"存在"处理不删（外接盘/网络盘离线防误删）
func TestRegDisconnectedDrive(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)

	// 找一个本机不存在的盘符
	var dead string
	for _, l := range []string{"Q", "W", "V", "Y", "Z"} {
		if _, err := os.Stat(l + `:\`); err != nil {
			dead = l
			break
		}
	}
	if dead == "" {
		t.Skip("所有盘符都存在，无法测试断盘保护")
	}
	k := regMkKey(t, `MuiCache`)
	k.SetStringValue(dead+`:\gone\dead.exe.FriendlyAppName`, "断盘程序")
	k.Close()

	r := model.Rule{ID: "t-disc", RegKey: `HKCU\` + testRegRoot + `\MuiCache`,
		RegMode: "missing-target-name", Tool: "注册表清理", Risk: "caution", Action: "delete"}
	if items := scanRegRule(r, map[string]bool{}); len(items) != 0 {
		t.Fatalf("断盘路径不应判失效，got %+v", items)
	}
}

// regRoot HIVE 前缀解析
func TestRegRoot(t *testing.T) {
	k, sub, ok := regRoot(`HKCU\Software\X`)
	if !ok || k != registry.CURRENT_USER || sub != `Software\X` {
		t.Fatalf("HKCU parse: %v %q %v", ok, sub, k)
	}
	k, sub, ok = regRoot(`HKEY_LOCAL_MACHINE\SOFTWARE\Y`)
	if !ok || k != registry.LOCAL_MACHINE || sub != `SOFTWARE\Y` {
		t.Fatalf("HKLM parse: %v %q %v", ok, sub, k)
	}
	if _, _, ok = regRoot(`NOPE\X`); ok {
		t.Fatal("bad hive should fail")
	}
}

// orphan-uninstall：死路径子键列出，活路径/MSI/系统组件不列
func TestScanOrphanUninstall(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)

	exe, _ := os.Executable() // 一定存在的路径

	// 孤儿：所有路径引用都失效
	k := regMkKey(t, `Uninstall\{DEAD-1}`)
	k.SetStringValue("DisplayName", "死掉的软件")
	k.SetStringValue("InstallLocation", `C:\nonexistent-aiclean-test-xyz\dead`)
	k.SetStringValue("UninstallString", `C:\nonexistent-aiclean-test-xyz\unins.exe`)
	k.Close()

	// 活：InstallLocation 指向真实文件所在目录
	dir := filepath.Dir(exe)
	k = regMkKey(t, `Uninstall\{ALIVE-1}`)
	k.SetStringValue("DisplayName", "活着的软件")
	k.SetStringValue("InstallLocation", dir)
	k.Close()

	// MSI 管理：即使路径失效也跳过
	k = regMkKey(t, `Uninstall\{MSI-1}`)
	k.SetStringValue("DisplayName", "MSI 软件")
	k.SetStringValue("UninstallString", `MsiExec.exe /X{DEAD}`)
	k.SetDWordValue("WindowsInstaller", 1)
	k.Close()

	// 系统组件：跳过
	k = regMkKey(t, `Uninstall\{SYS-1}`)
	k.SetStringValue("DisplayName", "系统组件")
	k.SetDWordValue("SystemComponent", 1)
	k.SetStringValue("InstallLocation", `C:\nonexistent-aiclean-test-xyz`)
	k.Close()

	r := model.Rule{ID: "t-uninst", RegKey: `HKCU\` + testRegRoot + `\Uninstall`,
		RegMode: "orphan-uninstall", Tool: "注册表清理", Risk: "caution", Action: "review"}
	items := scanRegRule(r, map[string]bool{})

	if len(items) != 1 {
		t.Fatalf("want 1 orphan, got %d: %+v", len(items), items)
	}
	it := items[0]
	if !it.Registry || it.RegValue != "" || !strings.HasSuffix(it.RegKey, `{DEAD-1}`) {
		t.Fatalf("bad item: %+v", it)
	}
	if it.Dir != "死掉的软件" {
		t.Fatalf("dir should be DisplayName, got %q", it.Dir)
	}
	if it.Action != "none" { // review → 需确认
		t.Fatalf("action: %q", it.Action)
	}
}

// missing-target：值数据路径失效才列；相对路径/活路径不列
func TestScanMissingTarget(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)
	exe, _ := os.Executable()

	k := regMkKey(t, `Run`)
	k.SetStringValue("DeadApp", `"C:\nonexistent-aiclean-test-xyz\dead.exe" -silent`)
	k.SetStringValue("LiveApp", `"`+exe+`"`)
	k.SetStringValue("RelativeApp", `notepad.exe`) // 无法判定 → 保守不列
	k.Close()

	r := model.Rule{ID: "t-run", RegKey: `HKCU\` + testRegRoot + `\Run`,
		RegMode: "missing-target", Tool: "注册表清理", Risk: "caution", Action: "delete"}
	items := scanRegRule(r, map[string]bool{})

	if len(items) != 1 {
		t.Fatalf("want 1 stale value, got %d", len(items))
	}
	it := items[0]
	if it.RegValue != "DeadApp" || !strings.HasSuffix(it.Path, "#DeadApp") {
		t.Fatalf("bad item: %+v", it)
	}
	if it.Action != "clean" { // delete → 可清理
		t.Fatalf("action: %q", it.Action)
	}
}

// missing-target-name：值名里的路径失效才列（MuiCache/Store 形态）
func TestScanMissingTargetName(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)
	exe, _ := os.Executable()

	k := regMkKey(t, `MuiCache`)
	k.SetStringValue(`C:\nonexistent-aiclean-test-xyz\dead.exe.FriendlyAppName`, "死程序")
	k.SetStringValue(exe+`.FriendlyAppName`, "活程序")
	k.Close()

	r := model.Rule{ID: "t-mui", RegKey: `HKCU\` + testRegRoot + `\MuiCache`,
		RegMode: "missing-target-name", Tool: "注册表清理", Risk: "caution", Action: "delete"}
	items := scanRegRule(r, map[string]bool{})
	if len(items) != 1 || !strings.Contains(items[0].RegValue, "dead.exe") {
		t.Fatalf("want stale name item, got %+v", items)
	}
}

// missing-subkey：子键默认值路径失效才列（App Paths 形态）
func TestScanMissingSubkey(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)
	exe, _ := os.Executable()

	k := regMkKey(t, `AppPaths\dead.exe`)
	k.SetStringValue("", `C:\nonexistent-aiclean-test-xyz\dead.exe`)
	k.Close()
	k = regMkKey(t, `AppPaths\live.exe`)
	k.SetStringValue("", exe)
	k.Close()
	_ = regMkKey(t, `AppPaths\nodef.exe`) // 无默认值 → 跳过

	r := model.Rule{ID: "t-apppaths", RegKey: `HKCU\` + testRegRoot + `\AppPaths`,
		RegMode: "missing-subkey", Tool: "注册表清理", Risk: "caution", Action: "review"}
	items := scanRegRule(r, map[string]bool{})
	if len(items) != 1 || !strings.HasSuffix(items[0].RegKey, `dead.exe`) {
		t.Fatalf("want dead subkey item, got %+v", items)
	}
	if items[0].RegValue != "" {
		t.Fatalf("key-level item should have empty RegValue")
	}
}

// clear-values：每个含值的键一条项，RegValue="*"，递归子键
func TestScanClearValues(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)

	k := regMkKey(t, `MRU`)
	k.SetStringValue("a", "1")
	k.SetStringValue("b", "2")
	k.Close()
	k = regMkKey(t, `MRU\ext`)
	k.SetStringValue("x", "1")
	k.Close()
	_ = regMkKey(t, `MRU\empty`) // 空键不产项

	r := model.Rule{ID: "t-mru", RegKey: `HKCU\` + testRegRoot + `\MRU`,
		RegMode: "clear-values", Tool: "隐私痕迹", Risk: "caution", Action: "delete"}
	items := scanRegRule(r, map[string]bool{})

	if len(items) != 2 {
		t.Fatalf("want 2 items (root+ext), got %+v", items)
	}
	for _, it := range items {
		if it.RegValue != "*" {
			t.Fatalf("clear-values item RegValue should be '*', got %q", it.RegValue)
		}
	}
}

// 不存在的键 → 空结果不报错
func TestScanRegMissingKey(t *testing.T) {
	regTestCleanup(t)
	r := model.Rule{ID: "t-none", RegKey: `HKCU\` + testRegRoot + `\NoSuch`,
		RegMode: "missing-target", Tool: "注册表清理", Risk: "caution", Action: "delete"}
	if items := scanRegRule(r, map[string]bool{}); len(items) != 0 {
		t.Fatalf("want empty, got %+v", items)
	}
}

// requiresAdmin + 未提权 → 项标 needsAdmin/locked
func TestScanRegNeedsAdmin(t *testing.T) {
	regTestCleanup(t)
	defer regTestCleanup(t)
	old := Admin
	Admin = false
	defer func() { Admin = old }()

	k := regMkKey(t, `AdmRun`)
	k.SetStringValue("Dead", `C:\nonexistent-aiclean-test-xyz\d.exe`)
	k.Close()

	r := model.Rule{ID: "t-adm", RegKey: `HKLM\` + testRegRoot + `\AdmRun`, // HKLM 也会先走枚举失败→空；
		RegMode: "missing-target", Tool: "注册表清理", Risk: "caution", Action: "delete", RequiresAdmin: true}
	// HKLM 下的测试键不存在，换 HKCU 验证 needsAdmin 标记路径
	r.RegKey = `HKCU\` + testRegRoot + `\AdmRun`
	items := scanRegRule(r, map[string]bool{})
	if len(items) != 1 || !items[0].NeedsAdmin || items[0].State != "locked" {
		t.Fatalf("want needsAdmin locked item, got %+v", items)
	}
}
