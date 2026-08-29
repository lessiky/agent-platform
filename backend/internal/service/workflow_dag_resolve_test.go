package service

import (
	"testing"
)

func TestResolveEmbeddedRef(t *testing.T) {
	ctx := &VarContext{
		Inputs:      map[string]interface{}{"title": "WF-TKT3", "level": float64(10)},
		NodeOutputs: map[string]interface{}{"n2": map[string]interface{}{"content": "hi"}},
		ExecutionID: "abc123",
	}
	got := ResolveVariables("high: $inputs.title", ctx)
	want := "high: WF-TKT3"
	if got != want {
		t.Fatalf("embedded ref: got %q want %q", got, want)
	}
	got2 := ResolveVariables("$inputs.title", ctx)
	if got2 != "WF-TKT3" {
		t.Fatalf("whole ref: got %q want %q", got2, "WF-TKT3")
	}
	got3 := ResolveVariables("exec=$execution.id n=$nodes.n2.content", ctx)
	if got3 != "exec=abc123 n=hi" {
		t.Fatalf("multi ref: got %q", got3)
	}
}

func TestResolveEmbeddedRefCJKAdjacent(t *testing.T) {
	ctx := &VarContext{
		Inputs: map[string]interface{}{
			"testurl": "https://example.com/api",
			"网址":    "https://example.cn",
			"items":  []interface{}{map[string]interface{}{"name": "n1"}},
		},
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"变量后紧跟中文", "检查$inputs.testurl访问是否正常", "检查https://example.com/api访问是否正常"},
		{"变量后紧跟全角标点", "访问$inputs.testurl。", "访问https://example.com/api。"},
		{"中文 key 空格分隔", "url: $inputs.网址", "url: https://example.cn"},
		{"中文 key 后紧跟中文", "访问$inputs.网址正常", "访问https://example.cn正常"},
		{"数组下标路径嵌入", "第一项$inputs.items[0].name完成", "第一项n1完成"},
		{"ASCII 路径续接不截取前缀", "检查$inputs.ygturlx访问", "检查$inputs.ygturlx访问"},
		{"未命中引用原样保留", "检查$inputs.nope访问", "检查$inputs.nope访问"},
	}
	for _, tc := range cases {
		if got := ResolveVariables(tc.in, ctx); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
