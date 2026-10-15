# 求职助手 V1 Implementation Plan

> For agentic workers: 按依赖顺序执行本文任务，不自动启用并行代理。修改 Go 功能后应用 go-feature-change-review，提交 Go 代码前应用 go-code-review。

**Goal:** 交付可信官方岗位看板、按账号隔离的投递管理、VIP 简历推荐和飞书群摘要，支持后续专业与行业扩展。

**Architecture:** Go 单体模块以 API 和 worker 两种进程运行，Vue 网页使用同源 API，PostgreSQL 提供业务数据、会话、持久化任务和通知发件箱。岗位采集器按官方来源独立实现，原简历私有保存。

**Tech Stack:** Go/net/http/pgx、Vue 3/TypeScript/Vite/Vue Router/Pinia、PostgreSQL 18、chromedp、Docker Compose；云端模型与飞书通过独立适配器访问。

---

日期：2026-10-14。配套 [设计文档](../specs/2026-10-14-job-assistant-design.md) 定义角色和业务规则，[来源验证清单](../../research/2026-10-14-source-validation.md) 记录 S0 验证。S0–S2 已完成；S1 代码、运行和测试结果见 [验收记录](../../verification/2026-10-14-s1-identity.md)。S3–S5 仍为后续开发要求。

## 1. 开工基线和阶段顺序

当前本机已检查 Go 1.23.5、Node 20.18.0、npm 10.8.2、pnpm 11.25.0、Docker CLI / Engine 27.3.1、Compose 2.30.3、Git 2.47.1；psql 不在 PATH。S1 实际使用独立 Go 1.26.8 和容器 PostgreSQL 18.6，数据库运行已验证。S2 已实现并验收 Vue 网页与手机浏览器布局。

后端固定 Go 1.26.8、PostgreSQL 18.6；前端使用独立 Node 22 系列且不低于 22.18.0。初次镜像下载受第三方镜像站完整性问题影响；已固定官方 Go 1.26.8 镜像摘要，补验完整 Docker 构建和运行。实际依赖与镜像版本写入锁文件，禁止部署时隐式使用 latest。

| 阶段 | 范围 | 阶段结束的可验证产物 |
|---|---|---|
| S0 | 三类官方来源可行性 | 授权链、真实样本、字段覆盖、重复采集与状态证据 |
| S1 | 工程基线与权限 | 可运行 API/数据库，管理员初始化，三级权限与越权测试 |
| S2 | 岗位和投递闭环 | 来源入库、组合筛选、电脑/手机看板、个人投递管理 |
| S3 | VIP 简历推荐 | 文件处理、解析确认、规则判断、语义解释、权限撤销 |
| S4 | 飞书与可靠调度 | 个人绑定、站内记录、每日摘要、失败恢复和去重 |
| S5 | 扩源、部署、运行验收 | 20 家实际自动来源、云端部署、备份恢复、7 天观察 |

S0 未证明至少三类来源的真实详情获取方式时，先处理接入问题；不以模拟岗位充当正式岗位推进验收。允许在隔离测试中使用有出处的历史样本，但不将其发布为开放职位。

## 2. 计划中的工程结构

以下路径是实现边界；S1 身份、配置、HTTP、数据库和迁移代码已存在，其他业务模块按阶段创建。

| 路径 | 职责 |
|---|---|
| backend/cmd/api/main.go | HTTP 服务入口与关闭流程 |
| backend/cmd/worker/main.go | 每日任务、任务租约、外部服务处理入口 |
| backend/cmd/admin/main.go | 首个管理员创建和部署管理命令 |
| backend/internal/identity/ | 用户、密码、会话、能力与资源授权 |
| backend/internal/catalog/ | 公司、岗位、批次、筛选字典、公共查询 |
| backend/internal/ingest/ | 来源适配器、采集记录、标准化、重复和变更判断 |
| backend/internal/profile/ | 手动条件、私有文件、解析版本与确认 |
| backend/internal/matching/ | 条件规则、语义推荐、证据和缓存版本 |
| backend/internal/application/ | 收藏、个人投递、历史、事项、版本冲突 |
| backend/internal/notification/ | 订阅、飞书配置、事件与发件箱 |
| backend/internal/task/ | 幂等调度、领取、租约恢复、重试、取消 |
| backend/internal/audit/ | 管理权限和敏感数据访问记录 |
| backend/migrations/ | 按业务阶段拆分的数据库迁移 |
| backend/tests/integration/ | 使用真实 PostgreSQL 的隔离、事务和恢复测试 |
| web/src/features/ | account、jobs、applications、resume、recommendations、notifications、admin |
| web/src/shared/api/ | 带会话/CSRF 的 API 客户端及统一错误处理 |
| web/tests/e2e/ | 电脑和手机的用户操作场景 |
| deploy/compose.yaml | 服务、网络和持久化目录 |
| deploy/Caddyfile | HTTPS 和同源路由 |
| docs/research/ | 来源可行性、抓取样本说明、实际接入台账 |

业务层依赖明确的存储和外部服务接口，不直接在 handler 中调用模型或解析网页。内部模块只在确有共享概念时引用公共类型，不建立包罗所有业务的 utils 包。

## 3. S0：来源验证

交付文件：docs/research/2026-10-14-source-validation.md，以及后续逐来源证据记录和不含账号信息的样本。

- [x] S0.1 核实腾讯、华为、招商银行官网到招聘入口的完整授权链。
- [x] S0.2 查看列表、详情、招聘批次、投递入口、笔试说明及截止依据；缺失字段明确 unknown，未使用搜索摘要确认在招。
- [x] S0.3 验证公开结构化数据和 HTML；三个来源均可匿名公开读取，无需浏览器渲染，CDP 未连接单列为环境记录。
- [x] S0.4 每类保留至少三条有出处样本；招商包含两技术实习和一非技术对照，未把对照发布为计算机岗位。
- [x] S0.5 每个来源完成两次独立获取，验证身份键、完整分页和成功空结果；华为越过末页的 null 响应保留为异常分类反例。
- [x] S0.6 三类来源公开读取技术可行性通过，范围和未验证字段写入报告；生产自动接入数量保持 0/20。
- [x] S0.7 记录三类方法的请求、耗时、响应哈希与维护边界；首批采集器采用官方前端实际使用的公开 API，保留配置发现能力。

验收：三类来源的岗位详情、条件、官方入口与身份键可重复取得；关闭判断有独立依据；采集失败与空职位明确区分。若某类受限，说明具体阻碍、保留证据，并在扩大应用开发前调整来源接入范围。

S0 结果：已具备开始 S1 的来源依据。招商验证了真实下线/过期；腾讯与华为缺少真实关闭对照，禁止编造通用关闭码。该缺口通过 unknown/核验异常处理，不冒充全量来源和长期稳定性已验收。

## 4. S1：工程与权限

计划文件：backend/go.mod、backend/cmd/api/main.go、backend/cmd/admin/main.go、backend/internal/identity/、backend/migrations/0001_identity.sql、backend/tests/integration/identity_test.go、deploy/compose.yaml。

- [x] S1.1 创建 Go 模块、配置读取、数据库连接、迁移命令、请求 ID 与健康检查；配置缺失时明确退出，不使用隐藏默认密钥。
- [x] S1.2 创建 users/sessions/audit_events；用户名唯一，密码哈希，会话有归属和固定有效期。
- [x] S1.3 实现管理员初始化命令；仅无管理员时创建首个管理员，防止公开请求提升角色。
- [x] S1.4 角色和资源归属行为测试、登录、退出、当前用户、管理员创建/停用账号、VIP 授予/撤销均完成。
- [x] S1.5 实现按动作的能力判断；有效 VIP 时间条件和管理员能力按设计文档处理，不单靠角色字符串或前端按钮。
- [x] S1.6 真实 PostgreSQL 两用户越权、管理权限、会话过期、账号停用、即时角色变更和审计测试通过。
- [x] S1.7 未来私有接口授权模式记录在 backend/README.md；新私有接口必须增加跨用户测试，未创建假接口。

实际测试文件：backend/internal/identity/policy_test.go、backend/internal/config/config_test.go、backend/tests/integration/identity_test.go。会话行为使用真实数据库集成测试，不另建重复的 session_test.go。

| 用例 | 预期 |
|---|---|
| standard 请求简历上传或推荐创建 | 403；没有文件保存和队列任务 |
| vip 到期时刻等于当前时刻 | 按 standard 判断 |
| admin 无 VIP 到期字段 | 允许全部业务能力 |
| 用户 A 请求用户 B 的私有记录 | 404；响应不泄露存在性 |
| 管理员撤销 VIP，旧会话再次请求 | 403 |
| 用户被停用，所有设备会话再次请求 | 拒绝 |
| standard 删除自己曾上传的简历 | 允许；其他简历动作继续拒绝 |

开发验证命令在 Go 模块创建后执行：

```powershell
go -C backend test ./internal/identity/... -count=1
go -C backend test ./tests/integration/... -run Identity -count=1
```

角色策略和管理员旧对象读取回归测试均有失败再修复的记录；数据库集成测试使用专用 `_test` 数据库，每个测试隔离 schema。完整应用测试和静态检查已通过，实际 CLI 初始化与 API 登录/退出已验证。容器构建与运行、Linux 下含真实 PostgreSQL 的 race、正常关闭和临时资源清理已补验通过，见 [补验记录](../../verification/2026-10-14-s1-supplement.md)。本机 Windows C 编译器 / DLL 错误作为历史环境问题保留。

## 5. S2：岗位、采集与投递管理

计划文件：backend/internal/catalog/、backend/internal/ingest/、backend/internal/application/、对应数据库迁移，以及 web/src/features/jobs/ 和 web/src/features/applications/。

- [x] S2.1 实现公司/招聘主体、来源、批次、岗位版本、岗位子方向、多地点、分类字典；同时保存标准化字段与原文依据，部门与城市按方向成组。
- [x] S2.2 将 S0 已验证方法实现为来源适配器；单来源失败隔离记录，不中止所有来源，不关闭旧职位。
- [x] S2.3 先测试同源去重、跨批次同名、截止变化、空列表、受限页面，再实现岗位归一和版本变更。
- [x] S2.4 实现 GET /api/v1/jobs 和岗位详情；分页、排序及组合筛选在后端完成，未知信息具有显式值。
- [x] S2.5 实现本人收藏、投递、状态历史、事项和备注；主记录具有 owner/job 唯一约束与 version 冲突控制。
- [x] S2.6 初始化 Vue 网页、登录和统一 API 客户端；实现电脑列表/表格和手机列表，切换视图沿用筛选。
- [x] S2.7 实现管理员公共来源与岗位维护页面，明确区分正式岗位、待核实线索和失败来源。
- [x] S2.8 用电脑创建投递，在手机查看并修改；验证已关闭公共岗位仍保留个人进度。

测试文件：backend/internal/ingest/normalize_test.go、client_test.go、adapters_test.go、backend/internal/catalog/filter_test.go、backend/tests/integration/catalog_test.go、application_test.go、recruitment_http_test.go、web/tests/e2e/jobs-and-applications.spec.ts。

2026-10-15 S2 验收通过：三个来源两次实采后公开 58 个计算机方向广告；71 条已存身份、哈希与版本无差异，第二轮内容变更为零。网页、真实数据库、Linux race 和 Docker 通过，详见 [S2 验收记录](../../verification/2026-10-15-s2-jobs.md)。

必测：未披露笔试仍为 unknown；截止为空不参与“即将截止”；国内/海外日期要求不混用；公开岗位 closed 不改变个人申请状态；同名新批次不被错误合并；并发修改返回 409；收藏与投递的跨用户读写全部拒绝。

S0 新增实际反例：华为 AI 数据系统方向不能继承广告层额外的合肥；腾讯旧父菜单年份不能覆盖当前 2027 子批次；招商旧公告可被搜索到但实时已下线；腾讯成功空结果是 count=0/list=null，华为越过末页返回 SUCCESS 加无计数 null，二者不能被同一规则误判；大整数 postId 不转为浮点数。

阶段验收：三个真实来源的数据能进入公共查询，过滤结果可追溯，电脑与手机形成官方入口到个人投递记录的完整操作链。

## 6. S3：VIP 简历与匹配

计划文件：backend/internal/profile/、backend/internal/matching/、backend/internal/task/、对应迁移，web/src/features/resume/ 和 web/src/features/recommendations/。

- [ ] S3.1 在接受上传前校验能力；实现 10 MiB 文件限制、真实类型识别、私有存储和授权下载。
- [ ] S3.2 本地 PDF/DOCX 文本提取，扫描/加密/损坏文件明确失败；DOCX 解压大小有限制。
- [ ] S3.3 先验证脱敏样本不会发送姓名、电话、邮箱和证件信息，再接入云端解析；没有密钥时清楚显示服务未配置。
- [ ] S3.4 用户确认草稿后创建 profile_version，指定当前版本；未确认草稿不触发推荐。
- [ ] S3.5 编写学历、毕业时间、专业、经验、学生身份与实习出勤/时长的规则用例，按岗位子方向实现 eligible/needs_confirmation/ineligible 及逐项原文依据。
- [ ] S3.6 对非 ineligible 岗位实现语义匹配，区分必需/优先条件，校验输出结构和证据引用；不生成录用概率。
- [ ] S3.7 按用户、简历、JD、规则和模型版本缓存结果；变化后使旧结果失效，任务处理期间版本变化则取消旧任务。
- [ ] S3.8 覆盖 VIP 撤销、账号停用、简历删除时的请求及后台任务阻断，包含外部调用前与结果返回前的检查。
- [ ] S3.9 网页展示解析确认、可投条件与技能解释；待确认机会单独标明，不静默隐藏。

测试文件：backend/internal/profile/redaction_test.go、backend/internal/matching/eligibility_test.go、backend/internal/matching/evidence_test.go、backend/tests/integration/recommendation_authorization_test.go、web/tests/e2e/vip-recommendations.spec.ts。

固定测试场景：

| 输入/变化 | 预期 |
|---|---|
| 岗位明确硕士及以上，用户确认本科 | ineligible；不调用语义服务 |
| 岗位写相关专业，缺少专业对应依据 | needs_confirmation |
| 简历没有 Java，JD 写 Java 优先 | 显示信息不足或偏好差距，不判为明确不会 |
| 企业批次与岗位毕业范围相冲突 | needs_confirmation，并显示两段依据 |
| 父菜单旧年份与明确当前子批次不一致 | 标明菜单冲突，不将不适用的菜单文案强制合并为门槛 |
| 华为某方向不含合肥，父广告包含合肥 | 不输出“该方向合肥可投” |
| 实习要求每周 5 天、至少 1 月，用户未确认 | 时长/出勤为 needs_confirmation |
| 实习只写 2027 届优先 | 保留偏好，其他届数不自动判不满足 |
| 模型返回空内容、无效结构或不存在的引用 | 语义失败，规则结果仍可读 |
| A 与 B 上传内容相同的简历 | 私有解析和推荐缓存仍按用户隔离 |
| 任务排队后管理员撤销 VIP | 任务 cancelled，无模型调用和个性化通知 |
| 模型运行中用户删除简历 | 不重新创建内容，结果与通知不发布 |

阶段验收：普通用户不能通过接口、任务状态、下载路径或缓存读取推荐；有效 VIP 可完成上传、确认、推荐、解释；不满足和待确认结论有可靠依据。

## 7. S4：持久化任务、站内与飞书

计划文件：backend/internal/task/、backend/internal/notification/、backend/cmd/worker/main.go、对应迁移，web/src/features/notifications/。

- [ ] S4.1 实现任务唯一调度键、数据库领取与有限租约、重试状态、宕机恢复；模拟时钟测试每日 08:00 调度。
- [ ] S4.2 实现个人筛选订阅与一个默认接收群；绑定时验证合法飞书 URL，加密保存签名配置。
- [ ] S4.3 生成岗位事件与截止窗口；同日同岗位在摘要中合并，已投递/已结束的机会排除。
- [ ] S4.4 实现 09:07 摘要截止线，显式展示未完成来源；迟到结果进入下一次批次。
- [ ] S4.5 普通通知使用筛选，个性化通知使用当前有效推荐和即时权限；默认不输出简历信息、技能缺口和备注。
- [ ] S4.6 实现最多 10 条、18 KiB 内容预算和最终 JSON 字节长度检查，超出部分显示总数与网页链接。
- [ ] S4.7 发件箱事件和批次唯一；区分明确发送失败与送达状态不明，校验飞书业务码，不因 HTTP 200 就标成功。
- [ ] S4.8 模拟错误/限流验证有界退避；超时不盲目重发，留站内及管理员处理入口。
- [ ] S4.9 用两个用户、两个群配置测试内容路由及配置关闭/权限撤销后的取消。

测试文件：backend/internal/task/scheduler_test.go、backend/tests/integration/task_recovery_test.go、backend/internal/notification/digest_test.go、backend/internal/notification/feishu_test.go、backend/tests/integration/notification_isolation_test.go。

必测：重启不丢待执行任务；重复调度不形成双份事件；HTTP 200 但业务失败仍为 failed；限流有退避；超长中文内容按 UTF-8 实际字节检查；未知送达不无限重试；VIP 到期不会继续发送个性化内容；无相关事件时不推送。

真实飞书测试只在本人绑定并主动触发测试发送后进行。开发自动测试使用假 HTTP 服务，不产生外部群消息。

## 8. S5：扩源、部署和运行验收

计划文件：deploy/compose.yaml、deploy/Caddyfile、部署操作文档、来源台账、运行验收记录、web/tests/e2e/。

- [ ] S5.1 按来源验证清单逐家补齐到 20 家实际 automatic_ready 来源，每家公司保留至少两次重复获取证据。
- [ ] S5.2 列出云主机资源、域名、模型调用、存储及备份的实际费用；以实际测量估算，不承诺固定价格或零成本。
- [ ] S5.3 配置 HTTPS、私有目录、数据库隔离、密钥注入、容器健康检查和日志脱敏；创建管理员。
- [ ] S5.4 备份数据库和必要文件，在独立测试环境恢复，核对账号、个人投递和来源版本。
- [ ] S5.5 执行角色/资源权限集成测试，以及电脑和手机浏览器端到端流程。
- [ ] S5.6 连续记录 7 天：来源调度、成功/失败、数据新鲜度、新增与关闭、匹配错误、发送状态、恢复事件及资源用量。
- [ ] S5.7 输出实际接入名单、明确受限来源和验收结果；未达到 20 家时记录真实缺口，不能标记 V1 全部完成。

验收命令在对应工程和脚本已实现后运行：

```powershell
go -C backend test ./... -count=1
go -C backend vet ./...
pnpm --dir web run type-check
pnpm --dir web run build
pnpm --dir web exec playwright test
docker compose -f deploy/compose.yaml config --quiet
```

Go race 检查在支持的 Linux 开发/CI 环境运行：

```bash
cd backend
go test -race ./internal/... ./tests/integration/... -count=1
```

不把 Windows 上缺少 C 编译器导致的 race 环境问题写成业务测试通过；不把 Docker CLI 存在写成容器服务可用。

## 9. 进入开发后的执行约定

每个业务任务先写能够验证实际行为的测试，确认失败原因后实现最小行为，运行相应测试并记录结果。文档、标签或可逆样式修改不要求为形式完整而编写测试。

源代码修改提交前先做 Go 功能与风格审查；提交内容只包含该任务变更。正式部署、购买资源或发送外部消息必须具备对应的实际配置与授权，不用真实用户简历作测试数据。

本计划不需要提前创建全部空目录、占位 handler 或假岗位接口。S0–S2 已完成，下一项工作为 S2 岗位和投递闭环；前三来源的公开 API、子方向及未知状态边界构成后续采集实现依据。

## 10. 本轮文档检查记录

- [x] 已与上一轮需求核对：个人优先、跨行业计算机岗位、每日一次、Go/Vue、手动 VIP、飞书群、20 家目标。
- [x] 普通/VIP/admin 的能力、资源归属和即时撤销具有统一约定。
- [x] 未公布/未确认与明确不满足分开，采集失败不会关闭岗位。
- [x] 明确初期来源及环境未验证内容，不将目标写成完成成果。
- [x] 任务具有计划路径、依赖和具体行为验收，缺失执行技能不作为开工依赖。
- [x] 三类真实公开岗位读取及重复证据检查已执行，关键反例同步到设计和测试计划。
- [x] S1 应用测试、真实数据库权限与事务测试、构建和本机启动已执行并记录。
- [x] 完整 Docker 镜像构建、容器运行及 Linux race 已补验通过；临时资源已清理。
- [ ] 生产采集器、云部署和 7 天运行观察仍未执行。
