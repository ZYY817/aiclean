package ops

// 注册表项的"隔离"实现：注册表没法移动文件，等价物 = 导出备份再删。
// 每个隔离目录含：
//   backup.json  类型精确的机器可读备份（恢复用它）
//   backup.reg   标准 .reg 文本（用户双击也能手动导入）
//   .aiclean.meta.json  与文件隔离项相同的 Meta，OrigPath=reg:HIVE\Key[\#Value]
// Restore 走同一目录；purge 删除目录即不可恢复，和文件语义一致。

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

type regVal struct {
	Name string `json:"name"`
	Type uint32 `json:"type"`
	Str  string `json:"str,omitempty"`  // SZ / EXPAND_SZ
	Strs []string `json:"strs,omitempty"` // MULTI_SZ
	Num  uint64 `json:"num,omitempty"`  // DWORD / QWORD
	Bin  string `json:"bin,omitempty"`  // 其余类型 base64
}

type regNode struct {
	Values []regVal            `json:"values,omitempty"`
	Sub    map[string]*regNode `json:"sub,omitempty"`
}

type regBackup struct {
	Root   string   `json:"root"`           // HKCU/HKLM/...
	Sub    string   `json:"sub"`            // 键相对路径
	Value  bool     `json:"valueOnly"`      // true=只备份了几个值（value 删除）；false=整键
	Keys   *regNode `json:"key,omitempty"`  // 整键树（键删除）
	Values []regVal `json:"values,omitempty"` // 被删的值（值删除）
}

func regRootOf(full string) (registry.Key, string, error) {
	for _, h := range []struct {
		p string
		k registry.Key
	}{
		{`HKCU\`, registry.CURRENT_USER}, {`HKLM\`, registry.LOCAL_MACHINE},
		{`HKCR\`, registry.CLASSES_ROOT}, {`HKU\`, registry.USERS},
		{`HKCC\`, registry.CURRENT_CONFIG},
		{`HKEY_CURRENT_USER\`, registry.CURRENT_USER}, {`HKEY_LOCAL_MACHINE\`, registry.LOCAL_MACHINE},
		{`HKEY_CLASSES_ROOT\`, registry.CLASSES_ROOT}, {`HKEY_USERS\`, registry.USERS},
		{`HKEY_CURRENT_CONFIG\`, registry.CURRENT_CONFIG},
	} {
		if strings.HasPrefix(strings.ToUpper(full), strings.ToUpper(h.p)) {
			return h.k, full[len(h.p):], nil
		}
	}
	return 0, "", fmt.Errorf("不认识的注册表根: %s", full)
}

// regReadValue 读一个值的类型化数据（含未知类型的原始字节）
func regReadValue(k registry.Key, name string) (regVal, error) {
	n, vtype, err := k.GetValue(name, nil)
	if err != nil && !errors.Is(err, syscall.ERROR_MORE_DATA) {
		return regVal{}, err
	}
	buf := make([]byte, n+8)
	var n2 int
	n2, vtype, err = k.GetValue(name, buf)
	if err != nil {
		return regVal{}, err
	}
	data := buf[:n2]
	v := regVal{Name: name, Type: vtype}
	switch vtype {
	case registry.SZ, registry.EXPAND_SZ:
		u := make([]uint16, len(data)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(data[i*2:])
		}
		v.Str = strings.TrimRight(string(utf16.Decode(u)), "\x00")
	case registry.MULTI_SZ:
		u := make([]uint16, len(data)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(data[i*2:])
		}
		for _, s := range strings.Split(string(utf16.Decode(u)), "\x00") {
			if s != "" {
				v.Strs = append(v.Strs, s)
			}
		}
	case registry.DWORD:
		if len(data) >= 4 {
			v.Num = uint64(binary.LittleEndian.Uint32(data))
		}
	case registry.QWORD:
		if len(data) >= 8 {
			v.Num = binary.LittleEndian.Uint64(data)
		}
	default:
		v.Bin = base64.StdEncoding.EncodeToString(data)
	}
	return v, nil
}

// regReadTree 读整棵子树（键删除备份用）
func regReadTree(k registry.Key, root registry.Key, sub string) (*regNode, error) {
	node := &regNode{}
	if names, err := k.ReadValueNames(-1); err == nil {
		for _, vn := range names {
			if v, err := regReadValue(k, vn); err == nil {
				node.Values = append(node.Values, v)
			}
		}
	}
	if subs, err := k.ReadSubKeyNames(-1); err == nil {
		for _, sn := range subs {
			sk, err := registry.OpenKey(root, sub+`\`+sn, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
			if err != nil {
				continue
			}
			if child, err := regReadTree(sk, root, sub+`\`+sn); err == nil && child != nil {
				if node.Sub == nil {
					node.Sub = map[string]*regNode{}
				}
				node.Sub[sn] = child
			}
			sk.Close()
		}
	}
	return node, nil
}

// regWriteValues 把备份值写回已打开的键
func regWriteValues(k registry.Key, vs []regVal) error {
	for _, v := range vs {
		var err error
		switch v.Type {
		case registry.SZ:
			err = k.SetStringValue(v.Name, v.Str)
		case registry.EXPAND_SZ:
			err = k.SetExpandStringValue(v.Name, v.Str)
		case registry.MULTI_SZ:
			err = k.SetStringsValue(v.Name, v.Strs)
		case registry.DWORD:
			err = k.SetDWordValue(v.Name, uint32(v.Num))
		case registry.QWORD:
			err = k.SetQWordValue(v.Name, v.Num)
		default:
			var b []byte
			if b, err = base64.StdEncoding.DecodeString(v.Bin); err == nil {
				err = k.SetBinaryValue(v.Name, b)
			}
		}
		if err != nil {
			return fmt.Errorf("写回值 %s 失败: %w", v.Name, err)
		}
	}
	return nil
}

func regWriteTree(k registry.Key, root registry.Key, sub string, node *regNode) error {
	if err := regWriteValues(k, node.Values); err != nil {
		return err
	}
	for name, child := range node.Sub {
		sk, _, err := registry.CreateKey(root, sub+`\`+name, registry.SET_VALUE|registry.CREATE_SUB_KEY)
		if err != nil {
			return fmt.Errorf("重建子键 %s 失败: %w", name, err)
		}
		err = regWriteTree(sk, root, sub+`\`+name, child)
		sk.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// regDeleteTree 递归删除子键后删键本身（RegDeleteKey 不删非空键）
func regDeleteTree(root registry.Key, sub string) error {
	k, err := registry.OpenKey(root, sub, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return registry.DeleteKey(root, sub) // 打不开就交给系统报准确错误
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, sn := range subs {
		if err := regDeleteTree(root, sub+`\`+sn); err != nil {
			return err
		}
	}
	return registry.DeleteKey(root, sub)
}

// regHiveDot 完整键路径 → .reg 文件里用的 HKEY_ 全称
func regHiveDot(full string) string {
	up := strings.ToUpper(full)
	switch {
	case strings.HasPrefix(up, "HKCU"), strings.HasPrefix(up, "HKEY_CURRENT_USER"):
		return "HKEY_CURRENT_USER\\" + full[strings.Index(full, `\`)+1:]
	case strings.HasPrefix(up, "HKLM"), strings.HasPrefix(up, "HKEY_LOCAL_MACHINE"):
		return "HKEY_LOCAL_MACHINE\\" + full[strings.Index(full, `\`)+1:]
	case strings.HasPrefix(up, "HKCR"), strings.HasPrefix(up, "HKEY_CLASSES_ROOT"):
		return "HKEY_CLASSES_ROOT\\" + full[strings.Index(full, `\`)+1:]
	case strings.HasPrefix(up, "HKU"), strings.HasPrefix(up, "HKEY_USERS"):
		return "HKEY_USERS\\" + full[strings.Index(full, `\`)+1:]
	}
	return full
}

// regExportText 生成标准 .reg 文本
func regExportText(bk *regBackup, fullKey string) string {
	var sb strings.Builder
	sb.WriteString("Windows Registry Editor Version 5.00\r\n\r\n")
	writeVals := func(keyName string, vs []regVal) {
		sb.WriteString("[" + keyName + "]\r\n")
		for _, v := range vs {
			name := `"` + strings.ReplaceAll(v.Name, `\`, `\\`) + `"`
			if v.Name == "" {
				name = "@"
			}
			switch v.Type {
			case registry.SZ:
				sb.WriteString(fmt.Sprintf("%s=\"%s\"\r\n", name, strings.ReplaceAll(v.Str, `"`, `\"`)))
			case registry.DWORD:
				sb.WriteString(fmt.Sprintf("%s=dword:%08x\r\n", name, v.Num))
			case registry.QWORD:
				sb.WriteString(fmt.Sprintf("%s=hex(b):%s\r\n", name, hexQword(v.Num)))
			case registry.EXPAND_SZ:
				sb.WriteString(fmt.Sprintf("%s=hex(2):%s\r\n", name, hexUTF16(v.Str)))
			case registry.MULTI_SZ:
				sb.WriteString(fmt.Sprintf("%s=hex(7):%s\r\n", name, hexUTF16(strings.Join(v.Strs, "\x00")+"\x00")))
			default:
				b, _ := base64.StdEncoding.DecodeString(v.Bin)
				sb.WriteString(fmt.Sprintf("%s=hex:%s\r\n", name, hexBytes(b)))
			}
		}
		sb.WriteString("\r\n")
	}
	dot := regHiveDot(fullKey)
	if bk.Value {
		writeVals(dot, bk.Values)
	} else {
		var walk func(keyName string, n *regNode)
		walk = func(keyName string, n *regNode) {
			writeVals(keyName, n.Values)
			for name, c := range n.Sub {
				walk(keyName+`\`+name, c)
			}
		}
		walk(dot, bk.Keys)
	}
	return sb.String()
}

func hexBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%02x", c)
	}
	return sb.String()
}

func hexQword(v uint64) string {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return hexBytes(b)
}

func hexUTF16(s string) string {
	var b []byte
	for _, u := range utf16.Encode([]rune(s + "\x00")) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return hexBytes(b)
}

// RegBackupDelete 注册表版"隔离"：先备份（typed JSON + .reg）再删键/值。
// 备份失败→不删；删除失败→删备份不留垃圾。返回 Meta 供前端/审计。
// regKey=完整键路径(HKCU\...)，regValue=值名（空=删整个键）。
func (s *Store) RegBackupDelete(regKey, regValue, app, note string, retainDays int) (*Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regBackupDeleteL(regKey, regValue, app, note, retainDays)
}

func (s *Store) regBackupDeleteL(regKey, regValue, app, note string, retainDays int) (*Meta, error) {
	if retainDays <= 0 {
		retainDays = 30
	}
	root, sub, err := regRootOf(regKey)
	if err != nil {
		return nil, err
	}
	k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, fmt.Errorf("键不存在或不可读: %w", err)
	}
	bk := &regBackup{Root: regHivePrefixShort(regKey), Sub: sub}
	switch regValue {
	case "":
		bk.Value = false
		if bk.Keys, err = regReadTree(k, root, sub); err != nil {
			k.Close()
			return nil, fmt.Errorf("读取键失败: %w", err)
		}
	case "*": // 清空该键全部值（MRU 列表），保留键本身
		bk.Value = true
		names, err := k.ReadValueNames(-1)
		if err != nil || len(names) == 0 {
			k.Close()
			return nil, fmt.Errorf("键下没有值可清")
		}
		for _, vn := range names {
			v, err := regReadValue(k, vn)
			if err != nil {
				k.Close()
				return nil, fmt.Errorf("读取值 %s 失败: %w", vn, err)
			}
			bk.Values = append(bk.Values, v)
		}
	default:
		bk.Value = true
		v, err := regReadValue(k, regValue)
		if err != nil {
			k.Close()
			return nil, fmt.Errorf("读取值 %s 失败（可能已被删）: %w", regValue, err)
		}
		bk.Values = []regVal{v}
	}
	k.Close()

	// 隔离目录：qar_<ts>_<us>_<id>
	id := fmt.Sprintf("qar_%s_%06d_reg", time.Now().Format("20060102_150405"), time.Now().Nanosecond()/1000)
	dst := filepath.Join(s.quarDir(), id)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	jb, _ := json.MarshalIndent(bk, "", "  ")
	if err := os.WriteFile(filepath.Join(dst, "backup.json"), jb, 0o644); err != nil {
		os.RemoveAll(dst)
		return nil, err
	}
	regTxt := regExportText(bk, regKey)
	if err := os.WriteFile(filepath.Join(dst, "backup.reg"), []byte(regTxt), 0o644); err != nil {
		os.RemoveAll(dst)
		return nil, err
	}

	// 备份落盘后才执行删除
	switch regValue {
	case "":
		err = regDeleteTree(root, sub)
	case "*":
		var dk registry.Key
		dk, err = registry.OpenKey(root, sub, registry.SET_VALUE)
		if err == nil {
			for _, v := range bk.Values {
				if e := dk.DeleteValue(v.Name); e != nil {
					err = fmt.Errorf("删除值 %s 失败: %w", v.Name, e)
					break
				}
			}
			dk.Close()
		}
	default:
		var dk registry.Key
		dk, err = registry.OpenKey(root, sub, registry.SET_VALUE)
		if err == nil {
			err = dk.DeleteValue(regValue)
			dk.Close()
		}
	}
	if err != nil {
		os.RemoveAll(dst)
		s.logL("reg-delete", "reg:"+regKey, 0, false, err.Error())
		return nil, fmt.Errorf("已备份但删除失败: %w", err)
	}

	path := "reg:" + regKey
	if regValue != "" {
		path += "#" + regValue
	}
	m := &Meta{ID: id, OrigPath: path, App: app, Size: 0, Files: 1,
		QuarAt: time.Now().Format("2006-01-02"), RetainDay: retainDays, Note: note}
	mb, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(filepath.Join(dst, ".aiclean.meta.json"), mb, 0o644)
	s.logL("reg-delete", path, 0, true, "备份→"+dst)
	return m, nil
}

func regHivePrefixShort(full string) string {
	up := strings.ToUpper(full)
	for _, h := range []struct{ p, s string }{
		{"HKEY_CURRENT_USER", "HKCU"}, {"HKCU", "HKCU"},
		{"HKEY_LOCAL_MACHINE", "HKLM"}, {"HKLM", "HKLM"},
		{"HKEY_CLASSES_ROOT", "HKCR"}, {"HKCR", "HKCR"},
		{"HKEY_USERS", "HKU"}, {"HKU", "HKU"},
		{"HKEY_CURRENT_CONFIG", "HKCC"}, {"HKCC", "HKCC"},
	} {
		if strings.HasPrefix(up, h.p) {
			return h.s
		}
	}
	return "HKCU"
}

// RegRestore 恢复：读 backup.json → 重建键/值 → 删隔离目录
func (s *Store) RegRestore(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regRestoreL(id, filepath.Join(s.quarDir(), id))
}

// regRestoreL 已持锁的内部版本（Restore 公共入口按 origPath 前缀分流到这里）
func (s *Store) regRestoreL(id, dst string) error {
	b, err := os.ReadFile(filepath.Join(dst, "backup.json"))
	if err != nil {
		return fmt.Errorf("没有注册表备份: %s", id)
	}
	var bk regBackup
	if err := json.Unmarshal(b, &bk); err != nil {
		return fmt.Errorf("备份损坏: %w", err)
	}
	root, sub, err := regRootOf(bk.Root + `\` + bk.Sub)
	if err != nil {
		return err
	}
	if bk.Value {
		k, _, err := registry.CreateKey(root, sub, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("打开键失败: %w", err)
		}
		err = regWriteValues(k, bk.Values)
		k.Close()
		if err != nil {
			s.logL("reg-restore", "reg:"+bk.Root+`\`+sub, 0, false, err.Error())
			return err
		}
	} else {
		k, _, err := registry.CreateKey(root, sub, registry.SET_VALUE|registry.CREATE_SUB_KEY)
		if err != nil {
			return fmt.Errorf("重建键失败: %w", err)
		}
		err = regWriteTree(k, root, sub, bk.Keys)
		k.Close()
		if err != nil {
			s.logL("reg-restore", "reg:"+bk.Root+`\`+sub, 0, false, err.Error())
			return err
		}
	}
	os.RemoveAll(dst)
	s.logL("reg-restore", "reg:"+bk.Root+`\`+sub, 0, true, "")
	return nil
}
