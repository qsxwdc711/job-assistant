import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
const evidence=JSON.parse(await fs.readFile(new URL('./2026-10-16-evidence.json',import.meta.url),'utf8'));
const allIds=new Set();let checks=0;
const check=(condition,message)=>{assert.ok(condition,message);checks++};
for(const batch of evidence.tencent.batches){
 const positions=batch.positions.toSorted((a,b)=>a.postId.localeCompare(b.postId));
 const hash=crypto.createHash('sha256').update(JSON.stringify(positions)).digest('hex');
 check(batch.reads.length===2,'Two independent Tencent reads required');
 for(const read of batch.reads){
  const ids=read.pages.flatMap(page=>page.ids);
  check(read.normalized_sha256===hash,'Selected Tencent content hash changed');
  check(read.total===ids.length&&ids.length===batch.positions.length&&new Set(ids).size===ids.length,'Incomplete or repeated Tencent page');
  check(read.scope.workCountryType===1&&read.pages.every(page=>page.status===200&&page.business_status===0&&page.count===read.total),'Tencent scope or status changed');
  check(JSON.stringify([...ids].sort())===JSON.stringify(positions.map(job=>job.postId).sort()),'Page identities differ from selected content');
 }
 for(const position of positions){assert.equal(typeof position.postId,'string');assert.ok(!allIds.has(position.postId),'Cross-batch duplicate');allIds.add(position.postId)}
 check(batch.implementation_scope===(batch.mapping_id===20?'research_only':'implemented'),'Special internship scope changed');
}
check(allIds.size===521,'Research identity count changed');
check(evidence.huawei.reads.length===2,'Two independent Huawei reads required');
for(const read of evidence.huawei.reads){
 check(read.status===200&&read.business_status==='SUCCESS'&&read.total===31&&new Set(read.ids).size===31,'Huawei complete read changed');
 check(read.input.jobType==='CR'&&JSON.stringify(read.input.recruitmentType)==='["INTERN"]','Huawei internship scope changed');
}
check(JSON.stringify(evidence.huawei.reads[0].ids)===JSON.stringify(evidence.huawei.reads[1].ids),'Huawei identity set changed between reads');
check(evidence.tencent.detail_samples.length===4&&evidence.huawei.detail_samples.length===3,'Missing technical or nontechnical control');
console.log(`PASS: ${checks} internship evidence checks; Tencent 521 research IDs, Huawei two complete 31-ID reads.`);
