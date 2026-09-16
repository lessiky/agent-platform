import { useEffect, useRef, useState } from 'react';
import { Markdown } from '@/components/common/Markdown';

// Markdown 摘录 (检索试算命中等): 按行数折叠, 检测到溢出时显示 展开/收起
export function MarkdownExcerpt({
  text,
  rows = 3,
  className,
}: {
  text: string;
  rows?: number;
  className?: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  const clampRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = clampRef.current;
    if (!el || expanded) return;
    const check = () => setOverflowing(el.scrollHeight > el.clientHeight + 1);
    check();
    const observer = new ResizeObserver(check);
    observer.observe(el);
    return () => observer.disconnect();
  }, [text, expanded]);

  return (
    <div className={className}>
      <div
        ref={clampRef}
        style={
          expanded
            ? undefined
            : {
                display: '-webkit-box',
                WebkitBoxOrient: 'vertical',
                WebkitLineClamp: rows,
                overflow: 'hidden',
              }
        }
      >
        <Markdown content={text} />
      </div>
      {overflowing && (
        <a onClick={() => setExpanded((value) => !value)} style={{ fontSize: 12 }}>
          {expanded ? '收起' : '展开'}
        </a>
      )}
    </div>
  );
}
