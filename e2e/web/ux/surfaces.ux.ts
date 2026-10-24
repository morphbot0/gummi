import { expect, test } from '../fixtures/test';
import { seedBoard, seedLive, type Board } from '../fixtures/board';
import { Tour } from './tour';
import { live, openCard, toRail, toTab } from './nav';

// Every surface a person can reach, found the way a person finds it: each
// of a card's documents from its icons, each board view from the More menu
// (whatever that menu lists today), the palette and the keys. Not a flow:
// the reviewer reads it for consistency, empty states and dead ends.

let board: Board;

test.use({
  serverArgs: ['--terminal'],
  workspaceEnv: { GUMMI_E2E_SLOW_SECONDS: '900', SHELL: '/bin/sh' },
  seed: { run: async (ws) => { board = await seedBoard(ws); } },
});

for (const theme of ['light', 'dark'] as const) {
  test(`every surface, ${theme}`, async ({ pairedPage: page, server }, info) => {
    test.setTimeout(300_000);
    await seedLive(page.context().request, server, board, { running: false });
    await page.emulateMedia({ colorScheme: theme });
    await page.goto(server.url);
    const t = new Tour(page, info, `surfaces-${theme}`, 'Every surface a person can reach, for consistency, empty states and dead ends', theme);
    await live(t);

    // a card's documents, from the icons beside it
    await openCard(t, board.commented);
    await t.shot('card', 'A card with work and an open comment');
    for (const tab of ['spec', 'memory', 'stats', 'terminal', 'diff', 'tree', 'log', 'pr']) {
      if (await toTab(t, tab, { soft: true })) await t.shot(`doc-${tab}`, `The ${tab} document: is its title right, is its empty state helpful?`);
    }

    // the board's views, from the More menu
    await toRail(t);
    await t.click('open the More menu', page.getByTestId('rail-more'));
    await t.shot('more-menu', 'Everything the More menu offers: is it grouped and named so a person finds things?');
    const items = await page.locator('[data-testid^="menu-"]').evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')!));
    await page.keyboard.press('Escape');
    for (const item of items.filter((i) => !['menu-unpair', 'menu-push'].includes(i))) {
      const name = item.replace(/^menu-/, '');
      const opened = await t.maybe(`open ${name}`, async () => {
        await toRail(t);
        await page.getByTestId('rail-more').click();
        await page.getByTestId(item).click();
        await expect(page.getByTestId(`view-${name}`)).toBeVisible();
        await expect(page.getByTestId(`view-${name}`).locator('.spinner')).toHaveCount(0, { timeout: 15_000 });
      });
      if (!opened) continue;
      await t.shot(`view-${name}`, `The ${name} view: what is it for, and is the first thing to do obvious?`);
      await page.keyboard.press('Escape');
    }
    await t.maybe('open fleet stats', async () => { await toRail(t); await page.getByTestId('rail-fleet').click(); await expect(page.getByTestId('view-fleet')).toBeVisible(); });
    await t.shot('view-fleet', 'Fleet stats: what does a person learn here?');
    await page.keyboard.press('Escape');

    if (!t.phone) {
      await t.click('open the palette', page.getByTestId('btn-palette'));
      await t.shot('palette', 'The palette: does it find cards and views by what a person would type?');
      await page.keyboard.press('Escape');
      await t.click('open the keys', page.getByTestId('btn-keys'));
      await t.shot('keys', 'The key table: is it complete and scannable?');
      await page.keyboard.press('Escape');
    }
    t.finish();
  });
}
