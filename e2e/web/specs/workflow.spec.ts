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
// question is answered from its row; the run ends with its results and its
// report, and the head carries no validated mark; a step opens
// beside the thread with its transcript and its own diff, and takes a
// message; Revert run puts back what its preview listed. The board is
// served with --terminal, so the session has a Terminal tab and the step,
// working in the session's tree, still has none of its own.
test.describe('a board served with --terminal', () => {
  test.use({ serverArgs: ['--terminal'], workspaceEnv: { SHELL: '/bin/sh' } });

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
    // its baseline: the check run in the tree before the run, failing as a
    // done-when check should (a check that already passed would say so)
    await expect(proposal.getByTestId('baseline-DW-1')).toHaveText('DW-1 fails before the run', { timeout: 30_000 });
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

    // the run ends: each done-when item's result and the report as the
    // workflow's turn; nothing on the head or the block names a validated
    // tree
    await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
    await expect(block.getByTestId('run-result-DW-1')).toBeVisible();
    await expect(block.getByTestId('run-files')).toContainText('1 file');
    await expect(page.getByTestId('thread-workflow').last()).toContainText('has reported', { timeout: 30_000 });
    await expect(page.getByTestId('card-head')).not.toContainText('validated');
    await expect(block.getByTestId('run-head')).not.toContainText('validated');

    // a step opens beside the thread: its transcript, its own diff
    await block.getByTestId('run-steps').locator('summary').click();
    const row = block.getByTestId('run-row-make');
    await expect(row.getByTestId('run-row-line')).toContainText('The person said: Keep it plain');
    await row.getByTestId('run-open').click();
    const panel = page.getByTestId('step-panel');
    await expect(panel).toBeVisible();
    await expect(panel.getByTestId('step-crumb')).toContainText('make-and-read run 1');
    await expect(panel.getByTestId('step-title')).toHaveText('make');
    await expect(panel.getByRole('tab')).toHaveText(['Transcript', 'Diff', 'Brief']);
    await expect(panel.getByTestId('step-tab-terminal')).toHaveCount(0);
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
    // reverted, the run has nothing left to put back
    await block.getByTestId('run-revert').click();
    await expect(page.getByTestId('revert-dialog')).toContainText('Nothing differs');
    await expect(page.getByTestId('revert-confirm')).toBeDisabled();
    await page.getByTestId('revert-cancel').click();
  });
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

// A step opened beside the thread reads as the thread does (DESIGN
// §25.10): its transcript is spaced as the session's conversation is,
// opens on its newest line and follows what the step says while the reader
// is there; scrolled up, what arrives does not move the reader.
test('a step’s transcript reads and follows as the thread does', async ({ pairedPage: page, server, api, workspace }) => {
  test.setTimeout(150_000);
  fs.mkdirSync(path.join(workspace.repo, '.gummi', 'workflows'), { recursive: true });
  fs.writeFileSync(path.join(workspace.repo, '.gummi', 'workflows', 'make-and-read.yaml'), RECIPE);
  const id = await openSession(page, server, api, 'Follow the step');
  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill('[workflow=make-and-read] make the workflow file');
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('decision-option-0')).toContainText('Allow', { timeout: 45_000 });
  await page.getByTestId('decision-option-0').click();
  const block = page.getByTestId('run-1');
  await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
  await block.getByTestId('run-steps').locator('summary').click();
  const card = await block.getByTestId('run-row-make').getAttribute('data-card');
  await block.getByTestId('run-row-make').getByTestId('run-open').click();
  const panel = page.getByTestId('step-panel');
  const transcript = panel.getByTestId('step-transcript');
  await expect(transcript).toContainText('Wrote made.txt', { timeout: 15_000 });

  // spaced as the session's conversation
  const gap = (el: Element) => getComputedStyle(el).rowGap;
  expect(await transcript.evaluate(gap)).toBe(await page.locator('#thread-live').evaluate(gap));

  const pane = page.getByTestId('panel-pane');
  const atEnd = () => pane.evaluate(p => p.scrollHeight - p.scrollTop - p.clientHeight < 24);
  const say = async (text: string) => {
    const r = await api('POST', `/api/cards/${id}/steps/${encodeURIComponent(String(card))}/send`, { text });
    expect(r.status, r.text).toBe(200);
    await expect(transcript).toContainText(`Noted: ${text}`, { timeout: 30_000 });
  };
  // at its newest line, it follows the step as it talks
  for (let i = 1; i <= 10; i++) await say(`note ${i}`);
  expect(await pane.evaluate(p => p.scrollHeight > p.clientHeight)).toBe(true);
  await expect.poll(atEnd).toBe(true);

  // scrolled up, it stays where the reader is
  await pane.evaluate(p => { p.scrollTop = 0; });
  await page.waitForTimeout(200);
  await say('while scrolled up');
  await page.waitForTimeout(300);
  expect(await pane.evaluate(p => p.scrollTop)).toBe(0);

  // back at the end, it takes hold again
  await pane.evaluate(p => { p.scrollTop = p.scrollHeight; });
  await page.waitForTimeout(200);
  await say('back at the end');
  await expect.poll(atEnd).toBe(true);
});

// Every step at work reads as running, and stands out of the block (DESIGN
// §25.9): the two reviews of deliver's parallel run side by side, both lit;
// an ended step a person talks to is at work again.
test.describe('a run whose steps take a while', () => {
  test.use({ workspaceEnv: { GUMMI_E2E_STEP_SECONDS: '4' } });

  test('every step at work reads as running, side by side', async ({ pairedPage: page, server, api }, info) => {
    test.setTimeout(150_000);
    const id = await openSession(page, server, api, 'Run the reviews side by side');
    const input = page.getByTestId('composer-input');
    await input.click();
    await input.fill('[workflow] [step-slow] make the workflow file');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('decision-option-0')).toContainText('Allow', { timeout: 45_000 });
    await page.getByTestId('decision-option-0').click();
    const block = page.getByTestId('run-1');
    const a = block.getByTestId('run-row-build/1/reviews/review-a');
    const b = block.getByTestId('run-row-build/1/reviews/review-b');
    await expect(a).toHaveAttribute('data-state', 'running', { timeout: 60_000 });
    await expect(b).toHaveAttribute('data-state', 'running');
    await expect(a).toHaveClass(/s-running/);
    await expect(b).toHaveClass(/s-running/);
    // lit apart from a row that is done
    const look = (el: Element) => getComputedStyle(el).backgroundColor + getComputedStyle(el).boxShadow;
    const done = block.getByTestId('run-row-build/1/implement');
    await expect(done).toHaveAttribute('data-state', 'done');
    expect(await a.evaluate(look)).not.toBe(await done.evaluate(look));
    await expect(block.getByTestId('run-strip-build')).toHaveClass(/s-running/);
    await shot(page, info, 'workflow-side-by-side');

    // ended, a step a person talks to is at work again
    await expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
    await block.getByTestId('run-steps').locator('summary').click();
    const design = block.getByTestId('run-row-design');
    await expect(design).toHaveAttribute('data-state', 'done');
    const card = await design.getAttribute('data-card');
    const r = await api('POST', `/api/cards/${id}/steps/${encodeURIComponent(String(card))}/send`, { text: 'one more look' });
    expect(r.status, r.text).toBe(200);
    await expect(design).toHaveAttribute('data-state', 'running', { timeout: 15_000 });
    await expect(design.getByTestId('run-row-line')).toContainText('a follow-up');
    await expect(design).toHaveAttribute('data-state', 'done', { timeout: 30_000 });
  });
});

// While a run is open a live strip stands above the composer (DESIGN
// §25.9): the run, the step at work and its action, the money and the
// time; Jump scrolls the thread to the run's block and flashes it, Open
// opens the step at work; a pill at the top of the thread says the block
// is above while it is scrolled out of view, and jumps to it too. The
// strip is updated in place and goes when the run ends.
test.describe('a run that takes a while, under a conversation', () => {
  test.use({ workspaceEnv: { GUMMI_E2E_STEP_SECONDS: '10' } });

  test('the live strip follows the open run, and Jump and the pill go to its block', async ({ pairedPage: page, server, api }, info) => {
    test.setTimeout(240_000);
    const id = await openSession(page, server, api, 'Watch the run from below');
    const input = page.getByTestId('composer-input');
    await input.click();
    await input.fill('[workflow] [step-slow] make the workflow file');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('decision-option-0')).toContainText('Allow', { timeout: 45_000 });
    // no strip while the run is only proposed
    await expect(page.getByTestId('run-live')).toHaveCount(0);
    await page.getByTestId('decision-option-0').click();

    const block = page.getByTestId('run-1');
    const strip = page.getByTestId('run-live');
    await expect(strip).toBeVisible({ timeout: 30_000 });
    await expect(strip).toHaveAttribute('data-mood', 'running');
    await expect(strip.getByTestId('run-live-name')).toHaveText('deliver · run 1');
    await expect(strip.getByTestId('run-live-now')).toContainText('design', { timeout: 30_000 });
    await expect(strip.getByTestId('run-live-jump')).toBeVisible();
    const wide = (page.viewportSize()?.width ?? 1440) > 760;
    if (wide) await expect(strip.getByTestId('run-live-strip')).toContainText('build ↺');

    // the strip sits directly on the composer
    const sb = await strip.boundingBox();
    const cb = await page.getByTestId('composer').boundingBox();
    expect(sb && cb && sb.y + sb.height <= cb.y + 1 && cb.y - (sb.y + sb.height) < 16).toBe(true);
    // updated in place: its node outlives the run's next moves
    await strip.evaluate((el) => { el.dataset.kept = 'yes'; });

    // a conversation under the run pushes its block up out of view
    for (let i = 1; i <= 8; i++) {
      const r = await api('POST', `/api/cards/${id}/send`, { text: `While that runs, note ${i}: a long enough line to take some room in the thread, so the run's block scrolls up and away.` });
      expect(r.status, r.text).toBeLessThan(300);
      await expect(page.getByTestId('thread-live')).toContainText(`note ${i}`, { timeout: 30_000 });
    }
    const thread = page.getByTestId('thread');
    await thread.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    const pill = page.getByTestId('run-above');
    await expect(pill).toBeVisible({ timeout: 10_000 });
    await expect(pill).toHaveText('↑ deliver · run 1 is above');
    await shot(page, info, 'workflow-live-strip');
    await pill.click();
    await expect(block).toBeInViewport();
    await expect(block).toHaveClass(/rb-flash/);
    await expect(pill).toBeHidden();

    // Jump from the strip does the same
    await thread.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await expect(pill).toBeVisible();
    await strip.getByTestId('run-live-jump').click();
    await expect(block).toBeInViewport();
    await expect(block).toHaveClass(/rb-flash/);
    await expect(page.locator('[data-testid="run-live"][data-kept="yes"]')).toHaveCount(1);

    // Open goes to the step at work (a pick when several work side by side)
    await expect(strip).toHaveAttribute('data-mood', 'running');
    const open = strip.getByTestId('run-live-open');
    await expect(open).toBeVisible({ timeout: 30_000 });
    if ((await open.textContent())?.startsWith('Open…')) {
      await open.click();
      await page.getByTestId('run-live-pick').locator('button').first().click();
    } else {
      await open.click();
    }
    await expect(page.getByTestId('step-panel')).toBeVisible();
    await page.getByTestId('step-close').click();
    await backToThread(page);

    // the run ends: the strip goes, and so does the pill
    await expect(block).toHaveAttribute('data-state', 'done', { timeout: 150_000 });
    await expect(strip).toHaveCount(0);
    await thread.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await page.waitForTimeout(300);
    await expect(pill).toBeHidden();
  });
});

// A step that asks turns the strip amber with its question; Answer opens
// the step on that question, answered there.
test('the live strip says a step asks, and Answer opens it on its question', async ({ pairedPage: page, server, api, workspace }, info) => {
  test.setTimeout(180_000);
  fs.mkdirSync(path.join(workspace.repo, '.gummi', 'workflows'), { recursive: true });
  fs.writeFileSync(path.join(workspace.repo, '.gummi', 'workflows', 'make-and-read.yaml'), RECIPE);
  await openSession(page, server, api, 'Answer from the strip');
  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill('[workflow=make-and-read] [step-asks] make the workflow file');
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('decision-option-0')).toContainText('Allow', { timeout: 45_000 });
  await page.getByTestId('decision-option-0').click();

  const strip = page.getByTestId('run-live');
  await expect(strip).toHaveAttribute('data-mood', 'asking', { timeout: 45_000 });
  await expect(strip.getByTestId('run-live-now')).toContainText('make asks:');
  await expect(strip.getByTestId('run-live-now')).toContainText('Keep made.txt plain');
  await expect(strip.getByTestId('run-live-jump')).toBeVisible();
  await shot(page, info, 'workflow-live-strip-asks');
  await strip.getByTestId('run-live-answer').click();
  const panel = page.getByTestId('step-panel');
  await expect(panel).toBeVisible();
  await expect(panel.getByTestId('step-title')).toHaveText('make');
  const ask = panel.getByTestId('run-question');
  await expect(ask).toContainText('Keep made.txt plain');
  await ask.getByTestId('run-question-option-0').click();
  await page.getByTestId('step-close').click();
  await backToThread(page);
  await expect(page.getByTestId('run-1')).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
  await expect(strip).toHaveCount(0);
});
