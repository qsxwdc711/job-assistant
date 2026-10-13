# 华为官方招聘来源验证

验证日期：2026-10-14，北京时间约 01:22–01:31。本文仅记录公开读取，不登录、不投递，不调用收藏、推荐或简历接口。

**结论：公开自动读取的技术可行性已验证（automatic_ready，范围限本文验证的应届生列表和三条广告详情链）。尚未开发生产采集器，未完成全量详情及连续运行验收，实际自动接入完成数不增加。** 计算机岗位不能只读广告详情：职责和条件放在广告下的“岗位意向”中，必须保留这一级数据。

## 1. 官方授权链与当前批次

| 节点 | 当次取得的证据 | 本地样本 |
|---|---|---|
| [华为公司官网](https://www.huawei.com/cn/) | 公开 HTML 中“招贤纳士”锚点直接指向 `https://career.huawei.com/cn` | `huawei-samples/corporate-page.body.txt` |
| [华为招聘首页](https://career.huawei.com/cn) | 页标题为“华为招聘官网首页”；导航 JSON 将“校园招聘”链接到 `/cn/campus-recruitment` | `huawei-samples/career-home.body.txt` |
| [校园招聘](https://career.huawei.com/cn/campus-recruitment) | 当次公开页面 JSON 明确“2027届应届生招聘对象”，提供应届生、实习生列表入口 | `huawei-samples/campus-round1.body.txt`、`campus-round2.body.txt` |
| [应届生岗位列表](https://career.huawei.com/cn/campus-recruitment-job-list?recruitmentType=FRESH_GRADUATE) | 页面标题“校招职位列表”、列表标题“在招职位”，详情键为 `advertisementId` | `huawei-samples/list-page.body.txt` |
| 官方公开数据接口 | 列表及详情 HTML 声明的组件脚本给出 `/recruitmentPosition/pub/…` 路径；公共脚本给出 API 主机和公开应用 ID。没有通过猜测路径寻找接口 | 组件、公共脚本及对应 `.meta.json` |

上述链均属于华为自有域名，没有第三方 ATS 授权链。官网页脚版权主体是华为技术有限公司；岗位字段仅提供 BU、实验室等业务部门，**不能据此证明每个方向的法律招聘主体都是该公司，不能把部门当作法律用工单位**。

批次的已验证通用规则：国内高校本科、硕士毕业范围为 2027-01-01 至 2027-12-31；国内博士及海外高校本、硕、博为 2026-01-01 至 2027-12-31。实习项目面向海内外在校大学生，本文没有验证实习列表或为其补充精确毕业范围。规则原文与组件 ID 保存于 [campaign-evidence.json](huawei-samples/campaign-evidence.json)。

API 没有返回明确的“2027”批次字段；样本批次标签引用当次校园入口文案，保留其来源。不能把这些广告 ID 的两次稳定性当作跨年份绝不复用的证明。

## 2. 可复查的公开获取方法

页面原始 HTML 含 `<script type="application/json" id="comData_…">` 的内容配置，而职位由公开接口加载。普通正文提取为空不代表没有职位。本次无需渲染浏览器即可取得真实数据。

官方 HTML 的 `window.$devPath` 分别声明列表和详情组件路径，`common.min.js` 声明生产 API 主机 `https://apigw-dgg-b0.huawei.com/api/apig/channelhw`、公开应用 ID `app_000000035886`、租户 `hcm`。生产配置的 CSRF 字段为空；读取使用页面实际设置的公开应用、语言、来源、环境头，不发送 Cookie、账户凭据或 CSRF 私密值。

| 读取目标 | HTTP 方法及公开路径 | 已验证请求体 |
|---|---|---|
| 应届生列表 | POST `/recruitmentPosition/pub/getJobPage?X-HW-ID=app_000000035886` | `{"curPage":1,"pageSize":100,"jobType":"CR","recruitmentType":["FRESH_GRADUATE"]}` |
| 广告详情 | POST `/recruitmentPosition/pub/getRecruitmentPositionDetail?X-HW-ID=app_000000035886` | `{"advertisementId":36373}` |
| 岗位意向及真实 JD | POST `/recruitmentPosition/pub/getPositionIntentionList?X-HW-ID=app_000000035886` | `{"jobId":103906}` |
| 选定意向的部门树 | POST `/recruitmentPosition/pub/getDeptIntentionList?X-HW-ID=app_000000035886` | `{"jobId":103906,"externalPostCode":"JT2026071500037"}` |

请求头及只允许公开招聘路径的验证辅助脚本见 [api-probe.mjs](huawei-samples/api-probe.mjs)。HTML 获取脚本见 [probe.mjs](huawei-samples/probe.mjs)。二者是研究工具，不是生产采集器；运行前仍须按 web-access skill 检查环境。

本次四类接口均 HTTP 200、JSON `status=SUCCESS`。没有调用 `/servlet/getHeaderInfo`，避免保存任何个人登录数据；没有调用任何非公开投递、推荐、收藏接口。

## 3. 列表、详情、分页及状态证据

初次 `pageSize=10` 返回 `totalRows=71`、`totalPages=8`。网页组件明确提供 10/20/50/100 的每页数量选项；改用其 100 选项后，两轮均取得 **71 个唯一 advertisementId，totalPages=1，startIndex=1，endIndex=71**。由记录数和分页元数据相互验证覆盖。71 是该筛选下全部应届生广告数，包含非计算机方向，不能宣传为 71 条计算机可投岗位。

软件开发广告详情的 `mainBusiness` 和 `jobRequire` 均是“请您详见岗位意向…”占位文案。继续按详情返回的 jobId 读取意向，才取得真实 `jobResponsibilities`、`jobDemand`、`externalPostCode`、`positionIntentionId` 和部门城市关系。

- 软件开发工程师（36373 / 103906）：4 个意向，数据库、操作系统与编译器、通用软件、嵌入式软件。
- AI Infra工程师（36384 / 103891）：4 个意向，AI算子技术、AI训推系统、AI数据系统、建模仿真。
- AI安全与隐私工程师（36397 / 103892）：2 个意向，AI模型与Infra安全、AI应用安全。

数据库意向的部门接口返回 7 个一级部门，15 个下级条目；相关名称、代码和介绍均为公开组织资料。意向数据的 `deptAndPlaceList` 有重复条目，标准化可按部门代码＋原始城市集合去重，但不得把全部部门与全部地点做笛卡尔积。每个方向的职责、条件、城市及部门关系作为一组保留和匹配，不能混用同一广告下不同意向的字段。

当前三广告均在成功返回的“在招职位”列表中，详情和真实意向可读，详情组件在存在 `jobname` 时展示“申请”按钮。据此样本记为 `open`，依据是“官方当前列表及详情可读”；**没有实际提交验证，不能保证账号能够完成投递、名额未满或每个部门都接受该候选人**。

截止日期：三广告及其意向未提供，记为未公布；不使用更新时间、意向创建时间替代截止。岗位最低学历和工作经验没有单独明示时保存 unknown，毕业适用规则单独引用批次，不能用 null 推导不限学历或经验。

笔试：当前校招页面 FAQ 明确研发类需要上机考试与综合测评。三广告均 `categoryName=研发类`，样本 `written_test=yes`，证据适用范围为当前应届生研发类。非研发类只写部分岗位涉及考试，不能统一标 yes 或 no。FAQ 原文与组件 ID 同样保存于 `campaign-evidence.json`。

末页试验：`curPage=2,pageSize=100` 返回 `SUCCESS`，但 `pageVO=null,result=null`。只有先前已验证 71 条且 totalPages=1 时可解释为越过末页；**首次请求遇到这种响应不能直接认定 valid_empty，更不能关掉原有岗位**。保存的真实响应是采集器空响应分类的输入样本。

未找到或请求任何真实已关闭广告。本次没有验证关闭错误码。未来列表缺失、详情异常、null 响应均按采集异常或状态待确认处理；只有另行核实的明确关闭/截止证据才转换为 closed。

## 4. 三条可复查样本与数据模型影响

| 广告及方向样本 | 官方详情与标识 | 真实条件摘要 | 地点及未知字段 |
|---|---|---|---|
| 软件开发工程师 · 数据库 | [详情 36373](https://career.huawei.com/cn/job-details?advertisementId=36373)，jobId 103906，positionIntentionId 11394，externalPostCode JT2026071500037 | 计算机、电子、通信、数学、AI 等相关专业；编程、算法与 Linux 基础。数据库内核经验、开源和竞赛、分布式数据引擎等为优选项 | 12 个城市，具体部门与城市关系有原始数据；岗位单独最低学历、截止、经验年限 unknown |
| AI Infra工程师 · AI数据系统 | [详情 36384](https://career.huawei.com/cn/job-details?advertisementId=36384)，jobId 103891，positionIntentionId 11585，externalPostCode JT2026071500153 | 计算机系统及编程基础，涉及数据工程、分布式计算与存储；相关系统实践或实习、竞赛、论文条目需按原文区分优选 | 12 个城市；广告总体有合肥，此方向的原始城市集合没有合肥，不能从广告层直接继承；最低学历、截止、经验年限 unknown |
| AI安全与隐私工程师 · AI模型与Infra安全 | [详情 36397](https://career.huawei.com/cn/job-details?advertisementId=36397)，jobId 103892，positionIntentionId 11480，externalPostCode JT2026071500183 | Python/C++、深度学习框架、Linux 与容器安全、算法数学等；可信计算与研究成果列为加分项，不能作为统一硬门槛 | 12 个城市；具体方向要求未单独给专业/最低学历/截止/经验年限时保持 unknown |

原始广告、所有意向字段分别在 `detail-api-*-round*.body.txt`、`intent-api-*-round*.body.txt`；选定三样本的标准化结果在 [normalized-samples.json](huawei-samples/normalized-samples.json)。各样本保留全部原始职责/条件 HTML 与部门城市映射。

建议生产采集器保留广告与意向的父子关系：广告身份是 `来源 + 批次 + advertisementId`；意向身份额外加入 `positionIntentionId`，同时保存 jobId、externalPostCode。不能只用岗位名去重，不能将三个广告当作只有三套 JD，也不能把业务部门拆成公司来增加覆盖数。

投递链接级别为“岗位详情入口”：详情页面要求选择意向、地点和部门。其公开代码在用户未登录时转向华为 Uniportal 登录，并随后检查简历完整性；这说明阅读无需登录、申请需登录。本文没有执行该动作。没有把构造的内部提交接口当作用户投递直达链接。

## 5. 两轮重复读取、耗时及资源

所有时间元数据保留 UTC，用户展示换算为北京时间。响应 Date 与本地时间相符；校园 HTML 命中公开 CDN 缓存，第二次 Age 约 10214 秒，真实职位 JSON 独立请求没有依赖这份 HTML缓存当作职位刷新结果。

| 读取 | 第一轮北京时间 / 请求耗时 | 第二轮北京时间 / 请求耗时 | 比对 |
|---|---|---|---|
| 完整应届生列表 | 01:28:03 / 2794 ms | 01:29:41 / 933 ms | 71 身份键相同，完整响应 SHA-256 相同 |
| 软件开发广告 / 意向 | 01:26:41 / 488 ms；01:26:52 / 1095 ms | 01:29:47 / 459 ms；01:29:55 / 1356 ms | 广告与 4 意向响应均相同 |
| AI Infra广告 / 意向 | 01:28:21 / 476 ms；01:28:26 / 1011 ms | 01:30:00 / 496 ms；01:30:07 / 2111 ms | 广告与 4 意向响应均相同 |
| AI安全广告 / 意向 | 01:28:32 / 435 ms；01:28:36 / 791 ms | 01:30:12 / 460 ms；01:30:17 / 713 ms | 广告与 2 意向响应均相同 |
| 校招资格及 FAQ 页 | 01:23:40 / 399 ms | 01:31:35 / 417 ms | 完整响应相同 |

完整机器比对结果见 [verification-summary.json](huawei-samples/verification-summary.json)。按名归档的 **26 次成功 HTTP 读取**，累计正文 3,998,456 字节，fetch 等待合计 27,872 ms；另有前期一次未归档成功 HTML 读取，以及一次默认沙箱 DNS 失败。请求串行、未占用浏览器，无批量穷举 ID。

第二轮“完整列表＋三个广告详情＋三个意向”7 请求耗时合计 6528 ms，正文 479,395 字节；这只是 3 个广告的端到端样本，**不是 71 条广告全量详情耗时**。未遍历其余 68 广告，不能按该数字直接估算 20 家全量任务成本。

离线验证命令：`node docs/research/sources/huawei-samples/summarize.mjs`。已检查 26 份正文与元数据 SHA-256、分页完整性、唯一身份键、8 组独立响应一致性、真实意向 JD、研发分类和末页 null 形态；检查通过。两次结果相同只证明当次重复性，不证明长周期接口稳定。

## 6. 环境限制、维护风险与剩余验收

- `web-access/scripts/check-deps.mjs` 返回 Node v20.18.0 警告、Chrome 未连接。未启动或操作用户 Chrome；CDP 渲染未验证。公开原始 HTML 和实际脚本声明的接口足以完成此次读取。
- 默认沙箱 curl 对域名报无法解析，升级后公开只读网络请求成功。属于执行环境网络限制，不是华为无岗位或岗位关闭。没有收到验证码或登录墙阻碍公开招聘读取。
- API 主机、公开应用 ID、组件版本和字段随官网更新可能变化；生产适配器应先解析官方声明的配置，并在 schema、分页或身份字段异常时暂停该来源，而不是继续输出假空列表。
- 对职责/条件做展示前净化 HTML，保留原始证据；内容哈希包含真实方向条件及部门城市关系，不能只使用广告 lastUpdateDate。三广告均更新于 2026-08-17，不能据此断言意向以后没变化。
- 没有覆盖实习、社招、博士研究员的完整详情、海外地点、真实已关闭样本或 CAPTCHA/login 错误响应。下一步实现来源适配器时补齐这些适用范围；未验证范围不套用应届生研发类规则。
- 生产端接入、持久化、调度、权限与飞书发送均尚未实现，本报告只作为来源和数据模型的证据。
