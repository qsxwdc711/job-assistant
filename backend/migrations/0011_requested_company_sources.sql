-- New sources remain paused until their complete public snapshots and acceptance
-- checks pass. Registration is not a claim that jobs have already been imported.
INSERT INTO companies(code,name,industry,basis) VALUES
('voyah','岚图汽车','manufacturing','2026-10-22 岚图官方 https://m.dsmp.voyah.com.cn/joinus.html 直接链接 Moka 校招 voyah/146293。行业为项目分类；性质及规模未核实。'),
('weride','文远知行','technology','2026-10-22 https://zh.weride.ai/zh/careers 的中国校园招聘链接 jingchi/2137。行业为项目分类；性质及规模未核实。'),
('kingnet','恺英网络','games','2026-10-22 https://www.kingnet.com/other/contacts 直接链接 kingnet/2246 校招。行业为项目分类；性质及规模未核实。'),
('ourpalm','掌趣科技','games','2026-10-22 https://ourpalm.com/t2/471/6911.html 直接链接 ourpalm/43628；公开租户 orgId=playcrab。集团岗位的实际主体以详情为准。'),
('ztgame','巨人网络','games','2026-10-22 https://hr.ztgame.com/campus/ 直接链接 ztgame/92438 校招及实习。行业为项目分类；性质及规模未核实。'),
('sungrow','阳光电源','manufacturing','2026-10-22 https://cn.sungrowpower.com/recruitment.html 至 https://jobs.sungrowpower.com/，校园招聘链接 sungrow/94416。行业为项目分类；性质及规模未核实。'),
('37games','三七互娱','games','2026-10-22 企业招聘站 https://zhaopin.37.com/ 及 Moka 37/58016 的公开校招合同；具体主体以岗位详情为准。行业为项目分类；性质及规模未核实。'),
('funplus','FunPlus','games','2026-10-22 https://campus.funplus.com.cn/ 直接链接 funplus01/147931，应届和实习使用同一公开校招站。行业为项目分类；性质及规模未核实。'),
('bosch','博世','manufacturing','2026-10-22 博世中国官方招聘至 Moka bosch/168626。仅本次验证的招聘项目，不代表集团所有岗位；具体主体以详情为准。'),
('ey','安永','professional_services','2026-10-22 安永中国官方招聘至 Moka ey/166374。仅该项目已核实的技术岗位；性质及规模标签不根据用户分组推断。'),
('bambulab','拓竹科技','technology','2026-10-23 https://bambulab.cn/zh-cn/join-us 的校园招聘直接链接 https://bambulab.jobs.feishu.cn/campus ，面向应届和在校实习生。行业为项目分类；性质及规模未核实。'),
('nvidia','英伟达','technology','2026-10-22 企业官方招聘至 jobs.nvidia.com，再至 NVIDIA Workday 招聘站。仅中国地点的 Intern、New College Graduate 和有明确最多一年经验依据的 Regular 岗位；不代表全球全量。'),
('miniso','名创优品','consumer','2026-10-22 企业官网 https://miniso.cn/ 招聘链接 https://miniso.zhiye.com/ 。本次只接入公开校园类别2；性质及规模未核实。'),
('mxbc','蜜雪冰城','consumer','2026-10-22 企业官网 https://www.mxbc.com/about.html 投递简历链接 https://careers.mxbc.com/ 。本次只接入公开校园类别2；性质及规模未核实。'),
('agibot','智元机器人','technology','2026-10-24 https://www.agibot.com.cn/join_us 直接链接 agirobot.jobs.feishu.cn/campusrecruitment，已观测相同公开飞书列表合同。子公司标题与实际招聘主体保留原文，不据用户分组设置规模标签。'),
('insta360','影石创新','technology','2026-10-24 https://www.insta360.com/cn/jobs 直接链接 https://arashivision.jobs.feishu.cn/campus 。已匿名观察公开列表与站点身份；行业为项目分类，不据用户分组设置规模或性质。'),
('cambricon','寒武纪','technology','2026-10-24 https://www.cambricon.com/ 直接链接 Moka cambricon/44201；公开初始化确认中科寒武纪科技股份有限公司、org=cambricon、mode=campus。行业为项目分类；性质与规模未核实。');

INSERT INTO sources(code,company_code,name,official_url,coverage,enabled) VALUES
('voyah-campus','voyah','岚图汽车官方校园招聘','https://app.mokahr.com/campus-recruitment/voyah/146293','完整公开列表及同ID开放详情；计算机技术校招与明确实习，硬性工作经验超过一年或类型冲突排除。',false),
('weride-campus','weride','文远知行官方校园招聘','https://app.mokahr.com/campus_apply/jingchi/2137','中国校园站完整公开列表及详情；计算机技术校招与实习；不冒充全球所有岗位。',false),
('kingnet-campus','kingnet','恺英网络官方校园招聘','https://app.mokahr.com/campus_apply/kingnet/2246','完整公开列表及详情；标题、招聘类型、工作职责核实后发布计算机技术校招与实习。',false),
('ourpalm-campus','ourpalm','掌趣科技官方校园招聘','https://app.mokahr.com/campus-recruitment/ourpalm/43628','完整公开列表及详情；URL租户名ourpalm，实际组织playcrab；具体部门与招聘主体保留官方原文。',false),
('ztgame-campus','ztgame','巨人网络官方校园与实习招聘','https://app.mokahr.com/campus-recruitment/ztgame/92438','完整公开列表及详情；计算机技术校招及明确实习，硬性工作经验超过一年排除。',false),
('sungrow-campus','sungrow','阳光电源官方校园招聘','https://app.mokahr.com/campus-recruitment/sungrow/94416','完整公开列表及详情；计算机技术校招及实习；官方工作城市、学历和经验字段保留。',false),
('37games-campus','37games','三七互娱官方校园招聘','https://app.mokahr.com/campus-recruitment/37/58016','完整公开列表及详情；计算机技术方向；未知毕业日期与笔试条件保留未知。',false),
('funplus-campus','funplus','FunPlus官方校园与实习招聘','https://app.mokahr.com/campus-recruitment/funplus01/147931','完整公开列表及详情；应届与实习依据实际招聘类型和正文区分，非技术岗位排除。',false),
('bosch-campus','bosch','博世官方校园招聘项目','https://app.mokahr.com/campus-recruitment/bosch/168626','仅此租户项目完整列表及同ID详情；公开自定义学历、工作经验及主体字段纳入资格；超过一年硬经验排除。',false),
('ey-campus','ey','安永官方校园招聘项目','https://app.mokahr.com/campus-recruitment/ey/166374','仅此租户项目完整列表及详情；计算机技术职责校招与实习，不把审计或管理岗位视为技术岗。',false),
('bambulab-careers','bambulab','拓竹科技官方校园与实习招聘','https://bambulab.jobs.feishu.cn/campus','公开匿名会话及CSRF，完整列表分页和逐岗位开放详情核验；校招、实习，原文保留；不访问申请人数据。',false),
('nvidia-careers','nvidia','英伟达官方中国校园与实习招聘','https://nvidia.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite','中国地点原生Intern、New College Graduate、Regular三类分别完整分页；posted与canApply详情核验；Regular须明确接受应届或最多一年工作经验。',false),
('miniso-campus','miniso','名创优品官方校园招聘','https://miniso.zhiye.com/campus','北森公开校园类别2完整分页及GUID/数字ID同岗位详情核验；仅计算机实际技术职责；学历及工作经验保留。',false),
('mxbc-campus','mxbc','蜜雪冰城官方校园招聘','https://careers.mxbc.com/campus','北森公开校园类别2完整分页及同ID详情核验；标题与正文届数冲突的岗位排除，保留原文待人工确认。',false),
('agibot-campus','agibot','智元机器人官方校园招聘','https://agirobot.jobs.feishu.cn/campusrecruitment','官网直接授权校园站；匿名公开会话、完整列表与逐岗位详情；计算机技术岗位，子公司名称及要求保留原文。仅通过完整验证后启用。',false),
('insta360-campus','insta360','影石创新官方校园招聘','https://arashivision.jobs.feishu.cn/campus','官网直接授权校园站；匿名公开会话、完整列表与逐岗位开放详情核验；计算机技术校招与明确实习，非技术职责排除。仅通过完整验证后启用。',false),
('cambricon-campus','cambricon','寒武纪官方校园招聘','https://app.mokahr.com/campus-recruitment/cambricon/44201','官网直接授权的校园站；完整公开分页及同ID开放详情；计算机技术校招与明确实习，非技术与超过一年硬工作经验岗位排除。仅通过完整验证后启用。',false);
