# Moka 公开岗位样本

样本最初于 2026-10-15 通过企业官网指向的 Moka 公共招聘页匿名读取。2026-10-18 重新核对企业官网链接、租户首页和首屏列表；本日完整读取结果另记在 `docs/research/2026-10-18-moka-sources.md`，不把旧样本当作当前岗位仍开放的证明。

| 文件 | 已验证招聘入口 | 用途 |
|---|---|---|
| nsfocus-security.json | https://app.mokahr.com/campus_apply/nsfocus/29118 | 校招安全研发、学历及区县归属 |
| shokz-ai-intern.json | https://app.mokahr.com/campus-recruitment/aftershokzhr/36940 | 明确实习、在校和三个月要求 |
| shokz-master-intern.json | 同上 | 硕士在读要求的原文样本 |
| shokz-joint-topic.json | 同上 | 2026-10-18 公开详情成功但联培课题无职责条件，暂不发布的反例 |
| bluepoch-experienced-java.json | https://app.mokahr.com/apply/blueinteractive/38433 | 三年以上社招排除反例 |
| bluepoch-video.json | 同上 | 非计算机技术岗位的原文反例 |

仅保留公开岗位 ID、主体、标题、状态、招聘类型、职责条件、地点、部门和项目名称等适配所需字段。删除联系人、账户、会话、后台运行配置及无关字段。测试的 IV、封装键和匿名 Cookie 均为自造值；真实响应的临时封装参数不进入仓库。
