# -*- coding: utf-8 -*-
"""示例：外部调用 Agent 并获取执行结果（API Key 认证，异步执行任务）

对应 docs/api/external-api.md 「1. 外部调用 Agent」「2. 获取执行任务状态」「3. 取消执行任务」：
  - POST /agents/:id/invoke 立即返回 202 + execution_id（执行链在平台后台运行，
    多轮工具调用耗时不影响调用方超时设置）；
  - 随后凭同一 API Key 轮询 GET /agents/:id/invoke/executions/:executionId，
    status / stage / last_activity_at 可明确区分「执行中」与「卡死」：
      * running + stage=tool:<mcp>/<tool>  -> 正在调用该工具，看 last_activity_at 是否新鲜
      * success                           -> result 即模型应答与调用明细
        （涉及人工审核时 result 另含 pre_review_mcp_calls = 审核前工具调用明细,
        pending_approvals 累积全部审核单; 脚本分别打印审核前明细/审核决策/审核后明细）
      * failed / stalled                  -> error 给出原因（stalled = 平台 watchdog 判定卡死并已取消）
      * cancelled                         -> 外部方经取消端点主动放弃任务
      * waiting_approval                  -> 有工具待人工审核（在平台内完成审批；
        审核决策后平台自动回填本任务终态与模型续答，继续轮询本端点即可）
  - 降级同步路径（无可用模型时）：直接返回 200，data.reply 即最终应答。

  用法：
  pip install requests
  set AGENT_API_KEY=akp_xxx          # 或直接传 --api-key
  python external_invoke_agent.py --agent-id <AgentID> --message "创建一个工单" [--session-id <sid>]
  python external_invoke_agent.py --agent-id <AgentID> --message "x" --cancel <execution_id>
      # --cancel: 不发起新调用, 直接取消指定执行任务（外部方主动放弃）
"""

import argparse
import os
import sys
import time

import requests

DEFAULT_BASE_URL = "http://localhost:8080/api/v1"
POLL_INTERVAL = 3        # 执行任务轮询间隔（秒），文档建议 2~5s
EXECUTION_TIMEOUT = 600  # 整体等待上限（秒）；平台侧另有 deadline/watchdog 兜底
TERMINAL_STATUSES = ("success", "failed", "stalled", "cancelled")


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
    """发起一次外部调用，返回 (http_status, data)。异步路径立即返回 202 + execution_id。"""
    url = f"{base_url}/agents/{agent_id}/invoke"
    body = {"message": message}
    if session_id:
        body["session_id"] = session_id
    resp = requests.post(
        url,
        json=body,
        headers={"Authorization": f"Bearer {api_key}"},
        timeout=30,  # 接口为异步受理，正常几百毫秒内返回 202
    )
    check_response(resp)
    payload = resp.json()
    return resp.status_code, payload.get("data") or {}


def poll_execution(base_url, api_key, agent_id, execution_id, timeout=EXECUTION_TIMEOUT):
    """
    轮询执行任务直至终态（success/failed/stalled/cancelled），返回终态 data。
    中间态持续打印进度：running 显示当前阶段；waiting_approval 提示待审核单。
    """
    url = f"{base_url}/agents/{agent_id}/invoke/executions/{execution_id}"
    headers = {"Authorization": f"Bearer {api_key}"}
    deadline = time.time() + timeout
    while time.time() < deadline:
        view = check_response(requests.get(url, headers=headers, timeout=30)).json()["data"]
        status = view["status"]
        stage = view.get("stage") or "-"
        last_activity = view.get("last_activity_at") or "-"
        print(f"  [轮询] status={status}  stage={stage}  last_activity={last_activity}")

        if status == "waiting_approval":
            print("  ⏳ 等待人工审核（请在平台内完成审批/驳回；决策后平台自动回填终态）:")
            for approval_id in view.get("pending_approvals") or []:
                print(f"     approval_id={approval_id}")
        if status in TERMINAL_STATUSES:
            return view
        time.sleep(POLL_INTERVAL)
    raise TimeoutError(f"等待执行任务 {execution_id} 超时（{timeout}s）")


def cancel_execution(base_url, api_key, agent_id, execution_id):
    """取消执行任务（外部方主动放弃），返回 data（cancelled 是否由本次调用触发 + 操作后状态）。"""
    url = f"{base_url}/agents/{agent_id}/invoke/executions/{execution_id}/cancel"
    resp = requests.post(url, headers={"Authorization": f"Bearer {api_key}"}, timeout=30)
    try:
        check_response(resp)
    except RuntimeError as exc:
        # 409 = 任务在等待人工审核或不在当前进程, 提示后退出
        print(f"取消失败: {exc}")
        sys.exit(1)
    return resp.json()["data"]


APPROVAL_STATUS_LABELS = {
    "pending": "待审核",
    "approved": "已通过",
    "rejected": "已驳回",
    "expired": "已超时",
}


def get_approval(base_url, api_key, agent_id, approval_id):
    """查询审核请求（审核前工具调用详情 + 审核决策），返回 data。"""
    url = f"{base_url}/agents/{agent_id}/invoke/approvals/{approval_id}"
    resp = requests.get(url, headers={"Authorization": f"Bearer {api_key}"}, timeout=30)
    check_response(resp)
    return resp.json()["data"]


def fetch_approvals(base_url, api_key, agent_id, view):
    """查询本次执行的全部审核请求（view.pending_approvals, 多轮审核时累积）;
    返回 {approval_id: data}（按 view 中出现顺序）。"""
    approvals = {}
    for approval_id in view.get("pending_approvals") or []:
        try:
            approvals[approval_id] = get_approval(base_url, api_key, agent_id, approval_id)
        except Exception as exc:  # 审核详情查询失败不阻断结果汇总
            print(f"   approval_id={approval_id}  (详情查询失败: {exc})")
    return approvals


def _approval_id_of_call(call):
    """从审核前明细的 pending 项提取 approval_id（detail 形如 approval_id=<uuid>）。"""
    if call.get("status") != "pending":
        return None
    detail = call.get("detail") or ""
    if detail.startswith("approval_id="):
        return detail[len("approval_id="):]
    return None


def _print_call_line(call, indent):
    detail = f"  ({call['detail']})" if call.get("detail") else ""
    print(f"{indent}tool {call.get('mcp_name', '')}/{call['tool_name']} "
          f"status={call['status']} latency={call.get('latency_ms', 0)}ms{detail}")


def _print_approval_line(approval, indent):
    label = APPROVAL_STATUS_LABELS.get(approval["status"], approval["status"])
    print(f"{indent}审核={label}  requested_at={approval['requested_at']}  "
          f"decided_at={approval.get('decided_at') or '-'}  "
          f"executed_at={approval.get('executed_at') or '-'}")
    if approval.get("comment"):
        print(f"{indent}审核意见: {approval['comment']}")


def print_approval_summary(view, base_url, api_key, agent_id, indent="   "):
    """打印审核前的工具调用情况:
    命中人工审核门禁轮的工具调用明细 (含未执行的 pending 项) + 对应审核决策。
    返回 {approval_id: data} 供调用方判断是否区分「审核后」明细。"""
    approvals = fetch_approvals(base_url, api_key, agent_id, view)
    pre_review = (view.get("result") or {}).get("pre_review_mcp_calls") or []
    if pre_review:
        print(f"{indent}审核前工具调用（命中人工审核门禁轮）:")
        for call in pre_review:
            _print_call_line(call, indent + "  ")
            approval = approvals.get(_approval_id_of_call(call))
            if approval:
                _print_approval_line(approval, indent + "    ")
    else:
        for approval in approvals.values():
            label = APPROVAL_STATUS_LABELS.get(approval["status"], approval["status"])
            print(f"{indent}审核前 tool={approval['tool_name']}  审核={label}  "
                  f"requested_at={approval['requested_at']}  "
                  f"decided_at={approval.get('decided_at') or '-'}  "
                  f"executed_at={approval.get('executed_at') or '-'}")
            if approval.get("comment"):
                print(f"{indent}  审核意见: {approval['comment']}")
    return approvals


def print_execution_result(view, base_url, api_key, agent_id):
    status = view["status"]
    if status == "success":
        result = view.get("result") or {}
        print(f"\n✅ 执行成功: reply: {result.get('reply')}")
        print(f"   session_id={result.get('session_id')}  "
              f"tokens={result.get('total_tokens')}  latency={result.get('latency_ms')}ms")
        approvals = print_approval_summary(view, base_url, api_key, agent_id)
        calls = result.get("mcp_calls") or []
        if calls:
            if approvals:
                print("   审核后工具调用:")
                for call in calls:
                    _print_call_line(call, "     ")
            else:
                for call in calls:
                    _print_call_line(call, "   ")
    elif status == "cancelled":
        print(f"\n🛑 执行已取消: error={view.get('error')}")
        print("   （cancelled = 外部方主动放弃任务; 进行中的模型/MCP 调用已中断）")
    else:
        print(f"\n❌ 执行未成功: status={status}  error={view.get('error')}")
        print("   （stalled = 平台判定执行卡死并已取消；failed 见 error 原因）")
        print_approval_summary(view, base_url, api_key, agent_id)


def main():
    parser = argparse.ArgumentParser(description="外部调用 Agent 示例（API Key 认证，异步执行任务）")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--agent-id", required=True, help="Agent ID")
    parser.add_argument("--message", required=True, help="用户提示词")
    parser.add_argument("--session-id", default=None, help="复用会话（多轮上下文）；不传则自动新建")
    parser.add_argument("--api-key", default=os.environ.get("AGENT_API_KEY"),
                        help="Agent API Key（默认读取环境变量 AGENT_API_KEY）")
    parser.add_argument("--cancel", default=None, metavar="EXECUTION_ID",
                        help="不发起新调用, 直接取消指定执行任务（外部方主动放弃）")
    args = parser.parse_args()

    if not args.api_key:
        sys.exit("请设置环境变量 AGENT_API_KEY 或传入 --api-key")

    if args.cancel:
        data = cancel_execution(args.base_url, args.api_key, args.agent_id, args.cancel)
        if data.get("cancelled"):
            print(f"已取消: execution_id={data['execution_id']}  status={data['status']}")
        else:
            print(f"任务已处于终态, 无需取消: execution_id={data['execution_id']}  status={data['status']}")
        return

    status_code, data = invoke_agent(
        args.base_url, args.api_key, args.agent_id, args.message, args.session_id
    )

    if status_code == 200:
        # 降级同步路径（无可用模型）：应答即最终结果
        print(f"调用完成: http={status_code}（降级同步路径）  agent_id={data.get('agent_id')}")
        print(f"  reply: {data.get('reply')}  model_ok={data.get('model_ok')}")
        return

    # 异步执行任务：202 + execution_id
    execution_id = data.get("execution_id")
    print(f"已受理: http={status_code}  execution_id={execution_id}")
    view = poll_execution(args.base_url, args.api_key, args.agent_id, execution_id)
    print_execution_result(view, args.base_url, args.api_key, args.agent_id)


if __name__ == "__main__":
    main()
