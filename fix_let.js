const fs = require('fs');
const p = 'D:/newwork/agent-platform/frontend/src/pages/workflow/WorkflowEditorPage.tsx';
let c = fs.readFileSync(p, 'utf8');
const o = 'function buildNodeDefFromValues(nodeDef: WorkflowNodeDef, values: Record<string, any>): WorkflowNodeDef {\n  const config: Record<string, unknown> = {};';
const n = 'function buildNodeDefFromValues(nodeDef: WorkflowNodeDef, values: Record<string, any>): WorkflowNodeDef {\n  let config: Record<string, unknown> = {};';
if (c.split(o).length !== 2) throw new Error('anchor not found');
c = c.replace(o, n);
fs.writeFileSync(p, c);
console.log('ok: let config');