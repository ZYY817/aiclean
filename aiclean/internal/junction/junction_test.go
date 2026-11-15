package junction

import (
	"os"
	"path/filepath"
	"testing"
)

// 真实 NTFS 测试：全部使用 t.TempDir() 合成目录，不触碰真实用户目录。

func setup(t *testing.T) (target, link string) {
	t.Helper()
	base := t.TempDir()
	target = filepath.Join(base, "目标数据_中文")
	link = filepath.Join(base, "链接载体")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "data.bin"), []byte("payload-真实数据"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(link, 0755); err != nil {
		t.Fatal(err)
	}
	return target, link
}

func TestCreateReadRemove(t *testing.T) {
	target, link := setup(t)

	info, err := Create(link, target)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !IsJunction(link) {
		t.Fatal("创建后 IsJunction=false")
	}
	// 通过 junction 访问目标内容
	b, err := os.ReadFile(filepath.Join(link, "data.bin"))
	if err != nil || string(b) != "payload-真实数据" {
		t.Fatalf("junction 读取目标失败: %v", err)
	}
	// 删除：目标数据必须原封不动
	if err := Remove(link, target); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "data.bin")); err != nil {
		t.Fatalf("目标数据丢失: %v", err)
	}
	if IsJunction(link) {
		t.Fatal("删除后 IsJunction 仍为 true")
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Fatal("链接载体未删除")
	}
	_ = info
}

func TestCreateRejectsNonEmpty(t *testing.T) {
	target, link := setup(t)
	os.WriteFile(filepath.Join(link, "x.txt"), []byte("x"), 0644)
	if _, err := Create(link, target); err != ErrDirNotEmpty {
		t.Fatalf("非空目录应拒绝, got %v", err)
	}
}

func TestCreateRejectsBadTarget(t *testing.T) {
	_, link := setup(t)
	for _, bad := range []string{"relative\\path", `\\server\share`, "C:\\..\\Windows"} {
		if _, err := Create(link, bad); err == nil {
			t.Fatalf("非法目标应拒绝: %s", bad)
		}
	}
}

func TestRemoveRejectsWrongTarget(t *testing.T) {
	target, link := setup(t)
	if _, err := Create(link, target); err != nil {
		t.Fatal(err)
	}
	if err := Remove(link, filepath.Join(t.TempDir(), "other")); err == nil {
		t.Fatal("目标不匹配应拒绝删除")
	}
	// 链接仍在
	if !IsJunction(link) {
		t.Fatal("误删了 junction")
	}
}

func TestReadNonJunction(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(dir); err != ErrNotJunction {
		t.Fatalf("普通目录应返回 ErrNotJunction, got %v", err)
	}
}
