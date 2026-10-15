# 收藏与投递按钮状态修复验收

日期：2026-10-15，Asia/Shanghai。针对[全功能测试报告](2026-10-15-full-functional.md)中的 F-01、F-02 完成修复，原失败记录和证据保留。

## 修复结果

- 106 个收藏的末页六条均正确显示“取消收藏”，一次点击执行 DELETE 并移除；剩余五条仍保持正确状态。
- 106 条投递对应的看板页，100 个岗位均显示“查看进度”；点击直接进入投递管理，不发送重复创建请求。
- 状态查询只包含当前页岗位，最多 100 个 ID。看板不再读取全部收藏或投递列表来判断按钮。
- 状态请求失败时展示重试入口；上一轮搜索的延迟状态响应不能覆盖当前搜索结果。

## 实现与接口

新增 `GET /api/v1/me/job-states?job_id=<uuid>&job_id=<uuid>`，只返回 `job_id/favorite/recorded`。账号 ID 来自当前会话，查询在事务内复核当前权限；即使管理员传入其他 owner 参数，也只读取自己。空输入、非法 UUID、超过 100 条返回 400；未知 ID 返回两个 false，隐藏岗位保留本人的状态。

SQL 使用请求数组、ordinality 与 EXISTS，利用已有 owner/job 唯一索引；查询范围随当前页大小变化。前端把岗位页及其状态作为一次完整加载，并使用 requestID 拒绝旧响应。没有数据库迁移。

主要文件：

- `backend/internal/application/job_states.go`：仅本人、批量有界的事务查询。
- `backend/internal/httpapi/job_states.go`、`recruitment.go`：参数验证与鉴权路由。
- `web/src/features/jobs/JobsPage.vue`、`shared/api/types.ts`：当前页状态查询与整体更新。
- `backend/tests/integration/job_states_test.go`：HTTP 归属、上限、输入、未知/隐藏岗位、停用复核。
- `web/tests/functional/all-features.spec.ts`：两个原失败用例、操作结果以及延迟/异常恢复。

## 实测

| 检查 | 结果 |
|---|---|
| 新接口先失败再通过 | 初次真实 HTTP 测试为 404；实现后该测试通过 |
| 增强浏览器功能测试 | 14/14 通过；含两个原失败场景及新增状态异常/旧响应场景 |
| 原有桌面与手机用例 | 2/2 通过；独立会话、跨设备版本冲突、保留草稿和隐藏岗位后的个人记录 |
| Go 测试 | 34 个顶层测试、含子用例 46 项通过，无跳过 |
| 并发与静态检查 | Linux Go 1.26.8、真实 PostgreSQL 18.6；race、vet、gofmt 通过 |
| 前端 | Node 22.23.3、pnpm 11.25.0；类型检查与生产构建通过 |
| 本机更新 | 使用已通过增强验收的镜像更新 API；本机健康和当前页状态接口检查通过，持久数据保留 |

复现：使用 `scripts/verify-all.ps1` 与 `scripts/verify-s2.ps1`，工具链参数与原测试报告一致。增强验收日志在 `.cache/verification/job-assistant-full-verify-298e6857/`，原用例日志在 `.cache/verification/job-assistant-s2-verify-ab8c94b3/`。无凭据摘要见[修复证据](evidence/2026-10-15-personal-job-state-fix.json)。

## 功能与风格审查

使用 `go-feature-change-review` 与 `go-code-review` 复核本次修改及关联边界：请求 UUID → handler 校验 → 会话账号 → 事务内当前授权 → 参数化 owner/job EXISTS → 仅本人布尔状态 → 前端当前页按钮。检查了普通/VIP/管理员、伪造 owner、未知/隐藏 ID、停用后的旧 actor、大批量边界，以及原有私有记录与审计路径。

Must Fix：无未解决项。Should Fix：无未解决项。Nits：无待处理项。检查包含实际 gofmt、vet、race 和浏览器运行，没有运行 golangci-lint。没有修改收藏或投递的写入、版本冲突、历史和审计逻辑。

本轮未重新请求官网采集，采集器不在修改范围。浏览器为独立 Edge 桌面与手机模拟上下文；未作真实手机、Safari/Firefox 兼容或压力测试。测试库和账号仅存在于临时环境，验收容器及网络已清理。
