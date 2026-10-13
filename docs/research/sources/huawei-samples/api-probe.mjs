// Public recruitment read endpoints identified in the official page scripts.
// No Cookie, no account data, no application submission endpoints.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
const [suffix, name, input] = process.argv.slice(2);
if (!suffix?.startsWith('/recruitmentPosition/pub/') || !/^[a-z0-9-]+$/.test(name)) throw new Error('public recruitment endpoint and safe sample name required');
const url = `https://apigw-dgg-b0.huawei.com/api/apig/channelhw${suffix}?X-HW-ID=app_000000035886`;
const requestBody = JSON.parse(input);
const headers = { 'Content-Type': 'application/json;charset=UTF-8', 'X-HW-ID': 'app_000000035886',
  'x-jalor-tenantAlias': 'hcm', 'x-language': 'zh_CN', 'x-Referer': 'https://career.huawei.com/cn',
  'x-alb-gray': 'prod', Origin: 'https://career.huawei.com', Referer: 'https://career.huawei.com/cn/campus-recruitment-job-list?recruitmentType=FRESH_GRADUATE' };
const startedAt = new Date().toISOString(); const started = performance.now();
const response = await fetch(url, {method: 'POST', headers, body: JSON.stringify(requestBody), signal: AbortSignal.timeout(45000)});
const body = await response.text();
const metadata = { startedAt, finishedAt: new Date().toISOString(), elapsedMs: Math.round(performance.now()-started),
  requestUrl: url, finalUrl: response.url, method: 'POST', requestBody, status: response.status,
  contentType: response.headers.get('content-type'), bytes: Buffer.byteLength(body),
  sha256: crypto.createHash('sha256').update(body).digest('hex'), responseDate: response.headers.get('date') };
const directory = path.dirname(fileURLToPath(import.meta.url));
fs.writeFileSync(path.join(directory, `${name}.body.txt`), body);
fs.writeFileSync(path.join(directory, `${name}.meta.json`), JSON.stringify(metadata,null,2)+'\n');
let result; try { result=JSON.parse(body); } catch { result=body.slice(0,2000); }
let summary=result;
if (Array.isArray(result?.data)) summary={status:result.status,count:result.data.length,
  records:result.data.map(x=>({positionIntentionId:x.positionIntentionId,positionIntention:x.positionIntention,
    externalPostCode:x.externalPostCode,jobId:x.jobId,jobPlaceName:x.jobPlaceName,
    jobResponsibilities:x.jobResponsibilities,jobDemand:x.jobDemand})),errors:result.errors};
else if(Array.isArray(result?.data?.result)) summary={status:result.status,pageVO:result.data.pageVO,
  records:result.data.result.map(x=>({advertisementId:x.advertisementId,jobId:x.jobId,jobName:x.jobName,
    workPlace:x.workPlace,scenarioName:x.scenarioName,lastUpdateDate:x.lastUpdateDate})),errors:result.errors};
console.log(JSON.stringify({metadata,result:summary},null,2));
