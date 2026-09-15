package service

// kb_chunker_test.go — M11.5 分块器单测 (A25: 标题边界 / 段落合并 / 重叠 / 字数上限 / 空正文 / 短条目 1 块)

import (
	"strings"
	"testing"
)

// testChunkOpts 精细参数 (便于断言确定行为)
var testChunkOpts = KBChunkOptions{Size: 200, Overlap: 20, Threshold: 300}

func TestChunkEmpty(t *testing.T) {
	if got := ChunkMarkdown("", testChunkOpts); len(got) != 0 {
		t.Fatalf("empty: got %d chunks, want 0", len(got))
	}
	if got := ChunkMarkdown("  \n\t  ", testChunkOpts); len(got) != 0 {
		t.Fatalf("whitespace: got %d chunks, want 0", len(got))
	}
}

func TestChunkShortOneBlock(t *testing.T) {
	// 默认阈值 1000: 正文 ≤ 阈值 → 整条 1 块 (行为与现状一致, A30)
	content := strings.Repeat("短", 999)
	got := ChunkMarkdown(content, KBChunkOptions{})
	if len(got) != 1 || got[0] != content {
		t.Fatalf("short: got %d chunks (first=%q), want 1 chunk = whole", len(got), got[0][:10])
	}
	// 恰好阈值
	if got := ChunkMarkdown(strings.Repeat("x", 1000), KBChunkOptions{}); len(got) != 1 {
		t.Fatalf("at threshold: got %d chunks, want 1", len(got))
	}
	// 超过阈值 → 分块
	if got := ChunkMarkdown(strings.Repeat("x", 1001), KBChunkOptions{}); len(got) < 2 {
		t.Fatalf("above threshold: got %d chunks, want >= 2", len(got))
	}
}

func TestChunkHeadingBoundary(t *testing.T) {
	// 两节各 150 字 (< Size 200): 每节 1 块, 标题不混节, 相邻块携带 20 rune 重叠
	content := "# 一\n" + strings.Repeat("A", 150) + "\n\n# 二\n" + strings.Repeat("B", 150)
	got := ChunkMarkdown(content, testChunkOpts)
	if len(got) != 2 {
		t.Fatalf("heading: got %d chunks, want 2: %v", len(got), runeLens(got))
	}
	if !strings.Contains(got[0], "# 一") || strings.Contains(got[0], "# 二") {
		t.Fatalf("chunk[0] = %q, want 仅含 # 一 节", got[0][:60])
	}
	if !strings.Contains(got[1], "# 二") {
		t.Fatalf("chunk[1] = %q, want 含 # 二", got[1][:60])
	}
	// 重叠: chunk[1] 前缀 = chunk[0] 尾部 20 rune
	last := tailRunes(got[0], 20)
	if !strings.HasPrefix(got[1], last) {
		t.Fatalf("overlap: chunk[1] prefix = %q, want %q", got[1][:25], last)
	}
}

func TestChunkParagraphMerge(t *testing.T) {
	// 阈值 50: 三个 60 字段落 (共 182) 合并进一块 (≤ Size 200)
	content := strings.Repeat("p", 60) + "\n\n" + strings.Repeat("q", 60) + "\n\n" + strings.Repeat("r", 60)
	got := ChunkMarkdown(content, KBChunkOptions{Size: 200, Overlap: 20, Threshold: 50})
	if len(got) != 1 {
		t.Fatalf("merge: got %d chunks, want 1", len(got))
	}
	if got[0] != strings.Join([]string{strings.Repeat("p", 60), strings.Repeat("q", 60), strings.Repeat("r", 60)}, "\n") {
		t.Fatalf("merged content mismatch: %q", got[0][:40])
	}
}

func TestChunkPackOverlap(t *testing.T) {
	// 三个 80 字段落 (共 242 > 阈值 50), Size 100: 每块 1 主段 + 前块尾部 19 rune 重叠
	opts := KBChunkOptions{Size: 100, Overlap: 30, Threshold: 50}
	content := strings.Repeat("x", 80) + "\n\n" + strings.Repeat("y", 80) + "\n\n" + strings.Repeat("z", 80)
	got := ChunkMarkdown(content, opts)
	if len(got) != 3 {
		t.Fatalf("pack: got %d chunks, want 3: %v", len(got), runeLens(got))
	}
	if len([]rune(got[0])) > 100 || len([]rune(got[1])) > 100 || len([]rune(got[2])) > 100 {
		t.Fatalf("size limit broken: %v", runeLens(got))
	}
	// chunk[1] = 重叠 19 + "\n" + y*80 (allow = 100-80-1 = 19)
	want1 := strings.Repeat("x", 19) + "\n" + strings.Repeat("y", 80)
	if got[1] != want1 {
		t.Fatalf("chunk[1] = %q (len %d), want %q (len %d)", got[1], len([]rune(got[1])), want1, len([]rune(want1)))
	}
	if !strings.HasPrefix(got[2], strings.Repeat("y", 19)) {
		t.Fatalf("chunk[2] prefix = %q, want 19 个 y 重叠", got[2][:25])
	}
}

func TestChunkHardSplit(t *testing.T) {
	// 超长单段 350 rune, Size 100 / Overlap 30 → 硬切 5 块, 相邻重叠 30, 每块 ≤ 100
	opts := KBChunkOptions{Size: 100, Overlap: 30, Threshold: 50}
	content := strings.Repeat("q", 350)
	got := ChunkMarkdown(content, opts)
	if len(got) != 5 {
		t.Fatalf("hard split: got %d chunks, want 5: %v", len(got), runeLens(got))
	}
	for i, c := range got {
		if len([]rune(c)) > 100 {
			t.Fatalf("chunk[%d] len = %d > 100", i, len([]rune(c)))
		}
		if i > 0 && got[i][:30] != got[i-1][70:] {
			t.Fatalf("chunk[%d] overlap mismatch", i)
		}
	}
}

func TestChunkDefaultsSmoke(t *testing.T) {
	// 零值参数 → 默认 500/50/1000, 不 panic 且每块 ≤ 500
	content := "# H\n" + strings.Repeat("a", 1200) + "\n\n" + strings.Repeat("b", 1200)
	got := ChunkMarkdown(content, KBChunkOptions{})
	if len(got) == 0 {
		t.Fatal("defaults: no chunks")
	}
	for i, c := range got {
		if len([]rune(c)) > 500 {
			t.Fatalf("chunk[%d] len = %d > 500", i, len([]rune(c)))
		}
	}
}

// ---------- 工具 ----------

func runeLens(ss []string) []int {
	out := make([]int, len(ss))
	for i, s := range ss {
		out[i] = len([]rune(s))
	}
	return out
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}