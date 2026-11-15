// Package junction 实现 NTFS Mount Point Reparse Point（目录联结）的
// 创建、读取、验证与安全删除。遵循 AIClean 技术架构 V1.1.1：
//   - CreateFile(FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT)
//   - DeviceIoControl(FSCTL_SET_REPARSE_POINT) 写入 IO_REPARSE_TAG_MOUNT_POINT
//   - SubstituteName/PrintName 长度均不含 UTF-16 NUL
//   - 删除：先验证 tag+目标+FileID，同句柄 FSCTL_DELETE_REPARSE_POINT，再删空目录
//   - 任何情况下不得递归删除 Junction 目标
package junction

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	fsctlSetReparsePoint    = 0x000900A4
	fsctlGetReparsePoint    = 0x000900A8
	fsctlDeleteReparsePoint = 0x000900AC
	ioReparseTagMountPoint  = 0xA0000003

	fileFlagBackupSemantics  = 0x02000000
	fileFlagOpenReparsePoint = 0x00200000
	fileShareAll             = 0x7
	openExisting             = 3
	maxReparseBuf            = 16 * 1024 // Windows 16KB 上限
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateFile   = kernel32.NewProc("CreateFileW")
	procDeviceIoCtrl = kernel32.NewProc("DeviceIoControl")
	procGetFileInfo  = kernel32.NewProc("GetFileInformationByHandle")
)

var (
	ErrNotJunction   = errors.New("不是 mount-point junction")
	ErrTargetInvalid = errors.New("目标路径必须是规范化本地绝对路径")
	ErrDirNotEmpty   = errors.New("链接载体必须为空目录")
	ErrVerifyFailed  = errors.New("junction 验证失败")
)

// Info 描述一个已验证的 Junction
type Info struct {
	LinkPath  string // 联结目录自身路径
	TargetNT  string // \??\C:\... NT 形式
	TargetWin string // C:\... Win32 形式
	FileID    uint64
	Tag       uint32
}

// reparseDataBuffer 布局（mount point）：
//
//	ReparseTag(4) ReparseDataLength(2) Reserved(2)
//	SubstituteNameOffset(2) SubstituteNameLength(2) PrintNameOffset(2) PrintNameLength(2)
//	PathBuffer[sub\0print\0]
func buildReparseBuffer(targetNT, printName string) ([]byte, error) {
	sub := append([]uint16{}, utf16FromString(targetNT)...)
	prn := append([]uint16{}, utf16FromString(printName)...)
	subLen := len(sub) * 2 // 不含 NUL
	prnLen := len(prn) * 2 // 不含 NUL
	pathBytes := (len(sub) + 1 + len(prn) + 1) * 2
	dataLen := 8 + pathBytes
	total := 8 + dataLen
	if total > maxReparseBuf {
		return nil, fmt.Errorf("%w：reparse buffer 超过 16KB", ErrTargetInvalid)
	}
	buf := make([]byte, total)
	le := func(off int, v uint32) { *(*uint32)(unsafe.Pointer(&buf[off])) = v }
	le2 := func(off int, v uint16) { *(*uint16)(unsafe.Pointer(&buf[off])) = v }
	le(0, ioReparseTagMountPoint)
	le2(4, uint16(dataLen))
	le2(6, 0)
	le2(8, 0)                 // SubstituteNameOffset
	le2(10, uint16(subLen))   // SubstituteNameLength（不含 NUL）
	le2(12, uint16(subLen+2)) // PrintNameOffset = subLen + 2
	le2(14, uint16(prnLen))   // PrintNameLength（不含 NUL）
	off := 16
	for _, u := range sub {
		le2(off, u)
		off += 2
	}
	le2(off, 0)
	off += 2
	for _, u := range prn {
		le2(off, u)
		off += 2
	}
	le2(off, 0)
	return buf, nil
}

// parseMountPoint 从 GetReparsePoint 缓冲区解析目标
func parseMountPoint(buf []byte) (sub, prn string, tag uint32, err error) {
	if len(buf) < 16 {
		return "", "", 0, ErrNotJunction
	}
	tag = *(*uint32)(unsafe.Pointer(&buf[0]))
	if tag != ioReparseTagMountPoint {
		return "", "", tag, ErrNotJunction
	}
	subOff := int(*(*uint16)(unsafe.Pointer(&buf[8])))
	subLen := int(*(*uint16)(unsafe.Pointer(&buf[10])))
	prnOff := int(*(*uint16)(unsafe.Pointer(&buf[12])))
	prnLen := int(*(*uint16)(unsafe.Pointer(&buf[14])))
	base := 16 // PathBuffer 绝对偏移：8 字节头 + 8 字节 name 字段
	if base+subOff+subLen > len(buf) || base+prnOff+prnLen > len(buf) {
		return "", "", tag, ErrNotJunction
	}
	read := func(off, ln int) string {
		u := make([]uint16, ln/2)
		for i := range u {
			u[i] = *(*uint16)(unsafe.Pointer(&buf[base+off+i*2]))
		}
		return syscall.UTF16ToString(u)
	}
	return read(subOff, subLen), read(prnOff, prnLen), tag, nil
}

func utf16FromString(s string) []uint16 {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return nil
	}
	if len(u) > 0 && u[len(u)-1] == 0 {
		u = u[:len(u)-1]
	}
	return u
}

// normalizeTarget 校验并规范化目标为 NT 形式 \??\X:\path
// 拒绝：相对路径、`.`/`..` 组件、UNC/网络路径（在规范化之前检查）
func normalizeTarget(target string) (nt, win string, err error) {
	for _, seg := range strings.FieldsFunc(target, func(r rune) bool { return r == '\\' || r == '/' }) {
		if seg == ".." || seg == "." {
			return "", "", ErrTargetInvalid
		}
	}
	win = filepath.Clean(target)
	if !filepath.IsAbs(win) || strings.HasPrefix(target, `\\`) || strings.HasPrefix(win, `\\`) {
		return "", "", ErrTargetInvalid
	}
	if len(win) < 3 || win[1] != ':' {
		return "", "", ErrTargetInvalid
	}
	return `\??\` + win, win, nil
}

func openDir(path string, flags uintptr) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	r, _, e := procCreateFile.Call(
		uintptr(unsafe.Pointer(p)),
		0x80000000|0x00100000|0x00000001|0x00000002|0x00000004, // GENERIC_READ|GENERIC_WRITE|SYNCHRONIZE|READ|WRITE
		fileShareAll, 0, openExisting, flags, 0)
	if r == ^uintptr(0) {
		return 0, e
	}
	return syscall.Handle(r), nil
}

type byHandleInfo struct {
	Attributes    uint32
	CreationTime  [8]byte
	LastAccess    [8]byte
	LastWrite     [8]byte
	VolumeSerial  uint32
	FileSizeHigh  uint32
	FileSizeLow   uint32
	NumLinks      uint32
	FileIndexHigh uint32
	FileIndexLow  uint32
}

func fileID(h syscall.Handle) (uint64, error) {
	var i byHandleInfo
	r, _, e := procGetFileInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&i)))
	if r == 0 {
		return 0, e
	}
	return uint64(i.FileIndexHigh)<<32 | uint64(i.FileIndexLow), nil
}

func ioctl(h syscall.Handle, code uint32, in []byte, out []byte) error {
	var inPtr, outPtr unsafe.Pointer
	if len(in) > 0 {
		inPtr = unsafe.Pointer(&in[0])
	}
	if len(out) > 0 {
		outPtr = unsafe.Pointer(&out[0])
	}
	var ret uint32
	r, _, e := procDeviceIoCtrl.Call(
		uintptr(h), uintptr(code), uintptr(inPtr), uintptr(len(in)),
		uintptr(outPtr), uintptr(len(out)), uintptr(unsafe.Pointer(&ret)), 0)
	if r == 0 {
		return e
	}
	return nil
}

func readReparse(h syscall.Handle) ([]byte, error) {
	buf := make([]byte, maxReparseBuf)
	return buf, ioctl(h, fsctlGetReparsePoint, nil, buf)
}

// Create 在 linkPath（须为空目录）创建指向 target 的 Junction，并读回验证。
func Create(linkPath, target string) (*Info, error) {
	nt, win, err := normalizeTarget(target)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(linkPath)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w：链接载体目录不存在", ErrDirNotEmpty)
	}
	// 必须为空目录（载体由本次操作创建并记录身份）
	ents, err := os.ReadDir(linkPath)
	if err != nil {
		return nil, err
	}
	if len(ents) != 0 {
		return nil, ErrDirNotEmpty
	}
	if tgt, err := Read(linkPath); err == nil && tgt != nil {
		return nil, fmt.Errorf("路径已是 junction")
	}
	buf, err := buildReparseBuffer(nt, win)
	if err != nil {
		return nil, err
	}
	h, err := openDir(linkPath, fileFlagBackupSemantics)
	if err != nil {
		return nil, fmt.Errorf("打开目录句柄: %w", err)
	}
	defer syscall.CloseHandle(h)
	if err := ioctl(h, fsctlSetReparsePoint, buf, nil); err != nil {
		return nil, fmt.Errorf("设置 reparse point: %w", err)
	}
	// 读回验证
	info, err := Read(linkPath)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(info.TargetNT, nt) {
		return nil, ErrVerifyFailed
	}
	return info, nil
}

// Read 读取 linkPath 的 Junction 信息；非 Junction 返回 ErrNotJunction。
func Read(linkPath string) (*Info, error) {
	h, err := openDir(linkPath, fileFlagBackupSemantics|fileFlagOpenReparsePoint)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(h)
	buf, err := readReparse(h)
	if err != nil {
		return nil, ErrNotJunction // ERROR_NOT_A_REPARSE_POINT 等
	}
	sub, prn, tag, err := parseMountPoint(buf)
	if err != nil {
		return nil, err
	}
	fid, err := fileID(h)
	if err != nil {
		return nil, err
	}
	win := prn
	if win == "" {
		win = strings.TrimPrefix(sub, `\??\`)
	}
	return &Info{LinkPath: linkPath, TargetNT: sub, TargetWin: win, FileID: fid, Tag: tag}, nil
}

// Remove 安全删除 Junction：先开句柄验证 tag+目标+FileID，
// 同句柄 FSCTL_DELETE_REPARSE_POINT，再删除空目录载体并复核。
// expectedTarget 为空则不做目标比对（仅用于已确认是我们创建的链接）。
func Remove(linkPath, expectedTarget string) error {
	info, err := Read(linkPath)
	if err != nil {
		return err
	}
	if expectedTarget != "" && !strings.EqualFold(info.TargetWin, filepath.Clean(expectedTarget)) {
		return fmt.Errorf("%w：目标不一致 (%s != %s)", ErrVerifyFailed, info.TargetWin, expectedTarget)
	}
	wantID := info.FileID
	h, err := openDir(linkPath, fileFlagBackupSemantics|fileFlagOpenReparsePoint)
	if err != nil {
		return err
	}
	// 二次验证 FileID，防止路径替换竞争
	cur, err := fileID(h)
	if err != nil {
		syscall.CloseHandle(h)
		return err
	}
	if cur != wantID {
		syscall.CloseHandle(h)
		return ErrVerifyFailed
	}
	// 同一句柄删除 reparse point（只删链接属性，不碰目标数据）
	rd := make([]byte, 8)
	*(*uint32)(unsafe.Pointer(&rd[0])) = ioReparseTagMountPoint
	*(*uint16)(unsafe.Pointer(&rd[4])) = 0
	if err := ioctl(h, fsctlDeleteReparsePoint, rd, nil); err != nil {
		syscall.CloseHandle(h)
		return fmt.Errorf("删除 reparse point: %w", err)
	}
	syscall.CloseHandle(h)
	// 现在 linkPath 是普通空目录，删除载体（RemoveDirectory 不递归）
	if err := os.Remove(linkPath); err != nil {
		return fmt.Errorf("删除空目录载体: %w", err)
	}
	if _, err := os.Stat(linkPath); !os.IsNotExist(err) {
		return fmt.Errorf("载体删除后路径仍存在")
	}
	return nil
}

// IsJunction 快速判断路径是否为 mount-point junction。
func IsJunction(path string) bool {
	_, err := Read(path)
	return err == nil
}
