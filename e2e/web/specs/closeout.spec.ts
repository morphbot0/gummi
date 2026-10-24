import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { expect, test, type GummiServer, type Workspace } from '../fixtures/test';
import type { Page } from '@playwright/test';
import { shot } from '../fixtures/shots';

// A card that closes takes its worktree and branch with it, unless the
// person ticks "Keep the worktree and branch" or the branch is the only copy
// of the work. The landing form always offers the choice; a hand-off's
// confirmation offers it only where the branch would go (it is on a remote).
// The repositories view's "Clean up all" takes every closed card's worktree
// across every repository at once.

async function open(page: Page, server: GummiServer, id: string, phone: boolean) {
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
  if (phone) await page.getByTestId('tab-thread').click();
}

async function menu(page: Page, action: string) {
  await page.getByTestId('card-actions').click();
  await expect(page.getByTestId('card-actions-menu')).toBeVisible();
  await page.getByTestId(`action-${action}`).click();
}

const phone = (info: { project: { name: string } }) => info.project.name === 'phone';

async function branchExists(ws: Workspace, branch: string): Promise<boolean> {
  return (await ws.git('branch', '--list', branch)).trim() !== '';
}

async function stage(api: (m: string, p: string) => Promise<{ json: any }>, id: string): Promise<string> {
  return (await api('GET', `/api/cards/${id}`)).json.stage;
}

test.describe('landing', () => {
  let id: string;
  test.use({ seed: { run: async (ws) => { id = await ws.seedReady('Add a tidy helper'); } } });

  test('takes the worktree and branch by default', async ({ pairedPage: page, server, workspace, api }, info) => {
    const branch = (await api('GET', `/api/cards/${id}`)).json.branch as string;
    expect(await branchExists(workspace, branch)).toBe(true);
    await open(page, server, id, phone(info));
    await menu(page, 'merge');
    const keep = page.getByTestId('keep-branch');
    await expect(keep).toBeVisible();
    await expect(keep).not.toBeChecked();
    await shot(page, info, 'closeout-land');
    await page.getByTestId('action-input').fill('feat: add a tidy helper');
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-dialog')).toHaveCount(0, { timeout: 30_000 });
    await expect.poll(() => stage(api, id)).toBe('done');
    await expect.poll(() => branchExists(workspace, branch)).toBe(false);
    expect(fs.existsSync(workspace.worktree(id))).toBe(false);
    expect((await workspace.git('log', '-1', '--format=%s', 'main')).trim()).toBe('feat: add a tidy helper');
  });

  test('keeps them when asked', async ({ pairedPage: page, server, workspace, api }, info) => {
    const branch = (await api('GET', `/api/cards/${id}`)).json.branch as string;
    await open(page, server, id, phone(info));
    await menu(page, 'merge');
    await page.getByTestId('keep-branch').check();
    await page.getByTestId('action-input').fill('feat: add a tidy helper');
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-dialog')).toHaveCount(0, { timeout: 30_000 });
    await expect.poll(() => stage(api, id)).toBe('done');
    expect(await branchExists(workspace, branch)).toBe(true);
    expect(fs.existsSync(workspace.worktree(id))).toBe(true);
  });
});

test.describe('hand-off', () => {
  let id: string;
  test.use({ seed: { run: async (ws) => { id = await ws.seedReady('Add a parting helper'); } } });

  test('of a branch only this machine has keeps it, and does not offer the choice', async ({ pairedPage: page, server, workspace, api }, info) => {
    const branch = (await api('GET', `/api/cards/${id}`)).json.branch as string;
    await open(page, server, id, phone(info));
    await menu(page, 'handoff');
    await expect(page.getByTestId('action-confirm')).toHaveText(/Yes, hand off/);
    await expect(page.getByTestId('keep-branch')).toHaveCount(0);
    await page.getByTestId('action-confirm').click();
    await expect.poll(() => stage(api, id)).toBe('done');
    expect(await branchExists(workspace, branch)).toBe(true);
  });

  test('of a pushed branch offers to keep it, and takes it unless kept', async ({ pairedPage: page, server, workspace, api }, info) => {
    const branch = (await api('GET', `/api/cards/${id}`)).json.branch as string;
    await workspace.addGitHubRemote();
    await workspace.git('push', '-q', 'origin', branch);
    await open(page, server, id, phone(info));
    await menu(page, 'handoff');
    await expect(page.getByTestId('action-confirm')).toHaveText(/Yes, hand off/);
    const keep = page.getByTestId('keep-branch');
    await expect(keep).toBeVisible();
    await expect(keep).not.toBeChecked();
    await shot(page, info, 'closeout-handoff');
    await page.getByTestId('action-confirm').click();
    await expect.poll(() => stage(api, id)).toBe('done');
    await expect.poll(() => branchExists(workspace, branch)).toBe(false);
    expect(fs.existsSync(workspace.worktree(id))).toBe(false);
  });
});

test.describe('clean up all', () => {
  const ids: string[] = [];
  test.use({
    seed: {
      run: async (ws) => {
        ids.length = 0;
        // two repositories, each a copy of the tiny repo so the scripted
        // agent's checks hold in both; a repos: workspace has no default
        await fs.promises.appendFile(path.join(ws.repo, '.gummi', 'config.yaml'), 'repos:\n  a: git/a\n  b: git/b\n');
        for (const name of ['a', 'b']) execFileSync('git', ['clone', '-q', ws.repo, path.join(ws.repo, 'git', name)], { stdio: 'ignore' });
        for (const name of ['a', 'b']) {
          const id = await ws.seedReady(`Add a keeper helper in ${name}`, { repo: name });
          ids.push(id);
          await ws.land(id, `feat: add a keeper helper in ${name}`, { keepBranch: true });
        }
      },
    },
  });

  test('takes every closed card across every repository', async ({ pairedPage: page, workspace }, info) => {
    for (const id of ids) expect(fs.existsSync(workspace.worktree(id))).toBe(true);
    await page.getByTestId('rail-more').click();
    await page.getByTestId('menu-repos').click();
    await expect(page.getByTestId('view-repos')).toBeVisible();
    const all = page.getByTestId('repos-clean-all');
    await expect(all).toHaveText('Clean up all (2)');
    await all.click();
    const confirm = page.getByTestId('repos-clean-all-confirm');
    await expect(confirm).toContainText('Clean up 2 closed cards across every repository?');
    await shot(page, info, 'closeout-clean-all');
    await page.getByTestId('repos-clean-all-confirm-yes').click();
    await expect.poll(() => ids.map((id) => fs.existsSync(workspace.worktree(id)))).toEqual([false, false]);
    await expect(all).toBeHidden();
  });
});
