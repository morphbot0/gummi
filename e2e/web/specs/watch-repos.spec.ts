import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';

// Sessions across repositories (DESIGN §27) and an agent's watch (DESIGN
// §26), against a real `gummi web`. A session attaches a second repository
// from its composer and through its agent's repo_attach; the Diff tab reads
// each repository; detach refuses one with work in it. An agent proposes a
// run schedule whose gate is a command; the person enables it, a fire with
// nothing to do records no-change, and one with something to do dispatches
// a session into the repository the recipe named.

test.use({
  seed: {
    run: async (ws) => {
      const lxd = path.join(ws.repo, 'git', 'lxd');
      await fs.promises.mkdir(lxd, { recursive: true });
      const git = (...args: string[]) =>
        execFileSync('git', ['-C', lxd, '-c', 'user.name=E2E Tester', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', ...args], { stdio: 'ignore' });
      git('init', '-q', '-b', 'main');
      await fs.promises.writeFile(path.join(lxd, 'README.md'), '# lxd\n');
      git('add', '-A');
      git('commit', '-q', '-m', 'init: lxd');
      // a workspace of named repositories has no default: the session
      // names its own, and attaches the other
      await fs.promises.appendFile(path.join(ws.repo, '.gummi', 'config.yaml'), 'repos:\n  tiny: .\n  lxd: git/lxd\n');
      await ws.seedSessions(['Span two repos'], { repo: 'tiny' });
    },
  },
});

// Run now, through its confirm when the row asks for one.
async function runNow(page: Page) {
  await page.getByTestId('schedule-upstream-watch-run').click();
  const yes = page.getByTestId('schedule-upstream-watch-confirm-yes');
  if (await yes.isVisible().catch(() => false)) await yes.click();
}

function composer(page: Page) {
  const input = page.getByTestId('composer-input');
  const says = page.getByTestId('composer-says');
  return async (line: string) => {
    await expect(says).not.toContainText('stop this turn', { timeout: 30_000 });
    await input.click();
    await input.fill(line);
    // a message of several lines is sent with the send button: Enter in a
    // multi-line draft may only add a line
    await page.getByTestId('composer-send').click();
    await expect(input).toHaveValue('');
    await expect(says).not.toContainText('stop this turn', { timeout: 30_000 });
  };
}

async function cardId(page: Page, api: (m: string, p: string, b?: unknown) => Promise<{ json: any }>, title: string): Promise<string> {
  const rows: Array<{ id: string; title: string }> = (await api('GET', '/api/board')).json.rows;
  const row = rows.find((r) => r.title === title);
  expect(row, `a card titled ${title}`).toBeTruthy();
  return row!.id;
}

test('a session attaches a repository, reads its diff per repository, and keeps one with work', async ({ pairedPage: page, server, api, workspace }, info) => {
  test.skip(info.project.name !== 'desktop', 'one drive is enough');
  test.setTimeout(180_000);
  const id = await cardId(page, api, 'Span two repos');
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  const send = composer(page);

  // the agent lists what it may attach, and attaches lxd with no decision
  await send('[call repo_list {}]\n[call repo_attach {"repo": "lxd"}]');
  const thread = page.getByTestId('thread');
  await expect(thread).toContainText('repo_attach:', { timeout: 30_000 });
  await expect(page.getByTestId('card-repos')).toContainText('lxd', { timeout: 15_000 });
  await shot(page, info, 'repos-attached');

  // its worktree sits beside the primary's, on the card's branch
  const tree = path.join(workspace.repo, '.gummi', 'worktrees', `${id}@lxd`);
  expect(fs.existsSync(tree), `${tree} exists`).toBe(true);
  const branch = execFileSync('git', ['-C', tree, 'branch', '--show-current']).toString().trim();
  const primary = execFileSync('git', ['-C', path.join(workspace.repo, '.gummi', 'worktrees', id), 'branch', '--show-current']).toString().trim();
  expect(branch).toBe(primary);

  // work in the attached tree shows on its own tab of the Diff
  await fs.promises.writeFile(path.join(tree, 'NOTES.md'), 'a change in lxd\n');
  await page.getByTestId('tab-diff').click();
  await expect(page.getByTestId('diff-repos')).toBeVisible({ timeout: 15_000 });
  await page.getByTestId('diff-repo-lxd').click();
  await expect(page.getByText('a change in lxd')).toBeVisible({ timeout: 15_000 });
  await shot(page, info, 'repos-diff-lxd');

  // detach refuses an attachment holding work
  // a refused command keeps its line in the composer and says why
  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill('/detach lxd');
  await page.keyboard.press('Enter');
  await expect(page.getByText(/lxd has changes in .*before detaching/i)).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('card-repos')).toContainText('lxd');
  await shot(page, info, 'repos-detach-refused');
});

test('an agent proposes a watch, the person enables it, and a fire dispatches a session', async ({ pairedPage: page, server, api, workspace }, info) => {
  test.skip(info.project.name !== 'desktop', 'one drive is enough');
  test.setTimeout(240_000);
  const made = await api('POST', '/api/cards', { kind: 'freeform', title: 'Watch upstream', home: true, envelope: 2000 });
  expect(made.json?.id ?? made.json?.card?.id, JSON.stringify(made.json)).toBeTruthy();
  const agent = String(made.json?.id ?? made.json?.card?.id);
  await page.goto(`${server.url}/#${agent}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', agent);
  const send = composer(page);

  const recipe = [
    'name: upstream-watch',
    'steps:',
    '  - id: check',
    '    run: test -f trigger',
    '    quietExit: 1',
    '  - id: bump',
    '    dispatch:',
    '      repo: lxd',
    '      title: Bump for upstream',
    '      prompt: Upstream moved; bump it.',
    '      envelope: 1',
    '',
  ].join('\n');
  const propose = { name: 'upstream watch', preset: '@daily', recipe_yaml: recipe, spec: 1 };
  await send(`[call spec_start {"title": "Watch upstream"}]\n[call schedule_propose ${JSON.stringify(propose)}]`);
  const thread = page.getByTestId('thread');
  await expect(thread).toContainText('schedule_propose:', { timeout: 30_000 });
  await shot(page, info, 'watch-proposed');

  // the schedules view lists it off, as a run on the agent's spec
  await page.getByTestId('rail-more').click();
  await page.getByTestId('menu-schedules').click();
  await expect(page.getByTestId('view-schedules')).toBeVisible();
  const row = page.getByTestId('schedule-upstream-watch');
  await expect(row).toBeVisible({ timeout: 15_000 });
  await expect(row).toHaveAttribute('data-enabled', 'false');
  await expect(row).toContainText('run · “Watch upstream” · S1');
  await shot(page, info, 'watch-listed-off');

  // enabling shows the command and the repository it allows
  await page.getByTestId('schedule-upstream-watch-toggle').click();
  const confirm = page.getByTestId('schedule-upstream-watch-confirm');
  await expect(confirm).toBeVisible();
  await shot(page, info, 'watch-enable-confirm');
  await expect(page.getByTestId('view-schedules')).toContainText('test -f trigger');
  await expect(page.getByTestId('view-schedules')).toContainText('lxd');
  await page.getByTestId('schedule-upstream-watch-confirm-yes').click();
  await expect(row).toHaveAttribute('data-enabled', 'true', { timeout: 15_000 });

  // a fire with nothing to do: no-change, and no run on the agent
  await runNow(page);
  await expect(page.getByTestId('schedule-upstream-watch-status')).toContainText('checked, nothing to do', { timeout: 60_000 });
  // the fire's own "run 1 started" gives way to what the gate recorded
  await expect(page.getByTestId('schedule-upstream-watch-result')).toHaveCount(0, { timeout: 15_000 });
  // the recipe's envelope is in dollars, as every budget a person types
  await expect(page.getByTestId('schedule-upstream-watch')).toContainText('envelope $1.00');
  const runs = (await api('GET', `/api/cards/${agent}/live`)).json;
  await shot(page, info, 'watch-no-change');

  // something to do: the gate passes and a session is dispatched into lxd
  await fs.promises.writeFile(path.join(workspace.repo, '.gummi', 'homes', agent, 'trigger'), 'v2\n');
  await runNow(page);
  await expect
    .poll(async () => ((await api('GET', '/api/board')).json.rows as Array<{ title: string }>).some((r) => r.title === 'Bump for upstream'), { timeout: 90_000 })
    .toBe(true);
  const child = await cardId(page, api, 'Bump for upstream');
  await page.goto(`${server.url}/#${child}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', child);
  await shot(page, info, 'watch-dispatched-session');
  expect(runs).toBeTruthy();
});
