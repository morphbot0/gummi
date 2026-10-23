import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { expect, showCard, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';
import type { Page } from '@playwright/test';

// What a person does to a workflow run beyond allowing it (DESIGN §25.6,
// §25.7, §25.9): a run parked by a restart asks to be resumed; Stop and
// Withdraw ask first; a loop at its max hears the person's words and keeps
// its question; Top up… takes dollars and carries the run on; a run's
// artifacts and its whole diff are read from its block; a queued run is
// withdrawn and an always-allow taken back; a step's question that takes
// several answers sends every one ticked.

// openSession makes a session and opens it, its opening turn over.
async function openSession(page: Page, server: { url: string }, api: any, title: string, envelope = 2000): Promise<string> {
  const made = await api('POST', '/api/cards', { kind: 'freeform', title, envelope });
  expect(made.status, made.text).toBe(201);
  const id = String(made.json?.id);
  await page.goto(`${server.url}/#${id}`);
  await showCard(page, id);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
  return id;
}

async function say(page: Page, text: string): Promise<void> {
  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill(text);
  await page.keyboard.press('Enter');
}

// allow proposes a run with words and allows it.
async function allow(page: Page, text: string): Promise<void> {
  await say(page, text);
  await expect(page.getByTestId('decision-option-0')).toContainText('Allow', { timeout: 45_000 });
  await expect(page.getByTestId('proposal-spec')).toContainText('frozen now');
  await page.getByTestId('decision-option-0').click();
}

test.describe('a run whose steps take a while', () => {
  test.use({ workspaceEnv: { GUMMI_E2E_STEP_SECONDS: '6' } });

  test('a run parked by a restart asks to be resumed, its steps stopped where they were', async ({ pairedPage: page, server, api }, info) => {
    test.setTimeout(240_000);
    const id = await openSession(page, server, api, 'Park it');
    await allow(page, '[workflow] [step-slow] make the workflow file');
    const block = page.getByTestId('run-1');
    await expect(block).toHaveAttribute('data-state', 'running', { timeout: 30_000 });
    await expect(block.getByTestId('run-row-design')).toHaveAttribute('data-state', 'running', { timeout: 30_000 });

    await server.restart();
    await page.goto(`${server.url}/#${id}`);
    await showCard(page, id);
    // the run is parked at once and asks its person, who is told
    await expect(page.getByTestId('decision-option-0')).toContainText('Resume the run', { timeout: 20_000 });
    await expect(page.getByTestId('decision-option-1')).toContainText('Stop the run');
    await expect(block).toHaveAttribute('data-state', 'paused');
    await expect(block.getByTestId('run-reason')).toContainText('parked');
    const row = block.getByTestId('run-row-design');
    await expect(row).toHaveAttribute('data-state', 'paused');
    await expect(row.getByTestId('run-row-line')).toContainText('stopped where it was');
    await shot(page, info, 'workflow-parked');
    await page.getByTestId('decision-option-0').click();
    await expect(block).toHaveAttribute('data-state', 'running', { timeout: 30_000 });
    await expect(block).toHaveAttribute('data-state', 'done', { timeout: 150_000 });
  });

  test('Stop asks first', async ({ pairedPage: page, server, api }) => {
    test.setTimeout(150_000);
    await openSession(page, server, api, 'Stop it');
    await allow(page, '[workflow] [step-slow] make the workflow file');
    const block = page.getByTestId('run-1');
    await expect(block).toHaveAttribute('data-state', 'running', { timeout: 30_000 });
    await block.getByTestId('run-stop').click();
    const dialog = page.getByTestId('run-stop-dialog');
    await expect(dialog).toContainText('Nothing is reverted');
    await dialog.getByTestId('run-stop-cancel').click();
    await expect(dialog).toHaveCount(0);
    await expect(block).toHaveAttribute('data-state', 'running');
    await block.getByTestId('run-stop').click();
    await page.getByTestId('run-stop-confirm').click();
    await expect(block).toHaveAttribute('data-state', 'stopped', { timeout: 60_000 });
  });

  test('a queued run is withdrawn, and an always-allow is said and taken back', async ({ pairedPage: page, server, api }, info) => {
    test.setTimeout(180_000);
    await openSession(page, server, api, 'Queue and withdraw');
    await say(page, '[workflow] [step-slow] make the workflow file');
    await expect(page.getByTestId('decision-option-1')).toContainText('Always allow', { timeout: 45_000 });
    await page.getByTestId('decision-option-1').click();
    const first = page.getByTestId('run-1');
    await expect(first).toHaveAttribute('data-state', 'running', { timeout: 30_000 });
    // allowed without asking, it waits behind the open run
    await say(page, '[workflow=investigate] look again');
    const second = page.getByTestId('run-2');
    await expect(second).toHaveAttribute('data-state', 'queued', { timeout: 45_000 });
    await expect(second.getByTestId('run-always')).toContainText('starts without asking');
    await shot(page, info, 'workflow-queued');
    await second.getByTestId('run-withdraw').click();
    await page.getByTestId('run-withdraw-confirm').click();
    await expect(second).toHaveAttribute('data-state', 'denied', { timeout: 15_000 });
    // ask again: the next proposal waits on the person
    await page.getByTestId('run-askagain').click();
    await expect(page.getByTestId('run-always')).toHaveCount(0, { timeout: 15_000 });
  });
});

test('a loop at its max hears the person and keeps its question', async ({ pairedPage: page, server, api, workspace }) => {
  test.setTimeout(150_000);
  fs.mkdirSync(path.join(workspace.repo, '.gummi', 'workflows'), { recursive: true });
  fs.writeFileSync(path.join(workspace.repo, '.gummi', 'workflows', 'spin.yaml'),
    'name: spin\nsteps:\n  - id: fix\n    repeat:\n      max: 1\n      until: checks\n      onMax: pause\n      steps:\n        - { id: look, role: reviewer, prompt: "Look at {{spec}}" }\n');
  await openSession(page, server, api, 'Spin and talk');
  await allow(page, '[workflow=spin] spin');
  await expect(page.getByTestId('decision-option-0')).toContainText('Run one more iteration', { timeout: 45_000 });
  const block = page.getByTestId('run-1');
  // the loop that failed its condition reads as waiting on the person
  await expect(block.getByTestId('run-strip-fix')).toHaveClass(/s-asking/);
  await expect(page.getByTestId('decision-option-chat')).toContainText('Talk it over');
  await say(page, 'Why does DW-1 keep failing?');
  await expect(page.getByTestId('thread-live')).toContainText('Why does DW-1 keep failing?', { timeout: 30_000 });
  await expect(page.getByTestId('decision-option-0')).toContainText('Run one more iteration');
  await expect(page.locator('[data-testid="decision-option-2"].danger, [data-testid="decision-option-2"][data-danger]')).toHaveCount(1);
  await page.getByTestId('decision-option-1').click();
  await expect(block).toHaveAttribute('data-state', 'partial', { timeout: 60_000 });
});

test('Top up… takes dollars and the run carries on', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(180_000);
  await openSession(page, server, api, 'Run dry', 60);
  await allow(page, '[workflow] make the workflow file');
  const block = page.getByTestId('run-1');
  await expect(block).toHaveAttribute('data-state', 'paused', { timeout: 90_000 });
  await expect(block.getByTestId('run-reason')).toContainText(/spent \$\d/);
  await page.getByTestId('run-live-topup').click();
  const dialog = page.getByTestId('topup-dialog');
  await expect(page.getByTestId('topup-amount')).toHaveValue(/^\d/);
  await page.getByTestId('topup-amount').fill('abc');
  await page.getByTestId('topup-confirm').click();
  await expect(dialog.getByTestId('topup-error')).toContainText('dollars');
  await page.getByTestId('topup-amount').fill('5');
  await shot(page, info, 'workflow-topup');
  await page.getByTestId('topup-confirm').click();
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
});

test('a run’s artifacts and its whole diff are read from its block', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(150_000);
  await openSession(page, server, api, 'Read what it made');
  await allow(page, '[workflow] make the workflow file');
  const block = page.getByTestId('run-1');
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
  // an ended step says how long it took
  await block.getByTestId('run-steps').locator('summary').click();
  await expect(block.getByTestId('run-row-design').getByTestId('run-row-took')).toBeVisible();
  await block.getByTestId('run-artifact-design').click();
  await expect(page.getByTestId('artifact-text')).toContainText('Written by step');
  await page.getByTestId('artifact-close').click();
  await block.getByTestId('run-diff').click();
  const diff = page.getByTestId('run-diff-dialog');
  await expect(diff.getByTestId('step-diff-file').first()).toContainText('made.txt');
  await shot(page, info, 'workflow-run-diff');
  await page.getByTestId('run-diff-close').click();
});

test('a step’s question that takes several answers sends every one ticked', async ({ pairedPage: page, server, api, workspace }) => {
  test.setTimeout(150_000);
  const id = await openSession(page, server, api, 'Pick several');
  await allow(page, '[workflow] [step-asks-multi] make the workflow file');
  const block = page.getByTestId('run-1');
  const ask = block.getByTestId('run-question');
  await expect(ask).toContainText('pick any', { timeout: 60_000 });
  await ask.getByTestId('run-question-pick-0').check();
  await ask.getByTestId('run-question-pick-1').check();
  await ask.getByTestId('run-question-send-picked').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'Answered implement: Keep it plain (recommended), Give it a header' })).toBeVisible();
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
  // Revert run puts back what the run staged too
  const wt = workspace.worktree(id);
  execFileSync('git', ['add', '-A'], { cwd: wt });
  await block.getByTestId('run-revert').click();
  await page.getByTestId('revert-confirm').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'reverted' })).toBeVisible();
  expect(execFileSync('git', ['status', '--porcelain', '--untracked-files=no'], { cwd: wt, encoding: 'utf8' })).not.toContain('made.txt');
});

// A run that ended partial on a failed step is retried from its block: the
// failed step runs again beside its earlier attempt, and the run ends
// anew.
test('Retry runs the failed step again', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(180_000);
  await openSession(page, server, api, 'Try again');
  await allow(page, '[workflow] [step-fails-once] make the workflow file');
  const block = page.getByTestId('run-1');
  await expect(block).toHaveAttribute('data-state', 'partial', { timeout: 90_000 });
  await block.getByTestId('run-retry').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'its failed steps run again' })).toBeVisible();
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 120_000 });
  await expect(block.getByTestId('run-result-DW-1')).toBeVisible();
  await shot(page, info, 'workflow-retried');
  await expect(block.getByTestId('run-retry')).toHaveCount(0);
});

// A step opened beside the thread reverts the tree to where it started,
// keeping what the steps before it did.
test('a step reverts the tree to where it started', async ({ pairedPage: page, server, api, workspace }) => {
  test.setTimeout(150_000);
  const id = await openSession(page, server, api, 'Back to a step');
  await allow(page, '[workflow] make the workflow file');
  const block = page.getByTestId('run-1');
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
  await block.getByTestId('run-steps').locator('summary').click();
  await block.getByTestId('run-row-design').getByTestId('run-open').click();
  const panel = page.getByTestId('step-panel');
  await panel.getByTestId('step-revert').click();
  const dialog = page.getByTestId('revert-dialog');
  await expect(dialog).toContainText('before design started');
  await expect(dialog.getByTestId('revert-paths')).toContainText('made.txt');
  await dialog.getByTestId('revert-confirm').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'reverted' })).toBeVisible();
  expect(fs.existsSync(path.join(workspace.worktree(id), 'made.txt'))).toBe(false);
});
