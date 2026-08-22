package service

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// buildSkillZip 从有序的 (路径, 内容) 列表构建内存 zip 包
func buildSkillZip(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("zip create %s: %v", e[0], err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("zip write %s: %v", e[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// assertValidationError 断言 err 为 400 校验错误且消息包含 want
func assertValidationError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	appErr, ok := err.(*errors.AppError)
	if !ok {
		t.Fatalf("expected *errors.AppError, got %T: %v", err, err)
	}
	if appErr.HTTPCode != 400 {
		t.Fatalf("expected HTTPCode 400, got %d: %v", appErr.HTTPCode, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

const validSkillMD = "---\n" +
	"name: weekly-report\n" +
	"description: 生成周报\n" +
	"version: 1.2.0\n" +
	"author: platform\n" +
	"tags: [report, weekly]\n" +
	"---\n" +
	"# 生成周报\n" +
	"步骤1: 收集数据\n" +
	"步骤2: 汇总\n"

func TestParseSkillPackage_Valid(t *testing.T) {
	entries := [][2]string{
		{"SKILL.md", validSkillMD},
		{"templates/report.md", "# 模板\n"},
		{"assets/chart.png", "pngdata"},
	}
	data := buildSkillZip(t, entries)
	parsed, err := parseSkillPackage(data, DefaultSkillLimits())
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if parsed.Name != "weekly-report" || parsed.VersionSpec != "1.2.0" || parsed.Description != "生成周报" || parsed.Author != "platform" {
		t.Fatalf("frontmatter 字段不正确: %+v", parsed)
	}
	if len(parsed.Tags) != 2 || parsed.Tags[0] != "report" || parsed.Tags[1] != "weekly" {
		t.Fatalf("tags 不正确: %v", parsed.Tags)
	}
	if !strings.HasPrefix(parsed.EntryContent, "# 生成周报") {
		t.Fatalf("正文不正确: %q", parsed.EntryContent)
	}
	if len(parsed.Files) != 2 || parsed.Files[0].Path != "assets/chart.png" || parsed.Files[1].Path != "templates/report.md" {
		t.Fatalf("文件列表不正确: %+v", parsed.Files)
	}
	wantSize := int64(len(validSkillMD) + len("# 模板\n") + len("pngdata"))
	if parsed.SizeBytes != wantSize {
		t.Fatalf("总大小不正确: %d want %d", parsed.SizeBytes, wantSize)
	}
}

func TestParseSkillPackage_SingleTopLevelDir(t *testing.T) {
	entries := [][2]string{
		{"weekly-report/SKILL.md", validSkillMD},
		{"weekly-report/templates/report.md", "# 模板\n"},
	}
	data := buildSkillZip(t, entries)
	parsed, err := parseSkillPackage(data, DefaultSkillLimits())
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "templates/report.md" {
		t.Fatalf("未剥离顶层目录: %+v", parsed.Files)
	}
}

func TestParseSkillPackage_FrontmatterVariants(t *testing.T) {
	tests := []struct {
		name  string
		md    string
		check func(t *testing.T, p *ParsedSkillPackage)
	}{
		{
			name: "CRLF 换行",
			md:   "---\r\nname: demo\r\ndescription: CRLF\r\n---\r\n正文内容\r\n",
			check: func(t *testing.T, p *ParsedSkillPackage) {
				if p.Name != "demo" || !strings.Contains(p.EntryContent, "正文内容") {
					t.Fatalf("%+v", p)
				}
			},
		},
		{
			name: "BOM 头",
			md:   "\ufeff---\nname: demo\ndescription: BOM\n---\n正文内容\n",
			check: func(t *testing.T, p *ParsedSkillPackage) {
				if p.Name != "demo" {
					t.Fatalf("%+v", p)
				}
			},
		},
		{
			name: "块列表",
			md:   "---\nname: demo\ndescription: 块列表\ntags:\n  - a\n  - b\nrequired_tools:\n  - get_weather\n---\n正文内容\n",
			check: func(t *testing.T, p *ParsedSkillPackage) {
				if len(p.Tags) != 2 || p.Tags[0] != "a" || p.Tags[1] != "b" {
					t.Fatalf("tags: %v", p.Tags)
				}
				if len(p.RequiredTools) != 1 || p.RequiredTools[0] != "get_weather" {
					t.Fatalf("required_tools: %v", p.RequiredTools)
				}
			},
		},
		{
			name: "行内注释",
			md:   "---\nname: demo\ndescription: 注释\nversion: 2.0.0 # minor\n---\n正文\n",
			check: func(t *testing.T, p *ParsedSkillPackage) {
				if p.VersionSpec != "2.0.0" {
					t.Fatalf("version: %q", p.VersionSpec)
				}
			},
		},
		{
			name: "引号值",
			md:   "---\nname: demo\ndescription: \"带引号描述\"\n---\n正文\n",
			check: func(t *testing.T, p *ParsedSkillPackage) {
				if p.Description != "带引号描述" {
					t.Fatalf("description: %q", p.Description)
				}
			},
		},
		{
			name: "version 缺省 1.0.0",
			md:   "---\nname: demo\ndescription: 缺省\n---\n正文\n",
			check: func(t *testing.T, p *ParsedSkillPackage) {
				if p.VersionSpec != "1.0.0" {
					t.Fatalf("version: %q", p.VersionSpec)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildSkillZip(t, [][2]string{{"SKILL.md", tt.md}})
			parsed, err := parseSkillPackage(data, DefaultSkillLimits())
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			tt.check(t, parsed)
		})
	}
}
func TestParseSkillPackage_Rejected(t *testing.T) {
	smallLimits := SkillLimits{MaxPackageSize: 1000, MaxFileCount: 10, MaxFileSize: 100, MaxEntrySize: 100, AllowedExt: []string{"md", "txt"}}
	skillMD := func(body string) string { return "---\nname: demo\ndescription: d\n---\n" + body }
	cases := []struct {
		name    string
		build   func() []byte
		limits  SkillLimits
		wantMsg string
	}{
		{
			name:    "非 zip 文件",
			build:   func() []byte { return []byte("this is not a zip") },
			limits:  DefaultSkillLimits(),
			wantMsg: "解压失败",
		},
		{
			name:    "缺少 SKILL.md",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"readme.txt", "hi"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "缺少 SKILL.md",
		},
		{
			name: "缺少 name",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\ndescription: 缺name\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "name",
		},
		{
			name:    "缺少 description",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\n---\n正文\n"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "description",
		},
		{
			name: "技能名含大写字母",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: Bad_Name\ndescription: d\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "技能名不合法",
		},
		{
			name: "技能名以下划线开头",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: _bad\ndescription: d\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "技能名不合法",
		},
		{
			name: "正文为空",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\ndescription: d\n---\n   \n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "正文为空",
		},
		{
			name:    "无 frontmatter",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"SKILL.md", "# 标题\n正文\n"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "frontmatter",
		},
		{
			name:    "frontmatter 未闭合",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\ndescription: d\n"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "未闭合",
		},
		{
			name:    "zip-slip 路径",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"../evil.txt", "x"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "非法文件路径",
		},
		{
			name:    "绝对路径",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"/etc/passwd", "x"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "非法文件路径",
		},
		{
			name:    "重复文件路径",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"a.txt", "x"}, {"a.txt", "y"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "重复文件路径",
		},
		{
			name:    "扩展名不在白名单",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"SKILL.md", skillMD("正文\n")}, {"a.exe", "x"}}) },
			limits:  DefaultSkillLimits(),
			wantMsg: "白名单",
		},
		{
			name: "文件数超限",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", skillMD("正文\n")}, {"b.txt", "1"}, {"c.txt", "2"}})
			},
			limits:  SkillLimits{MaxPackageSize: 1000, MaxFileCount: 2, MaxFileSize: 100, MaxEntrySize: 100, AllowedExt: []string{"md", "txt"}},
			wantMsg: "文件数",
		},
		{
			name:    "包总大小超限",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"SKILL.md", skillMD("正文\n")}}) },
			limits:  SkillLimits{MaxPackageSize: 8, MaxFileCount: 10, MaxFileSize: 100, MaxEntrySize: 100, AllowedExt: []string{"md"}},
			wantMsg: "大小上限",
		},
		{
			name:    "单文件超限",
			build:   func() []byte { return buildSkillZip(t, [][2]string{{"big.txt", strings.Repeat("x", 150)}}) },
			limits:  smallLimits,
			wantMsg: "单文件大小上限",
		},
		{
			name: "SKILL.md 超限",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\ndescription: d\n---\n" + strings.Repeat("x", 120)}})
			},
			limits:  SkillLimits{MaxPackageSize: 1000, MaxFileCount: 10, MaxFileSize: 500, MaxEntrySize: 100, AllowedExt: []string{"md"}},
			wantMsg: "SKILL.md 超过",
		},
		{
			name: "description 超长",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\ndescription: " + strings.Repeat("a", 513) + "\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "description 超过",
		},
		{
			name: "tags 超 10 个",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\ndescription: d\ntags: [t1, t2, t3, t4, t5, t6, t7, t8, t9, t10, t11]\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "tags 超过",
		},
		{
			name: "列表项前无键",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\n- a\nname: demo\ndescription: d\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "列表项前须有键",
		},
		{
			name: "version 超长",
			build: func() []byte {
				return buildSkillZip(t, [][2]string{{"SKILL.md", "---\nname: demo\ndescription: d\nversion: " + strings.Repeat("v", 33) + "\n---\n正文\n"}})
			},
			limits:  DefaultSkillLimits(),
			wantMsg: "version 字段超过",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSkillPackage(tc.build(), tc.limits)
			assertValidationError(t, err, tc.wantMsg)
		})
	}
}

func TestSafeSkillPath(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"SKILL.md", "SKILL.md", true},
		{"a/b.md", "a/b.md", true},
		{"a\\b.md", "a/b.md", true},
		{"a/./b.md", "a/b.md", true},
		{"../x.md", "", false},
		{"a/../x.md", "", false},
		{"/abs/x.md", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := safeSkillPath(c.raw)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("safeSkillPath(%q) = (%q, %v), want (%q, %v)", c.raw, got, ok, c.want, c.ok)
		}
	}
}

func TestCommonTopLevelDir(t *testing.T) {
	if got := commonTopLevelDir(nil); got != "" {
		t.Fatalf("nil: %q", got)
	}
	if got := commonTopLevelDir([]string{}); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := commonTopLevelDir([]string{"a/x", "a/b/y"}); got != "a/" {
		t.Fatalf("same top: %q", got)
	}
	if got := commonTopLevelDir([]string{"a/x", "b/y"}); got != "" {
		t.Fatalf("diff top: %q", got)
	}
	if got := commonTopLevelDir([]string{"x.md"}); got != "" {
		t.Fatalf("flat file: %q", got)
	}
}

// stubAgentLogs chatService 执行日志打桩 (单测)
type stubAgentLogs struct {
	entries []*model.AgentLog
}

func (s *stubAgentLogs) Append(_ context.Context, entries []*model.AgentLog) error {
	s.entries = append(s.entries, entries...)
	return nil
}

func (s *stubAgentLogs) List(_ context.Context, _ repository.AgentLogFilter) ([]*model.AgentLog, int64, error) {
	return nil, 0, nil
}

func (s *stubAgentLogs) Trim(_ context.Context, _ string, _ int) error { return nil }

// newSkillLoadFixture 构造 load_skill 测试场景: 一个依赖 get_weather 的周报技能
func newSkillLoadFixture() (*chatService, *skillTurn, map[string]toolRef) {
	skill := model.Skill{
		ID:            "skill-1",
		Name:          "weekly-report",
		Version:       2,
		Description:   "生成周报",
		EntryContent:  "步骤1: 收集数据\n步骤2: 汇总",
		RequiredTools: datatypes.JSON(`["get_weather"]`),
	}
	st := newSkillTurn("metadata_injection", []model.Skill{skill})
	svc := &chatService{logs: &stubAgentLogs{}}
	toolIndex := map[string]toolRef{"get_weather": {MCPID: "mcp-1", MCPName: "mock-mcp"}}
	return svc, st, toolIndex
}

func loadSkillCall(name string) modelclient.ChatToolCall {
	var tc modelclient.ChatToolCall
	tc.Function.Name = loadSkillToolName
	tc.Function.Arguments = `{"skill_name":"` + name + `"}`
	return tc
}

func TestExecuteSkillLoad_NotBound(t *testing.T) {
	svc, st, _ := newSkillLoadFixture()
	out := svc.executeSkillLoad("agent-1", "chat", "exec-1", map[string]toolRef{}, loadSkillCall("nope"), st)
	if len(st.calls) != 1 || st.calls[0].Status != "error" {
		t.Fatalf("calls 不正确: %+v", st.calls)
	}
	if !strings.Contains(out, "not bound") {
		t.Fatalf("out: %s", out)
	}
}

func TestExecuteSkillLoad_InvalidArgs(t *testing.T) {
	svc, st, _ := newSkillLoadFixture()
	tc := loadSkillCall("")
	tc.Function.Arguments = "not-json"
	out := svc.executeSkillLoad("agent-1", "chat", "exec-1", map[string]toolRef{}, tc, st)
	if len(st.calls) != 1 || st.calls[0].Status != "error" {
		t.Fatalf("calls 不正确: %+v", st.calls)
	}
	if !strings.Contains(out, "invalid") {
		t.Fatalf("out: %s", out)
	}
}

func TestExecuteSkillLoad_OkAndDuplicate(t *testing.T) {
	svc, st, toolIndex := newSkillLoadFixture()
	out := svc.executeSkillLoad("agent-1", "chat", "exec-1", toolIndex, loadSkillCall("weekly-report"), st)
	wantChars := len([]rune("步骤1: 收集数据\n步骤2: 汇总"))
	if len(st.calls) != 1 || st.calls[0].Status != "ok" || st.calls[0].Chars != wantChars || st.calls[0].Version != 2 {
		t.Fatalf("calls 不正确: %+v", st.calls)
	}
	if !strings.Contains(out, "步骤2: 汇总") || !strings.Contains(out, "技能指令:") {
		t.Fatalf("out: %s", out)
	}
	out = svc.executeSkillLoad("agent-1", "chat", "exec-1", toolIndex, loadSkillCall("weekly-report"), st)
	if len(st.calls) != 2 || st.calls[1].Status != "duplicate" {
		t.Fatalf("重复加载 calls 不正确: %+v", st.calls)
	}
	if !strings.Contains(out, "already loaded") {
		t.Fatalf("out: %s", out)
	}
}

func TestExecuteSkillLoad_Partial(t *testing.T) {
	svc, st, _ := newSkillLoadFixture()
	out := svc.executeSkillLoad("agent-1", "chat", "exec-1", map[string]toolRef{}, loadSkillCall("weekly-report"), st)
	if len(st.calls) != 1 || st.calls[0].Status != "partial" || st.calls[0].Detail != "get_weather" {
		t.Fatalf("calls 不正确: %+v", st.calls)
	}
	if !strings.Contains(out, "不可用") {
		t.Fatalf("out: %s", out)
	}
}

func TestSkillSystemSection(t *testing.T) {
	svc := &chatService{}
	skills := []model.Skill{
		{Name: "weekly-report", Version: 1, Description: "生成周报", EntryContent: "正文: 仅全量模式出现"},
	}
	out := svc.skillSystemSection(newSkillTurn("full_injection", skills))
	if !strings.Contains(out, "技能参考数据 开始") || !strings.Contains(out, "正文: 仅全量模式出现") || !strings.Contains(out, "weekly-report") {
		t.Fatalf("full 模式: %s", out)
	}
	out = svc.skillSystemSection(newSkillTurn("metadata_injection", skills))
	if !strings.Contains(out, "技能目录 开始") || !strings.Contains(out, "生成周报") {
		t.Fatalf("metadata 模式: %s", out)
	}
	if strings.Contains(out, "正文: 仅全量模式出现") {
		t.Fatalf("metadata 模式不应包含正文: %s", out)
	}
	if out := svc.skillSystemSection(newSkillTurn("metadata_injection", nil)); out != "" {
		t.Fatalf("空技能: %s", out)
	}
	if out := svc.skillSystemSection(nil); out != "" {
		t.Fatalf("nil: %s", out)
	}
}

func TestSkillTurnModes(t *testing.T) {
	var nilTurn *skillTurn
	if nilTurn.active() || nilTurn.loadTool() || nilTurn.fullMode() {
		t.Fatal("nil turn 应全部为 false")
	}
	if !newSkillTurn("full_injection", []model.Skill{{Name: "a-b"}}).fullMode() {
		t.Fatal("full_injection 模式识别失败")
	}
	if !newSkillTurn("", []model.Skill{{Name: "a-b"}}).loadTool() {
		t.Fatal("默认模式应注册 load_skill")
	}
	if newSkillTurn("full_injection", []model.Skill{{Name: "a-b"}}).loadTool() {
		t.Fatal("full 模式不应注册 load_skill")
	}
	if newSkillTurn("metadata_injection", nil).loadTool() {
		t.Fatal("无技能不应注册 load_skill")
	}
}

func TestLoadSkillToolDef(t *testing.T) {
	def := loadSkillToolDef()
	if def.Function.Name != loadSkillToolName || def.Type != "function" || def.Function.Parameters == nil {
		t.Fatalf("工具定义不正确: %+v", def)
	}
}
