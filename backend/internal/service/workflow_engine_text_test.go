package service

import (
	"encoding/json"
	"testing"

	"agent-platform/internal/mcpclient"

	"gorm.io/datatypes"
)

func TestFlattenMCPTextSingleBlock(t *testing.T) {
	content := []mcpclient.ToolContent{
		{Type: "text", Text: "echo: hello"},
	}
	if got := flattenMCPText(content); got != "echo: hello" {
		t.Fatalf("got %q", got)
	}
}

func TestFlattenMCPTextMultipleBlocks(t *testing.T) {
	content := []mcpclient.ToolContent{
		{Type: "text", Text: "line1"},
		{Type: "text", Text: "line2"},
	}
	if got := flattenMCPText(content); got != "line1"+"\n"+"line2" {
		t.Fatalf("got %q", got)
	}
}

func TestFlattenMCPTextIgnoresNonText(t *testing.T) {
	content := []mcpclient.ToolContent{
		{Type: "image", Data: "base64..."},
		{Type: "text", Text: "only"},
	}
	if got := flattenMCPText(content); got != "only" {
		t.Fatalf("got %q", got)
	}
}

func TestFlattenMCPTextEmpty(t *testing.T) {
	if got := flattenMCPText(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeApprovalResultAddsText(t *testing.T) {
	raw := datatypes.JSON(`{"content":[{"type":"text","text":"hello"}],"is_error":false}`)
	out := normalizeApprovalResult(raw)
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["text"] != "hello" || payload["is_error"] != false {
		t.Fatalf("got %v", payload)
	}
	if _, ok := payload["content"]; !ok {
		t.Fatalf("content missing: %v", payload)
	}
}

func TestNormalizeApprovalResultPassThrough(t *testing.T) {
	raw := datatypes.JSON(`{"error":"boom"}`)
	if out := normalizeApprovalResult(raw); string(out) != string(raw) {
		t.Fatalf("got %s", out)
	}
}
