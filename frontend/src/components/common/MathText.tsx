import katex from 'katex';
import { memo, useMemo } from 'react';
import 'katex/dist/katex.min.css';

// 匹配 LaTeX 数学片段: $$...$$ / \[...\] (块级), \(...\) / $...$ (行内)
const MATH_PATTERN = /(\$\$[\s\S]+?\$\$|\\\[[\s\S]+?\\\]|\\\((?:\\.|[^\\()])*?\\\)|\$[^\n$]+?\$)/g;

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function renderMathToHtml(text: string): string {
  let html = '';
  let last = 0;
  MATH_PATTERN.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = MATH_PATTERN.exec(text)) !== null) {
    if (m.index > last) {
      html += escapeHtml(text.slice(last, m.index));
    }
    const token = m[1];
    let expr: string;
    let display: boolean;
    if (token.startsWith('$$')) {
      expr = token.slice(2, -2);
      display = true;
    } else if (token.startsWith('\\[')) {
      expr = token.slice(2, -2);
      display = true;
    } else if (token.startsWith('\\(')) {
      expr = token.slice(2, -2);
      display = false;
    } else {
      expr = token.slice(1, -1);
      display = false;
    }
    try {
      html += katex.renderToString(expr, { displayMode: display, throwOnError: true });
    } catch {
      // 公式解析失败时原样展示
      html += `<code class="math-fallback">${escapeHtml(token)}</code>`;
    }
    last = m.index + token.length;
  }
  if (last < text.length) {
    html += escapeHtml(text.slice(last));
  }
  return html;
}

// 富文本消息内容: 普通文本 + LaTeX 公式 (KaTeX 渲染, 解析失败原样展示)
export const MathText = memo(function MathText({ text }: { text: string }) {
  const html = useMemo(() => renderMathToHtml(text), [text]);
  return <span className="math-text" dangerouslySetInnerHTML={{ __html: html }} />;
});
