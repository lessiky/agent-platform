# -*- coding: utf-8 -*-
"""示例：Webhook 触发工作流执行并获取执行结果（Webhook Token）

对应 docs/external-api.md 「3. Webhook 触发工作流」「4. 查询工作流执行状态」：
  - POST /webhooks/workflows/:token                  -> 触发执行，返回执行 ID
  - GET  /webhooks/workflows/:token/executions/:id   -> 轮询执行状态（仅状态视图）
  - 场景 A（无需审核）：status 直接 running -> success/failed，进入终态即结束。
  - 场景 B（需要审核）：某节点等待人工审核时 status 变为 waiting_approval，
    需平台内操作者通过/驳回（外部无法代审），外部端持续轮询直至终态
    （success/failed/cancelled）。

用法：
  pip install requests
  python webhook_trigger_workflow.py --token <webhook_token> [--input '{"order_id":"SO-1001"}']
"""

import argparse
import json
import sys
import time

import requests

DEFAULT_BASE_URL = "http://localhost:8080/api/v1"
POLL_INTERVAL = 2  # 执行轮询间隔（秒），文档建议 1~5s
TERMINAL_STATUSES = ("success", "failed", "cancelled")


def check_response(resp):
    """非 2xx 时抛出带可读信息的异常。"""
    if resp.ok:
        return resp
    try:
        message = resp.json().get("message", "")
    except ValueError:
        message = resp.text
    raise RuntimeError(f"HTTP {resp.status_code}: {message}")


def trigger_workflow(base_url, token, input_payload):
    """经 webhook 触发执行，返回创建的执行对象（含执行 ID）。"""
    url = f"{base_url}/webhooks/workflows/{token}"
    resp = requests.post(url, json=input_payload, timeout=30)
    check_response(resp)
    return resp.json()["data"]


def poll_execution(base_url, token, execution_id, timeout=600):
    """轮询执行状态直至终态；waiting_approval 时提示去平台内处理。"""
    url = f"{base_url}/webhooks/workflows/{token}/executions/{execution_id}"
    deadline = time.time() + timeout
    waiting_notified = False
    while time.time() < deadline:
        view = check_response(requests.get(url, timeout=30)).json()["data"]
        status = view["status"]
        nodes = view.get("nodes") or []
        node_summary = ", ".join(
            f"{n.get('node_name') or n.get('node_id')}:{n.get('status')}" for n in nodes
        )
        print(f"  [轮询] status={status}  nodes=[{node_summary}]")

        if status == "waiting_approval" and not waiting_notified:
            approval_ids = [n.get("approval_id") for n in nodes if n.get("approval_id")]
            print(f"  ⏳ 需要审核：有节点等待人工审核，请在平台内审批/驳回"
                  f"（approval_id: {', '.join(approval_ids) or '见节点详情'}），继续轮询...")
            waiting_notified = True

        if status in TERMINAL_STATUSES:
            return view
        time.sleep(POLL_INTERVAL)
    raise TimeoutError(f"等待执行 {execution_id} 超时（{timeout}s）")


def main():
    parser = argparse.ArgumentParser(description="Webhook 触发工作流执行示例")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--token", required=True, help="工作流 webhook_token（32 位 hex）")
    parser.add_argument("--input", default="{}",
                        help='执行输入 JSON 对象，如 \'{"order_id":"SO-1001"}\'')
    args = parser.parse_args()

    try:
        input_payload = json.loads(args.input)
    except json.JSONDecodeError:
        sys.exit("--input 必须是合法 JSON")
    if not isinstance(input_payload, dict):
        sys.exit("--input 必须是 JSON 对象")

    execution = trigger_workflow(args.base_url, args.token, input_payload)
    execution_id = execution["id"]
    print(f"触发成功: execution_id={execution_id}  status={execution['status']}"
          f"  trace_id={execution.get('trace_id')}")
    print(f"工作流: {execution.get('workflow_name')} (version {execution.get('workflow_version')})")

    print("\n轮询执行状态 ...")
    view = poll_execution(args.base_url, args.token, execution_id)

    print("\n执行结束:")
    print(f"  status: {view['status']}")
    if view.get("error"):
        print(f"  error:  {view['error']}")
    print(f"  起止: {view.get('started_at')} -> {view.get('finished_at')}")
    for node in view.get("nodes") or []:
        line = f"  - {node.get('node_name') or node.get('node_id')} ({node.get('node_type')}): {node.get('status')}"
        if node.get("error"):
            line += f"  error={node.get('error')}"
        print(line)

    if view["status"] != "success":
        sys.exit(1)


if __name__ == "__main__":
    main()
