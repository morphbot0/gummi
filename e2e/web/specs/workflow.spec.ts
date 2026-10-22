import fs from 'node:fs';
import path from 'node:path';
import { expect, showCard, showTab, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';
import type { Page } from '@playwright/test';

// A workspace recipe whose steps are both top-level, so the writing step
// keeps a row of its own once the run has ended: an implementer makes what
// the spec's checks look for, and a reviewer judges the done-when items.
const RECIPE = `name: make-and-read
description: Make what the spec asks for, then read it
steps:
  - id: make
    role: implementer
    prompt: |
      Make what the spec below asks for.

      {{spec}}
  - id: read
    role: reviewer
    prompt: |
      Read {{run.diff}} against the spec's done-when items. End with one
      line per item: MET: DW-n or UNMET: DW-n <why>.

      {{spec}}
`;

// openSession makes a session and opens it, its opening turn over.
async function openSession(page: Page, server: { url: string }, api: any, title: string): Promise<string> {
  const made = await api('POST', '/api/cards', { kind: 'freeform', title, envelope: 2000 });
  expect(made.status, made.text).toBe(201);
  const id = String(made.json?.id);
  await page.goto(`${server.url}/#${id}`);
  await showCard(page, id);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
  return id;
}

// backToThread shows the thread again where a document covers it (a phone).
async function backToThread(page: Page): Promise<void> {
  if ((page.viewportSize()?.width ?? 1440) > 760) return;
  if ((await page.getByTestId('app').getAttribute('data-view')) !== 'thread') await page.getByTestId('tab-thread').click();
}

// A session runs a workflow (DESIGN §25): its proposal is the pinned
// decision, drawn as the recipe's steps, the checks and the money; allowed,
// the run is one block in the thread that updates in place; a step's
// question is answered from its row; the run ends with its results, its
// report and the validated mark, which Re-check reads again; a step opens
// beside the thread with its transcript and its own diff, and takes a
// message; Revert run puts back what its preview listed.
test('a session runs a workflow, from its proposal to Revert run', async ({ pairedPage: page, server, api, workspace }, info) => {
  test.setTimeout(180_000);
  fs.mkdirSync(path.join(workspace.repo, '.gummi', 'workflows'), { recursive: true });
  fs.writeFileSync(path.join(workspace.repo, '.gummi', 'workflows', 'make-and-read.yaml'), RECIPE);
  const id = await openSession(page, server, api, 'Make the workflow file');

  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill('[workflow=make-and-read] [step-asks] make the workflow file');
  await page.keyboard.press('Enter');

  // the proposal: its steps with their roles, the check it will run, and
  // what the session's pool has left — a run estimates nothing
  const proposal = page.getByTestId('run-proposal');
  await expect(proposal).toBeVisible({ timeout: 45_000 });
  await expect(proposal.getByTestId('proposal-step-make')).toContainText('implementer');
  await expect(proposal.getByTestId('proposal-step-read')).toContainText('reviewer');
  await expect(proposal.getByTestId('proposal-checks')).toContainText('test -f made.txt');
  await expect(proposal.getByTestId('proposal-money')).toContainText('left in the session; the run spends from it');
  await expect(proposal).not.toContainText('~$');
  await expect(page.getByTestId('decision-feedback')).toBeVisible();
  await expect(page.getByTestId('decision-option-1')).toHaveText(/Always allow/);
  // the run's block stands in the thread already, waiting on the answer
  const block = page.getByTestId('run-1');
  await expect(block).toHaveAttribute('data-state', 'proposed');
  await expect(page.getByTestId('decision-option-0')).toContainText('Allow');
  await shot(page, info, 'workflow-proposal');
  await page.getByTestId('decision-option-0').click();

  // allowed, the block runs and updates in place: the step asks its
  // question under its row, and it is answered there
  await expect(block).toHaveAttribute('data-state', 'running', { timeout: 30_000 });
  await expect(block.getByTestId('run-strip-make')).toBeVisible();
  const ask = block.getByTestId('run-question');
  await expect(ask).toContainText('Keep made.txt plain', { timeout: 45_000 });
  await expect(block.getByTestId('run-row-make')).toHaveClass(/asking/);
  await expect(ask.getByTestId('run-question-option-0')).toContainText('recommended');
  await shot(page, info, 'workflow-asks');
  await ask.getByTestId('run-question-option-0').click();

  // the run ends: each done-when item's result, the validated tree, the
  // report as the workflow's turn, and the mark on the head
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
  await expect(block.getByTestId('run-result-DW-1')).toBeVisible();
  await expect(block.getByTestId('run-validated')).toBeVisible();
  await expect(block.getByTestId('run-files')).toContainText('1 file');
  await expect(page.getByTestId('thread-workflow').last()).toContainText('has reported', { timeout: 30_000 });
  const mark = page.getByTestId('validated-mark');
  await expect(mark).toContainText('validated at tree', { timeout: 15_000 });
  await mark.getByTestId('validated-recheck').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'the done-when checks pass' })).toBeVisible({ timeout: 30_000 });

  // a step opens beside the thread: its transcript, its own diff
  await block.getByTestId('run-steps').locator('summary').click();
  const row = block.getByTestId('run-row-make');
  await expect(row.getByTestId('run-row-line')).toContainText('The person said: Keep it plain');
  await row.getByTestId('run-open').click();
  const panel = page.getByTestId('step-panel');
  await expect(panel).toBeVisible();
  await expect(panel.getByTestId('step-crumb')).toContainText('make-and-read run 1');
  await expect(panel.getByTestId('step-title')).toHaveText('make');
  await expect(panel.getByTestId('step-transcript')).toContainText('Wrote made.txt', { timeout: 15_000 });
  await panel.getByTestId('step-tab-diff').click();
  await expect(panel.getByTestId('step-diff-file')).toContainText('made.txt');
  await panel.getByTestId('step-tab-brief').click();
  await expect(panel.getByTestId('step-brief')).toContainText('Make what the spec below asks for.');

  // a message to the step is a turn on its card, answered in its transcript
  await panel.getByTestId('step-tab-transcript').click();
  await panel.getByTestId('step-input').fill('keep the file short');
  await panel.getByTestId('step-send').click();
  await expect(panel.getByTestId('step-transcript')).toContainText('Noted: keep the file short', { timeout: 30_000 });
  await shot(page, info, 'workflow-step');

  // back to the session; the report link opens its spec on the Specs tab
  await panel.getByTestId('step-close').click();
  await expect(page.getByTestId('step-panel')).toHaveCount(0);
  await backToThread(page);
  await block.getByTestId('run-report').click();
  await expect(page.getByTestId('specs-state-1')).toHaveText('met', { timeout: 15_000 });
  await expect(page.getByTestId('spec-doc')).toContainText('Report');
  await backToThread(page);

  // a change to the session that moves nothing in the run leaves its
  // block as drawn: the node survives the session's next refetch
  await block.evaluate((el) => { el.dataset.kept = 'yes'; });
  const more = await api('POST', `/api/cards/${id}/spec/start`, { title: 'Another piece' });
  expect(more.status, more.text).toBe(200);
  await expect(page.getByTestId('tab-spec')).toContainText('2');
  await expect(page.locator('[data-testid="run-1"][data-kept="yes"]')).toHaveCount(1);

  // Revert run lists what it puts back, and puts back what it listed: a
  // tree that moved after the preview is refused, and once it reads as
  // previewed again the revert goes through
  await block.getByTestId('run-revert').click();
  const dialog = page.getByTestId('revert-dialog');
  await expect(dialog.getByTestId('revert-paths')).toContainText('made.txt');
  const later = path.join(workspace.worktree(id), 'later.txt');
  fs.writeFileSync(later, 'later\n');
  await dialog.getByTestId('revert-confirm').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'changed since the revert was previewed' })).toBeVisible();
  await expect(dialog).toBeVisible();
  expect(fs.existsSync(path.join(workspace.worktree(id), 'made.txt'))).toBe(true);
  fs.rmSync(later);
  await dialog.getByTestId('revert-confirm').click();
  await expect(page.getByTestId('toast').filter({ hasText: 'Run 1 reverted' })).toBeVisible();
  expect(fs.existsSync(path.join(workspace.worktree(id), 'made.txt'))).toBe(false);
  await expect(mark).toContainText('changed since validation', { timeout: 15_000 });
  // reverted, the run has nothing left to put back
  await block.getByTestId('run-revert').click();
  await expect(page.getByTestId('revert-dialog')).toContainText('Nothing differs');
  await expect(page.getByTestId('revert-confirm')).toBeDisabled();
  await page.getByTestId('revert-cancel').click();
});

// The Specs tab lists a session's specs, each with its state, and shows
// the one picked; a new spec is started from the list, and a spec is
// edited whole, each save a revision of its own.
test('the Specs tab switches between a session’s specs', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(90_000);
  const id = await openSession(page, server, api, 'Two pieces of work');
  await showTab(page, 'spec');
  await expect(page.getByTestId('spec-none')).toContainText('No spec');
  await page.getByTestId('spec-start-session').click();
  await expect(page.getByTestId('specs-item-1')).toBeVisible();
  for (const title of ['Import CSV']) {
    const r = await api('POST', `/api/cards/${id}/spec/start`, { title });
    expect(r.status, r.text).toBe(200);
  }
  // a new spec from the list itself
  await page.getByTestId('specs-new').click();
  await page.getByTestId('specs-new-title').fill('Export JSON');
  await page.getByTestId('specs-new-start').click();
  await expect(page.getByTestId('specs-item-3')).toHaveAttribute('aria-current', 'true');
  await expect(page.getByTestId('spec-title')).toHaveText('Export JSON');
  await expect(page.getByTestId('specs-state-3')).toHaveText('draft');

  await page.getByTestId('specs-item-2').click();
  await expect(page.getByTestId('specs-item-2')).toHaveAttribute('aria-current', 'true');
  await expect(page.getByTestId('spec-title')).toHaveText('Import CSV');

  // the spec shown is the one edited
  await page.getByTestId('spec-edit').click();
  const ed = page.getByTestId('spec-editor');
  const text = await ed.inputValue();
  await ed.fill(text.replace('## Problem\n', '## Problem\n\nRows arrive as CSV.\n'));
  await page.getByTestId('spec-save').click();
  await expect(page.getByTestId('spec-revision-2')).toBeVisible();
  await expect(page.getByTestId('spec-doc')).toContainText('Rows arrive as CSV.');
  await expect(page.getByTestId('spec-revision-2')).toContainText('Tester');

  await page.getByTestId('specs-item-3').click();
  await expect(page.getByTestId('spec-title')).toHaveText('Export JSON');
  await expect(page.getByTestId('spec-doc')).not.toContainText('Rows arrive as CSV.');
  await shot(page, info, 'workflow-specs');
  const two = (await api('GET', `/api/cards/${id}/spec?spec=2`)).json;
  expect(two.revisions?.length).toBe(2);
  const list = (await api('GET', `/api/cards/${id}/specs`)).json;
  expect(list.specs.map((s: any) => s.title)).toEqual(['Two pieces of work', 'Import CSV', 'Export JSON']);
});
