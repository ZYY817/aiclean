package scanner

// 注册表清理扫描：规则用 regKey+regMode 描述，产出 path=reg:HIVE\Key[\#Value] 的 ScanItem。
//
// regMode 匹配模式：
//   orphan-uninstall      卸载残留：子键的 InstallLocation/UninstallString/DisplayIcon
//                         引用的文件全部不存在 → 子键整体可删
//   missing-target        值数据里的 exe 路径不存在 → 该值可删（Run/RunOnce 失效启动项）
//   missing-target-name   值名里的 exe 路径不存在 → 该值可删（MuiCache/AppCompatFlags Store）
//   missing-subkey        子键默认值的 exe 路径不存在 → 子键可删（App Paths 失效项）
//   clear-values          键及子键下所有值都是痕迹记录 → 每个值一个项（MRU 类）
//
// 安全约束：
//   - 只枚举 regKey 指定的键，不支持通配符/跨键扩散
//   - 只产出"可证据判定"的项（路径不存在才删），存疑一律不列
//   - HKLM 键标 requiresAdmin，未提权时整组标 locked

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aiclean/internal/model"
	"golang.org/x/sys/windows/registry"
)

// regRoot 解析 HIVE 前缀 → x/sys Key 常量 + 剩余子键路径
func regRoot(full string) (registry.Key, string, bool) {
	for _, h := range []struct {
		p string
		k registry.Key
	}{
		{`HKCU\`, registry.CURRENT_USER}, {`HKEY_CURRENT_USER\`, registry.CURRENT_USER},
		{`HKLM\`, registry.LOCAL_MACHINE}, {`HKEY_LOCAL_MACHINE\`, registry.LOCAL_MACHINE},
		{`HKCR\`, registry.CLASSES_ROOT}, {`HKEY_CLASSES_ROOT\`, registry.CLASSES_ROOT},
		{`HKU\`, registry.USERS}, {`HKEY_USERS\`, registry.USERS},
		{`HKCC\`, registry.CURRENT_CONFIG}, {`HKEY_CURRENT_CONFIG\`, registry.CURRENT_CONFIG},
	} {
		if strings.HasPrefix(strings.ToUpper(full), strings.ToUpper(h.p)) {
			return h.k, full[len(h.p):], true
		}
	}
	return 0, "", false
}

// regHivePrefix 取完整键路径的 HIVE 短前缀（显示/序列化用）
func regHivePrefix(full string) string {
	up := strings.ToUpper(full)
	switch {
	case strings.HasPrefix(up, "HKEY_CURRENT_USER"), strings.HasPrefix(up, "HKCU"):
		return "HKCU"
	case strings.HasPrefix(up, "HKEY_LOCAL_MACHINE"), strings.HasPrefix(up, "HKLM"):
		return "HKLM"
	case strings.HasPrefix(up, "HKEY_CLASSES_ROOT"), strings.HasPrefix(up, "HKCR"):
		return "HKCR"
	case strings.HasPrefix(up, "HKEY_USERS"), strings.HasPrefix(up, "HKU"):
		return "HKU"
	default:
		return "HKCC"
	}
}

// exeFromCmdline 从命令行/图标串里提取可执行文件路径：
// "C:\x\y.exe" -arg → C:\x\y.exe；C:\x\y.exe -arg → C:\x\y.exe；
// C:\x\y.exe,0 → C:\x\y.exe。提取不到返回 ""。
func exeFromCmdline(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		if i := strings.IndexByte(s[1:], '"'); i > 0 {
			return s[1 : 1+i]
		}
		return ""
	}
	low := strings.ToLower(s)
	if i := strings.Index(low, ".exe"); i >= 0 {
		return strings.TrimSpace(s[:i+4])
	}
	// 无 .exe：取首个空格前 token（如 explorer.exe / shell32.dll 形）
	if i := strings.IndexAny(s, " \t"); i > 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, `",`)
}

// pathFromValueName 从值名提取文件路径（MuiCache/Store 形态）：
// 先剥 .FriendlyAppName/.ApplicationCompany 等后缀，再截取到已知文件扩展名。
// 提取不出合法文件路径返回 ""（该值无法判定，跳过）。
func pathFromValueName(name string) string {
	s := strings.TrimSpace(name)
	low := strings.ToLower(s)
	for _, suf := range []string{".friendlyappname", ".applicationcompany", ".companyname", ".mui", ".version"} {
		if strings.HasSuffix(low, suf) {
			s = s[:len(s)-len(suf)]
			low = strings.ToLower(s)
			break
		}
	}
	for _, ext := range []string{".exe", ".dll", ".bat", ".cmd", ".com", ".msi", ".msc",
		".sys", ".ps1", ".scr", ".ocx", ".cpl", ".drv", ".efi", ".vbs", ".js"} {
		if i := strings.Index(low, ext); i >= 0 {
			return s[:i+len(ext)]
		}
	}
	return ""
}

// regPathExists 注册表值里的路径是否还存在。
// 展开 %ENV%，剥掉引号/尾逗号参数；路径太怪（非绝对路径）按"存在"处理避免误删；
// 所在盘根不存在（外接盘没插/网络盘离线）同样按"存在"处理——拿不准就不碰。
func regPathExists(p string) bool {
	p = strings.Trim(strings.TrimSpace(expandEnv(p)), `"`)
	if p == "" {
		return true
	}
	if !filepath.IsAbs(p) && !strings.HasPrefix(p, `\\`) {
		return true // 相对路径/裸文件名（notepad.exe）无法判定 → 保守不删
	}
	vol := filepath.VolumeName(p) // "D:" 或 "\\server\share"
	if vol != "" {
		root := vol + `\`
		if strings.HasPrefix(vol, `\\`) {
			root = vol
		}
		if _, err := os.Stat(root); err != nil {
			return true // 盘没插/离线 → 无法判定
		}
	}
	_, err := os.Stat(p)
	return err == nil
}

// regItem 由规则构造一个注册表项
func regItem(r model.Rule, fullKey, value, dir, note string) model.ScanItem {
	path := "reg:" + fullKey
	if value != "" {
		path += "#" + value
	}
	it := r.ToItem(path, dir)
	it.Registry, it.RegKey, it.RegValue = true, fullKey, value
	it.Files, it.Size = 1, 0
	if note != "" {
		it.Note = note
	}
	return it
}

// scanRegRule 扫描一条注册表规则 → 命中项列表（无命中返回 nil）
func scanRegRule(r model.Rule, procs map[string]bool) []model.ScanItem {
	root, sub, ok := regRoot(r.RegKey)
	if !ok || sub == "" {
		return nil
	}
	k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil // 键不存在 = 没有可清的，不算错误
	}
	defer k.Close()

	var items []model.ScanItem
	addItem := func(it model.ScanItem) {
		for _, pn := range r.RequiresClosed {
			if procMatch(procs, pn) {
				it.LockedBy = append(it.LockedBy, pn)
			}
		}
		if len(it.LockedBy) > 0 {
			it.State, it.StatusTxt, it.RiskTxt = "locked", "暂不可操作", "运行中"
		}
		if r.RequiresAdmin && !Admin && it.State != "locked" {
			it.NeedsAdmin = true
			it.State, it.StatusTxt = "locked", "需要管理员"
		}
		items = append(items, it)
	}

	switch r.RegMode {
	case "orphan-uninstall":
		scanOrphanUninstall(k, r, addItem)
	case "missing-target", "missing-target-name":
		names, err := k.ReadValueNames(-1)
		if err != nil {
			return nil
		}
		for _, vn := range names {
			var target string
			if r.RegMode == "missing-target-name" {
				target = pathFromValueName(vn) // 值名即路径+后缀（MuiCache/Store）
			} else {
				s, _, err := k.GetStringValue(vn)
				if err != nil {
					continue
				}
				target = exeFromCmdline(s) // 值数据里的启动命令
			}
			if target == "" {
				continue
			}
			// 系统级命令（msiexec/rundll32/shell32）指向的是系统组件，不是失效残留
			if !filepath.IsAbs(expandEnv(strings.Trim(target, `"`))) && !strings.HasPrefix(target, `\\`) {
				continue
			}
			if !regPathExists(target) {
				it := regItem(r, r.RegKey, vn, vn, r.Note)
				it.Dir = vn
				addItem(it)
			}
		}
	case "missing-subkey":
		subs, err := k.ReadSubKeyNames(-1)
		if err != nil {
			return nil
		}
		for _, sn := range subs {
			sk, err := registry.OpenKey(root, sub+`\`+sn, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			def, _, derr := sk.GetStringValue("") // 默认值 = 程序路径（App Paths）
			sk.Close()
			if derr != nil || def == "" {
				continue
			}
			target := exeFromCmdline(def)
			if target == "" || regPathExists(target) {
				continue
			}
			it := regItem(r, r.RegKey+`\`+sn, "", sn, r.Note)
			addItem(it)
		}
	case "clear-values":
		scanClearValues(k, root, r.RegKey, sub, r, addItem)
	}
	return items
}

// scanOrphanUninstall 卸载残留：子键引用的所有路径都不存在 → 孤儿项
func scanOrphanUninstall(k registry.Key, r model.Rule, add func(model.ScanItem)) {
	root, sub, _ := regRoot(r.RegKey)
	subs, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return
	}
	for _, sn := range subs {
		fullSub := sub + `\` + sn
		sk, err := registry.OpenKey(root, fullSub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		getS := func(n string) string {
			s, _, e := sk.GetStringValue(n)
			if e != nil {
				return ""
			}
			return s
		}
		getD := func(n string) uint64 {
			d, _, e := sk.GetIntegerValue(n)
			if e != nil {
				return 0
			}
			return d
		}
		display := getS("DisplayName")
		sysComp := getD("SystemComponent")
		winInst := getD("WindowsInstaller")
		parent := getS("ParentKeyName")
		cands := []string{
			getS("InstallLocation"),
			exeFromCmdline(getS("UninstallString")),
			exeFromCmdline(getS("QuietUninstallString")),
			exeFromCmdline(getS("DisplayIcon")),
		}
		sk.Close()
		// MSI/系统组件/更新子项由安装器管理，不动
		if sysComp == 1 || winInst == 1 || parent != "" {
			continue
		}
		var valid []string
		msi := false
		for _, c := range cands {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if strings.Contains(strings.ToLower(c), "msiexec") {
				msi = true
			}
			valid = append(valid, c)
		}
		if msi || len(valid) == 0 {
			continue // msiexec 卸载串归 Windows Installer 管；无路径引用无法判定
		}
		orphan := true
		for _, c := range valid {
			if regPathExists(c) {
				orphan = false
				break
			}
		}
		if !orphan {
			continue
		}
		dir := display
		if dir == "" {
			dir = sn
		}
		note := r.Note
		if display != "" {
			note = display + " — " + r.Note
		}
		it := regItem(r, r.RegKey+`\`+sn, "", dir, note)
		add(it)
	}
}

// scanClearValues 递归找含值的键，每个键一条项（RegValue="*" = 清空该键全部值）。
// MRU 痕迹按"清空整个列表"处理，一个列表一行，不逐值刷列表。
func scanClearValues(k registry.Key, root registry.Key, baseFull, sub string, r model.Rule, add func(model.ScanItem)) {
	if names, _ := k.ReadValueNames(-1); len(names) > 0 {
		it := regItem(r, baseFull, "*", filepath.Base(baseFull), r.Note)
		it.Files = int64(len(names))
		it.Note = fmt.Sprintf("%s（%d 条记录）", r.Note, len(names))
		add(it)
	}
	subs, _ := k.ReadSubKeyNames(-1)
	for _, sn := range subs {
		sk, err := registry.OpenKey(root, sub+`\`+sn, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		scanClearValues(sk, root, baseFull+`\`+sn, sub+`\`+sn, r, add)
		sk.Close()
	}
}
