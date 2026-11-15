package migrate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"aiclean/internal/junction"
)

func mkSrc(t *testing.T) string {
	d := t.TempDir()
	src := filepath.Join(d, "AI数据_测试")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a.bin"), []byte("data"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.bin"), make([]byte, 50), 0o644)
	return src
}

func TestMigrateSwitchover(t *testing.T) {
	root := t.TempDir()
	src := mkSrc(t)
	dst := filepath.Join(root, "moved", "AI数据_测试")
	rec, err := Migrate(src, dst)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 源路径应成为指向 dst 的 junction
	info, err := junction.Read(src)
	if err != nil {
		t.Fatalf("junction read: %v", err)
	}
	if info.TargetWin != filepath.Clean(dst) {
		t.Fatalf("target = %s, want %s", info.TargetWin, dst)
	}
	// 经由 junction 能读到数据
	if _, err := os.Stat(filepath.Join(src, "sub", "b.bin")); err != nil {
		t.Fatal("junction 下数据不可达")
	}
	if _, err := os.Stat(rec.Bak); err != nil {
		t.Fatal("bak 应保留")
	}
	// 回滚
	if err := Rollback(rec); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if junction.IsJunction(src) {
		t.Fatal("回滚后不应再是 junction")
	}
	if b, err := os.ReadFile(filepath.Join(src, "a.bin")); err != nil || string(b) != "data" {
		t.Fatal("回滚后数据不完整")
	}
}

// 目标被外部删除后 Rollback：必须从 bak 还原，绝不能删 bak 造成数据全丢
func TestRollbackDstGone(t *testing.T) {
	root := t.TempDir()
	src := mkSrc(t)
	dst := filepath.Join(root, "moved", "AI数据_测试")
	rec, err := Migrate(src, dst)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	os.RemoveAll(dst) // 模拟目标盘数据被外部清掉
	if err := Rollback(rec); err != nil {
		t.Fatalf("rollback dst-gone: %v", err)
	}
	if junction.IsJunction(src) {
		t.Fatal("回滚后不应再是 junction")
	}
	if b, err := os.ReadFile(filepath.Join(src, "a.bin")); err != nil || string(b) != "data" {
		t.Fatal("bak 还原后数据不完整")
	}
	if _, err := os.Stat(rec.Bak); !os.IsNotExist(err) {
		t.Fatal("bak 已变成 src，不应再单独存在")
	}
}

// 目标和备份都没了：拒绝（不能假装成功把 junction 拆掉留下空位）
func TestRollbackBothGone(t *testing.T) {
	root := t.TempDir()
	src := mkSrc(t)
	dst := filepath.Join(root, "moved", "AI数据_测试")
	rec, err := Migrate(src, dst)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	os.RemoveAll(dst)
	os.RemoveAll(rec.Bak)
	if err := Rollback(rec); err == nil {
		t.Fatal("目标与备份都丢失时应报错")
	}
	if !junction.IsJunction(src) {
		t.Fatal("回滚失败应保留 junction（数据位置线索）")
	}
}

func TestMigrateRejectsOccupiedDst(t *testing.T) {
	src := mkSrc(t)
	dst := filepath.Join(t.TempDir(), "x")
	os.MkdirAll(dst, 0o755)
	if _, err := Migrate(src, dst); err == nil {
		t.Fatal("目标被占用应拒绝")
	}
}

func TestPreflight(t *testing.T) {
	src := mkSrc(t)
	cs := Preflight(src, filepath.Join(t.TempDir(), "new"))
	for _, c := range cs {
		if !c.OK {
			t.Fatalf("preflight %q 未通过: %s", c.Name, c.Msg)
		}
	}
}

// ---------- 发布审计回归：链接保护 + 快速迁回 ----------

// TestMigrateRejectsLinksInside 源目录内含 junction/symlink 时必须整体拒绝：
// copyTree 会跳过链接、dirStat 校验也跳过 → 校验照样过、删备份后链接永久丢失
func TestMigrateRejectsLinksInside(t *testing.T) {
	src := mkSrc(t)
	real := filepath.Join(t.TempDir(), "real-target")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(src, "sub", "inner-link")
	if _, err := junction.Create(link, real); err != nil {
		t.Skipf("junction create: %v", err)
	}
	var failed bool
	for _, c := range Preflight(src, filepath.Join(t.TempDir(), "new")) {
		if !c.OK {
			failed = true
			t.Logf("refused by: %s — %s", c.Name, c.Msg)
		}
	}
	if !failed {
		t.Fatal("含内部链接的源目录应被预检拒绝")
	}
	if _, err := Migrate(src, filepath.Join(t.TempDir(), "new")); err == nil {
		t.Fatal("Migrate 应拒绝含内部链接的源")
	}
}

// TestRollbackPreflightFast 备份还在且目标未改动 → 快速路径（直接改名，不占空间）
func TestRollbackPreflightFast(t *testing.T) {
	src := mkSrc(t)
	dst := filepath.Join(t.TempDir(), "moved", "x")
	rec, err := Migrate(src, dst)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	need, _, fast := RollbackPreflight(rec)
	if !fast || need != 0 {
		t.Fatalf("unchanged migration should be fastPath, need=%d fast=%v", need, fast)
	}
}

// TestRollbackModifiedDst 目标盘数据迁移后被改动 → 快速路径不适用，
// 必须走复制（新数据赢），且 bak 与 dst 都清掉
func TestRollbackModifiedDst(t *testing.T) {
	src := mkSrc(t)
	dst := filepath.Join(t.TempDir(), "moved", "x")
	rec, err := Migrate(src, dst)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 模拟迁移后应用往链接里写了新数据
	os.WriteFile(filepath.Join(dst, "new-after-mig.bin"), []byte("new"), 0o644)
	// dst 被改动后不应命中"备份直接改名"快速路径（同卷时仍走 rename——结果等价）；
	// 跨卷场景 RollbackPreflight 才会报 need>0
	if err := Rollback(rec); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(src, "new-after-mig.bin")); err != nil || string(b) != "new" {
		t.Fatal("迁移后新写的数据应保留（复制路径）")
	}
	if _, err := os.Lstat(rec.Bak); !os.IsNotExist(err) {
		t.Fatal("bak 应在迁回后清除")
	}
	if _, err := os.Lstat(rec.Dst); !os.IsNotExist(err) {
		t.Fatal("dst 副本应在迁回后清除")
	}
}

// TestRollbackSameSizeEditNotLost 同尺寸改写不能丢数据：
// 迁移后通过链接把文件改写成同长度新内容（SQLite/聊天库典型场景），
// 迁回必须保留新内容——旧版只比总字节会把备份改回原名静默丢数据。
func TestRollbackSameSizeEditNotLost(t *testing.T) {
	src := mkSrc(t)
	dst := filepath.Join(t.TempDir(), "moved", "x")
	rec, err := Migrate(src, dst)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 同长度改写（"data"→"XXXX"）：总字节/文件数不变，mtime 变
	if err := os.WriteFile(filepath.Join(dst, "a.bin"), []byte("XXXX"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 关键：manifest 必须判出"已被改动"——旧版只比总字节会在这里放行
	if sameContent(rec.Bak, rec.Dst) {
		t.Fatal("same-size edit not detected by manifest compare")
	}
	if err := Rollback(rec); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(src, "a.bin"))
	if err != nil || string(b) != "XXXX" {
		t.Fatalf("same-size edit lost: got %q want XXXX", string(b))
	}
	if _, err := os.Lstat(rec.Dst); !os.IsNotExist(err) {
		t.Fatal("dst should be removed after rollback")
	}
}

// TestManifestDetectsSameSizeEdit manifest 比对本身：同尺寸不同 mtime 判不等
func TestManifestDetectsSameSizeEdit(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, d := range []string{a, b} {
		os.WriteFile(filepath.Join(d, "f.bin"), []byte("1234"), 0o644)
	}
	// 强制不同 mtime
	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.Local)
	os.Chtimes(filepath.Join(a, "f.bin"), old, old)
	if sameContent(a, b) {
		t.Fatal("same size, different mtime should be unequal")
	}
	os.Chtimes(filepath.Join(b, "f.bin"), old, old)
	if !sameContent(a, b) {
		t.Fatal("identical size+mtime should be equal")
	}
}
