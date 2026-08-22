package service

import (
    "testing"
)

func TestResolveEmbeddedRef(t *testing.T) {
    ctx := &VarContext{
        Inputs: map[string]interface{}{"title": "WF-TKT3", "level": float64(10)},
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
