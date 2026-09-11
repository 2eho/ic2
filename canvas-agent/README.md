# ic-canvas-agent · 本机 Agent 桥接器

把本机的 Codex app-server / Claude Code CLI 接到 IC 服务端。

## 为什么需要它

云端模型适合「通用对话」，但很多创作任务需要**本机能力**：读本地文件、
跑本地脚本、用已有的 CLI 工具链。IC 服务端不该、也不能具备这些能力
（那等于把服务器变成一台可被远程操控的机器）。

桥接器的定位是**单向、无特权**的转接器：

```
IC 服务端  ──(SSE 事件 + 工具调用请求)──▶  浏览器  ──(HTTP, 127.0.0.1)──▶  桥接器  ──▶  Codex / Claude
```

关键约束（对齐 docs/design/07-agent-protocol.md）：

1. **只监听 127.0.0.1**，且启动时生成一次性 token（不接受固定 token，避免被扫到即用）。
2. **token 不进 URL**（URL 会进日志与 referer），走 `Authorization` 头。
3. **Origin 白名单**：只接受 IC 站点的 Origin，拒绝其他网页发起的请求
   （否则任意网页都能通过浏览器访问你的本机端口）。
4. **配置与日志不落明文密钥**：配置文件权限 0600，日志一律脱敏。
5. **服务端不信任桥接器**：桥接器上报的所有操作仍然走标准 op 路径，
   由服务端校验、限额、审计。桥接器没有绕过校验的能力。

## 用法

```bash
# 启动（默认 17371 端口，可用 IC_AGENT_PORT 覆盖）
node src/cli.js

# 输出示例：
# ic-canvas-agent 已启动 http://127.0.0.1:17371
# 一次性令牌（写入 ~/.ic/agent.json，权限 0600）：<token>
# 在 IC 的「画布助手 → 本机 Agent」中填入上面的地址与令牌
```

## 事件归一化

两个后端的事件形状差异很大，桥接器把它们归一化为 IC 的 `Item`：

| 归一化 Item | Codex app-server | Claude Code CLI |
| --- | --- | --- |
| `agent_message` | `item.completed` (type=message) | `assistant` text block |
| `reasoning` | `item.started/completed` (type=reasoning) | `thinking` block |
| `tool_call` | `item.started` (type=command_execution) | `tool_use` block |
| `tool_result` | `item.completed` (type=command_execution) | `tool_result` block |
| `file_change` | `item.completed` (type=file_change) | `Edit`/`Write` tool_use |
| `error` | `error` 事件 | `error` 事件 |

归一化规则写在 `src/normalize.js`，并被单测穷举——**上游字段改名会立刻红**，
而不是静默丢掉一个事件（用户会看到「Agent 卡住了」却查不出原因）。
