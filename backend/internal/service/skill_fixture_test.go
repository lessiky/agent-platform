package service

import (
	"os"
	"strings"
	"testing"
)

func TestSkillFixtureZipValid(t *testing.T) {
	data, err := os.ReadFile("../../testdata/skills/weekly-report.zip")
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	parsed, err := parseSkillPackage(data, DefaultSkillLimits())
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	t.Logf("name=%s v=%s files=%d size=%d tools=%v", parsed.Name, parsed.VersionSpec, len(parsed.Files), parsed.SizeBytes, parsed.RequiredTools)
	if parsed.Name != "weekly-report" || parsed.VersionSpec != "1.0.0" {
		t.Fatalf("unexpected: %+v", parsed)
	}
	if len(parsed.RequiredTools) != 1 || parsed.RequiredTools[0] != "kb.search" {
		t.Fatalf("required_tools: %v", parsed.RequiredTools)
	}
	if len(parsed.Files) != 2 || !strings.HasPrefix(parsed.EntryContent, "# 周报生成技能") {
		t.Fatalf("files/body: %+v", parsed.Files)
	}
}
