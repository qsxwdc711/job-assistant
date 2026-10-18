# S4 Scheduling and Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现北京时间每日岗位更新、独立账号订阅、站内摘要、加密飞书配置和可恢复的发送发件箱，并完成模拟验收。

**Architecture:** 按已批准的 `2026-10-14-job-assistant-design.md` 第 8–9 节与 S4 清单实施。现有私有简历 worker 保留；新增公开采集任务表、来源域名租约和独立摘要循环，避免官网慢请求阻塞 09:07 截止。通知根据不可变岗位版本形成去重事件，发送时重查账号、配置、投递状态和推荐版本。

**Tech Stack:** Go 1.26.8、pgx/PostgreSQL 18.6、AES-256-GCM、net/http、Vue 3/TypeScript、Docker Compose、Playwright。

---

用户已选择“先完成模拟验收，暂不发到真实群”。本轮运行配置保持 `NOTIFICATIONS_SEND_ENABLED=false`；真实 Webhook 调用只在隔离的假 HTTP 传输中验证。生产发送代码保留严格飞书地址校验、签名和响应判断，不自动替用户绑定群。

## Task 1: 配置、存储与队列基线

Files: `backend/migrations/0006_scheduling_notifications.sql`, `backend/internal/task/schedule.go`, `backend/internal/task/schedule_test.go`, `backend/internal/config/notifications.go`.

- [x] 隔离 worktree `.worktrees/s4-notifications` 从 `e97801c` 建立；基线 `go test ./internal/task ./internal/workflow ./internal/matching ./internal/config` 通过。
- [x] 新迁移增加 `scheduled_collections`（唯一 schedule_key、source/domain、状态、attempts≤3、available_at、lease/token）、`subscriptions`（每用户一份组合筛选与 filtered/personalized 模式）、`notification_channels`（每用户一份加密配置与 revision）、`notification_batches`、`notification_events`、`notification_deliveries`。不修改旧迁移。
- [x] `task.CollectionStore.Plan(ctx, now)` 08:00 后幂等创建当日启用来源任务；同日期重复规划只产生一份任务。重启补当日，不无限补历史日期。
- [x] `Claim` 在事务内用行锁与域名 advisory lock 检查有效运行租约，同域名自动任务最多一个；最多三个采集循环。20 分钟租约覆盖 17 分钟有界采集；宕机恢复有限重试，旧 token 不能完成新租约。
- [x] `Finish` 保存稳定错误码和结果计数，只有 network_error/timeout/source_busy 有界退避；schema_changed/restricted 不密集重试。暂停来源的待执行任务取消。
- [x] 时钟单测：07:59 不调度，08:00 调度，UTC 前一天日期正确映射北京时间；集成测并发领取同域名、不同域名、过期租约和最大尝试数。

核心调度边界：

```go
var Shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)
// DayTimes(now) returns local date, 08:00 collection time and 09:07 cutoff.
// Plan/GenerateDue only act after the corresponding timestamp.
```

## Task 2: 飞书加密、发送和内容预算

Files: `backend/internal/notification/crypto.go`, `feishu.go`, `digest.go`, 对应 `_test.go`.

- [x] 先写测试：加密往返；密文不含 URL/密钥；不同用户 AAD 解密失败；篡改失败；非法地址（HTTP、userinfo、端口、片段、非官方 host、非 hook 路径）拒绝。
- [x] AES-256-GCM 使用独立 `NOTIFICATION_KEY`（base64 32 字节）和随机 nonce，AAD 绑定用户。GET 只返回 configured/enabled/revision/has_secret，不返回密文、完整 URL 或签名密钥。
- [x] 签名按[飞书官方自定义机器人指南](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)：HMAC-SHA256 的 key 为 `timestamp + "\n" + secret`，内容为空，再 Base64；禁止重定向。
- [x] 消息只含公司、岗位、匹配方向地点、截止、待确认标志、官方链接和站内详情；不含账号名、简历、匹配差距或投递备注。纯文本转义可能的 @ 标记。
- [x] 最多显示 10 个唯一广告，合并重复事件；按 1/3/7 天截止、重要变更、新机会排序。序列化含签名后的最终 JSON≤18 KiB，超限逐条裁减，余量链接到站内。
- [x] 模拟响应：HTTP 200/code=0 为 sent；HTTP 200/非零业务码及明确限流有界重试；缺少业务码、响应损坏、网络超时或崩溃后的未知发送为 delivery_unknown，禁止自动重发。最多三次，每次重用同一 delivery。

## Task 3: 订阅、快照、事件和发件箱

Files: `backend/internal/catalog/match.go`, `backend/internal/notification/model.go`, `store.go`, `generate.go`, `dispatch.go`.

- [x] 保存筛选查询前调用 `catalog.ParseFilter` 并限制允许参数；复用纯函数匹配不可变 `job_versions`，方向、专业、城市必须在同一 variant。多条件行为与现有看板一致，权限字段不能来自用户输入。
- [x] Store 的个人接口只作用于登录人；管理接口必须重新检查管理员和显式 owner。普通用户可 filtered 订阅，个性化模式必须有效 VIP/admin。写入使用 revision 防覆盖。
- [x] 09:07 采用 `job_versions.created_at<=cutoff` 的最新版本；之后到达的版本留到下一摘要。首次启用订阅纳入当前已知匹配机会；之后按上次 cutoff 生成新增与实质条件变更。
- [x] 截止仅使用明确 `deadline_at`；首次已在多个窗口内只取最近的 1/3/7 天窗口。事件唯一键含用户、岗位、版本或截止窗口；同日一个广告只显示一次。
- [x] 排除已 submitted/written_test/interview/offer/rejected/withdrawn 的个人记录，draft 保留。隐藏、关闭或明确过期岗位不发送。
- [x] 个性化只消费当前本人简历、当前岗位版本/variant hash、当前规则和模型版本的有效推荐；禁止读取推荐正文加入消息。推荐缺失/失效不回退成全量筛选摘要。
- [x] 每用户/日/接收配置 revision 的批次唯一，事件只分配一次；无相关事件记录 empty，不发群消息。未配置接收群仍保留 station_only 站内摘要。记录来源成功、失败和未完成情况，不声称全量更新。
- [x] 发送前在同一事务内重查并锁定用户、订阅、接收配置与岗位；失效订阅/配置/权限、版本变化或已投递的项目移除或取消。先写入 sending，宕机残留 sending 只转 delivery_unknown。
- [x] delivery_unknown 提供管理员显式确认已收到或确认未收到后重试的入口，写入审计，不提供隐式自动重发。

## Task 4: Worker、API 和页面

Files: `backend/internal/automation/worker.go`, `backend/cmd/worker/main.go`, `backend/cmd/api/main.go`, `backend/internal/httpapi/notifications.go`, `handler.go`, `web/src/features/notifications/NotificationsPage.vue`, `web/src/router.ts`, `web/src/App.vue`, `web/src/shared/display.ts`, `deploy/compose*.yaml`, `.env.example`, `scripts/new-local-env.ps1`.

- [x] API 注册 `/me/subscriptions`、`/me/notification-channel`、`/me/notification-channel/test`、`/me/notifications`；对应 `/admin` 查询与 unknown 确认。管理员提供手动排队采集/生成摘要入口；HTTP 不直接采集或等待飞书。
- [x] `automation.Worker` 的采集、规划/生成摘要和投递循环相互独立，均继承 shutdown context。主 worker 与原简历循环同时运行；任何循环退出均通知主函数取消、等待其它循环结束。
- [x] 页面提供保存筛选、普通/个性化模式、启停、机器人密码输入、替换/清除配置、手动连接测试和站内发送状态；API 不将完整密钥回填表单。显示模拟发送状态与未完成来源。
- [x] 电脑/手机布局均加入“订阅通知”入口，避免手机底栏新增项目挤压。模型权限收回后个性化入口和后续任务即时受限。
- [x] 新环境生成独立加密密钥；已有 `.env` 只追加缺失配置，不改旧密码、CSRF 或模型配置。Compose 传入密钥和真实发送开关；本轮开关保持 false。

## Task 5: 隔离验收、审查与本机部署

Files: `backend/tests/integration/notification_isolation_test.go`, `task_recovery_test.go`, `web/tests/e2e/s4.spec.ts`, `backend/tests/browser/main.go`, `scripts/verify-s4.ps1`, `deploy/compose.verify-s4.yaml`, `docs/verification/2026-10-17-s4-notifications.md`, `docs/operations/s4-notifications.md`, `README.md`.

- [x] 两个用户两个群的受控 HTTP 服务验证路由、去重、暂停配置、停用、VIP 到期、资料更换、岗位变更、投递后的取消和日志无密钥；检查数据库只保存密文。
- [x] 模拟时钟验证 09:07 截止，故意让来源晚于 cutoff 才完成，确认下一日才入摘要；失败来源列入站内状态。
- [x] 测试 sending 后崩溃恢复不重发；显式 429/业务失败有退避并在第三次终止；最终 JSON 字节预算和中文裁减。
- [x] 使用随机项目、专用 PostgreSQL 和独立合成账号执行 `go test -race -v ./... -count=1 -timeout=180s`、`go vet ./...`，确认没有数据库测试跳过。
- [x] 前端类型检查、生产构建和 Playwright 电脑/手机操作，保留 S2/S3 回归；API/worker 正常关闭及重启恢复。
- [x] 按 go-feature-change-review 和 go-code-review 审查输入→选择→执行→结果；修复后只重跑受影响检查。
- [ ] 合并本轮改动，保留已有未提交研究文档；构建/使用已验收镜像、执行迁移、启动本机服务、检查就绪与队列状态。本机只进行只读浏览，不绑定真实机器人、不发送真实群消息。
- [ ] 记录实际命令、结果和边界，清理本轮测试资源和干净 worktree；真实飞书与连续多日运行明确留待后续验收。
