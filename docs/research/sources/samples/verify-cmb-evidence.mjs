import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const load = async (name) => JSON.parse(await readFile(
  new URL('./' + name, import.meta.url),
  'utf8',
));

const firstFiles = await Promise.all([
  load('cmb-2026-10-14-notices.json'),
  load('cmb-2026-10-14-jobs-run1.json'),
  load('cmb-2026-10-14-job-details.json'),
]);
const second = await load('cmb-2026-10-14-run2.json');
const recorded = await load('cmb-2026-10-14-comparison.json');
const firstByLabel = new Map(firstFiles.flatMap((file) => file.observations)
  .map((entry) => [entry.label, entry]));
const secondByLabel = new Map(second.observations.map((entry) => [entry.label, entry]));

assert.equal(recorded.comparisons.length, 10);
for (const comparison of recorded.comparisons) {
  const first = firstByLabel.get(comparison.firstLabel);
  const repeat = secondByLabel.get(comparison.repeatLabel);
  assert.ok(first);
  assert.ok(repeat);
  assert.equal(first.httpStatus, 200);
  assert.equal(repeat.httpStatus, 200);
  assert.match(first.rawResponseSHA256, /^[a-f0-9]{64}$/);
  assert.equal(first.rawResponseSHA256, repeat.rawResponseSHA256);
}

const rows = (entry) => {
  assert.equal(entry.response.returnCode, 'SUC0000');
  assert.ok(Array.isArray(entry.response.body.data));
  return entry.response.body.data;
};
const firstIntern = [
  ...rows(firstByLabel.get('intern-jobs-run1')),
  ...rows(firstByLabel.get('intern-jobs-page2-run1')),
];
const secondIntern = [
  ...rows(secondByLabel.get('intern-jobs-page1-run2')),
  ...rows(secondByLabel.get('intern-jobs-page2-run2')),
];
const ids = (items) => items.map((item) => item.publishGID).sort();
assert.equal(firstIntern.length, 19);
assert.equal(new Set(ids(firstIntern)).size, 19);
assert.deepEqual(ids(firstIntern), ids(secondIntern));
assert.equal(Number(firstByLabel.get('intern-jobs-run1').response.body.total), 19);
assert.equal(Number(secondByLabel.get('intern-jobs-page1-run2').response.body.total), 19);

for (const [map, label] of [
  [firstByLabel, 'school-jobs-run1'],
  [secondByLabel, 'school-jobs-run2'],
]) {
  const entry = map.get(label);
  assert.deepEqual(rows(entry), []);
  assert.equal(Number(entry.response.body.total), 0);
}

for (const label of ['job-data-intern', 'job-digital-intern']) {
  const entry = firstByLabel.get(label);
  assert.equal(entry.response.returnCode, 'SUC0000');
  const content = JSON.stringify(entry.response.body);
  assert.match(content, /2026-12-31/);
  assert.match(content, /2027/);
}
const expired = firstByLabel.get('job-school-old');
assert.equal(expired.response.returnCode, 'EZPREC0001');
assert.match(expired.response.errorMsg, /该职位已过期/);

console.log(JSON.stringify({
  checks: 'passed',
  repeatedResponseComparisons: recorded.comparisons.length,
  internshipCount: firstIntern.length,
  schoolListValidEmpty: true,
  technicalInternSamples: 2,
  explicitExpiredJob: true,
}, null, 2));
