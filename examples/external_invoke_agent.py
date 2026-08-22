# -*- coding: utf-8 -*-
"""示例：外部调用 Agent 并获取执行结果（API Key 认证）

对应 docs/external-api.md 「1. 外部调用 Agent」「2. 获取 Agent 续答结果」：
  - 场景 A（无需审核）：POST /agents/:id/invoke 直接返回 200，data.reply 即最终应答。
  - 场景 B（需要审核）：存在待审核工具调用时返回 202 pending_approval，
    随后凭同一 API Key 轮询 GET /agents/:id/invoke/approvals/:approvalId，
    直到终态（approved/rejected/expired），获取工具执行结果（result）
    与模型续答（continuation）。审核由平台内操作者完成（或按全局超时策略自动处理）。

用法：
  pip install requests
  set AGENT_API_KEY=akp_xxx          # 或直接传 --api-key
  python external_invoke_agent.py --agent-id <AgentID> --message "创建一个工单" [--session-id <sid>]
"""

import argparse
import os
import sys
import time

import requests

DEFAULT_BASE_URL = "http://localhost:8080/api/v1"
POLL_INTERVAL = 3  # 审核轮询间隔（秒），文档建议 2~5s
TERMINAL_STATUSES = ("approved", "rejected", "expired")


def check_response(resp):
    """非 2xx 时抛出带可读信息的异常。"""
    if resp.ok:
        return resp
    try:
        message = resp.json().get("message", "")
    except ValueError:
        message = resp.text
    raise RuntimeError(f"HTTP {resp.status_code}: {message}")


def invoke_agent(base_url, api_key, agent_id, message, session_id=None):
    """发起一次外部调用，返回 (http_status, data)。"""
    url = f"{base_url}/agents/{agent_id}/invoke"
    body = {"message": message}
    if session_id:
        body["session_id"] = session_id
    resp = requests.post(
        url,
        json=body,
        headers={"Authorization": f"Bearer {api_key}"},
        timeout=300,
    )
    check_response(resp)
    payload = resp.json()
    return resp.status_code, payload.get("data") or {}


def poll_approval(base_url, api_key, agent_id, approval_id, timeout=600, continuation_wait=60):
    """
    轮询审核请求直至可返回：
      - status 进入终态且 continuation（模型续答）已回填 -> 立即返回
      - approved 且 result（工具执行结果）已回填后，continuation 宽限
        continuation_wait 秒仍未回填（续答轮可能失败）-> 以 result 为最终输出返回
      - rejected / expired 时 result 按设计为空，终态后宽限期满即返回
    """
    url = f"{base_url}/agents/{agent_id}/invoke/approvals/{approval_id}"
    headers = {"Authorization": f"Bearer {api_key}"}
    deadline = time.time() + timeout
    terminal_since = None
    while time.time() < deadline:
        view = check_response(requests.get(url, headers=headers, timeout=30)).json()["data"]
        status = view["status"]
        has_result = "有" if view.get("result") is not None else "无"
        has_continuation = "有" if view.get("continuation") is not None else "无"
        print(f"  [轮询] status={status}  result={has_result}  continuation={has_continuation}")

        if status in TERMINAL_STATUSES:
            if view.get("continuation") is not None:
                return view
            if terminal_since is None:
                terminal_since = time.time()
            grace_elapsed = time.time() - terminal_since > continuation_wait
            has_result = view.get("result") is not None
            if grace_elapsed and (has_result or status != "approved"):
                print("  continuation 未回填（续答轮可能失败或尚未完成），返回当前状态")
                return view
        else:
            terminal_since = None
        time.sleep(POLL_INTERVAL)
    raise TimeoutError(f"等待审核请求 {approval_id} 超时（{timeout}s）")


def print_approval_result(view):
    print(f"  终态: {view['status']}  (审核意见: {view.get('comment') or '无'})")
    if view.get("result") is not None:
        print(f"  工具执行结果: {view['result']}")
    else:
        print("  工具执行结果: 无（工具未执行）")
    if view.get("continuation") is not None:
        print(f"  模型续答: {view['continuation']['reply']}")


def main():
    parser = argparse.ArgumentParser(description="外部调用 Agent 示例（API Key 认证）")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--agent-id", required=True, help="Agent ID")
    parser.add_argument("--message", required=True, help="用户提示词")
    parser.add_argument("--session-id", default=None, help="复用会话（多轮上下文）；不传则自动新建")
    parser.add_argument("--api-key", default=os.environ.get("AGENT_API_KEY"),
                        help="Agent API Key（默认读取环境变量 AGENT_API_KEY）")
    args = parser.parse_args()

    if not args.api_key:
        sys.exit("请设置环境变量 AGENT_API_KEY 或传入 --api-key")

    status_code, data = invoke_agent(
        args.base_url, args.api_key, args.agent_id, args.message, args.session_id
    )
    print(f"调用完成: http={status_code}  agent_id={data.get('agent_id')}")
    print(f"session_id={data.get('session_id')}  model={data.get('model_name')}"
          f"  tokens={data.get('tokens')}  latency={data.get('latency_ms')}ms")

    if status_code == 200:
        # 场景 A：无需审核，应答即最终结果
        print("\n✅ 场景 A（无需审核）直接应答:")
        print(f"  reply: {data.get('reply')}")
        return

    # 场景 B：需要审核（202 pending_approval），对应工具尚未执行
    print("\n⏳ 场景 B（需要审核）：以下工具待人工审核，请在平台内完成审批/驳回:")
    for pending in data.get("pending_approvals", []):
        print(f"  - {pending['mcp_name']}/{pending['tool_name']}  approval_id={pending['approval_id']}")

    for pending in data.get("pending_approvals", []):
        print(f"\n轮询审核请求 {pending['approval_id']} ...")
        view = poll_approval(args.base_url, args.api_key, args.agent_id, pending["approval_id"])
        print_approval_result(view)


if __name__ == "__main__":
    main()
