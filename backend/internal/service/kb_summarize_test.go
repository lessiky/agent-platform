package service

// kb_summarize_test.go — M11 W2 一键总结单测 (计划 D10):
// parseKBSummary 严格解析 / suggestKBCategory 建议分类

import (
	"strings"
	"testing"
)

func TestParseKBSummary(t *testing.T) {
	valid := `{"title":"备份流程","content":"` + strings.Repeat("知", 300) + `"}`

	title, content, err := parseKBSummary(valid)
	if err != nil || title != "备份流程" || len([]rune(content)) != 300 {
		t.Fatalf("valid: title=%q len=%d err=%v", title, len([]rune(content)), err)
	}

	// 容忍代码块包裹
	if _, _, err := parseKBSummary("```json\n" + valid + "\n```"); err != nil {
		t.Fatalf("code-wrapped: %v", err)
	}

	// 容忍前后杂散文字
	if _, _, err := parseKBSummary("总结结果如下: " + valid + " 以上"); err != nil {
		t.Fatalf("with surrounding text: %v", err)
	}

	// 标题 trim
	trimmed, _, err := parseKBSummary(`{"title":"  备份流程  ","content":"` + strings.Repeat("知", 300) + `"}`)
	if err != nil || trimmed != "备份流程" {
		t.Fatalf("title trim: %q err=%v", trimmed, err)
	}

	// 非法 JSON
	if _, _, err := parseKBSummary("not a json at all"); err == nil {
		t.Fatal("invalid JSON should error")
	}

	// 标题越界 (空 / 61 字符)
	if _, _, err := parseKBSummary(`{"title":"","content":"` + strings.Repeat("知", 300) + `"}`); err == nil {
		t.Fatal("empty title should error")
	}
	if _, _, err := parseKBSummary(`{"title":"` + strings.Repeat("标", 61) + `","content":"` + strings.Repeat("知", 300) + `"}`); err == nil {
		t.Fatal("61-char title should error")
	}

	// 正文过短 (299 < 300)
	if _, _, err := parseKBSummary(`{"title":"备份流程","content":"` + strings.Repeat("知", 299) + `"}`); err == nil {
		t.Fatal("299-char content should error")
	}

	// 正文超上限截断到 1500
	_, long, err := parseKBSummary(`{"title":"备份流程","content":"` + strings.Repeat("知", 1600) + `"}`)
	if err != nil {
		t.Fatalf("long content: %v", err)
	}
	if n := len([]rune(long)); n != 1500 {
		t.Fatalf("content should be truncated to 1500, got %d", n)
	}
}

func TestSuggestKBCategory(t *testing.T) {
	cats := []AgentKBCategoryView{
		{ID: "c1", Name: "数据库运维", Description: "postgres 备份 恢复"},
		{ID: "c2", Name: "前端规范", Description: "css 组件"},
	}
	if got := suggestKBCategory("postgres 备份", cats); got != "c1" {
		t.Fatalf("suggested = %q, want c1", got)
	}
	// 最高分持平 → 不建议
	tied := []AgentKBCategoryView{
		{ID: "c1", Name: "alpha beta", Description: ""},
		{ID: "c2", Name: "alpha beta", Description: ""},
	}
	if got := suggestKBCategory("alpha", tied); got != "" {
		t.Fatalf("tied scores should return empty, got %q", got)
	}
	// 无匹配 / 空查询 / 空分类 → 不建议
	if got := suggestKBCategory("zzz", cats); got != "" {
		t.Fatalf("no match should return empty, got %q", got)
	}
	if got := suggestKBCategory("", cats); got != "" {
		t.Fatal("empty query should return empty")
	}
	if got := suggestKBCategory("postgres", nil); got != "" {
		t.Fatal("empty categories should return empty")
	}
}
