# 收藏与投递状态分页修复计划

**Goal:** 修复个人记录超过 100 条后，岗位卡片的收藏与投递按钮错误。

**Architecture:** 新增仅查询当前账号的岗位状态批量接口，每批 1–100 个 UUID。通过现有事务权限校验和参数化 SQL 返回每个岗位的收藏、投递布尔值。前端先读取当前页，再读取这一页的个人状态；两者成功后一起展示，旧请求不得覆盖新路由。

**Tech Stack:** Go、PostgreSQL、Vue 3、TypeScript、Playwright、Docker Compose。

## 设计取舍

- 采用按当前页批量查询：开销受当前页大小限制，最多 100 个状态，不传备注、历史或原始个人记录。
- 不采用每次翻页遍历全部个人记录：虽然能消除截断，但数据量增长后请求与响应持续增加。
- 不将私有状态放入公共 catalog 模型：私有数据仍由 application 包与 `/me` 路由负责，保留现有职责边界。

## 接口约定

`GET /api/v1/me/job-states?job_id=<uuid>&job_id=<uuid>`，已登录账号可用。1–100 个合法 UUID；空列表、非法 ID、超过上限返回 400。`owner_user_id` 不参与归属判断；管理员调用也仅返回本人状态。未知岗位返回两个 false，不暴露公共岗位存在性。返回顺序与请求一致。

```json
{"items":[{"job_id":"00000000-0000-0000-0000-000000000000","favorite":false,"recorded":false}]}
```

## 执行步骤

- [x] 新增 `backend/tests/integration/job_states_test.go`：真实 HTTP 验证匿名 401、批量上下限、非法 UUID、跨账号与管理员仅本人、未知与隐藏岗位；直接 store 调用验证停用账号被拒绝。初次路由测试确认为 404，修复后通过。
- [x] 新增 `backend/internal/application/job_states.go`：受权限保护的事务，使用 `unnest($2::text[]) WITH ORDINALITY` 及校验后 UUID 转换、`EXISTS` 与 owner/job 索引读取两个布尔状态。注册 `/me/job-states`，handler 复用 UUID 校验及统一错误响应。
- [x] 修改 `web/src/shared/api/types.ts` 和 `web/src/features/jobs/JobsPage.vue`：定义 JobState，将页面读取和状态读取串联，统一 requestID；删除固定前 100 条个人列表加载。状态失败时保留错误与重试，不展示错误的可点击按钮。
- [x] 更新已有两个超过 100 条记录的浏览器用例，验证一次点击取消收藏、已有投递直接进入进度且没有重复 POST；添加延迟旧响应及状态请求失败后重试的行为测试。
- [x] `scripts/verify-all.ps1` 验证 14/14 增强浏览器用例、类型检查、生产构建、真实 PostgreSQL、Linux race、gofmt、vet；`scripts/verify-s2.ps1` 验证原有 2/2 场景。
- [x] 按 `go-feature-change-review` 与 `go-code-review` 完成 SQL 归属、接口边界、异常和请求竞争审查；更新修复报告与无凭据证据，保留原失败报告。
- [x] 使用已构建并验收的镜像更新已有本机应用，健康、登录、20 个当前页状态检查通过，持久记录数量与更新前一致。

本修复在现有工作目录中执行，保留上一轮测试资产与未提交改动。不需要数据库迁移，不新增外部服务，不向招聘网站提交申请。
