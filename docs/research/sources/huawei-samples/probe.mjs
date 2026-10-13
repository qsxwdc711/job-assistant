// Public, read-only source-validation helper. Never sends login cookies.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

const [url, name] = process.argv.slice(2);
if (!url || !name || !/^[a-z0-9-]+$/.test(name)) throw new Error('url and safe sample name required');
const startedAt = new Date().toISOString();
const started = performance.now();
const response = await fetch(url, { signal: AbortSignal.timeout(45000), redirect: 'follow' });
const body = await response.text();
const metadata = {
  startedAt, finishedAt: new Date().toISOString(), elapsedMs: Math.round(performance.now() - started),
  requestUrl: url, finalUrl: response.url, status: response.status,
  contentType: response.headers.get('content-type'), bytes: Buffer.byteLength(body),
  sha256: crypto.createHash('sha256').update(body).digest('hex'),
  responseDate: response.headers.get('date'), cacheAge: response.headers.get('age'),
};
const directory = path.dirname(new URL(import.meta.url).pathname.replace(/^\/(.:)/, '$1'));
const outputDirectory = decodeURIComponent(directory);
fs.writeFileSync(path.join(outputDirectory, `${name}.body.txt`), body);
fs.writeFileSync(path.join(outputDirectory, `${name}.meta.json`), JSON.stringify(metadata, null, 2) + '\n');
const scripts = [...body.matchAll(/<script\b[^>]*\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi)].map(m => m[1] ?? m[2] ?? m[3]);
const links = [...new Set([...body.matchAll(/(?:href|url|searchPageUrl|detailsPageUrl)\s*[:=]\s*"([^"]+)"/gi)].map(m => m[1]).filter(s => /career|recruit|job|join|campus/.test(s)))];
const configs = [...body.matchAll(/<script\b[^>]*\bid="(comData_[^"]+)"[^>]*>([\s\S]*?)<\/script>/gi)].map(m => {
  try { return { id: m[1], data: JSON.parse(m[2]) }; } catch { return {id: m[1], parseError: true}; }
});
console.log(JSON.stringify({metadata, title: body.match(/<title>([\s\S]*?)<\/title>/i)?.[1], scripts, links,
  componentPaths: body.match(/window\.\$devPath\s*=\s*({[^;]+})/)?.[1],
  configs: configs.filter(x => x.data && (x.data.freshStudentData || x.data.listInfo || x.data.filterData || x.data.jobDetails || x.data.detailsInfo)).slice(0, 5)
}, null, 2));
