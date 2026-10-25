# 219 家目标企业岗位接入：暂停交接记录

记录时间：2026-10-23 03:32（Asia/Shanghai）。

用户明确要求：**先保留当前进度，下次说“继续”再继续执行。** 本轮开发、来源核验和验证工作暂停，不自动恢复；现有开发服务与既有每日更新配置未因本次暂停而修改。

## 已确认的范围与授权

- 219 个目标名称及原分组以 `docs/research/targets/2026-10-22-requested-companies.json` 为准。
- 仅计算机技术岗位；包括校招、实习，以及明确接受应届或最多一年工作经验的岗位。
- 用户已批准 `docs/superpowers/specs/2026-10-22-company-list-ingestion-design.md`，允许按方案完成接入、测试后分批发布并接入每日更新，不需要重复询问是否实施。
- 沿用现有权限、事务、来源隔离和原子发布规则。飞书消息仍只做模拟验收，不向真实群发送。
- 不能把全部腾讯或华为岗位标成天美 T1 或华为车 BU；韶音复用已有来源；集团、品牌、子公司合并需要官方证据。“中小厂”分组不直接作为企业规模事实。

## 文件与分支

- 主工作区：`D:/Users/zcn20/Documents/ChatGPT/求职助手计算机专业版`。
- 实现工作区：主工作区下 `.worktrees/s5-requested-company-import`。
- 实现分支：`codex/s5-requested-company-import`，以已提交设计 `89de3d4` 为基线；实现代码尚未提交，**保留该 worktree，不清理或重置**。
- 分步计划：实现工作区内 `docs/superpowers/plans/2026-10-22-requested-company-import.md`。
- 原始公开研究材料：主工作区 `.cache/research/bulk-20261022/`，包含研究阶段的临时匿名会话材料；不得将 cookie、CSRF token 或其他会话信息提交为 fixture。
- 本轮研究台账在主工作区 `docs/research/targets/2026-10-22-auto-games-sources.json`、`2026-10-22-consumer-sources.json`、`2026-10-22-state-foreign-sources.json`，尚未合入实现分支。
- 暂停前已核对主工作区与实现工作区的 `git status`。原有用户修改与长尾来源研究文件保持原样，不能一并暂存、覆盖或清理。

## 当前实现与验证状态

1. **Moka**：已增加配置、可选列表字段处理及结构化资格归一化，新增十个来源配置；对应文件为 `moka.go`、`moka_document.go`、`moka_sources.go`、`moka_requested_test.go` 和 `testdata/moka-requested/`。来源含岚图、文远知行、恺英、掌趣、巨人、阳光电源、三七、FunPlus、博世、安永。研究执行代理报告定向新旧 Moka 测试、gofmt 和 vet 已通过，但最后英语资格边界仍有收尾，完整包与真实源两次完整读取尚未验收。工作区根 `.cache-moka-*.log` 是未提交验证日志，应保留并归档到忽略目录后再审查提交。
2. **飞书招聘公开读取器**：`feishu.go`、`feishu_document.go`、`feishu_sources.go`、`feishu_test.go` 和三个公开 fixture 已写入。读取流程为独立匿名 cookie jar、官网页面初始化、公开 CSRF、完整列表分页、逐条同 ID 详情核验；不访问申请人或登录 API。初始来源仅 `bambulab-careers`，尚需补企业官网到该招聘租户的授权链和真实完整读取。学历沿用匹配器已有枚举 `bachelor_or_above` / `unknown`，高学历保留在硬要求原文中。全包测试曾因仍在编写的北森 `beisenDirection` / `beisenDocument` 缺失而编译失败，**飞书测试不能标为通过**。最后一次排除北森文件的飞书测试命令被用户中断，结果未取回。首次失败日志位于主工作区 `.cache/verification/s5-requested-20261022/feishu-red.log`。
3. **Workday**：`workday.go`、`workday_document.go`、`workday_sources.go`、`workday_test.go`、`testdata/workday/` 已存在，来源 `nvidia-careers`。代理报告定向分页、异常、身份核验、取消上下文和真实资格测试及 vet 已通过；随后发现“在读优先”不能视为硬要求、“0 to 1 年”不能变成至少一年，正在修正时暂停，须检查文件与重新测试。迁移中该源 OfficialURL 应为 `https://nvidia.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite`，以满足详情链接同 host 校验。
4. **北森**：已有 `beisen.go`、`beisen_sources.go`、`beisen_test.go` 和 fixture，**缺少 `beisen_document.go`，当前全包预期不能编译**。MINISO 校招实际观察到 28 条（20+8）；蜜雪校招 26 条（20+6），列表和独立详情合同已验证。约定来源 `miniso-campus` / company `miniso`、`mxbc-campus` / company `mxbc`。蜜雪“2027届校招-信息安全方向(J13403)”正文写“2026届本科”，按冲突排除并保留原文；不得按标题默认为 2027 届。
5. **共享代码**：`client.go` 已增加三个平台 Base 测试覆盖字段及飞书请求间隔；精确可信 host、`SupportsSource`、`Collect` 调度、CLI/PowerShell 来源枚举、数据库迁移和数据库集成测试尚未完成。Moka 代理已交接 `moka.go` 内 `SupportsSource` 的修改权，恢复后主执行者可以统一注册。
6. **219 项覆盖台账**：其他三个分组研究文件共覆盖 165 个目标；主执行者负责的 54 项（中小厂 34 + 游戏前 20）仍需补完整逐项台账。已保存多项官方入口与搜索线索，但搜索摘要不能当作官方授权或完整岗位验证。

以上“代理报告”是暂停前收到的定向验证结果，不代表本阶段完整验收通过。暂停时尚未执行新增源的完整 Go 回归、专用 PostgreSQL 集成验证、Linux race、镜像与迁移验证、浏览器验收或发布。本轮**没有新增实际发布岗位，也没有启用新的每日来源**。

## 恢复后的顺序

1. 先核对本记录、工作区差异和代理最后状态，检查上次中断是否留有本任务进程；不要重启既有 Docker/WSL 或删除 worktree。
2. 完成北森文档归一化、Workday/Moka 待修资格边界，重新运行飞书定向测试；重点检查项目经历与工作经验不能混淆、优先条件不能变为硬门槛、已有匹配器可实际识别保留的资格条件。
3. 补已核实租户的官方链、精确 host 白名单、公共分发及来源迁移，使用生产读取器对每个候选源重复完整只读采集，记录排除项与稳定内容哈希。
4. 按 AGENTS.md 完成专用 PostgreSQL 实际测试、完整 vet / Go / Linux race、容器迁移和运行验证；需要前端或部署验收时运行相应检查。未运行、跳过、失败项均记为待验证。
5. 发布前取得新的数据库备份与基线，审查提交，再按正常采集机制分批发布；不得直接编辑正式岗位表。保留既有岗位、个人数据和模拟通知配置。
6. 按 219 个目标逐项对账，分别记录实际发布数量、仅入口已核实、受限、无符合范围岗位或待核验等真实状态，完成阶段验收记录。

## 暂停核对

- 三名本轮执行代理均已中断，不再安排后续工作。
- 暂停检查未发现命令行匹配本轮飞书、Moka、Workday、北森或 requested-probe 的 Go 测试进程。
- 本次仅保存暂停记录并检查内容，不继续采集、测试、部署或发布。
