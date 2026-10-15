import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from '../fixtures/test';
import type { Workspace } from '../fixtures/workspace';

// The skills view against a real `gummi web`: a pack seeded the way a
// curator run leaves it — one curator skill written in a commit of its
// own, published, and the ledger row naming the pack's tip before and
// after. A person pins and unpins the skill and reverts the run; with no
// curator configured, Run now is off. A person sets the curator up from the
// view, changes it, and turns it off again.

async function seedCuratorRun(ws: Workspace) {
  const pack = path.join(ws.repo, '.gummi', 'skills');
  await fs.promises.mkdir(path.join(pack, 'scoped-tests'), { recursive: true });
  const git = async (...args: string[]) => {
    const r = await ws.exec('git', ['-C', pack, '-c', 'user.name=gummi', '-c', 'user.email=gummi@localhost', '-c', 'commit.gpgsign=false', ...args]);
    if (r.code !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
    return r.stdout.trim();
  };
  await git('init', '-q', '-b', 'main');
  await git('commit', '-q', '--allow-empty', '-m', 'an empty skill pack');
  const before = await git('rev-parse', 'HEAD');
  await fs.promises.writeFile(path.join(pack, 'scoped-tests', 'SKILL.md'),
    '---\nname: scoped-tests\ndescription: Run the named tests and check they ran\nscope: general\nsource: curator\n---\n\nRun them, then count them.\n');
  await git('add', '-A');
  await git('commit', '-q', '-m', 'curator: write scoped-tests');
  const after = await git('rev-parse', 'HEAD');
  await git('update-ref', 'refs/gummi/published', 'HEAD');
  const now = new Date();
  const row = {
    id: 'cur-e2e', why: 'schedule', start: new Date(now.getTime() - 60_000).toISOString(), end: now.toISOString(),
    backend: 'headless', before, after, credits: 3, outcome: 'done', written: ['scoped-tests'],
  };
  await fs.promises.mkdir(path.join(ws.repo, '.gummi', 'state'), { recursive: true });
  await fs.promises.writeFile(path.join(ws.repo, '.gummi', 'state', 'curator.jsonl'), JSON.stringify(row) + '\n');
}

test.use({ seed: { run: seedCuratorRun } });

async function openSkills(page: Page) {
  await page.getByTestId('rail-more').click();
  await page.getByTestId('menu-skills').click();
  await expect(page.getByTestId('view-skills')).toBeVisible();
}

test('a curator skill is pinned and unpinned, and its run reverted', async ({ pairedPage: page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  await openSkills(page);

  // no curator configured: the view says so and Run now is off
  await expect(page.getByTestId('skills-config')).toContainText('Curator off');
  await expect(page.getByTestId('skills-run')).toBeDisabled();

  const skill = page.getByTestId('skill-scoped-tests');
  await expect(skill).toHaveAttribute('data-owner', 'curator');
  await expect(page.getByTestId('skill-scoped-tests-owner')).toHaveText('curator');
  await expect(page.getByTestId('curator-run-cur-e2e-digest')).toContainText('wrote scoped-tests');

  // pin, then unpin
  await page.getByTestId('skill-scoped-tests-pin').click();
  await expect(skill).toHaveAttribute('data-owner', 'pinned');
  await expect(page.getByTestId('skills-notice')).toContainText('pinned');
  await page.getByTestId('skill-scoped-tests-unpin').click();
  await expect(skill).toHaveAttribute('data-owner', 'curator');

  // revert asks first, then the pack no longer holds the skill
  await page.getByTestId('curator-run-cur-e2e-revert').click();
  await expect(page.getByTestId('curator-run-cur-e2e-confirm')).toBeVisible();
  await page.getByTestId('curator-run-cur-e2e-confirm-yes').click();
  await expect(page.getByTestId('skills-empty')).toBeVisible();
  await expect(page.getByTestId('skills-notice')).toContainText('reverted');

  expect(errors).toEqual([]);
});

test('a person sets the curator up, changes it and turns it off', async ({ pairedPage: page, workspace }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  const cfgPath = path.join(workspace.repo, '.gummi', 'config.yaml');
  const cfg = async () => fs.promises.readFile(cfgPath, 'utf8').catch(() => '');
  await openSkills(page);
  await expect(page.getByTestId('skills-config')).toContainText('Curator off');

  // a monthly cap below one run is the board's refusal, shown in the form
  await page.getByTestId('skills-configure').click();
  // the agent and model come from the composer's own picker
  await page.getByTestId('skills-form-pair').click();
  await page.getByTestId('model-picker').locator('[data-testid^="model-headless-"]').first().click();
  await expect(page.getByTestId('model-picker')).toHaveCount(0);
  await expect(page.getByTestId('skills-form-pair')).toHaveAttribute('data-backend', 'headless');
  await page.getByTestId('skills-form-budget').fill('2');
  await page.getByTestId('skills-form-monthly').fill('1');
  await page.getByTestId('skills-form-save').click();
  await expect(page.getByTestId('skills-form-error')).toContainText('monthly');
  expect(await cfg()).not.toContain('curator');

  await page.getByTestId('skills-form-monthly').fill('20');
  await page.getByTestId('skills-form-schedule').fill('@weekly');
  await page.getByTestId('skills-form-save').click();
  await expect(page.getByTestId('skills-form')).toHaveCount(0);
  await expect(page.getByTestId('skills-config')).toContainText('headless');
  await expect(page.getByTestId('skills-config')).toContainText('@weekly');
  await expect(page.getByTestId('skills-config')).toContainText('$2.00 a run');
  await expect(page.getByTestId('skills-spend')).toContainText('of $20.00');
  await expect(page.getByTestId('skills-run')).toBeEnabled();
  expect(await cfg()).toContain('backend: headless');

  // the form opens on what is set
  await page.getByTestId('skills-configure').click();
  await expect(page.getByTestId('skills-form-schedule')).toHaveValue('@weekly');
  await page.getByTestId('skills-form-budget').fill('3');
  await page.getByTestId('skills-form-save').click();
  await expect(page.getByTestId('skills-config')).toContainText('$3.00 a run');

  // turning it off asks first, then removes the block
  await page.getByTestId('skills-configure').click();
  await page.getByTestId('skills-form-off').click();
  await page.getByTestId('skills-off-confirm-yes').click();
  await expect(page.getByTestId('skills-config')).toContainText('Curator off');
  await expect(page.getByTestId('skills-run')).toBeDisabled();
  expect(await cfg()).not.toContain('curator');

  expect(errors).toEqual([]);
});
