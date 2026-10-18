INSERT INTO companies(code,name,industry,basis) VALUES
('nsfocus','绿盟科技','technology','官方招聘链：https://www.nsfocus.com.cn/campus/ -> https://app.mokahr.com/campus_apply/nsfocus/29118；行业为项目分类，企业性质及规模未核实'),
('shokz','韶音科技','technology','官方招聘链：https://shokz.com.cn/ -> https://app.mokahr.com/apply/aftershokzhr/40779 -> campus-recruitment/aftershokzhr/36940；行业为项目分类，企业性质及规模未核实'),
('bluepoch','深蓝互动','games','官方招聘链：https://www.bluepoch.com/ -> https://app.mokahr.com/apply/blueinteractive/38433；行业为项目分类，企业性质及规模未核实');

INSERT INTO sources(code,company_code,name,official_url,coverage) VALUES
('nsfocus-campus','nsfocus','绿盟官方校园与实习招聘','https://app.mokahr.com/campus_apply/nsfocus/29118','完整校园站列表；仅计算机技术岗位，具体毕业条件以岗位原文为准'),
('shokz-campus','shokz','韶音官方校园与实习招聘','https://app.mokahr.com/campus-recruitment/aftershokzhr/36940','完整校园站列表；按职位区分全职和实习，排除硬件、美术、商务等未确认计算机技术方向'),
('bluepoch-careers','bluepoch','深蓝互动官方招聘','https://app.mokahr.com/apply/blueinteractive/38433','完整官方招聘站；仅计算机实习或明确接受应届/零至一年经验岗位，未披露经验社招不直接发布');
