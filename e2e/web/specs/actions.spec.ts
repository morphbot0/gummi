import { expect, ref, test, type GummiServer } from '../fixtures/test';
import type { Page } from '@playwright/test';
import { shot } from '../fixtures/shots';

// A card's menu against a real `gummi web`: every entry collects what it
// needs first (a number, cards, a message, a yes), prefilled with what the
// board suggests, and runs through the code the TUI's key runs.

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

test.describe('a session', () => {
  let ids: string[];
  test.use({ seed: { run: async (ws) => { ids = await ws.seedSessions(['Add a shrug helper', 'Add a nod helper']); } } });

  test('the budget is raised from the number it is now', async ({ pairedPage: page, server, api }, info) => {
    await open(page, server, ids[0], phone(info));
    await menu(page, 'envelope');
    const input = page.getByTestId('action-input');
    await expect(input).toHaveValue('20');
    await input.fill('25');
    await shot(page, info, 'action-budget');
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-dialog')).toHaveCount(0);
    await expect(page.getByTestId('card-spend')).toContainText('/ $25.00');
    expect((await api('GET', `/api/cards/${ids[0]}`)).json.envelope).toBe(2500);
  });

  test('a budget that is not a dollar amount is said in the dialog, not sent', async ({ pairedPage: page, server }, info) => {
    await open(page, server, ids[0], phone(info));
    const sent: string[] = [];
    page.on('request', (r) => { if (r.method() === 'POST' && /\/actions\/envelope$/.test(r.url())) sent.push(r.postData() || '') });
    await menu(page, 'envelope');
    await expect(page.getByTestId('action-confirm')).toHaveText('Set budget');
    for (const v of ['1.505', '1e20', '-3']) {
      await page.getByTestId('action-input').fill(v);
      await page.getByTestId('action-confirm').click();
      await expect(page.getByTestId('action-error')).toContainText(/cent|dollar amount|negative/);
      await expect(page.getByTestId('action-dialog')).toBeVisible();
    }
    expect(sent).toEqual([]);
  });

  test('delete asks first, then the card is gone', async ({ pairedPage: page, server, api }, info) => {
    const id = ids[1];
    await open(page, server, id, phone(info));
    const sent: any[] = [];
    page.on('request', (r) => { if (r.url().includes('/actions/delete') && r.method() === 'POST') sent.push(r.postDataJSON() || {}); });
    await menu(page, 'delete');
    // the question is the server's own, whole: sent bare, asked back
    await expect(page.getByTestId('action-question')).toContainText(/Delete “.+”\?/);
    await expect(page.getByTestId('action-question')).toContainText('removes worktree, branch, and record');
    expect(sent).toHaveLength(1);
    expect(sent[0].confirm).toBeUndefined();
    await shot(page, info, 'action-delete');
    await page.getByTestId('action-cancel').click();
    expect((await api('GET', `/api/cards/${id}`)).status).toBe(200);
    // a bare yes is not one
    expect((await api('POST', `/api/cards/${id}/actions/delete`, { confirm: true })).status).toBe(400);
    await menu(page, 'delete');
    await page.getByTestId('action-confirm').click();
    // the yes is the token the question came with
    await expect.poll(() => sent.length).toBe(3);
    expect(typeof sent[2].confirm).toBe('string');
    expect(sent[2].confirm.length).toBeGreaterThan(8);
    await expect.poll(async () => (await api('GET', `/api/cards/${id}`)).status).toBe(404);
    // where it was open, it is said to be gone; the page does not move
    // itself onto another card (on a phone it is this screen's own delete,
    // so the page is back on the cards already)
    await expect(page.getByTestId('card-title')).toHaveText(`${ref(id)} · deleted`);
    await expect(page.getByTestId('card-id')).toHaveCount(0);
    await expect(page.getByTestId(`rail-row-${id}`)).toHaveCount(0);
  });
});

test.describe('a session ready to land', () => {
  let id: string;
  test.use({ seed: { run: async (ws) => { id = await ws.seedReady('Add a farewell helper'); } } });

  test('lands with a message drafted for its branch', async ({ pairedPage: page, server, workspace }, info) => {
    await open(page, server, id, phone(info));
    // nothing has drafted a message for this branch yet: the dialog says so
    await menu(page, 'merge');
    await expect(page.getByTestId('action-input')).toHaveValue('');
    await expect(page.getByTestId('action-hint')).toContainText('Nothing was drafted yet');
    await shot(page, info, 'action-land');
    // a squash in place is not a landing, and its hint does not say so
    await page.getByTestId('action-cancel').click();
    await menu(page, 'squash');
    await expect(page.getByTestId('action-hint')).not.toContainText(/lands/);
    // sent with no message, the squash waits on a drafting pass: the
    // dialog says it is drafting until the draft comes back into the box
    let release!: () => void;
    const held = new Promise<void>((r) => { release = r; });
    await page.route(`**/api/cards/${id}/actions/squash`, async (route) => { await held; await route.continue(); });
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-confirm')).toHaveText('Drafting…');
    await expect(page.getByTestId('action-confirm')).toBeDisabled();
    await expect(page.getByTestId('action-hint')).toContainText('gummi is drafting the message');
    await expect(page.getByTestId('action-input')).not.toBeEditable();
    await shot(page, info, 'action-drafting');
    release();
    await expect(page.getByTestId('action-input')).toHaveValue(/^feat: /, { timeout: 60_000 });
    // what came back is gummi's draft, and the hint says so
    await expect(page.getByTestId('action-hint')).toContainText('Drafted by gummi just now');
    await expect(page.getByTestId('action-confirm')).toHaveText('Squash');
    await expect(page.getByTestId('action-input')).toBeEditable();
    await page.unroute(`**/api/cards/${id}/actions/squash`);
    await page.getByTestId('action-cancel').click();
    // the landing, sent with no message, drafts its own for the person to
    // read, and the second press lands it
    await menu(page, 'merge');
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-input')).toHaveValue(/^feat: /, { timeout: 60_000 });
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-dialog')).toHaveCount(0, { timeout: 30_000 });
    if (phone(info)) await page.getByTestId('card-back').click();
    await expect(page.getByTestId('rail-group-done').getByTestId(`rail-row-${id}`)).toBeVisible();
    // the group holds every closed card, landed or handed off: it is not
    // headed as if each one landed
    await expect(page.getByTestId('rail-group-done')).toContainText('Done');
    await expect(page.getByTestId('rail-group-done')).not.toContainText('Landed');
    expect(await workspace.git('log', '-1', '--format=%s', 'main')).toMatch(/^feat: /);
    await shot(page, info, 'landed');
  });
});

test.describe('a running card', () => {
  test.use({ workspaceEnv: { GUMMI_E2E_SLOW_SECONDS: '30' } });

  test('stops its turn from the head’s button', async ({ pairedPage: page, server, api }, info) => {
    const c = (await api('POST', '/api/cards', { kind: 'freeform', title: '[slow] Add a lazy helper' })).json;
    await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.status).toBe('running');
    await open(page, server, c.id, phone(info));
    const pause = page.getByTestId('action-btn-pause');
    await expect(pause).toBeVisible();
    await shot(page, info, 'running');
    await pause.click();
    await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.status).not.toBe('running');
    if (phone(info)) await page.getByTestId('card-back').click();
    await expect(page.getByTestId(`rail-row-${c.id}`)).not.toHaveAttribute('data-status', 'running');
  });
});
