package model

import "testing"

func TestToItemRiskAction(t *testing.T) {
	cases := []struct {
		risk, action          string
		wantRisk, wantAction  string
		wantRiskTxt, wantStat string
	}{
		{"safe", "delete", "cache", "clean", "低风险", "可清理"},
		{"user-data", "migrate", "userData", "migrate", "用户数据", "建议迁移"},
		{"caution", "migrate", "userData", "migrate", "中风险", "建议迁移"},
		{"review", "review", "review", "none", "待确认", "需确认"},
		{"review", "", "review", "none", "待确认", "需确认"},
	}
	for _, c := range cases {
		it := (Rule{Risk: c.risk, Action: c.action}).ToItem(`C:\x`, "x")
		if it.Risk != c.wantRisk || it.Action != c.wantAction || it.RiskTxt != c.wantRiskTxt || it.StatusTxt != c.wantStat {
			t.Fatalf("risk=%s action=%s → %+v", c.risk, c.action, it)
		}
	}
}

func TestToItemPrunable(t *testing.T) {
	it := (Rule{OlderThanDays: 14}).ToItem(`C:\x`, "x")
	if !it.Prunable || it.PruneDays != 14 {
		t.Fatalf("olderThanDays → prunable14, got %+v", it)
	}
	it2 := (Rule{Prunable: true, DefaultPruneDays: 30}).ToItem(`C:\x`, "x")
	if !it2.Prunable || it2.PruneDays != 30 {
		t.Fatalf("prunable30, got %+v", it2)
	}
	it3 := (Rule{Prunable: true}).ToItem(`C:\x`, "x")
	if it3.PruneDays != 7 {
		t.Fatalf("prunable default should be 7, got %d", it3.PruneDays)
	}
}

func TestToItemReportOnly(t *testing.T) {
	if !(Rule{ReportOnly: true}).ToItem(`C:\x`, "x").ReportOnly {
		t.Fatal("reportOnly not propagated")
	}
	if !(Rule{ScanHeuristics: true}).ToItem(`C:\x`, "x").ReportOnly {
		t.Fatal("scanHeuristics should imply reportOnly on item")
	}
}

func TestRuleFileHeuristics(t *testing.T) {
	var rf RuleFile
	if rf.Heuristics.DeletePatterns != nil {
		t.Fatal("zero heuristics should be nil")
	}
}
