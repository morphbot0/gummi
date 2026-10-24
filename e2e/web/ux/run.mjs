// Runs the UX tour and writes ux-out/INDEX.md, the page a reviewer reads
// first: per journey and viewport, every step with its screenshot, what it
// cost to get there and what the probe found; then the probe's findings
// gathered across every screen, most often seen first.
//
//   node ux/run.mjs                     # every journey, every viewport
//   node ux/run.mjs --project=phone     # arguments pass to playwright test
//   node ux/run.mjs --index-only        # rebuild INDEX.md from ux-out/
//
// Playwright runs as a child whose output goes to ux-out/run.log: under a
// snap-confined node its own stdout is lost.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const suite = path.dirname(here);
const out = path.join(suite, 'ux-out');
const args = process.argv.slice(2);

fs.mkdirSync(out, { recursive: true });
let status = 0;
if (!args.includes('--index-only')) {
  const r = spawnSync(process.execPath, [path.join(suite, 'node_modules/@playwright/test/cli.js'), 'test', '-c', 'ux.config.ts', ...args], {
    cwd: suite, encoding: 'utf8', maxBuffer: 1 << 28,
  });
  fs.writeFileSync(path.join(out, 'run.log'), `${r.stdout || ''}\n--stderr--\n${r.stderr || ''}\nexit ${r.status} ${r.error || ''}\n`);
  status = r.status ?? 1;
}

const journeys = [];
for (const project of fs.existsSync(out) ? fs.readdirSync(out) : []) {
  const pdir = path.join(out, project);
  if (!fs.statSync(pdir).isDirectory()) continue;
  for (const j of fs.readdirSync(pdir)) {
    const f = path.join(pdir, j, 'journey.json');
    if (fs.existsSync(f)) journeys.push(JSON.parse(fs.readFileSync(f, 'utf8')));
  }
}
journeys.sort((a, b) => a.journey.localeCompare(b.journey) || a.project.localeCompare(b.project));

const KINDS = ['clipped', 'smallTargets', 'unnamed', 'covered', 'lowContrast', 'leaks'];
const seen = Object.fromEntries(KINDS.map((k) => [k, new Map()]));
const lines = ['# gummi web — UX tour', '', `${journeys.length} journeys. Screens are under \`ux-out/<project>/<journey>/\`; each step's full probe (with the head, dock and surface text) is in that folder's \`journey.json\`.`, ''];

lines.push('## Journeys', '');
lines.push('| journey | viewport | steps | actions | seconds | blocked |', '|---|---|---|---|---|---|');
for (const j of journeys) lines.push(`| ${j.journey} | ${j.project} | ${j.steps.length} | ${j.actions} | ${j.seconds} | ${j.blocked ? j.blocked.replace(/\|/g, '/') : ''} |`);
lines.push('');

for (const j of journeys) {
  lines.push(`## ${j.journey} · ${j.project}`, '', `Goal: ${j.goal}`, '');
  if (j.blocked) lines.push(`**Blocked:** ${j.blocked}`, '');
  for (const s of j.steps) {
    const p = s.probe;
    const rel = `${j.project}/${j.journey}/${s.file}`;
    lines.push(`### ${s.n}. ${s.name} — ${s.actions} actions`, '', `${s.intent}`, '', `Screen: \`${rel}\``);
    if (p.primaries.length) lines.push(`- primary buttons: ${p.primaries.join(', ')}`);
    else lines.push('- primary buttons: none');
    if (p.sideways) lines.push('- the page scrolls sideways');
    for (const k of KINDS) {
      for (const x of p[k] || []) {
        const m = seen[k];
        const e = m.get(x) || { n: 0, where: [] };
        e.n++; if (e.where.length < 4) e.where.push(rel);
        m.set(x, e);
      }
      if (p[k]?.length) lines.push(`- ${k}: ${p[k].length}`);
    }
    if (s.errors.length) lines.push(`- console errors: ${s.errors.join(' · ')}`);
    lines.push('');
  }
}

const TITLES = { clipped: 'Text cut off by its box', smallTargets: 'Targets under 24px (44px on a phone)', unnamed: 'Controls with no name', covered: 'Controls covered by something else', lowContrast: 'Text under WCAG AA contrast', leaks: 'Template leaks' };
lines.push('## What the probe found, across every screen', '');
for (const k of KINDS) {
  const rows = [...seen[k].entries()].sort((a, b) => b[1].n - a[1].n);
  if (!rows.length) continue;
  lines.push(`### ${TITLES[k]} (${rows.length})`, '');
  for (const [x, e] of rows.slice(0, 40)) lines.push(`- ${e.n}× ${x} — e.g. \`${e.where[0]}\``);
  lines.push('');
}

fs.writeFileSync(path.join(out, 'INDEX.md'), lines.join('\n'));
const blocked = journeys.filter((j) => j.blocked).length;
console.log(`ux tour: ${journeys.length} journeys, ${blocked} blocked, playwright exit ${status}; read ${path.join(out, 'INDEX.md')}`);
process.exit(journeys.length ? 0 : 1);
