# 腾讯官方招聘来源验证

验证日期：2026-10-14。结论：**已验证匿名公开 API 的列表、详情、分页和重复获取，尚未部署每日采集器，不计入 20 家稳定自动接入数量。**

本次主要范围为腾讯普通 2027 校招、中国工作地点。实习、日常实习、青云计划、海外和社会招聘尚未做相同的端到端验证。

## 1. 官方出处与入口

授权链：

1. [腾讯招聘的应届生说明](https://careers.tencent.com/jobopportunity.html) 实际引用官方 CDN 的 JobOpportunity_zh-cn.js。
2. 该官方文案明确指向 join.qq.com 作为中国区校园招聘入口。
3. [腾讯校招首页](https://join.qq.com/) 的 V2Index_zh-cn.js 配置给出当前 2027 校招及投递列表地址。
4. [岗位列表](https://join.qq.com/post.html?query=p_1) 的实际页面脚本声明公开岗位搜索、项目字典和详情接口。

本次读取的是官网实际引用的资源，不靠搜索摘要证明当前列表开放。

关键公开脚本：

- https://cdn.multilingualres.hr.tencent.com/careersmlr/JobOpportunity_zh-cn.js
- https://cdn.multilingualres.hr.tencent.com/campusjoin/V2Index_zh-cn.js
- https://cdn.multilingualres.hr.tencent.com/campusjoin/V2Post_zh-cn.js
- https://cdn.multilingualres.hr.tencent.com/joinqq/static2/js/p_zh-cn_post.build.js

岗位脚本本次 SHA-256 为 de293ebb49656d70fabf3b53c59d972e8b5c280467a44bd0cfa97f3b0d8a4ec6。

## 2. 已验证的访问方式

公开 HTML 返回 HTTP 200，但主要是客户端外壳，不能直接从正文取得岗位。

CDN 脚本首次直接请求返回 403；加入该资源所属官方网页的正常 Referer 头后返回 200。没有使用用户 Cookie、账号、登录信息或任何验证码处理。

公开请求使用 Referer https://join.qq.com/post.html?query=p_1；搜索 POST 另带 Content-Type: application/json。POST 仅是该页面的岗位查询方法，未调用申请、收藏、账户或简历接口。

| 方法 | 地址 | 验证结果 |
|---|---|---|
| GET | /api/v1/position/getProjectMapping | HTTP 200、status=0；取得当前批次及子项目 |
| GET | /api/v1/position/getAllProject | HTTP 200、status=0；项目字典 |
| GET | /api/v1/position/getPositionFamily?lang=zh-cn | HTTP 200、status=0；岗位方向字典 |
| POST | /api/v1/position/searchPosition | HTTP 200、status=0；返回 positionList 和 count |
| GET | /api/v1/jobDetails/getJobDetailsByPostId?postId=实际ID | 三条真实岗位均 HTTP 200、status=0 |

搜索的实际当前参数为：

```json
{
  "projectIdList": [],
  "projectMappingIdList": [1],
  "keyword": "",
  "bgList": [],
  "workCountryType": 1,
  "workCityList": [],
  "recruitCityList": [],
  "positionFidList": [],
  "pageIndex": 1,
  "pageSize": 50
}
```

projectMappingIdList 取当前字典中的 mappingId，不能依赖脚本里已经废弃的 projectId 示例。列表接口在前端实际使用当前参数，旧注释不代表当前请求格式。

## 3. 真实列表和重复获取

| 范围 | 每页数量 | 页数 | 两轮总数 | 完整性 |
|---|---|---|---|---|
| 普通 2027 校招、中国地点、全部关键词 | 50 | 3 | 105 / 105 | 每轮 105 个唯一 postId，均完整覆盖 |
| 相同批次、中国地点、关键词“开发” | 10 | 4 | 36 / 36 | 每轮 36 个唯一 postId，均完整覆盖 |

两轮列表的排序后 postId 集合完全一致。普通校招全列表中的 positionFamily=2 项为 41 条，表示腾讯的技术大类，不能直接等同于每条都适合计算机专业或本人。

技术大类字典包括软件开发、技术运营、安全、测试质量、算法研究、解决方案服务及硬件开发。专业和岗位要求仍需从详情判断，不能只用“开发”关键词覆盖计算机岗位。

测试筛选词“S0验证无匹配岗位20261014”返回 status=0、count=0、positionList=null，证明在成功业务码和明确总数 0 的共同依据下，该来源的筛选空结果可识别；这不表示公司没有岗位。不能仅凭 null 判为空，也不能要求所有来源的成功空结果都返回空数组。

列表 postId 为大整数形式的字符串，必须原样存为字符串。列表 id、position 与详情里的 id 含义不同，不能混作统一外部 ID。

详情页链接格式来自页面实际代码：

```text
https://join.qq.com/post_detail.html?postid=<列表postId>
```

## 4. 三条计算机岗位样本

| 样本 | postId | 地点 | 主要条件事实 |
|---|---|---|---|
| Agent 开发工程师 | 1282707395466077184 | 深圳、北京、上海、广州、成都 | 至少一门主流语言，包含 Python/TypeScript/Go；要求 Agent 架构及框架相关能力 |
| 游戏客户端开发 | 1282032034373805056 | 深圳、北京、上海、杭州 | 明确计算机科学/计算机工程相关专业；编程与基础计算机知识 |
| 后台开发 | 1282707383965295616 | 深圳、北京、上海、广州、成都、杭州 | 编程语言包含 C/C++/Java/Go；涉及网络、操作系统、数据库等基础 |

每条样本的核心详情进行了两次独立获取，标题、职责、要求、地点、项目、加分项等选择字段完全一致。

原始公开证据与实际请求摘要见 [JSON 证据](../samples/tencent/2026-10-14-public-api-evidence.json)。职责 desc、要求 request、校招加分项 graduateBonus 和实习加分项 internBonus 是不同字段，不能把加分项改写为必需条件。

原始城市“深圳总部”映射地级市深圳时需要保留原文。事业群和部门地点在详情中还有独立关联，后续若按部门投递，应保留该关系，不能随意组合部门和城市。

## 5. 毕业范围与数据冲突

首页当前普通校招项目与具体子批次均给出毕业时间 2026-01-01 至 2027-12-31；实习首页文案另给出 2026-09-01 至 2027-12-31，但本次没有验证实习岗位详情。

getProjectMapping 的应届生父菜单仍带 2025—2026 年显示范围，而该菜单下的具体“2027校园招聘”子项目给出 2026—2027 年范围。二者不能无条件取交集，否则会误筛掉 2027 年毕业者。

后续采集器应关联本次实际选择的 mappingId=1 子批次，保存父菜单文案为独立展示证据并标注冲突，不将该过时菜单文字自动当作有效硬门槛。只有共同适用于当前具体批次/岗位的招聘条件才合并判断。

## 6. 开放、截止、笔试与投递限制

三个样本当前出现在公开列表，并能匿名读取详情；其详情 raw status 为 null，未发现明确截止字段。

因此可以记录“2026-10-14 官方公开在列、详情核验成功”，不能将 null 解释为确定关闭或确定开放，也不能补造截止日期。若 UI 使用 open/closed/unknown，单凭本次证据的强状态应保留 unknown，并展示公开在列证据。

本次没有核实适用的统一笔试公告，三个样本的 written_test 保留 unknown。不能因为详情没有笔试字段就标为无笔试。

公开脚本将 index/post/post_detail 等页面列为无需登录即可访问的页面；实际提交简历仍需腾讯登录。未登录、未尝试投递。

没有对无依据的随机 ID 进行关闭或失效探测，尚未验证该来源的已关闭详情返回码。

## 7. 耗时、环境与完成程度

证据 JSON 记录 23 个成功的公开 JSON 请求，累计请求耗时约 5.759 秒，包含重复列表、样本详情和字典读取；官方 HTML/CDN 的定位请求另计。这只是当前机器、当前网络、当前小样本验证，不是全量详情采集或云端长期运行的成本测量。

本次不需要浏览器渲染。用户 Chrome 的 CDP 前置检查仍为未连接，Node 20.18.0 有版本提示；该本地环境限制没有阻止公开 API 验证。

已完成：官方链、当前批次、真实详情、方向字典、完整分页、两轮身份稳定性、筛选成功空结果。

尚未完成：所有招聘类型的覆盖、明确关闭证据、适用笔试证据、20 家扩源、可运行采集器、每日调度、7 天稳定性和正式岗位入库。

适配器可以优先使用本次验证过的公开 API；必须显式处理业务 status、未知截止/状态、项目字典更新、大整数 ID 与页面结构变化。公开前端接口不是对外稳定契约，后续需要失败检测和维护记录。

离线复核命令：node docs/research/samples/tencent/verify-evidence.mjs。检查覆盖完整分页、唯一身份、两轮身份集合、三份重复详情、当前批次与父菜单冲突，以及 success/count=0/list=null 的真实形态；不发送网络请求。
