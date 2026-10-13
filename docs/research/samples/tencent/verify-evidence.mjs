import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const evidence = JSON.parse(await readFile(
  new URL('./2026-10-14-public-api-evidence.json', import.meta.url),
  'utf8',
));

assert.equal(evidence.sourceId, 'tencent');
assert.equal(evidence.runs.length, 2);
assert.equal(evidence.regularCampusAllRuns.length, 2);

const sortedIds = (run) => run.items.map((item) => {
  assert.equal(typeof item.postId, 'string', 'External IDs must preserve precision');
  return item.postId;
}).sort();

for (const [runs, pageSize] of [
  [evidence.runs, evidence.searchRequest.pageSize],
  [evidence.regularCampusAllRuns, 50],
]) {
  for (const run of runs) {
    assert.equal(run.paginationComplete, true);
    assert.equal(run.items.length, run.count);
    assert.equal(new Set(sortedIds(run)).size, run.count);
    assert.equal(run.pagesFetched, Math.ceil(run.count / pageSize));
  }
  assert.deepEqual(sortedIds(runs[0]), sortedIds(runs[1]));
}

assert.equal(evidence.samples.length, 3);
for (const sample of evidence.samples) {
  assert.equal(sample.details.length, 2);
  const selected = sample.details.map(({ round, ...fields }) => fields);
  assert.deepEqual(selected[0], selected[1]);
  assert.equal(selected[0].postId, sample.listEntry.postId);
  assert.ok(selected[0].requirements.trim().length > 0);
  assert.ok(selected[0].responsibilities.trim().length > 0);
  assert.ok(selected[0].workCityList.length > 0);
  assert.equal(selected[0].rawStatus, null);
  assert.equal(selected[0].hasExplicitDeadline, false);
}

assert.equal(evidence.emptyFilterCheck.businessStatus, 0);
assert.equal(evidence.emptyFilterCheck.count, 0);
assert.equal(evidence.emptyFilterCheck.items, null);
for (const request of evidence.requests) {
  assert.equal(request.httpStatus, 200);
  assert.equal(request.businessStatus, 0);
  assert.match(request.sha256, /^[a-f0-9]{64}$/);
}

const campaign = evidence.projectMapping.find((item) => item.recruitType === 1);
assert.ok(campaign);
const currentBatch = campaign.subProjectList.find((item) => item.mappingId === 1);
assert.equal(currentBatch.recruitYear, '2027');
assert.notEqual(campaign.recruitRangDesc, currentBatch.recruitRangDesc);

console.log(JSON.stringify({
  checks: 'passed',
  regularCampusCount: evidence.regularCampusAllRuns[0].count,
  technicalCategoryCount: evidence.regularCampusAllRuns[0].items
    .filter((item) => item.positionFamily === 2).length,
  developerKeywordCount: evidence.runs[0].count,
  repeatedDetailSamples: evidence.samples.length,
  requestRecords: evidence.requests.length,
  parentMenuConflictPreserved: true,
}, null, 2));
