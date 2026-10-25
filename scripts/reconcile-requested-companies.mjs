// Reconcile research with an explicitly captured public source-count snapshot.
// This script never connects to a database or infers missing jobs from an entrance.
import fs from 'node:fs/promises'
import path from 'node:path'

const root=process.cwd()
const folder=path.join(root,'docs/research/targets')
const requested=JSON.parse(await fs.readFile(path.join(folder,'2026-10-22-requested-companies.json'),'utf8'))
const files=['2026-10-22-auto-games-sources.json','2026-10-22-consumer-sources.json','2026-10-22-state-foreign-sources.json','2026-10-24-emerging-sources.json','2026-10-23-main-games-sources.json']
const records=new Map()
for(const file of files){
 const data=JSON.parse(await fs.readFile(path.join(folder,file),'utf8'))
 for(const entry of data.companies){
  if(records.has(entry.target_id))throw new Error(`duplicate research target: ${entry.target_id}`)
  records.set(entry.target_id,{...entry,research_file:`docs/research/targets/${file}`})
 }
}
const sourcesByName={
 '岚图汽车':'voyah-campus','文远知行':'weride-campus','恺英网络':'kingnet-campus','掌趣科技':'ourpalm-campus','巨人网络':'ztgame-campus','阳光电源':'sungrow-campus','三七互娱':'37games-campus','FunPlus':'funplus-campus','博世':'bosch-campus','安永':'ey-campus','拓竹科技':'bambulab-careers','智元机器人':'agibot-campus','影石创新':'insta360-campus','寒武纪':'cambricon-campus','英伟达':'nvidia-careers','名创优品':'miniso-campus','蜜雪冰城':'mxbc-campus','韶音科技':'shokz-campus'
}
let live={sources:[],captured_at:null}
if(process.argv[2])live=JSON.parse(await fs.readFile(path.resolve(root,process.argv[2]),'utf8'))
const liveByCode=new Map(live.sources.map(s=>[s.code,s]))
const seen=new Set()
const companies=requested.companies.map(target=>{
 if(seen.has(target.target_id))throw new Error(`duplicate requested target: ${target.target_id}`)
 seen.add(target.target_id)
 const research=records.get(target.target_id)
 if(!research||research.requested_name!==target.requested_name||research.requested_group!==target.requested_group||!research.status)throw new Error(`missing or mixed research identity: ${target.target_id}`)
 const sourceCode=sourcesByName[target.requested_name]||null
 const source=liveByCode.get(sourceCode)
 const count=source?Number(source.job_count):null
 if(count!==null&&(!Number.isInteger(count)||count<0))throw new Error(`invalid published count: ${sourceCode}`)
 let status='pending_job_validation'
 if(count>0)status='published'
 else if(source&&!source.enabled)status='registered_paused'
 else if(source?.last_status==='succeeded')status='verified_no_matching_roles'
 else if(/restricted|受限/.test(research.status))status='access_restricted'
 else if(/network|certificate|tls|error/.test(research.status))status='network_or_certificate_pending'
 const note=research.note||research.notes||research.next_action||research.limitations||'官方入口不等于岗位已开放或已录入，详情与资格仍待验证。'
 return {target_id:target.target_id,requested_name:target.requested_name,requested_group:target.requested_group,status,research_status:research.status,source_code:sourceCode,published_count:count,source_enabled:source?.enabled??null,source_last_status:source?.last_status??null,source_last_checked_at:source?.last_checked_at??null,official_website:research.official_website||research.official_url||null,recruitment_url:research.recruitment_url||research.career_entry_url||null,research_file:research.research_file,note}
})
if(companies.length!==219||records.size!==219||seen.size!==219)throw new Error('coverage must contain exactly all 219 requested targets')
const counts={};for(const entry of companies)counts[entry.status]=(counts[entry.status]||0)+1
const result={requested_at:requested.requested_at,reconciled_at:new Date().toISOString(),database_snapshot_at:live.captured_at,scope:requested.scope,group_labels_are_user_categories:true,coverage:{requested:219,reconciled:companies.length,status_counts:counts},limitations:['Published counts are a timestamped database snapshot, not a promise that every listed company currently has matching jobs.','Pending or access-limited research never means the company has no jobs.','Parent-company jobs do not stand in for Timi T1 or Huawei car BU.','Company nature and size are never inferred from the user groups.'],companies}
await fs.writeFile(path.join(folder,'2026-10-24-company-coverage.json'),JSON.stringify(result,null,2)+'\n')
const labels={published:'已发布',registered_paused:'已注册，保持暂停',verified_no_matching_roles:'完整核验，本次未筛出',access_restricted:'访问受限',network_or_certificate_pending:'网络或证书待复验',pending_job_validation:'岗位或资格待核验'}
const clean=x=>String(x??'').replaceAll('|','／').replaceAll('\n',' ')
const lines=['# 219 家目标企业覆盖对账','',`对账时间：${result.reconciled_at}；数据库快照：${live.captured_at||'尚未取得，不宣称已发布'}。`,'','岗位范围：计算机技术校招、实习，以及明确接受应届或最低工作经验不超过一年的岗位。','用户原分组保留作为查找类别，不据此设置企业性质或规模。待核验、网络问题与访问受限均不表示没有岗位。','','| 原分组 | 企业 | 状态 | 已发布岗位 | 来源 |','|---|---|---|---:|---|']
for(const r of companies)lines.push(`| ${clean(r.requested_group)} | ${clean(r.requested_name)} | ${labels[r.status]} | ${r.published_count??'待对账'} | ${r.recruitment_url?`[官方入口](${r.recruitment_url})`:'入口待核验'} |`)
await fs.writeFile(path.join(folder,'2026-10-24-company-coverage.md'),lines.join('\n')+'\n')
console.log(JSON.stringify(result.coverage))
