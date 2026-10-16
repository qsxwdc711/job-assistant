# Internship Sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 将已验证的腾讯普通／日常实习与华为实习计算机岗位接入已有采集、筛选和推荐链路。

**Architecture:** 沿用已经批准的 `2026-10-14-job-assistant-design.md`，增加两个独立来源：`tencent-internships`、`huawei-internships`。共用现有企业的列表、详情、方向解析与完整快照发布逻辑，各自保存实习批次条件和运行状态；实习失败不会改变校招来源。按用户“继续”的授权在当前会话逐项实现和验证。

**Tech Stack:** Go 1.26.8、net/http、PostgreSQL 18.6、现有 Vue 看板、Docker Compose。

---

## 范围与依据

- 腾讯：仅实习父菜单 2 的子批次 2（应届实习）、104（日常实习），`workCountryType=1`。2026-10-16 两次完整分页各为 93、71 条广告；这不是计算机岗位发布数量。青云 20 使用不同详情字段，本轮只记录研究结论。
- 腾讯应届实习使用子批次明确的 2026-09-01 至 2027-12-31 毕业范围，不能继承过期父菜单。日常实习原文面向全体在校生，不加相邻批次的毕业限制。
- 华为：`jobType=CR`、`recruitmentType=["INTERN"]`，两次完整读取各为 31 条广告。按研发类别和实际岗位意向筛选计算机方向，保留意向对应的部门和地点。
- 华为在校身份为明确条件；FAQ“主要面向2027届毕业的在校学生”作为偏好原文，不能变成仅限 2027 届。不得继承应届全职的毕业日期范围或考试流程。
- 未披露的每周出勤、实习时长、截止日期保持未知；是否可投仍由用户确认资料与规则引擎判断。前端已有“全部机会／实习”，本轮不修改前端。

## 文件职责

| 文件 | 改动职责 |
| --- | --- |
| `backend/internal/ingest/tencent.go` | 两种来源共用普通详情解析；精确选择批次、验证分页及跨批次身份 |
| `backend/internal/ingest/huawei.go` | 按项目传入请求参数、来源、资格与批次元数据；复用意向解析 |
| `backend/internal/ingest/campaign.go` | 只从实习配置与实习对象 FAQ 提取实习证据 |
| `backend/internal/ingest/internships_test.go` | 真实最小样本、成功与异常响应、资格边界回归 |
| `backend/tests/integration/internships_test.go` | 复用已有独立 schema 夹具，在实际专用 PostgreSQL 中验证 dispatch → publish、筛选和来源隔离 |
| `backend/internal/ingest/testdata/internships/` | 2026-10-16 官方公开响应的必要招聘字段，不含账号数据 |
| `backend/migrations/0005_internship_sources.sql` | 新增来源配置，保留既有来源及岗位 |
| `backend/internal/ingest/run.go`、`backend/cmd/collector/main.go` | 注册两个新采集入口 |
| `scripts/collect-local.ps1`、`backend/README.md` | 本机命令白名单与使用范围 |
| `docs/research/2026-10-16-internship-sources.md` | 证据、测试、真实采集和未验证边界 |

## Task 1: 记录基线与最小真实样本

- [x] 在隔离 worktree 运行 `go test ./internal/ingest ./internal/catalog ./internal/matching`，记录已有集成测试因缺少专用数据库而跳过的事实。
- [x] 从已保存的公开响应构造最小 fixture；补读三类华为详情和意向时保留 `jobname` 与 `deptAndPlaceList`，腾讯再补读一个技术样本及视觉设计对照。保留字符串 ID，记录来源和读取时间。
- [x] 样本目录只保存 `getProjectMapping`、列表条目、职位详情、意向与实习配置，不保存 Cookie、个人简历或申请状态。

## Task 2: 腾讯实习解析与异常测试

- [x] 先写测试覆盖两个批次各自的条件与独立来源；核心断言：

```go
if doc.SourceCode != "tencent-internships" || doc.RecruitmentType != "internship" {
    t.Fatalf("internship metadata: %#v", doc)
}
// mapping 2: From=2026-09-01, Through=2027-12-31.
// mapping 104: len(GraduationRules)==0; no sibling/parent graduation gate.
// mapping 104: StudentRequired=yes; mapping 2 has no explicit student gate, so unknown.
```

- [x] 运行 `go test ./internal/ingest -run TencentInternship -count=1`，确认未实现入口导致失败。
- [x] 将原 `Tencent` 的分页与详情逻辑拆成内部共享函数，并增加 `TencentInternships`。批次选择为固定允许集合：

```go
func (r *Reader) TencentInternships(ctx context.Context) ([]catalog.Document, int, error) {
    return r.tencent(ctx, tencentProgramme{parentID: "2", mappingIDs: []string{"2", "104"}, recruitType: "2", sourceCode: "tencent-internships", recruitmentType: "internship", keyPrefix: "intern", studentRequired: "unknown"})
}
```

- [x] 验证子批次存在且唯一、招聘类型和年份非空；每批次总数一致，所有页 ID 唯一，批次间 ID 不重叠。第二批次网络失败、数量变化、重复 ID 或详情缺失均返回错误且不返回部分文档。
- [x] 保留校园入口原有 `ordinary:1:<year>`，实习使用 `intern:<mapping>:<year>`；本科、专业、笔试和时长无证据则未知，视觉设计不得因 AIGC 技能被纳入。
- [x] 运行腾讯相关测试及原校园回归，确认通过。

## Task 3: 华为实习元数据与意向解析

- [x] 先写测试：请求筛选和 Referer 都使用 `INTERN`；实习类型、在校条件、2027 偏好可见；毕业硬范围为空，笔试未知；意向不继承广告总城市；缺失实习配置失败。
- [x] 运行 `go test ./internal/ingest -run HuaweiInternship -count=1`，确认未实现入口导致失败。
- [x] 引入内部项目元数据（来源代码、官方类型、批次、名称、毕业规则、偏好、在校要求、考试原文），复用现有列表和意向逻辑。校园入口仍调用原 `huaweiCampaign`。
- [x] 新增 `huaweiInternshipCampaign`：遍历现有 `application/json` 配置，仅采集 `key=INTERN` 的 `descList` 及标题明确为实习对象的 FAQ，年份来自该 FAQ；泛化考试 FAQ 不参与实习。
- [x] 对缺失或相互冲突的实习条件返回 `ErrSchema`，不从系统年份、全职条件推断；读取三份真实技术详情，验证意向和部门地点关系。
- [x] 运行华为相关测试及原全职回归，确认通过。

## Task 4: 注册来源并验证原子发布

- [x] 增加不可变迁移文件，按既有表结构插入来源元数据；代码、名称、官方入口与覆盖范围对应实际实现。
- [x] 注册 `Collect`、CLI 和本机脚本入口：`tencent-internships`、`huawei-internships`；`all` 自动包含启用的新来源。
- [x] 集成测试使用 `TEST_DATABASE_URL` 指向以 `_test` 结尾的独立 PostgreSQL，每个测试独立 schema。通过真实 HTTP 测试服务器运行 `ingest.Collect` 后查询数据库：两来源确有 internship 文档，筛选返回对应岗位。
- [x] 为已有 campus 岗位记录基线；让实习来源发生分页错误，再读取有效空列表。断言 campus 的内容版本、发布时间、可见性和核验状态不变，失败轮次未发布部分实习岗位。
- [x] 验证重复成功采集不增版本、无依据的毕业年份不被写成硬范围；迁移可再次执行且不新增重复来源。

## Task 5: 验收、审查与本机运行

- [ ] 对改动执行 `gofmt`；运行 `go vet ./...` 和针对本轮测试。使用唯一 Compose 项目及随机测试凭据启动 `deploy/compose.verify.yaml` 的临时 PostgreSQL，构建 verifier 并执行 `go test -race -v ./... -count=1 -timeout=180s`，不能将跳过数据库测试计为通过。
- [ ] 按 go-feature-change-review / go-code-review 检查请求范围、错误路径、发布事务、既有校园行为、SQL、CLI 和测试；发现问题先修复，再重跑相关检查。
- [ ] 只提交本轮代码和验收文档，合并回原开发分支，保留原先未提交的研究文档。构建本机镜像，执行新增迁移，等待服务就绪，分别真实采集两个新来源。
- [ ] 查询实际岗位数、来源运行状态；用现有网页登录验证“实习”筛选及一个实习详情的官方链接和条件。确认原校园岗位仍在，原简历和个人投递数据不改动。
- [ ] 将实际命令、结果、采集范围和未验证边界写入验收记录；关闭本轮自建浏览器标签，清理唯一测试项目与已合并的干净 worktree，保留项目数据库。

## 本轮完成条件

两个新来源解析、注册和发布测试通过；实际专用 PostgreSQL 与 Linux race 已运行；本机镜像、迁移和真实采集已验证；网页可通过“实习”查看新岗位。外部官网当轮受限时明确写明失败来源和保留旧数据的结果，不将研究计数当作发布数。青云、NCSS 正式入库、全站覆盖、自动投递及飞书发送不在此实现范围。
