import { expect, test } from '../fixtures/test';

// On a slow link every read the page waits for is a round trip. The event
// stream brings the open card's live block itself, so a session's reply —
// which only its live block draws — is on screen as soon as the agent
// writes it, even while every read of the live block is held back.

test('the open card’s live block comes with its event, not a read later', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(90_000);
  const id = String((await api('POST', '/api/cards', { kind: 'freeform', title: 'Push the live block' })).json?.id);
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  if (info.project.name === 'phone') await page.getByTestId('tab-thread').click();
  await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
  const replies = page.getByTestId('reply').filter({ hasText: 'Done: noted it in NOTES.md.' });
  await expect(replies).toHaveCount(1, { timeout: 30_000 });

  // from here a read of the live block takes six seconds
  await page.route(`**/api/cards/${id}/live`, async (r) => { await new Promise((ok) => setTimeout(ok, 6000)); await r.fallback(); });
  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill('note it once more');
  await page.keyboard.press('Enter');
  await expect(replies).toHaveCount(2, { timeout: 3000 });
});
