// Package model 定义 AIClean 核心数据模型（对应 V1.1.1 §6）
package model

// ScanItem 一个已识别数据目录
type ScanItem struct {
	RuleID         string   `json:"ruleId"`
	App            string   `json:"app"`                    // 应用/工具名
	Dir            string   `json:"dir"`                    // 短显示路径（相对规则根）
	Path           string   `json:"path"`                   // 展开后的绝对路径
	Size           int64    `json:"size"`                   // 逻辑字节
	Files          int64    `json:"files"`                  // 文件数
	Note           string   `json:"note"`                   // 规则说明
	Risk           string   `json:"risk"`                   // cache|userData|review
	RiskTxt        string   `json:"riskTxt"`                // 低风险|用户数据|待确认|运行中
	Action         string   `json:"action"`                 // clean|migrate|none
	State          string   `json:"state"`                  // active|locked|migrated|quarantined
	StatusTxt      string   `json:"statusTxt"`              // 可清理|建议迁移|需确认|暂不可操作
	Category       string   `json:"category,omitempty"`     // ai|dev|system|social|user
	LockedBy       []string `json:"lockedBy,omitempty"`     // 命中的运行中进程名
	Prunable       bool     `json:"prunable,omitempty"`     // 只可按龄清理（禁止整目录删除）
	PruneDays      int      `json:"pruneDays,omitempty"`    // 按龄清理阈值天数
	PrunableSize   int64    `json:"prunableSize,omitempty"` // 超过阈值部分的字节数
	MovedTo        string   `json:"movedTo,omitempty"`      // migrated 项的 junction 目标
	NeedsAdmin     bool     `json:"needsAdmin,omitempty"`   // 需要管理员权限才能处理
	Special        string   `json:"special,omitempty"`      // 特殊操作: recycleBin|winUpdate|hiberOff
	Big            bool     `json:"big,omitempty"`          // 大文件猎手命中的散文件
	Dup            bool     `json:"dup,omitempty"`          // 疑似重复文件（尺寸+指纹一致）
	ReportOnly     bool     `json:"reportOnly,omitempty"`   // 汇总目录（显示尺寸已扣除子项）
	NoMigrate      bool     `json:"noMigrate,omitempty"`    // 禁止迁移（云同步根/shell 已知文件夹/程序本体）
	Heuristic      bool     `json:"heuristic,omitempty"`    // 启发式按名字发现的子目录（不进定时自动清理）
	LastMod        string   `json:"lastMod,omitempty"`
	Skipped        int      `json:"skipped,omitempty"`        // 跳过的 reparse 点数
	RequiresClosed []string `json:"requiresClosed,omitempty"` // 执行前须复查的进程名（扫描时状态会过期）
}

// Rule rules.json 单条规则
type Rule struct {
	ID               string   `json:"id"`
	Path             string   `json:"path"`
	Tool             string   `json:"tool"`
	Category         string   `json:"category"`
	Risk             string   `json:"risk"`   // safe|caution|user-data|review
	Action           string   `json:"action"` // delete|migrate|review|ignore
	RequiresClosed   []string `json:"requiresClosed"`
	Prunable         bool     `json:"prunable"`
	DefaultPruneDays int      `json:"defaultPruneDays"`
	OlderThanDays    int      `json:"olderThanDays"`  // delete 规则但只删旧文件（隐含 prunable）
	RequiresAdmin    bool     `json:"requiresAdmin"`  // 需要管理员权限（未提权时锁定）
	Special          string   `json:"special"`        // 特殊操作: recycleBin|winUpdate|hiberOff
	ReportOnly       bool     `json:"reportOnly"`     // 汇总目录：不吞掉子规则，显示尺寸=自身-已单列子项
	ScanHeuristics   bool     `json:"scanHeuristics"` // 按名字模式发现子目录（cache/logs→可清，data/session→待确认）
	MatchFile        string   `json:"matchFile"`      // 命中的目录里必须存在该文件才生效（支持 glob，如 *.vhdx）
	NoMigrate        bool     `json:"noMigrate"`      // 即使 reportOnly/migrate 也禁止 junction 迁移
	Note             string   `json:"note"`
}

// Heuristics 启发式子目录分类（rules.json 顶层 "heuristics" 字段）
type Heuristics struct {
	Comment          string   `json:"comment"`
	DeletePatterns   []string `json:"deletePatterns"`
	UserDataPatterns []string `json:"userDataPatterns"`
	IgnoreNames      []string `json:"ignoreNames"`
}

// RuleFile rules.json 顶层
type RuleFile struct {
	Version    int        `json:"version"`
	Comment    string     `json:"comment"`
	Rules      []Rule     `json:"rules"`
	Heuristics Heuristics `json:"heuristics"`
}

// 风险/动作 → UI 分类
func (r Rule) ToItem(path, dir string) ScanItem {
	it := ScanItem{RuleID: r.ID, App: r.Tool, Dir: dir, Path: path, Note: r.Note, State: "active", Category: r.Category, Special: r.Special, ReportOnly: r.ReportOnly || r.ScanHeuristics, NoMigrate: r.NoMigrate, RequiresClosed: r.RequiresClosed}
	if r.Prunable || r.OlderThanDays > 0 {
		it.Prunable = true
		it.PruneDays = r.DefaultPruneDays
		if it.PruneDays <= 0 {
			it.PruneDays = r.OlderThanDays
		}
		if it.PruneDays <= 0 {
			it.PruneDays = 7
		}
	}
	switch r.Risk {
	case "safe":
		it.Risk, it.RiskTxt = "cache", "低风险"
	case "user-data":
		it.Risk, it.RiskTxt = "userData", "用户数据"
	case "caution":
		it.Risk, it.RiskTxt = "userData", "中风险"
	default:
		it.Risk, it.RiskTxt = "review", "待确认"
	}
	switch r.Action {
	case "delete":
		it.Action, it.StatusTxt = "clean", "可清理"
	case "migrate":
		it.Action, it.StatusTxt = "migrate", "建议迁移"
	default:
		it.Action, it.StatusTxt = "none", "需确认"
	}
	return it
}
