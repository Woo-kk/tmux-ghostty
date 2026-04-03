# tmux-ghostty

[English](./README.md) | [中文](./README.zh-CN.md)

`tmux-ghostty` 是一个本地 macOS 工具：Ghostty 只负责用户可见的终端 UI，`tmux` 负责真实的文本/数据传递，并作为用户与 agent 共同读取和控制的共享文本事实源。

## v1 包含内容

- `tmux-ghostty` CLI，以及自动拉起的 `tmux-ghostty-broker`
- 基于 Unix domain socket 的 JSON-RPC 2.0
- workspace / pane / action 状态持久化
- 通过 Ghostty AppleScript 编排 window、tab、split、focus、文本输入和按键事件
- 一个逻辑 pane 对应一个本地 `tmux` session
- 从本地 `tmux` 抓取 pane 快照
- 显式控制权切换：`claim` / `release` / `interrupt` / `observe`
- 命令风险分类和审批流
- 复用本地 `tmux-jumpserver` runner 的 JumpServer attach 适配层
- broker 空闲自动退出逻辑

## 仓库结构

```text
cmd/
  tmux-ghostty/
  tmux-ghostty-broker/
internal/
  app/
  broker/
  control/
  execx/
  ghostty/
  jump/
  logx/
  model/
  observe/
  risk/
  rpc/
  store/
  tmux/
```

## 构建与测试

使用下面的命令运行测试并构建二进制。

```bash
go test ./...
go build ./cmd/tmux-ghostty
go build ./cmd/tmux-ghostty-broker
```

## 命令行

```text
tmux-ghostty up
tmux-ghostty down --force
tmux-ghostty status

tmux-ghostty workspace create
tmux-ghostty workspace reconcile
tmux-ghostty workspace close <workspace-id>

tmux-ghostty pane list
tmux-ghostty pane focus <pane-id>
tmux-ghostty pane snapshot <pane-id>

tmux-ghostty host attach <pane-id> <query>

tmux-ghostty claim <pane-id> --actor agent
tmux-ghostty claim <pane-id> --actor user
tmux-ghostty release <pane-id>
tmux-ghostty interrupt <pane-id>
tmux-ghostty observe <pane-id>

tmux-ghostty actions
tmux-ghostty approve <action-id>
tmux-ghostty deny <action-id>
```

另外还提供 `command preview` 和 `command send` 子命令，它们走的是同一套 broker RPC，主要给 agent 侧流程使用。

## 运行时路径

默认情况下，broker 使用：

```text
~/Library/Application Support/tmux-ghostty/
```

目录内容如下：

```text
broker.sock
broker.pid
state.json
actions.json
broker.log
```

常用环境变量：

- `TMUX_GHOSTTY_HOME`
- `TMUX_GHOSTTY_BROKER_BIN`
- `TMUX_GHOSTTY_IDLE_TIMEOUT`
- `TMUX_GHOSTTY_JUMP_PROFILE`
- `TMUX_GHOSTTY_JUMP_RUNNER`
- `TMUX_GHOSTTY_REMOTE_TMUX_SESSION`

## 说明

- Ghostty 只被当作可见前端使用。真正的文本/数据传递由 `tmux` 负责，所以快照文本来自本地 `tmux`，而不是 Ghostty 的内容 API。
- JumpServer 适配层默认假设本机已有 `/Users/guyuanshun/.codex/skills/tmux-jumpserver/scripts/run_jump_profile.sh`；如果需要，可通过 `TMUX_GHOSTTY_JUMP_RUNNER` 覆盖。
- 当前测试套件使用真实本地 `tmux` 加 fake Ghostty 编排，因此自动化测试时不会真的弹出 GUI 窗口。
