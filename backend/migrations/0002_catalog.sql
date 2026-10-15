CREATE TABLE dictionary_entries (
    kind text NOT NULL, code text NOT NULL, name text NOT NULL,
    PRIMARY KEY(kind, code)
);
INSERT INTO dictionary_entries(kind,code,name) VALUES
('direction','backend','后端开发'),('direction','frontend','前端开发'),('direction','mobile','移动端 / 客户端'),
('direction','embedded','嵌入式'),('direction','test','测试'),('direction','operations_sre','运维 / SRE'),
('direction','security','网络安全'),('direction','data','数据'),('direction','algorithm_ai','算法 / AI'),
('major','computer_science','计算机科学与技术'),('major','software_engineering','软件工程'),
('major','communications','通信工程'),('major','electronics','电子类'),('major','mathematics','数学类'),
('major','information_technology','信息技术'),('major','artificial_intelligence','人工智能'),
('nature','central_soe','央企'),('nature','state_owned','国企'),('nature','public_institution','事业单位'),
('nature','foreign','外企'),('nature','private','民企'),('nature','unknown','未核实'),
('tier','top','一线大厂'),('tier','less_known_large','冷门大厂'),('tier','leader','行业翘楚'),
('tier','mid','腰部名企'),('tier','small','中小厂'),
('industry','internet','互联网'),('industry','finance','银行 / 金融'),('industry','technology','科技'),
('industry','ecommerce','电商'),('industry','cross_border','跨境'),('industry','games','游戏'),
('industry','consumer','消费'),('industry','brand','品牌'),('industry','beauty','美妆'),('industry','media','传媒'),
('industry','live','直播'),('industry','biopharma','生物医药'),('industry','manufacturing','制造业'),
('industry','education','教育'),('industry','property','地产'),('industry','construction','建筑'),
('industry','mobility','出行'),('industry','travel','旅游'),('industry','hotel','酒店'),
('industry','logistics','物流'),('industry','supply_chain','供应链'),('industry','professional_services','专业服务'),
('industry','unknown','未核实');

CREATE TABLE companies (
    code text PRIMARY KEY, name text NOT NULL, industry text NOT NULL DEFAULT 'unknown',
    nature text NOT NULL DEFAULT 'unknown', tiers text[] NOT NULL DEFAULT '{}', basis text NOT NULL DEFAULT ''
);
INSERT INTO companies(code,name,industry,basis) VALUES
('tencent','腾讯','internet','公司官网 https://www.tencent.com/；行业标签为项目分类'),
('huawei','华为','technology','公司官网 https://www.huawei.com/cn/；行业标签为项目分类'),
('cmb','招商银行','finance','公司官网 https://www.cmbchina.com/；行业标签为项目分类');

CREATE TABLE sources (
    code text PRIMARY KEY, company_code text NOT NULL REFERENCES companies(code), name text NOT NULL,
    official_url text NOT NULL, coverage text NOT NULL, enabled boolean NOT NULL DEFAULT true,
    last_status text NOT NULL DEFAULT 'pending', last_error text NOT NULL DEFAULT '',
    last_checked_at timestamptz, last_succeeded_at timestamptz, last_success_started_at timestamptz
);
INSERT INTO sources(code,company_code,name,official_url,coverage) VALUES
('tencent-campus','tencent','腾讯官方校园招聘','https://join.qq.com/','普通应届生批次 / 计算机相关技术岗位'),
('huawei-campus','huawei','华为官方校园招聘','https://career.huawei.com/cn/campus-recruitment','应届生 / 计算机相关广告与岗位意向'),
('cmb-campus','cmb','招商银行官方校园与实习招聘','https://career.cmbchina.com/campus/home','校园与实习 / 计算机相关岗位；公告不代替开放岗位');

CREATE TABLE campaigns (
    source_code text NOT NULL REFERENCES sources(code), campaign_key text NOT NULL, name text NOT NULL,
    graduation_rules jsonb NOT NULL DEFAULT '[]', evidence text NOT NULL DEFAULT '',
    PRIMARY KEY(source_code,campaign_key)
);
CREATE TABLE ingest_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), source_code text NOT NULL REFERENCES sources(code),
    status text NOT NULL DEFAULT 'running', started_at timestamptz NOT NULL, finished_at timestamptz,
    fetched_count integer NOT NULL DEFAULT 0, published_count integer NOT NULL DEFAULT 0,
    changed_count integer NOT NULL DEFAULT 0, error_code text NOT NULL DEFAULT '',
    evidence jsonb NOT NULL DEFAULT '[]'
);
ALTER TABLE sources ADD COLUMN current_run_id uuid REFERENCES ingest_runs(id);
CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), company_code text NOT NULL REFERENCES companies(code),
    source_code text NOT NULL REFERENCES sources(code), campaign_key text NOT NULL, external_id text NOT NULL,
    title text NOT NULL, recruitment_type text NOT NULL CHECK(recruitment_type IN ('campus','internship','early_career')),
    public_status text NOT NULL CHECK(public_status IN ('open','closed','unknown')),
    written_test text NOT NULL CHECK(written_test IN ('yes','no','unknown')),
    deadline_date date, deadline_at timestamptz, data jsonb NOT NULL, content_hash text NOT NULL,
    version bigint NOT NULL DEFAULT 1, published boolean NOT NULL DEFAULT true,
    review_note text NOT NULL DEFAULT '', verification_status text NOT NULL DEFAULT 'verified',
    verified_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
    UNIQUE(source_code,campaign_key,external_id),
    FOREIGN KEY(source_code,campaign_key) REFERENCES campaigns(source_code,campaign_key)
);
CREATE INDEX jobs_updates ON jobs(updated_at DESC,id);
CREATE INDEX jobs_source ON jobs(source_code);
CREATE TABLE job_versions (
    job_id uuid NOT NULL REFERENCES jobs(id), version bigint NOT NULL, content_hash text NOT NULL,
    data jsonb NOT NULL, evidence jsonb NOT NULL, created_at timestamptz NOT NULL,
    PRIMARY KEY(job_id,version)
);
CREATE TABLE job_variants (
    job_id uuid NOT NULL REFERENCES jobs(id), variant_key text NOT NULL, direction text NOT NULL,
    major_kind text NOT NULL, major_tags text[] NOT NULL DEFAULT '{}', data jsonb NOT NULL,
    PRIMARY KEY(job_id,variant_key)
);
CREATE INDEX variants_direction ON job_variants(direction);
CREATE TABLE job_locations (
    job_id uuid NOT NULL, variant_key text NOT NULL, code text NOT NULL, kind text NOT NULL,
    country text NOT NULL, province text NOT NULL, city text NOT NULL, raw text NOT NULL,
    PRIMARY KEY(job_id,variant_key,code,raw),
    FOREIGN KEY(job_id,variant_key) REFERENCES job_variants(job_id,variant_key) ON DELETE CASCADE
);
CREATE INDEX locations_city ON job_locations(city,kind);
CREATE TABLE job_tags (
    job_id uuid NOT NULL REFERENCES jobs(id), kind text NOT NULL, code text NOT NULL,
    PRIMARY KEY(job_id,kind,code)
);
