import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Offline evidence checks. An optional raw directory also verifies captured bodies.
const base = new URL('./', import.meta.url);
const evidence = JSON.parse(await readFile(new URL('2026-10-15-evidence.json', base), 'utf8'));
const reads = (await readFile(new URL('2026-10-15-reads.jsonl', base), 'utf8')).trim().split('\n').map(JSON.parse);
const latest = new Map(reads.map(read => [read.label, read]));
const sha256 = value => createHash('sha256').update(value).digest('hex');
let checks = 0;
function check(name, run) {
  run();
  checks++;
  console.log('PASS ' + name);
}
function successful(label) {
  const read = latest.get(label);
  assert(read, 'Missing read: ' + label);
  assert.equal(read.status, 200, label);
  assert.equal(read.curl_exit, 0, label);
  assert(read.bytes > 0, label);
  return read;
}
function ids(round) { return round.pages.flatMap(page => page.ids); }

check('research leaves production publication at zero', () => {
  assert.equal(evidence.production_sources_added, 0);
  assert.equal(evidence.production_jobs_published, 0);
  assert(evidence.samples.every(sample => sample.publication_status === 'research_only'));
});
check('request audit includes unsuccessful reads', () => {
  assert.equal(evidence.read_statistics.attempts, reads.length);
  assert.equal(evidence.read_statistics.http_200, reads.filter(x => x.status === 200 && x.curl_exit === 0).length);
  assert.equal(evidence.read_statistics.transport_failures, reads.filter(x => x.curl_exit !== 0).length);
  assert(evidence.read_statistics.transport_failures > 0);
  assert(reads.every(x => /^[a-f0-9]{64}$/.test(x.sha256)));
  assert(reads.every(x => ['GET', 'POST'].includes(x.method)));
  assert(reads.every(x => new URL(x.requested_url).protocol === 'https:'));
});
check('NCSS two accessible snapshots preserve 50 identities', () => {
  const ncss = evidence.sources.ncss;
  assert.equal(ncss.rounds.length, 2);
  assert.equal(ncss.full_source_complete, false);
  for (const round of ncss.rounds) {
    assert.equal(round.pages.length, 5);
    assert.equal(new Set(ids(round)).size, 50);
    for (const page of round.pages) { successful(page.read); assert.equal(page.count, page.ids.length); }
  }
  assert.deepEqual(ids(ncss.rounds[0]), ids(ncss.rounds[1]));
});
check('NCSS HTTP 200 login wall remains a business failure', () => {
  const blocked = evidence.sources.ncss.restricted_response;
  successful(blocked.read);
  assert.equal(blocked.business_flag, false);
  assert.match(blocked.message, /登录/);
});
check('NCSS inconsistent empty response is not valid empty', () => {
  const empty = evidence.sources.ncss.empty_control;
  successful(empty.read);
  assert.equal(empty.count, 0);
  assert(empty.total_reported > 0);
  assert.equal(empty.classification, 'ambiguous_empty');
});
check('three Moka tenants have independently repeated complete lists', () => {
  const moka = evidence.sources.moka;
  assert.equal(moka.sites.length, 3);
  let count = 0;
  for (const site of moka.sites) {
    assert.equal(site.rounds.length, 2);
    for (const round of site.rounds) {
      const all = ids(round);
      assert.equal(new Set(all).size, all.length, site.tenant);
      assert.equal(all.length, round.pages[0].total_reported, site.tenant);
      for (const page of round.pages) {
        successful(page.read);
        assert.equal(page.count, page.ids.length);
        assert.equal(page.total_reported, all.length);
      }
    }
    assert.deepEqual(ids(site.rounds[0]).sort(), ids(site.rounds[1]).sort(), site.tenant);
    assert.equal(site.rounds[0].normalized_sha256, site.rounds[1].normalized_sha256, site.tenant);
    count += ids(site.rounds[0]).length;
  }
  assert.equal(count, moka.total_advertisements);
  assert.equal(count, 443);
});
check('Moka official link chains have successful source captures', () => {
  for (const site of evidence.sources.moka.sites) {
    assert(site.authority_reads.length > 0);
    site.authority_reads.forEach(successful);
  }
});
check('Moka no-match controls are successful zero totals', () => {
  for (const site of evidence.sources.moka.sites) {
    const empty = site.empty_control;
    successful(empty.read);
    assert.equal(empty.success, true);
    assert.equal(empty.code, 0);
    assert.equal(empty.total, 0);
    assert.equal(empty.count, 0);
  }
});
check('three technical details and nontechnical control were reread', () => {
  const samples = evidence.samples.filter(x => x.source === 'moka' && x.repeated_identity_and_description_equal);
  assert(samples.filter(x => x.direction !== 'nontechnical_control').length >= 3);
  assert(samples.some(x => x.direction === 'nontechnical_control'));
  samples.forEach(sample => sample.reads.forEach(successful));
  assert(samples.every(x => x.conditions.graduation_window === null));
});
check('small-company samples preserve evidence strength and unresolved fields', () => {
  const samples = evidence.samples.filter(x => x.source === 'ncss');
  assert.equal(samples.length, 4);
  assert(samples.some(x => x.scale === '1-49人'));
  assert(samples.some(x => x.review === 'conflicting_employment_type'));
  assert(samples.some(x => x.review === 'insufficient_job_description'));
  assert(samples.some(x => x.direction === 'game_art_control'));
  assert(samples.every(x => x.official_application_url === null));
  samples.forEach(sample => sample.reads.forEach(successful));
});
check('university keyword result is an 11-notice scope, not all employers', () => {
  const source = evidence.sources.universities.sustech;
  assert.equal(source.full_school_complete, false);
  for (const round of source.rounds) {
    successful(round.read); successful(round.count_read);
    assert.equal(round.count, 11);
    assert.equal(round.records.length, round.count);
    assert.equal(new Set(round.records.map(x => x.id)).size, round.count);
  }
  assert.deepEqual(source.rounds[0].records, source.rounds[1].records);
});
check('university no-match is distinct from expired detail', () => {
  const source = evidence.sources.universities.sustech;
  successful(source.empty_control.count_read); successful(source.empty_control.list_read);
  assert.equal(source.empty_control.total, 0);
  assert.equal(source.empty_control.count, 0);
  assert.equal(source.empty_control.code, 1);
  successful(source.expired_control.read);
  assert.match(source.expired_control.message, /不存在或已过期/);
  assert.equal(source.expired_control.classification, 'unavailable_or_expired_not_empty');
});
check('historical event counters do not become live opportunities', () => {
  const source = evidence.sources.universities.hati;
  assert.equal(source.event_state, 'ended');
  source.pages.forEach(page => successful(page.read));
  const ids = source.pages.flatMap(page => page.company_ids);
  assert.equal(new Set(ids).size, source.visible_company_ids);
  assert.equal(source.visible_company_ids, 103);
  assert.notEqual(source.visible_company_ids, source.reported_participating_companies);
  assert.equal(source.full_event_complete, false);
  assert.equal(source.counter_matches_visible_ids, false);
});
check('unresolved closure and sustained stability remain unverified', () => {
  assert.equal(evidence.sources.moka.closed_job_control_verified, false);
  assert.equal(evidence.checks.production_continuous_stability_verified, false);
  assert.equal(evidence.sources.moka.customer_api_is_public_directory, false);
});
check('sanitized evidence excludes session and customer credentials', () => {
  const forbidden = new Set(['cookie', 'authorization', 'password', 'token', 'necromancer', 'aesiv', 'apikey']);
  const walk = value => {
    if (!value || typeof value !== 'object') return;
    for (const [key, child] of Object.entries(value)) {
      assert(!forbidden.has(key.toLowerCase()), 'Unexpected credential field: ' + key);
      walk(child);
    }
  };
  walk(evidence); walk(reads);
});

if (process.argv[2]) {
  const directory = resolve(process.argv[2]);
  let bodies = 0;
  for (const read of latest.values()) {
    if (read.status !== 200 || read.curl_exit !== 0) continue;
    const body = await readFile(resolve(directory, read.label + '.body'));
    assert.equal(sha256(body), read.sha256, 'Captured body hash: ' + read.label);
    assert.equal(body.length, read.bytes, 'Captured byte count: ' + read.label);
    bodies++;
  }
  const body = async label => readFile(resolve(directory, label + '.body'), 'utf8');
  assert((await body('nsfocus-official-campus')).includes('https://app.mokahr.com/campus_apply/nsfocus/29118'));
  assert((await body('shokz-official')).includes('https://app.mokahr.com/apply/aftershokzhr/40779'));
  assert((await body('shokz-official-social')).includes('aftershokzhr/36940'));
  assert((await body('bluepoch-official')).includes('https://app.mokahr.com/apply/blueinteractive/38433'));
  console.log('PASS ' + bodies + ' captured body hashes and four official authority links');
  checks++;
}
console.log(JSON.stringify({ evidence: fileURLToPath(new URL('2026-10-15-evidence.json', base)), checks, result: 'passed' }));
