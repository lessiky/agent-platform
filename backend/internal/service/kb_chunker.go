package service

// kb_chunker.go — M11.5 条目分块器 (事项 4: 纯函数, 可单测)
//
// 分块策略 (Markdown 结构感知):
//   - 正文 ≤ KB_CHUNK_THRESHOLD (默认 1000) → 整条 1 块 (短条目行为与现状一致);
//   - 超过阈值 → 标题 (ATX) 为一级边界, 段落 (空行分隔) 为基础单元,
//     按 KB_CHUNK_SIZE (默认 500 rune) 打包; 相邻块携带前块尾部
//     KB_CHUNK_OVERLAP (默认 50 rune) 作为重叠, 保证跨块上下文连续;
//   - 超尺寸单元 (超长段落/代码块) 按 Size 硬切, 重叠规则同上。
//
// 分块结果供 kb_chunks 表存储与块级向量化使用 (W2 接入写/读路径)。

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agent-platform/internal/repository"
)

// KBChunkOptions 分块参数 (对应环境变量 KB_CHUNK_SIZE / KB_CHUNK_OVERLAP / KB_CHUNK_THRESHOLD)
type KBChunkOptions struct {
	Size      int // 目标块大小 (rune 数, 默认 500)
	Overlap   int // 相邻块重叠 (rune 数, 默认 50, 须 < Size)
	Threshold int // 分块阈值: 正文 ≤ 阈值整条 1 块 (默认 1000)
}

// normalized 填充默认值并防御非法组合
func (o KBChunkOptions) normalized() KBChunkOptions {
	if o.Size <= 0 {
		o.Size = 500
	}
	if o.Threshold <= 0 {
		o.Threshold = 1000
	}
	if o.Overlap < 0 {
		o.Overlap = 0
	}
	if o.Overlap >= o.Size {
		o.Overlap = o.Size / 2
	}
	return o
}

// ChunkMarkdown 将条目正文切分为块序列 (A25):
//   - 空正文 → 空序列;
//   - 正文 ≤ Threshold → []string{整条} (短条目 1 块);
//   - 否则按结构分块 + 打包 + 重叠。
//
// 返回值每块 ≤ Size rune (重叠除外), 块序 = 文档序; 内容为块文本 (含标题行), 不含文档级空白。
func ChunkMarkdown(content string, opts KBChunkOptions) []string {
	opts = opts.normalized()
	s := strings.TrimSpace(content)
	if s == "" {
		return nil
	}
	if len([]rune(s)) <= opts.Threshold {
		return []string{s}
	}

	var chunks []string
	var cur []rune

	// closeChunk 收尾当前块 (空块丢弃)
	closeChunk := func() {
		text := strings.TrimSpace(string(cur))
		cur = nil
		if text != "" {
			chunks = append(chunks, text)
		}
	}
	// carry 取 prev 尾部最多 Overlap rune 作为下一块重叠前缀
	carry := func(prev string) {
		r := []rune(prev)
		if len(r) > opts.Overlap {
			r = r[len(r)-opts.Overlap:]
		}
		cur = append([]rune(nil), r...)
	}

	for _, section := range splitKBSections(s) {
		for _, unit := range splitKBUnits(section) {
			u := []rune(unit)
			if len(u) > opts.Size {
				// 超尺寸单元: 硬切多块, 重叠规则同打包
				closeChunk()
				last := ""
				for _, part := range hardSplitKB(u, opts) {
					chunks = append(chunks, part)
					last = part
				}
				carry(last)
				continue
			}
			// 标题为一级边界: 标题单元恒从新块开始 (不挤入上一块尾部)
			if len(cur) > 0 && isKBHeadingLine(unit) {
				prev := string(cur)
				closeChunk()
				carry(prev)
				if allow := opts.Size - len(u) - 1; allow < 0 || len(cur) > allow {
					if allow < 0 {
						cur = nil
					} else {
						cur = cur[:allow]
					}
				}
			}
			if len(cur)+1+len(u) <= opts.Size {
				if len(cur) > 0 {
					cur = append(cur, '\n')
				}
				cur = append(cur, u...)
				continue
			}
			// 换块: 携带前块尾部重叠; 重叠必要时截断以容纳单元
			prev := string(cur)
			closeChunk()
			carry(prev)
			if len(cur) > 0 {
				allow := opts.Size - len(u) - 1
				if allow < 0 {
					allow = 0
				}
				if len(cur) > allow {
					cur = cur[:allow]
				}
			}
			if len(cur) > 0 {
				cur = append(cur, '\n')
			}
			cur = append(cur, u...)
		}
	}
	// 收尾: 剩余若是纯重叠携带 (全含于末块尾部) 则丢弃, 避免末块尾部重复成块
	tail := strings.TrimSpace(string(cur))
	cur = nil
	if tail == "" {
		return chunks
	}
	if len(chunks) > 0 && len([]rune(tail)) <= opts.Overlap && strings.HasSuffix(chunks[len(chunks)-1], tail) {
		return chunks
	}
	chunks = append(chunks, tail)
	return chunks
}

// hardSplitKB 超尺寸单元按 Size 硬切 (每块 ≤ Size rune, 相邻重叠 Overlap rune)
func hardSplitKB(u []rune, opts KBChunkOptions) []string {
	var parts []string
	for i := 0; i < len(u); {
		end := i + opts.Size
		if end > len(u) {
			parts = append(parts, string(u[i:]))
			break
		}
		parts = append(parts, string(u[i:end]))
		// normalized() 保证 overlap < size, 窗口恒前进 (步长 = size - overlap)
		i = end - opts.Overlap
	}
	return parts
}

// splitKBSections 按 Markdown 标题行 (ATX, 1-6 个 # + 空白) 切分; 标题行归入新节开头
func splitKBSections(s string) []string {
	lines := strings.Split(s, "\n")
	var sections []string
	var cur []string
	flush := func() {
		if text := strings.TrimSpace(strings.Join(cur, "\n")); text != "" {
			sections = append(sections, text)
		}
		cur = nil
	}
	for _, line := range lines {
		if isKBHeadingLine(line) {
			flush()
			cur = append(cur, line)
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return sections
}

// splitKBUnits 节内按空行切段落 (基础单元); 开头标题行独立成单元
func splitKBUnits(section string) []string {
	lines := strings.Split(section, "\n")
	var units []string
	var cur []string
	flush := func() {
		if text := strings.TrimSpace(strings.Join(cur, "\n")); text != "" {
			units = append(units, text)
		}
		cur = nil
	}
	for i, line := range lines {
		if i == 0 && isKBHeadingLine(line) {
			flush()
			units = append(units, strings.TrimSpace(line))
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return units
}

// isKBHeadingLine ATX 标题行判定 (#~###### 后接空白或行尾; 避免误判 #hashtag)
func isKBHeadingLine(line string) bool {
	t := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(t, "#") {
		return false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return false
	}
	return n == len(t) || t[n] == ' ' || t[n] == '\t'
}

// KBChunkEmbedText 块向量化文本 = 块正文本身 (M11.5 事项 4)。
// 标题不并入块向量: 同一文档各块共享标题不区分块内差异, 且检索重排阶段会以
// "标题 + 最佳命中块" 重新拼出 rerank 文本, 故块向量只承载块级内容信号。
// mock E2E 依赖此语义: 查询文本 == 块文本 -> 余弦 1.0 top-1 命中。
func KBChunkEmbedText(chunk string) string {
	return strings.TrimSpace(chunk)
}

// MigrateExistingKBChunks 存量分块迁移 (M11.5 事项 4: 上线时为现存 active 条目同步生成块,
// 纯 CPU 切分不含向量, 万级条目秒级完成; 向量化由回填任务补齐)。幂等可重跑: 已有块的条目跳过。
// 分批 (每批 500) 避免大库长事务; 空正文条目 (创建校验已禁) 防御性跳过防死循环。
func MigrateExistingKBChunks(ctx context.Context, chunks repository.KBChunkRepository, opts KBChunkOptions) (int64, error) {
	var migrated int64
	for {
		if err := ctx.Err(); err != nil {
			return migrated, err
		}
		docs, err := chunks.MissingDocs(ctx, 500, 0)
		if err != nil {
			return migrated, fmt.Errorf("list docs missing chunks: %w", err)
		}
		if len(docs) == 0 {
			return migrated, nil
		}
		progress := 0
		for i := range docs {
			contents := ChunkMarkdown(docs[i].Content, opts)
			if len(contents) == 0 {
				log.Printf("kb: chunk migrate skip empty content doc=%s", docs[i].ID)
				continue
			}
			if err := chunks.Rebuild(ctx, docs[i].ID, contents); err != nil {
				return migrated, fmt.Errorf("migrate chunks doc=%s: %w", docs[i].ID, err)
			}
			migrated++
			progress++
		}
		if progress == 0 {
			// 本批全部为空正文 (不应发生), 退出防死循环
			log.Printf("kb: chunk migrate stalled on %d empty-content doc(s), stopping", len(docs))
			return migrated, nil
		}
	}
}