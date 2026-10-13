// Offline evidence extraction and integrity checks; performs no network calls.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
const directory=path.dirname(fileURLToPath(import.meta.url));
const read=name=>fs.readFileSync(path.join(directory,name),'utf8');
const json=name=>JSON.parse(read(name));
const sha=text=>crypto.createHash('sha256').update(text).digest('hex');
const configurations=html=>[...html.matchAll(/<script\b[^>]*\bid="(comData_[^"]+)"[^>]*>([\s\S]*?)<\/script>/gi)].map(m=>({id:m[1],data:JSON.parse(m[2])}));
const configs=configurations(read('campus-round1.body.txt'));
const qualification=configs.find(x=>x.data.freshStudentData);
const questions=configs.find(x=>JSON.stringify(x.data).includes('研发类岗位：您需参加上机考试'));
const evidence={sourceId:'huawei',validatedDate:'2026-10-14',timezone:'Asia/Shanghai',
  qualification:{sourceUrl:'https://career.huawei.com/cn/campus-recruitment',componentId:qualification.id,
    campus:qualification.data.freshStudentData,intern:qualification.data.internsData},
  examFAQ:{sourceUrl:'https://career.huawei.com/cn/campus-recruitment',componentId:questions.id,data:questions.data}};
fs.writeFileSync(path.join(directory,'campaign-evidence.json'),JSON.stringify(evidence,null,2)+'\n');
const pairs=['campus','full-list','detail-api-36373','detail-api-36384','detail-api-36397','intent-api-103906','intent-api-103891','intent-api-103892'];
const comparison=pairs.map(prefix=>{
 const first=json(`${prefix}-round1.meta.json`),second=json(`${prefix}-round2.meta.json`);
 assert.equal(first.sha256,second.sha256,`${prefix} changed between rounds`);
 return {sample:prefix,firstStartedAt:first.startedAt,secondStartedAt:second.startedAt,firstElapsedMs:first.elapsedMs,
   secondElapsedMs:second.elapsedMs,firstSha256:first.sha256,secondSha256:second.sha256,bodyEqual:true};
});
const lists=[1,2].map(n=>json(`full-list-round${n}.body.txt`));
for(const list of lists){
 assert.equal(list.status,'SUCCESS');assert.equal(list.data.pageVO.totalPages,1);
 assert.equal(list.data.result.length,list.data.pageVO.totalRows);
 assert.equal(new Set(list.data.result.map(x=>x.advertisementId)).size,list.data.result.length);
}
assert.deepEqual(lists[0].data.result.map(x=>x.advertisementId).sort((a,b)=>a-b),lists[1].data.result.map(x=>x.advertisementId).sort((a,b)=>a-b));
const terminal=json('list-api-terminal-empty.body.txt');assert.equal(terminal.status,'SUCCESS');assert.equal(terminal.data.pageVO,null);assert.equal(terminal.data.result,null);
const sampleSelections=[[36373,103906,11394],[36384,103891,11585],[36397,103892,11480]];
const samples=sampleSelections.map(([advertisementId,jobId,positionIntentionId])=>{
 const detail=json(`detail-api-${advertisementId}-round1.body.txt`).data;
 const intent=json(`intent-api-${jobId}-round1.body.txt`).data.find(x=>x.positionIntentionId===positionIntentionId);
 const listing=lists[0].data.result.find(x=>x.advertisementId===advertisementId);
 assert.ok(listing);assert.equal(detail.jobId,jobId);assert.ok(intent);assert.equal(intent.jobId,jobId);
 assert.equal(detail.categoryName,'研发类');assert.ok(intent.jobResponsibilities&&!intent.jobResponsibilities.includes('请您详见'));
 return {sourceId:'huawei',sourceBatchLabel:'2027届应届生',batchLabelEvidence:'current campus landing-page text; API has no explicit year',
   advertisementId,jobId,positionIntentionId,externalPostCode:intent.externalPostCode,
   adIdentity:`huawei:2027-fresh:${advertisementId}`,directionIdentity:`huawei:2027-fresh:${advertisementId}:${positionIntentionId}`,
   company:'华为',legalRecruitingEntity:null,departmentScope:'business units and departments are not proven separate legal employers',
   jobName:detail.jobname,positionIntention:intent.positionIntention,recruitmentType:'campus-fulltime',
   originalResponsibilitiesHTML:intent.jobResponsibilities,originalRequirementsHTML:intent.jobDemand,
   cities:intent.fullJobPlaceList.map(x=>x.jobPlaceName),originalLocations:intent.fullJobPlaceList,
   departmentLocationRelations:intent.deptAndPlaceList,minimumDegree:null,experienceRequirement:null,
   graduationRulesReference:'campaign-evidence.json#/qualification/campus/descList',
   writtenTest:'yes',writtenTestScope:'current campus fresh-graduate R&D category; official FAQ requires machine exam',
   officialDeadline:null,deadlineTimezone:null,status:'open',statusBasis:'present in successful current in-recruitment list, detail and direction available; actual submission untested',
   officialDetailUrl:`https://career.huawei.com/cn/job-details?advertisementId=${advertisementId}`,
   applyEntryUrl:`https://career.huawei.com/cn/job-details?advertisementId=${advertisementId}`,applyEntryLevel:'job-detail',
   applicationRequiresLogin:true,applicationVerification:'official page code directs anonymous users to Uniportal; no apply action performed',
   verifiedAt:json(`intent-api-${jobId}-round2.meta.json`).finishedAt,
   contentSha256:sha(JSON.stringify({jobName:detail.jobname,direction:intent.positionIntention,responsibilities:intent.jobResponsibilities,
     requirements:intent.jobDemand,locations:intent.fullJobPlaceList,departmentLocations:intent.deptAndPlaceList}))};
});
fs.writeFileSync(path.join(directory,'normalized-samples.json'),JSON.stringify(samples,null,2)+'\n');
const records=fs.readdirSync(directory).filter(f=>f.endsWith('.meta.json')).map(f=>({file:f,...json(f)}));
for(const record of records){assert.equal(record.status,200);assert.equal(sha(read(record.file.replace('.meta.json','.body.txt'))),record.sha256);}
const summary={version:1,totalSavedRequests:records.length,totalFetchElapsedMs:records.reduce((n,x)=>n+x.elapsedMs,0),
 totalResponseBytes:records.reduce((n,x)=>n+x.bytes,0),listCoverage:{rows:lists[0].data.pageVO.totalRows,totalPages:1,pageSize:100,uniqueAdvertisementIds:lists[0].data.result.map(x=>x.advertisementId),
   note:'71 is all fresh-graduate advertisements in the verified filter, not 71 computing jobs'},
 comparison,allBodiesMatchSavedMetadata:true,
 terminalEmpty:{status:'SUCCESS',pageVONull:true,resultNull:true,validOnlyAsTerminalAfterVerifiedFirstPage:true,
   initialEmptyNeedsFurtherVerification:true},
 offlineChecks:['saved response SHA-256 integrity','HTTP status','complete pagination count','unique ad IDs','independent round equality','real direction JD content','R&D classification','terminal null shape']};
fs.writeFileSync(path.join(directory,'verification-summary.json'),JSON.stringify(summary,null,2)+'\n');
console.log(JSON.stringify({savedRequests:summary.totalSavedRequests,responseBytes:summary.totalResponseBytes,
 fetchElapsedMs:summary.totalFetchElapsedMs,comparisons:comparison.length,rows:summary.listCoverage.rows,samples:samples.map(s=>({job:s.jobName,direction:s.positionIntention,key:s.directionIdentity})),checks:'passed'},null,2));
