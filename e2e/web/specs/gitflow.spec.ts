import { expect, showCard, showTab, test } from '../fixtures/test';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { shot } from '../fixtures/shots';

// A person's hands on a card's branch, against a real `gummi web`: the
// Working tree tab stages a file and a single line of another, and commits
// by hand; and a repository whose remote closes cards by push lands a card
// and then pushes the base there, confirmed against the facts read.

const git = (cwd: string, ...args: string[]) => execFileSync('git', ['-C', cwd, ...args], { encoding: 'utf8' }).trim();
const phone = (info: { project: { name: string } }) => info.project.name === 'phone';

test.describe('the working tree tab', () => {
  let id: string;
  test.use({
    seed: {
      run: async (ws) => {
        id = await ws.seedVerified('Add a notes helper');
        const wt = ws.worktree(id);
        fs.writeFileSync(path.join(wt, 'notes.txt'), 'a\nb\nc\n');
        git(wt, 'add', 'notes.txt');
        git(wt, 'commit', '-q', '-m', 'notes');
        fs.writeFileSync(path.join(wt, 'notes.txt'), 'a\nB1\nB2\nc\n');
        fs.writeFileSync(path.join(wt, 'todo.txt'), 'later\n');
      },
    },
  });

  test('stages a file and one line, then commits by hand', async ({ pairedPage: page, workspace }, info) => {
    await showCard(page, id);
    await showTab(page, 'tree');
    await expect(page.getByTestId('tree-changes')).toContainText('2');
    await page.getByTestId('tree-stage-todo.txt').check();
    await expect(page.getByTestId('tree-unstage-todo.txt')).toBeVisible();

    // open notes.txt's diff and pick only the "+B1" line
    await page.getByTestId('tree-file-u-notes.txt').click();
    const diff = page.getByTestId('tree-diff-notes.txt');
    await expect(diff).toContainText('B2');
    await diff.locator('.tr-ln.a', { hasText: 'B1' }).click();
    await expect(page.getByTestId('tree-hunk-stage-0')).toHaveText('Stage 1 line');
    await shot(page, info, 'tree-pick');
    await page.getByTestId('tree-hunk-stage-0').click();
    await expect(page.getByTestId('tree-unstage-notes.txt')).toBeVisible();

    await page.getByTestId('tree-message').fill('feat: add the todo and the first note');
    await shot(page, info, 'tree-staged');
    await page.getByTestId('tree-commit').click();

    const wt = workspace.worktree(id);
    await expect.poll(() => git(wt, 'log', '-1', '--format=%s')).toBe('feat: add the todo and the first note');
    expect(git(wt, 'show', '--name-only', '--format=', 'HEAD').split('\n').sort()).toEqual(['notes.txt', 'todo.txt']);
    // the unpicked "-b" is not staged either: b stays beside the new line
    expect(git(wt, 'show', 'HEAD:notes.txt')).toBe('a\nb\nB1\nc');
    // what was not picked is still in the tree, unstaged
    expect(git(wt, 'status', '--porcelain')).toBe('M notes.txt');
    await expect(page.getByTestId('tree-staged')).toContainText('0');
  });
});

test.describe('fetching from a chosen remote', () => {
  let id: string;
  test.use({
    seed: {
      run: async (ws) => {
        id = await ws.seedVerified('Add a fetch helper');
        for (const name of ['origin', 'upstream']) {
          const bare = path.join(path.dirname(ws.repo), `${name}-${path.basename(ws.repo)}.git`);
          execFileSync('git', ['clone', '-q', '--bare', ws.repo, bare]);
          git(bare, 'config', 'maintenance.auto', 'false');
          git(bare, 'branch', '-q', `only-on-${name}`, 'main');
          git(ws.repo, 'remote', 'add', name, bare);
        }
      },
    },
  });

  test('fetches the remote picked, then every remote', async ({ pairedPage: page, workspace }) => {
    await showCard(page, id);
    await showTab(page, 'tree');
    const from = page.getByTestId('tree-fetch-remote');
    await from.selectOption('upstream');
    await expect(page.getByTestId('tree-fetch')).toHaveAttribute('title', 'git fetch --prune upstream');
    await page.getByTestId('tree-fetch').click();
    const has = (ref: string) => { try { git(workspace.repo, 'rev-parse', '--verify', '--quiet', ref); return true; } catch { return false; } };
    await expect.poll(() => has('refs/remotes/upstream/only-on-upstream')).toBe(true);
    expect(has('refs/remotes/origin/only-on-origin')).toBe(false);
    // the pick outlives the redraw the fetch answered with
    await expect(from).toHaveValue('upstream');

    await from.selectOption('*');
    await page.getByTestId('tree-fetch').click();
    await expect.poll(() => has('refs/remotes/origin/only-on-origin')).toBe(true);
  });
});

test.describe('a remote that closes cards by push', () => {
  let id: string;
  let bare: string;
  test.use({
    seed: {
      run: async (ws) => {
        id = await ws.seedVerified('Add a wave helper');
        bare = path.join(path.dirname(ws.repo), `internal-${path.basename(ws.repo)}.git`);
        execFileSync('git', ['clone', '-q', '--bare', ws.repo, bare]);
        git(bare, 'config', 'maintenance.auto', 'false');
        git(ws.repo, 'remote', 'add', 'internal', bare);
        git(ws.repo, 'fetch', '-q', 'internal');
      },
    },
  });

  test('lands, then pushes the base there once confirmed', async ({ pairedPage: page, server, api, workspace }, info) => {
    const set = await api('POST', '/api/repos/remotes/close', { remote: 'internal', close: 'push' });
    expect(set.status).toBe(200);
    const repos = (await api('GET', '/api/repos')).json;
    expect(repos.repos[0].closePush).toBe('internal');

    await page.goto(`${server.url}/#${id}`);
    await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
    await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
    if (phone(info)) await page.getByTestId('tab-thread').click();
    await page.getByTestId('card-actions').click();
    await page.getByTestId('action-merge').click();
    await expect(page.getByTestId('push-after-label')).toContainText('Then push main to internal');
    await page.getByTestId('land-method').selectOption('merge');
    await page.getByTestId('action-confirm').click();

    const dlg = page.getByTestId('pushbase-dialog');
    await expect(dlg.getByTestId('pushbase-lede')).toContainText('Push main to internal as a fast-forward', { timeout: 30_000 });
    await expect(dlg.getByTestId('pushbase-commands')).toContainText(':refs/heads/main');
    await shot(page, info, 'pushbase-confirm');
    await dlg.getByTestId('pushbase-confirm').click();
    await expect(dlg).toHaveCount(0);

    await expect.poll(() => git(bare, 'rev-parse', 'main')).toBe((await workspace.git('rev-parse', 'main')).trim());
  });
});
