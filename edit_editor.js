const fs = require('fs');
const path = require('path');
const root = 'D:/newwork/agent-platform';

function readLF(p) {
  const c = fs.readFileSync(path.join(root, p), 'utf8');
  return { c: c.replace(/\r\n/g, '\n'), crlf: c.includes('\r\n') };
}
function writeCRLF(p, c, crlf) {
  if (c.includes('\r')) throw new Error(p + ': stray CR in output');
  fs.writeFileSync(path.join(root, p), crlf ? c.replace(/\n/g, '\r\n') : c);
}
function replaceOnce(p, oldStr, newStr, label) {
  const { c: raw, crlf } = readLF(p);
  const idx = raw.indexOf(oldStr);
  if (idx === -1) throw new Error(p + ' [' + label + ']: anchor not found');
  if (raw.indexOf(oldStr, idx + 1) !== -1) throw new Error(p + ' [' + label + ']: anchor not unique');
  const c = raw.slice(0, idx) + newStr + raw.slice(idx + oldStr.length);
  writeCRLF(p, c, crlf);
  console.log('ok: [' + label + ']');
}

const ED = 'frontend/src/pages/workflow/WorkflowEditorPage.tsx';

// A1: module-scope helper after parseJsonField
replaceOnce(ED,
  "  } catch (err) {\n    throw new Error(`${label} 不是合法 JSON 对象: ${(err as Error).message}`);\n  }\n}",
  "  } catch (err) {\n    throw new Error(`${label} 不是合法 JSON 对象: ${(err as Error).message}`);\n  }\n}\n\n// 按节点类型把表单值组装为节点定义 (JSON 字段解析失败时抛错)\nfunction buildNodeDefFromValues(nodeDef: WorkflowNodeDef, values: Record<string, any>): WorkflowNodeDef {\n  const config: Record<string, unknown> = {};\n  switch (nodeDef.type) {\n    case 'agent':\n      config.agent_id = values.agent_id || '';\n      config.message = values.message ?? '';\n      break;\n    case 'mcp_tool':\n      config.mcp_server_id = values.mcp_server_id || '';\n      config.tool = values.tool || '';\n      config.arguments = parseJsonField(values.arguments ?? '', 'arguments');\n      break;\n    case 'http':\n      config.method = (values.method || 'GET').toUpperCase();\n      config.url = values.url || '';\n      if (values.headers) config.headers = parseJsonField(values.headers, 'headers');\n      if (values.body && config.method !== 'GET' && config.method !== 'DELETE') {\n        config.body = parseJsonField(values.body, 'body');\n      }\n      break;\n    case 'delay':\n      config.seconds = values.seconds ?? 1;\n      break;\n    case 'condition':\n      config.left = values.left ?? '';\n      config.operator = values.operator || '==';\n      config.right = values.right;\n      break;\n    case 'print':\n      config.message = values.print_message ?? '';\n      if (values.print_color) config.color = values.print_color;\n      break;\n    default:\n      config = { ...(nodeDef.config ?? {}) };\n  }\n  return {\n    ...nodeDef,\n    name: (values.name as string) || nodeDef.id,\n    config,\n    ...(values.timeout_seconds ? { timeout_seconds: values.timeout_seconds as number } : {}),\n    ...(values.retry_enabled\n      ? {\n          retry: {\n            max_attempts: (values.max_attempts as number) || 1,\n            interval_seconds: (values.interval_seconds as number) || 0,\n            backoff: (values.backoff as 'fixed' | 'exponential') || 'fixed',\n          },\n        }\n      : {}),\n  };\n}",
  'buildNodeDefFromValues helper');

// A5b: ref declaration
replaceOnce(ED,
  '  const seqRef = useRef(1);\n',
  '  const seqRef = useRef(1);\n  const prevSelectedIdRef = useRef<string | null>(null);\n',
  'prevSelectedIdRef');

// A5c: load() reset
replaceOnce(ED,
  "      if (!res.data) { message.error('加载工作流失败'); return; }\n      setWorkflow(res.data);",
  "      if (!res.data) { message.error('加载工作流失败'); return; }\n      prevSelectedIdRef.current = null; // 整幅画布加载, 不提交旧选中节点的表单\n      setWorkflow(res.data);",
  'load reset');

// A4: onAIGenerated reset
replaceOnce(ED,
  "  const onAIGenerated = (result: AIGenerateResult) => {\n    const def = result.definition;\n    setWorkflow((prev) => (prev ? { ...prev, name: result.name, description: result.description } : prev));",
  "  const onAIGenerated = (result: AIGenerateResult) => {\n    const def = result.definition;\n    prevSelectedIdRef.current = null; // 画布被整体替换, 不提交旧选中节点的表单\n    setWorkflow((prev) => (prev ? { ...prev, name: result.name, description: result.description } : prev));",
  'aiGen reset');

// A3: selection sync effect with commit-on-switch
replaceOnce(ED,
  "  // 选中节点 -> 表单同步\n  useEffect(() => {\n    if (selectedDef) {\n      const cfg = selectedDef.config ?? {};",
  "  // 选中节点 -> 表单同步 (切换时先把上一个节点的表单值提交回节点定义, 避免修改丢失)\n  useEffect(() => {\n    const prevId = prevSelectedIdRef.current;\n    prevSelectedIdRef.current = selectedNodeId;\n    if (prevId && prevId !== selectedNodeId) {\n      const values = form.getFieldsValue();\n      setNodes((nds) =>\n        nds.map((n) => {\n          if (n.id !== prevId) return n;\n          try {\n            const updated = buildNodeDefFromValues(n.data.nodeDef as WorkflowNodeDef, values);\n            return { ...n, data: { ...n.data, name: updated.name, nodeDef: updated } };\n          } catch {\n            return n; // 表单值不完整 (如 JSON 解析失败), 保持原配置\n          }\n        })\n      );\n    }\n    if (selectedDef) {\n      const cfg = selectedDef.config ?? {};",
  'commit-on-switch effect');

// A2: slim collectSelectedConfig
replaceOnce(ED,
  "  const collectSelectedConfig = useCallback((): { nodes: Node[]; updated: WorkflowNodeDef | null } => {\n    if (!selectedDef) return { nodes, updated: null };\n    const values = form.getFieldsValue();\n    const config: Record<string, unknown> = {};\n    switch (selectedDef.type) {\n      case 'agent':\n        config.agent_id = values.agent_id || '';\n        config.message = values.message ?? '';\n        break;\n      case 'mcp_tool':\n        config.mcp_server_id = values.mcp_server_id || '';\n        config.tool = values.tool || '';\n        config.arguments = parseJsonField(values.arguments ?? '', 'arguments');\n        break;\n      case 'http':\n        config.method = (values.method || 'GET').toUpperCase();\n        config.url = values.url || '';\n        if (values.headers) config.headers = parseJsonField(values.headers, 'headers');\n        if (values.body && config.method !== 'GET' && config.method !== 'DELETE') {\n          config.body = parseJsonField(values.body, 'body');\n        }\n        break;\n      case 'delay':\n        config.seconds = values.seconds ?? 1;\n        break;\n      case 'condition':\n        config.left = values.left ?? '';\n        config.operator = values.operator || '==';\n        config.right = values.right;\n        break;\n      case 'print':\n        config.message = values.print_message ?? '';\n        if (values.print_color) config.color = values.print_color;\n        break;\n    }\n    const updated: WorkflowNodeDef = {\n      ...selectedDef,\n      name: values.name || selectedDef.id,\n      config,\n      ...(values.timeout_seconds ? { timeout_seconds: values.timeout_seconds } : {}),\n      ...(values.retry_enabled\n        ? { retry: { max_attempts: values.max_attempts || 1, interval_seconds: values.interval_seconds || 0, backoff: values.backoff || 'fixed' } }\n        : {}),\n    };\n    const nextNodes = nodes.map((n) => (n.id === selectedDef.id ? { ...n, data: { ...n.data, name: updated.name, nodeDef: updated } } : n));\n    return { nodes: nextNodes, updated };\n  }, [selectedDef, form, nodes]);",
  "  const collectSelectedConfig = useCallback((): { nodes: Node[]; updated: WorkflowNodeDef | null } => {\n    if (!selectedDef) return { nodes, updated: null };\n    // buildNodeDefFromValues 可能抛错 (JSON 解析失败), 由调用方处理\n    const updated = buildNodeDefFromValues(selectedDef, form.getFieldsValue());\n    const nextNodes = nodes.map((n) => (n.id === selectedDef.id ? { ...n, data: { ...n.data, name: updated.name, nodeDef: updated } } : n));\n    return { nodes: nextNodes, updated };\n  }, [selectedDef, form, nodes]);",
  'slim collectSelectedConfig');

console.log('ALL DONE');