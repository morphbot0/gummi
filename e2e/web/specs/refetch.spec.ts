import { expect, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';

// The event stream only says what changed; the page reads the card itself.
// A read that fails on the way (a dropped connection while the stream
// stays up, a server that answered 5xx) must be asked again, or the page
// goes on drawing a card that has moved: here a session the page shows
// working long after its agent stopped on a question.

test('a card read that failed is read again', async ({ pairedPage: page, server, api }, info) => {
  const id = String((await api('POST', '/api/cards', { kind: 'freeform', title: 'Add a missed helper' })).json?.id);
  await expect.poll(async () => (await api('GET', `/api/cards/${id}`)).json.status, { timeout: 30_000 }).not.toBe('running');
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  const asking = page.locator('[data-testid="decision"][data-kind="ask"]');
  await expect(asking).toHaveCount(0);

  // every read fails while the turn runs and stops on its question; the
  // stream stays up
  let failing = true;
  await page.route((u) => u.pathname.startsWith('/api/') && u.pathname !== '/api/events', (r) => (failing && r.request().method() === 'GET' ? r.abort('internetdisconnected') : r.fallback()));
  const card = (await api('GET', `/api/cards/${id}`)).json;
  const sent = await api('POST', `/api/cards/${id}/send`, { text: '[ask] which way should it go?', against: card.decision?.against?.token });
  expect(sent.status, sent.text).toBe(200);
  await expect.poll(async () => (await api('GET', `/api/cards/${id}`)).json.decision?.kind, { timeout: 30_000 }).toBe('ask');
  // let what trails the question (its notices, the spend) go by unread too
  await page.waitForTimeout(8000);
  await expect(asking).toHaveCount(0);

  // the network comes back; nothing on the card moves again
  failing = false;
  await expect(page.getByTestId('decision')).toHaveAttribute('data-kind', 'ask', { timeout: 20_000 });
  await shot(page, info, 'refetched-ask');
});
