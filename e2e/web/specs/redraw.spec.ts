import { expect, test } from '../fixtures/test';

// Every event the page hears is an invalidation, and most leave what the
// open card shows as it was. A change elsewhere on the board, and a refetch
// that answers what the page already holds, must draw nothing: a node taken
// out and drawn again loses the reader's selection in it, the scroll of its
// output and whatever they had unclamped.

test('a change elsewhere on the board leaves the open card’s thread and panel standing', async ({ pairedPage: page, server, api }, info) => {
  test.skip(info.project.name !== 'desktop', 'node identity is page state; one viewport is enough');
  test.setTimeout(90_000);
  const made = await api('POST', '/api/cards', { kind: 'freeform', title: 'Hold still' });
  const id = String(made.json?.id);
  await page.goto(`${server.url}/#${id}/diff`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
  await expect(page.getByTestId('live-freeform-head')).toBeVisible();
  await expect(page.locator('#pane > *').first()).toBeVisible();
  // the fixture's turn ends on a commit, which offers landing: once the
  // head says so it has settled, and the page has drawn it once it falls
  // quiet
  await expect.poll(async () => ((await api('GET', `/api/cards/${id}`)).json?.actions || []).some((a: { id: string }) => a.id === 'merge'), { timeout: 30_000 }).toBe(true);
  await page.evaluate(() => new Promise<void>((done) => {
    let t = setTimeout(fin, 800);
    const mo = new MutationObserver(() => { clearTimeout(t); t = setTimeout(fin, 800); });
    function fin () { mo.disconnect(); done(); }
    for (const sel of ['#pane', '#thread-live']) mo.observe(document.querySelector(sel)!, { childList: true });
  }));

  // tag what is drawn now: a node drawn again comes back without the tag
  const tag = () => page.evaluate(() => {
    const els = [
      document.querySelector('[data-testid="live-freeform-head"]'),
      document.querySelector('#thread-live [data-testid="bubble"]'),
      document.querySelector('#pane > *')
    ];
    els.forEach(el => { if (el) (el as any).__kept = true; });
    return els.map(el => !!el);
  });
  const kept = () => page.evaluate(() => [
    document.querySelector('[data-testid="live-freeform-head"]'),
    document.querySelector('#thread-live [data-testid="bubble"]'),
    document.querySelector('#pane > *')
  ].map(el => !!(el as any)?.__kept));
  const present = await tag();
  expect(present).toEqual([true, true, true]);

  // another card is made: the board moves, the open card does not
  const other = await api('POST', '/api/cards', { kind: 'freeform', title: 'Somewhere else' });
  await expect(page.locator(`[data-id="${other.json.id}"]`).first()).toBeVisible({ timeout: 15_000 });
  expect(await kept()).toEqual([true, true, true]);
});
