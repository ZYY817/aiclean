package ops

// 注册表备份/删除/恢复测试：真 HKCU 测试键 + 临时隔离区。
// 覆盖：值删除、键删除、"*"清空、恢复类型回写、删除失败不残留、.reg 文本格式。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

const testRegSub = `Software\AIClean-OpsTest`

func regOpsCleanup(t *testing.T) {
	t.Helper()
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
	del(testRegSub)
}

func regOpsMk(t *testing.T, sub string) registry.Key {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, sub,
		registry.SET_VALUE|registry.CREATE_SUB_KEY|registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		t.Fatalf("create %s: %v", sub, err)
	}
	return k
}

func regTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStoreAt(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

// 值删除：备份值→删值→恢复值回来（类型保留）
func TestRegValueBackupDeleteRestore(t *testing.T) {
	regOpsCleanup(t)
	defer regOpsCleanup(t)
	s := regTestStore(t)

	k := regOpsMk(t, testRegSub+`\Vals`)
	k.SetStringValue("KeepMe", "留着")
	k.SetStringValue("DeadOne", "删掉我")
	k.SetDWordValue("NumOne", 42)
	k.Close()

	meta, err := s.RegBackupDelete(`HKCU\`+testRegSub+`\Vals`, "DeadOne", "测试", "note", 30)
	if err != nil {
		t.Fatalf("backup-delete: %v", err)
	}
	// 值已删
	k, _ = registry.OpenKey(registry.CURRENT_USER, testRegSub+`\Vals`, registry.QUERY_VALUE)
	if _, _, err := k.GetStringValue("DeadOne"); err == nil {
		t.Fatal("DeadOne should be deleted")
	}
	if v, _, err := k.GetStringValue("KeepMe"); err != nil || v != "留着" {
		t.Fatal("KeepMe should remain")
	}
	k.Close()
	// 备份文件齐：backup.json + backup.reg + meta
	dir := filepath.Join(s.quarDir(), meta.ID)
	for _, f := range []string{"backup.json", "backup.reg", ".aiclean.meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	// List() 能看到、origPath 是 reg:
	list := s.List()
	found := false
	for _, m := range list {
		if m.ID == meta.ID && strings.HasPrefix(m.OrigPath, "reg:") {
			found = true
		}
	}
	if !found {
		t.Fatal("quar list missing reg meta")
	}
	// 恢复 → 值回来
	if err := s.Restore(meta.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, testRegSub+`\Vals`, registry.QUERY_VALUE)
	v, _, err := k.GetStringValue("DeadOne")
	k.Close()
	if err != nil || v != "删掉我" {
		t.Fatalf("restored value = %q err %v", v, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("quar dir should be gone after restore")
	}
}

// 键删除：整树备份（含子键）→删键→恢复整树
func TestRegKeyBackupDeleteRestore(t *testing.T) {
	regOpsCleanup(t)
	defer regOpsCleanup(t)
	s := regTestStore(t)

	k := regOpsMk(t, testRegSub+`\Dead\{GUID}`)
	k.SetStringValue("DisplayName", "孤儿软件")
	k.SetDWordValue("Ver", 7)
	k.Close()
	sk := regOpsMk(t, testRegSub+`\Dead\{GUID}\Updates`)
	sk.SetStringValue("U1", "x")
	sk.Close()

	meta, err := s.RegBackupDelete(`HKCU\`+testRegSub+`\Dead\{GUID}`, "", "测试", "", 30)
	if err != nil {
		t.Fatalf("backup-delete key: %v", err)
	}
	// 键+子键都删了
	if _, err := registry.OpenKey(registry.CURRENT_USER, testRegSub+`\Dead\{GUID}`, registry.QUERY_VALUE); err == nil {
		t.Fatal("key should be deleted")
	}
	if _, err := registry.OpenKey(registry.CURRENT_USER, testRegSub+`\Dead\{GUID}\Updates`, registry.QUERY_VALUE); err == nil {
		t.Fatal("subkey should be deleted")
	}
	// 恢复 → 整树回来
	if err := s.Restore(meta.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, testRegSub+`\Dead\{GUID}`, registry.QUERY_VALUE)
	v, _, err := k.GetStringValue("DisplayName")
	k.Close()
	if err != nil || v != "孤儿软件" {
		t.Fatalf("restored DisplayName = %q err %v", v, err)
	}
	sk, _ = registry.OpenKey(registry.CURRENT_USER, testRegSub+`\Dead\{GUID}\Updates`, registry.QUERY_VALUE)
	v, _, err = sk.GetStringValue("U1")
	sk.Close()
	if err != nil || v != "x" {
		t.Fatalf("restored subkey value = %q err %v", v, err)
	}
}

// "*" 模式：清空键下全部值，保留键本身；恢复后全部回写
func TestRegClearValues(t *testing.T) {
	regOpsCleanup(t)
	defer regOpsCleanup(t)
	s := regTestStore(t)

	k := regOpsMk(t, testRegSub+`\MRU`)
	k.SetStringValue("a", "1")
	k.SetStringValue("b", "2")
	k.SetBinaryValue("bin", []byte{1, 2, 3})
	k.Close()

	meta, err := s.RegBackupDelete(`HKCU\`+testRegSub+`\MRU`, "*", "测试", "", 30)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, testRegSub+`\MRU`, registry.QUERY_VALUE)
	names, _ := k.ReadValueNames(-1)
	k.Close()
	if len(names) != 0 {
		t.Fatalf("key should have 0 values, got %v", names)
	}
	// 键本身还在
	if _, err := registry.OpenKey(registry.CURRENT_USER, testRegSub+`\MRU`, registry.QUERY_VALUE); err != nil {
		t.Fatal("key itself should remain")
	}
	if err := s.Restore(meta.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, testRegSub+`\MRU`, registry.QUERY_VALUE)
	names, _ = k.ReadValueNames(-1)
	b, _, _ := k.GetBinaryValue("bin")
	k.Close()
	if len(names) != 3 || len(b) != 3 {
		t.Fatalf("restored values = %v bin=%v", names, b)
	}
}

// 删除不存在的键 → 报错，隔离区不留垃圾（幂等：第二次操作不炸）
func TestRegDeleteMissing(t *testing.T) {
	regOpsCleanup(t)
	defer regOpsCleanup(t)
	s := regTestStore(t)

	_, err := s.RegBackupDelete(`HKCU\`+testRegSub+`\NoSuch`, "", "测试", "", 30)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	// 隔离区应为空
	entries, _ := os.ReadDir(s.quarDir())
	if len(entries) != 0 {
		t.Fatalf("quar dir should be empty, got %d entries", len(entries))
	}
	// 不存在的值
	k := regOpsMk(t, testRegSub+`\Empty`)
	k.Close()
	_, err = s.RegBackupDelete(`HKCU\`+testRegSub+`\Empty`, "NoValue", "测试", "", 30)
	if err == nil {
		t.Fatal("expected error for missing value")
	}
}

// backup.reg 是合法可导入格式
func TestRegExportFormat(t *testing.T) {
	regOpsCleanup(t)
	defer regOpsCleanup(t)
	s := regTestStore(t)

	k := regOpsMk(t, testRegSub+`\Exp`)
	k.SetStringValue("S", "文本")
	k.SetDWordValue("D", 255)
	k.SetBinaryValue("B", []byte{0xDE, 0xAD})
	k.Close()

	meta, err := s.RegBackupDelete(`HKCU\`+testRegSub+`\Exp`, "", "测试", "", 30)
	if err != nil {
		t.Fatalf("del: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(s.quarDir(), meta.ID, "backup.reg"))
	if err != nil {
		t.Fatalf("read reg: %v", err)
	}
	txt := string(b)
	if !strings.HasPrefix(txt, "Windows Registry Editor Version 5.00") {
		t.Fatal("bad reg header")
	}
	if !strings.Contains(txt, `[HKEY_CURRENT_USER\Software\AIClean-OpsTest\Exp]`) {
		t.Fatalf("missing key header:\n%s", txt)
	}
	if !strings.Contains(txt, `"S"="文本"`) || !strings.Contains(txt, `"D"=dword:000000ff`) || !strings.Contains(txt, `"B"=hex:de,ad`) {
		t.Fatalf("missing values:\n%s", txt)
	}
	// backup.json 里类型齐全
	jb, _ := os.ReadFile(filepath.Join(s.quarDir(), meta.ID, "backup.json"))
	var bk regBackup
	if err := json.Unmarshal(jb, &bk); err != nil || bk.Keys == nil || len(bk.Keys.Values) != 3 {
		t.Fatalf("backup.json malformed: %v", err)
	}
}

// 恢复不存在/损坏备份 → 报错不炸
func TestRegRestoreMissing(t *testing.T) {
	s := regTestStore(t)
	if err := s.Restore("qar_nonexistent"); err == nil {
		t.Fatal("expected error")
	}
	if err := s.RegRestore("qar_nonexistent"); err == nil {
		t.Fatal("expected error")
	}
}
