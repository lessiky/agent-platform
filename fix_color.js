const fs = require('fs');
const p = 'D:/newwork/agent-platform/frontend/src/pages/workflow/WorkflowEditorPage.tsx';
let c = fs.readFileSync(p, 'utf8');
function rep(o, n, label) {
  if (c.split(o).length !== 2) throw new Error('anchor not found: ' + label);
  c = c.replace(o, n);
  console.log('ok: ' + label);
}
rep(
  "    case 'print':\n      config.message = values.print_message ?? '';\n      if (values.print_color) config.color = values.print_color;\n      break;",
  "    case 'print': {\n      config.message = values.print_message ?? '';\n      // ColorPicker 经 Form 存的是 Color 对象, 落库前转为 hex 字符串\n      const colorValue = values.print_color;\n      if (colorValue) {\n        config.color = typeof colorValue === 'string' ? colorValue : colorValue.toHexString();\n      }\n      break;\n    }",
  'color to hex in collect'
);
rep(
  "        print_color: (cfg.color as string) || undefined,",
  "        print_color: typeof cfg.color === 'string' ? cfg.color : undefined, // 仅接受 hex 字符串 (兼容旧数据中的 Color 对象)",
  'form sync color string only'
);
fs.writeFileSync(p, c);