# PR: 进度预算（Progress Budget）可配置化

> 目标分支：`main-v2` · 提交：`340459042 add budget config`
> （基于 `f86e488c0`；已合并上游 `8b426dc87`，v1.38.9 基线）

> **上游合并后的落点（v1.38.9）**：上游在 v1.38.6→v1.38.9 之间整体退役了这项检查点所依赖的机制
> ——storm breaker、todo progress guard、零证据阶梯，以及 `internal/agent` 下约 70 个文件，
> 并改用 `repeat_reminder.go` 的非阻塞重复调用提醒。因此本功能不再是对既有阈值打补丁，而是由 fork
> 自带检查点内核：`internal/agent/progress_budget.go`（常量、归一化、两条消息、`trackTodoProgress`），
> 并由 `run_loop.go` 的 `handleToolRound` 在工具批次之后驱动，位置与退役前的调用点一致。
> 注入的两条消息文本保持逐字节不变——`preview.go` 仍把 `Host progress check:` /
> `Host progress redirect:` 识别为旧版本可能已持久化的主机消息。

## Summary

Agent 内置了固定的进度检查点：当活跃 todo 连续 **8 轮**工具调用没有新的主机可见进展（新完成、新读取、新命令、新写入）时，主机强制注入一条“重新评估”提示。该阈值写死在 `internal/agent` 中，对长任务（大范围重构、批量迁移）过低，会频繁打断正常工作流，且用户无法调整或关闭。

本 PR 将该检查点变为**用户可拥有的设置**：

- **内核**：`agent.Options` 新增 `ProgressBudgetRounds`，由 `NormalizeProgressBudgetRounds` 统一归一化（`0` = 内置默认 8；负数 = 关闭；越界值钳制到 **3–64**）。Goal 模式的二级重规划检查点改为 `nudgeRounds × 2` 派生，默认值下与旧行为（8/16）完全一致。
- **配置**：`[agent]` 新增两个 TOML 键——`progress_budget`（`*bool`，缺省视为开启，仅显式 `false` 关闭）与 `progress_budget_rounds`（`0` = 内置默认）；提供 `SetProgressBudgetEnabled` / `SetProgressBudgetRounds` 编辑器方法（关闭时保留轮次数，重新开启即恢复用户阈值而非回退默认值），TOML 渲染/差异输出同步支持。
- **启动接线**：`boot` 构建 executor 时经 `progressBudgetRoundsFromConfig` 注入；禁用时传入 `agent.ProgressBudgetRoundsOff` 哨兵，保证不会静默回退到内置默认。
- **桌面端**：`AgentView` 暴露 `progressBudgetEnabled` / `progressBudgetRounds`（始终返回生效值，便于数字输入框直接渲染）；新增 `App.SetProgressBudgetEnabled` / `App.SetProgressBudgetRounds` 主机 RPC 方法（Electron 迁移后由 `desktopContract.generated.*` + `host_command_owners.generated.json` 承载绑定契约，已同步重新生成），复用 `applyConfigChange` 自动热重建会话。
- **前端**：设置 → 模型 → 使用 → **Agent 运行**下方新增“进度预算”设置区：
  - 复选框开关：待办停滞时是否要求助手重新评估（关闭后零进展阶梯与循环守卫仍兜底）；
  - 触发轮次数字输入：3–64，Enter 应用 / Esc 还原，含范围校验与错误提示；
  - 旧后端缺省字段时默认开启，不会因字段缺失而禁用检查点。
- **文档**：`docs/GUIDE.md`、`docs/GUIDE.zh-CN.md`、`reasonix.example.toml` 补充两个新键的说明。

### 行为对照

| 场景 | 改动前 | 改动后 |
|---|---|---|
| 默认配置 | 8 轮后催促重新评估 | 相同（`0`/缺省 = 内置默认） |
| 长任务频繁被打断 | 无法调整 | 调高 `progress_budget_rounds`（最高 64） |
| 不希望被打扰 | 无法关闭 | `progress_budget = false` 或 UI 关闭开关 |
| Goal 二级重规划 | 固定 16 轮 | `2 × nudgeRounds`，默认下仍为 16 |

### 兼容性

- 旧配置文件不含新键：行为与之前完全一致（缺省 = 开启 + 默认 8 轮）。
- 旧前端 bundle 调新后端：`AgentView` 新字段仅追加，不破坏现有 JSON 契约。
- 新前端调旧后端：前端将缺失字段按“开启 + 默认 8”渲染，不误显示为关闭。

## Changed files

**内核（internal/）**

| 文件 | 说明 |
|---|---|
| `internal/agent/progress_budget.go` | 检查点内核：`NormalizeProgressBudgetRounds`、`ProgressBudgetRoundsOff`、`DefaultProgressBudgetRounds`、`progressRedirectRounds`、两条主机消息、`trackTodoProgress`（重建自上游已删除的 `storm_breaker.go` + `goal_run_boundary.go` 版本） |
| `internal/agent/agent.go` | `Options.ProgressBudgetRounds`；`New` 归一化后写入 agentConfig |
| `internal/agent/agent_config.go` | `progressBudgetRounds` 字段 |
| `internal/agent/run_loop.go` | `beginRunTurn` 播种检查点状态；`handleToolRound` 在工具批次后调用 `trackTodoProgress`（经 `appendCommittedMessages` 持久化） |
| `internal/agent/turnruntime.go` | 每回合停滞状态：`todoProgress` / `trackingTodoProgress` / `todoStallRounds` / `seenTodoProgress` |
| `internal/agent/todo_state.go` | `canonicalTodoStatus`、`canonicalTodoProgress`（标题改写与 pending 列表变动不算进展） |
| `internal/agent/continuation_policy.go` | `hostContinuationEnabled`：仅显式续跑流程（Goal 等）注入催促，普通对话不注入 |
| `internal/agent/todo_progress_guard_test.go` | 覆盖配置阈值生效、关闭后静默、普通对话静默、Goal 重定向、唯一主机产出续租、归一化边界 |
| `internal/config/config.go` | `ProgressBudget *bool`、`ProgressBudgetRounds` 字段 + `ProgressBudgetEnabled()` / `ProgressBudgetRoundsValue()` |
| `internal/config/edit.go` | `SetProgressBudgetEnabled` / `SetProgressBudgetRounds`（拒绝负数） |
| `internal/config/render.go` | 全量渲染与 diff 渲染支持两个新键 |
| `internal/boot/task_budget.go` | `progressBudgetRoundsFromConfig` 映射（禁用 → off 哨兵） |
| `internal/boot/boot.go` | executor `agent.Options` 注入 |

**桌面端（desktop/）**

| 文件 | 说明 |
|---|---|
| `desktop/settings_app.go` | `AgentView` 新字段；`desktopProgressBudgetRounds` 帮助函数；`SetProgressBudgetEnabled` / `SetProgressBudgetRounds` |
| `desktop/reasoning_display_app.go` | 默认视图补齐新字段 |
| `desktop/frontend/src/components/SettingsPanel.tsx` | “进度预算”设置区（开关 + 轮次输入 + 校验） |
| `desktop/frontend/src/lib/types.ts` | `AgentView.progressBudgetEnabled?` / `progressBudgetRounds?` |
| `desktop/frontend/src/lib/bridge.ts` | 绑定接口、mock 实现、事件路由分组 |
| `desktop/frontend/src/locales/{en,zh,zh-TW}.ts` | 每语言 9 条新文案 |
| `desktop/frontend/scripts/check-bundle-budget.mjs` | 在上游基线上显式上调有界预算：initial gzip 469.3→469.7、initial raw 2440.7→2442.7、zh 63.3→63.7、zh-TW 64.0→64.4（KiB） |
| `desktop/frontend/src/generated/desktopContract.generated.{ts,json}` | 重新生成：新增 `SetProgressBudgetEnabled` / `SetProgressBudgetRounds` 主机契约条目 |
| `desktop/host_command_owners.generated.json` | 重新生成：新增两个方法的 ownership 元数据（缺失会导致注册表拒绝调用） |

**文档**

| 文件 | 说明 |
|---|---|
| `docs/GUIDE.md` / `docs/GUIDE.zh-CN.md` | `[agent]` 示例补充两个新键 |
| `reasonix.example.toml` | 示例配置补充注释行 |

## Issues

无关联 issue（功能需求来自桌面端使用反馈：内置阈值过低且不可配置）。

## Verification

合并上游 v1.38.9（`8b426dc87`）后重新实测：

**Go**

```powershell
go build ./...                                  # 主模块 OK
cd desktop; go build ./...                      # desktop 模块 OK
go test ./internal/agent/                       # ok
go test ./internal/config/... ./internal/boot/...   # ok
go test ./internal/control/... ./internal/productdocs/...  # ok（含 GUIDE 文档一致性）
go test ./internal/agent/ -run "TestProgressBudget|TestNormalizeProgressBudget" -v
# TestProgressBudgetNudgeUsesConfiguredRounds (below/at threshold)、
# TestProgressBudgetOffStaysSilent、TestProgressBudgetOrdinaryChatStaysSilent、
# TestProgressBudgetGoalRedirectsAtDoubleRounds、TestProgressBudgetRenewsOnUniqueHostWork、
# TestNormalizeProgressBudgetRounds — 全部 PASS
cd desktop; go test . -timeout 900s              # 仅 1 例失败：TestRunShellForTabRoutesToRequestedTab
                                                # （本机 PATH 无 bash 的 Windows 环境问题，
                                                #  在纯净 8b426dc87 上同样失败）
```

新增方法必须进入主机契约注册表：`cd desktop && go run . -emit-contract frontend/src/generated`
已重新生成 `desktopContract.generated.{ts,json}` 与 `host_command_owners.generated.json`；
`bridge.ts` 的 `_CheckGenToApp` 编译期断言在 `tsc` 通过即证明前端绑定与 Go 方法名一致。

**前端**

```powershell
cd desktop/frontend
pnpm install                            # 上游新增依赖 anser
npx tsc --noEmit                        # OK（含 _CheckGenToApp 绑定漂移断言）
npx eslint src/components/SettingsPanel.tsx src/lib/bridge.ts src/lib/types.ts \
          src/locales/{en,zh,zh-TW}.ts  # 0 问题
node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/settings-refresh-snapshot.test.tsx
# 99/99 PASS
npx vite build && node scripts/check-bundle-budget.mjs   # 全部 PASS，见下
```

**包体预算（实测）**：初始 gzip `449.9 / 469.7 KiB`、zh `58.8 / 63.7`、zh-TW `59.7 / 64.4`、
initial raw `2423.0 / 2442.7 KiB` —— 合并上游后重测的实际值，均在上限之内。

## Documentation impact

Documentation-impact: updated — `docs/GUIDE.md`、`docs/GUIDE.zh-CN.md` 与 `reasonix.example.toml` 已补充 `agent.progress_budget` / `agent.progress_budget_rounds` 两个新键的语义（缺省开启、0 = 内置默认、关闭后保留轮次数）。

## Cache impact

Cache-impact: none — 不触碰系统提示词、memory 前缀、output style、工具 schema、provider 请求序列化或压缩逻辑。检查点仅改变一条主机生成的用户消息（“Host progress check”/“Host progress redirect”）在会话中的**注入时机**；两条消息的文本与上游旧版本逐字节一致，默认阈值下的会话内容因此保持不变。

Cache-guard: `go test ./internal/agent/ -run "TestProgressBudget|TestNormalizeProgressBudget"` 覆盖配置阈值下的催促/重定向注入时机与内容；`TestProgressBudgetOrdinaryChatStaysSilent` 与 `TestProgressBudgetOffStaysSilent` 保证不会在普通对话或关闭状态下注入。系统提示词零改动，无需 System-prompt-review。

System-prompt-review: N/A
