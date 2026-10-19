#!/usr/bin/env node
//
// Every key passed to a local guard must name a feature the company portal's
// own registry defines.
//
// The two vocabularies look alike — `company_profile.read` on the platform is
// `organization.read` here — and getting it wrong is silent in both directions:
// TypeScript sees `PermissionKey = string`, and at runtime an unknown feature
// reads as *disabled*, so the screen throws "module disabled" for everyone
// except an owner, who the super-admin bypass waves through. Four screens
// shipped that way before this check existed.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, '../apps/web-company/src');

const featureKeys = new Set();
for (const dir of readdirSync(join(ROOT, 'features'))) {
  const f = join(ROOT, 'features', dir, 'feature.ts');
  try {
    for (const m of readFileSync(f, 'utf8').matchAll(/key:\s*"([^"]+)"/g)) featureKeys.add(m[1]);
  } catch {}
}

const walk = (d) => readdirSync(d).flatMap((e) => {
  const p = join(d, e);
  return statSync(p).isDirectory() ? walk(p) : /\.tsx?$/.test(p) ? [p] : [];
});

const bad = [];
for (const file of walk(ROOT)) {
  if (file.includes('/lib/rbac/catalogue')) continue;
  const src = readFileSync(file, 'utf8');
  const re = /(?:requirePermission|requireAnyPermission|access\.can|access\.canAll|access\.canAny)\(\s*\[?\s*"([a-z0-9_.-]+)"/g;
  for (const m of src.matchAll(re)) {
    const feature = m[1].split('.')[0];
    if (!featureKeys.has(feature)) {
      const line = src.slice(0, m.index).split('\n').length;
      bad.push(`${file.replace(ROOT + '/', '')}:${line}  ${m[1]}  (no local feature "${feature}")`);
    }
  }
}

console.log(`${featureKeys.size} local features registered`);
if (bad.length) { bad.forEach((b) => console.log('  MISMATCH ' + b)); process.exit(1); }
console.log('every guarded key names a registered local feature');
