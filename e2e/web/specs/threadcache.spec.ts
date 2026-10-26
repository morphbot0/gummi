import { expect, test } from '../fixtures/test';

// On a slow link the thread's reads take a while. A card this browser has
// opened before draws its thread as it last saw it — after a reload, and
// when a person comes back to it from another card — rather than a spinner
// until the reads land; the reads still go out and replace it.

test('a card opened before draws its thread before its reads land', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(90_000);
  const id = String((await api('POST', '/api/cards', { kind: 'freeform', title: 'Keep the thread' })).json?.id);
  const other = String((await api('POST', '/api/cards', { kind: 'freeform', title: 'Somewhere else' })).json?.id);
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  if (info.project.name === 'phone') await page.getByTestId('tab-thread').click();
  const replies = page.getByTestId('reply').filter({ hasText: 'Done: noted it in NOTES.md.' });
  await expect(replies).toHaveCount(1, { timeout: 30_000 });
  // what the page keeps across a reload is written once the thread settles
  await page.waitForTimeout(2500);

  // from here every read of a card's thread or live block takes eight seconds
  let held = 0;
  const slow = async (r: { fallback: () => Promise<void> }) => { held++; await new Promise((ok) => setTimeout(ok, 8000)); await r.fallback(); };
  await page.route(`**/api/cards/*/thread*`, slow);
  await page.route(`**/api/cards/*/live`, slow);

  await page.reload();
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  if (info.project.name === 'phone') await page.getByTestId('tab-thread').click();
  await expect(replies).toHaveCount(1, { timeout: 4000 });
  expect(held).toBeGreaterThan(0);

  // away to another card and back: the same, without a reload
  await page.goto(`${server.url}/#${other}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', other);
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  if (info.project.name === 'phone') await page.getByTestId('tab-thread').click();
  await expect(replies).toHaveCount(1, { timeout: 4000 });
});

test('unpairing forgets the threads this browser kept', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(60_000);
  const id = String((await api('POST', '/api/cards', { kind: 'freeform', title: 'Forget the thread' })).json?.id);
  await page.goto(`${server.url}/#${id}`);
  if (info.project.name === 'phone') await page.getByTestId('tab-thread').click();
  await expect(page.getByTestId('reply').filter({ hasText: 'Done: noted it in NOTES.md.' })).toHaveCount(1, { timeout: 30_000 });
  await expect.poll(() => page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('gummi-web:threads:')).length), { timeout: 5000 }).toBeGreaterThan(0);
  await page.evaluate(async () => { await fetch('/api/unpair', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }); });
  await page.reload();
  await expect(page.getByTestId('pair-code')).toBeVisible({ timeout: 10_000 });
  expect(await page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('gummi-web:threads:')).length)).toBe(0);
});
