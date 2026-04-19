---
name: tmux-ghostty
description: Use tmux-ghostty to manage local Ghostty/tmux workspaces, pane control handoff, approvals, and JumpServer-based remote attachment through the local CLI and broker.
---

# Tmux Ghostty

Use this runbook when an agent should drive the local `tmux-ghostty` CLI instead of manipulating Ghostty or tmux state ad hoc.

## User Help

If the user says `help`, return a short Chinese usage note.

The help response should cover these points:

- 这个 skill 用来通过 `tmux-ghostty` 管理本地 Ghostty + tmux 工作区、pane 控制权、命令发送和审批，以及 JumpServer 挂接。
- Ghostty 只是可见终端界面，真正共享的文本状态在 tmux 里。
- 多数操作会自动拉起本地 broker。
- 受管 workspace 只通过 `tmux-ghostty workspace create` 创建，而且一定会新开 Ghostty window。
- `打开jumpserver` 的标准动作是 `tmux-ghostty host connect <pane-id>`；它只连到 JumpServer，并在可以继续手工输入时停下。
- 如果不知道 pane 或 action ID，先执行 `tmux-ghostty pane list` 或 `tmux-ghostty actions`。

Example requests for the help response:

- `打开jumpserver`
- `新建一个 workspace，然后打开jumpserver`
- `列出当前 pane`
- `把 pane-1 挂到 2801`
- `查看待审批动作`

Keep that response concise and task-oriented.

## Default Flow

1. Use `tmux-ghostty help` when CLI syntax is unclear.
2. Use `tmux-ghostty workspace create` to create a managed workspace and its first pane in a new Ghostty window.
3. Use `tmux-ghostty pane list`, `pane focus`, and `pane snapshot` to inspect panes.
4. Use `tmux-ghostty host connect <pane-id>` to open JumpServer and stop at `menu`, `target_search`, or `auth_prompt`.
5. Use `tmux-ghostty host attach <pane-id> <query>` when the goal is to enter a concrete remote host.
6. Use the safe control flow `claim` -> `command preview` -> `command send` -> `actions` -> `approve` or `deny`.

## Keyword Rules

- If the user says `打开jumpserver`:
  - If they already gave a pane ID, use that pane.
  - Otherwise create a workspace first and use the returned first pane.
  - Run `tmux-ghostty host connect <pane-id>`.
  - Stop as soon as the returned stage is `menu`, `target_search`, or `auth_prompt`.
  - Report the pane ID, the returned stage, and whether this used a newly created workspace.

- If the user asks for “当前窗口”, “这个窗口”, or pane splitting:
  - State clearly that managed workspaces only support new-window creation.
  - Offer `tmux-ghostty workspace create` as the available path.

## Operational Rules

- Do not invent pane IDs, workspace IDs, or action IDs. Query them first.
- Parse JSON output instead of scraping prose.
- If a pane already has a pending approval action, resolve it before sending more commands into that pane.
- When `host attach` fails but the pane is still in `menu`, `target_search`, or `selection`, inspect `tmux-ghostty pane snapshot <pane-id>` and continue the provider menu flow instead of giving up.
- Use `pane snapshot` when you need to verify the current stage or prompt.
- If the CLI lacks a capability, say so explicitly instead of improvising tmux or AppleScript fallbacks.

## Example Requests

- `打开jumpserver`
- `新建一个 workspace，然后打开jumpserver`
- `列出当前 panes，并告诉我每个 pane 连的是哪台机器`
- `把 pane-1 的控制权切给 agent，然后预览一条 kubectl 命令`
- `把 pane-2 挂到 2801`
- `现在 pane 里还是 Opt>，继续把它推进到远端 shell`
