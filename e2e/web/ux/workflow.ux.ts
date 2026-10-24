import fs from 'node:fs';
import path from 'node:path';
import { expect, test } from '../fixtures/test';
import { Tour } from './tour';
import { live, openCard, send, toTab, toThread } from './nav';

// A workflow run, from the session's proposal to its report: allow it,
// follow it, answer a step's question, open a step, read the report and the
// spec it judged, and put the run back with Revert run.

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

test.use({ serverArgs: ['--terminal'], workspaceEnv: { SHELL: '/bin/sh' } });

test('a workflow run from proposal to report', async ({ pairedPage: page, server, api, workspace }, info) => {
  test.setTimeout(300_000);
  fs.mkdirSync(path.join(workspace.repo, '.gummi', 'workflows'), { recursive: true });
  fs.writeFileSync(path.join(workspace.repo, '.gummi', 'workflows', 'make-and-read.yaml'), RECIPE);
  const made = await api('POST', '/api/cards', { kind: 'freeform', title: 'Make the workflow file', envelope: 2000 });
  expect(made.status, made.text).toBe(201);
  const id = String(made.json?.id);
  await page.goto(server.url);
  const t = new Tour(page, info, 'workflow', 'Run a workflow: read the proposal, allow it, follow it, answer a step, read the report, revert it');

  await live(t);
  await openCard(t, id);
  await t.wait('the opening turn ends', () => expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 45_000 }));
  await send(t, '[workflow=make-and-read] [step-asks] make the workflow file', { wait: false });
  await t.wait('the proposal arrives', () => expect(page.getByTestId('run-proposal').first()).toBeVisible({ timeout: 45_000 }));
  await t.wait('its baseline is in', () => expect(page.getByTestId('baseline-DW-1').first()).toBeVisible({ timeout: 30_000 }));
  await t.shot('proposal', 'The proposal: what will run, what it checks, what it may spend — and is "allow" the obvious answer?');
  await t.click('allow the run', page.getByTestId('decision-option-0'));

  const block = page.getByTestId('run-1');
  await t.wait('the run starts', () => expect(block).toHaveAttribute('data-state', 'running', { timeout: 30_000 }));
  await t.shot('running', 'The run under way: which step is working, and how do you steer, pause or stop it?');
  await t.wait('a step asks', () => expect(block.getByTestId('run-question')).toBeVisible({ timeout: 45_000 }));
  await t.shot('step-asks', 'A step asks a question: is it obvious that the run waits on you, and where to answer?');
  await t.click('answer the step', block.getByTestId('run-question-option-0'));
  await t.wait('the run ends', () => expect(block).toHaveAttribute('data-state', 'done', { timeout: 90_000 }));
  await t.wait('the report reaches the session', () => expect(page.getByTestId('thread-workflow').last()).toContainText('has reported', { timeout: 30_000 }));
  await t.shot('done', 'The run is over: did it work, what changed, and what should you do now (read, commit, revert)?');

  await t.click('open the run’s steps', block.getByTestId('run-steps').locator('summary'));
  await t.click('open the make step', block.getByTestId('run-row-make').getByTestId('run-open'));
  await t.wait('the step panel opens', () => expect(page.getByTestId('step-panel')).toBeVisible());
  await t.shot('step-panel', 'One step opened: its transcript, its own diff, its brief');
  await t.click('show the step’s diff', page.getByTestId('step-tab-diff'));
  await t.shot('step-diff', 'What this step changed');
  await t.click('close the step', page.getByTestId('step-close'));

  await toThread(t);
  await t.click('open the report', block.getByTestId('run-report'));
  await t.shot('report-spec', 'The report in its spec: are the results and the spec state (met/partial) clear?');
  await toTab(t, 'diff');
  await t.shot('run-diff', 'The tree the run left: is it clear these changes are uncommitted and yours to keep or revert?');
  await toThread(t);
  await t.click('press Revert run', block.getByTestId('run-revert'));
  await t.shot('revert-dialog', 'Revert run: is it clear what will be put back?');
  await t.maybe('cancel the revert', () => page.getByTestId('revert-cancel').click());
  await t.shot('after', 'Back on the thread with the run done: what is the next step?');
  t.finish();
});
