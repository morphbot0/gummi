import { expect, ref, test, type GummiServer } from '../fixtures/test';
import type { Page } from '@playwright/test';

// A question the board stops at — a landing message to read, a yes to give
// before a hand-off, a delete — is ordinary control
// flow, so the server answers it 202 (webapi.StatusQuestion) rather than as
// an HTTP error. A browser logs every 4xx/5xx a page fetches as "Failed to
// load resource" in red, and no page code can silence that; these flows
// must leave the console clean, while still asking every question.

// watch collects the console's failed loads, and the questions the server
// answered (a 202 from an answer or an action), so a test can tell that it
// really went through a question and that the question logged nothing.
function watch(page: Page) {
  const failed: string[] = [];
  const questions: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error' && /Failed to load resource/.test(m.text())) failed.push(`${m.text()} — ${m.location().url}`);
  });
  page.on('response', (r) => {
    if (r.status() === 202 && /\/api\/cards\/[^/]+\/(answer|actions\/)/.test(r.url())) questions.push(r.url());
  });
  return { failed, questions };
}

async function open(page: Page, server: GummiServer, id: string) {
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
}

// The console is the same on every viewport; one project is enough.
test.beforeEach(async ({}, info) => {
  test.skip(info.project.name !== 'desktop', 'console noise does not depend on the viewport');
});

test.describe('two sessions ready to land and one more', () => {
  let landing: string, handoff: string, sessions: string[];
  test.use({
    seed: {
      run: async (ws) => {
        landing = await ws.seedReady('Add a farewell helper');
        handoff = await ws.seedReady('Add a parting helper');
        sessions = await ws.seedSessions(['Add a shrug helper']);
      },
    },
  });

  test('landing, hand-off and delete ask without a console error', async ({ pairedPage: page, server, api }) => {
    const { failed, questions } = watch(page);

    // a landing sent with no message stops to have the drafted one read
    await open(page, server, landing);
    await page.getByTestId('card-actions').click();
    await page.getByTestId('action-merge').click();
    await page.getByTestId('action-confirm').click();
    await expect(page.getByTestId('action-input')).not.toHaveValue('', { timeout: 60_000 });
    await page.getByTestId('action-confirm').click();
    await expect.poll(async () => (await api('GET', `/api/cards/${landing}`)).json.stage, { timeout: 60_000 }).toBe('done');
    expect(failed, 'failed loads logged to the console').toEqual([]);
    expect(questions.length, 'the landing asked for its message').toBeGreaterThanOrEqual(1);

    // a hand-off stops at the board's yes
    const before = questions.length;
    await open(page, server, handoff);
    await page.getByTestId('card-actions').click();
    await page.getByTestId('action-handoff').click();
    await expect(page.getByTestId('action-question')).toContainText(/Hand off “.+”/);
    await page.getByTestId('action-confirm').click();
    await expect.poll(async () => (await api('GET', `/api/cards/${handoff}`)).json.stage).toBe('done');
    expect(failed, 'failed loads logged to the console').toEqual([]);
    expect(questions.length, 'the hand-off asked first').toBeGreaterThan(before);

    // a delete from the menu stops at the board's own question
    const mid = questions.length;
    await open(page, server, sessions[0]);
    await page.getByTestId('card-actions').click();
    await page.getByTestId('action-delete').click();
    await expect(page.getByTestId('action-question')).toContainText(/Delete “.+”\?/);
    await page.getByTestId('action-confirm').click();
    await expect.poll(async () => (await api('GET', `/api/cards/${sessions[0]}`)).status).toBe(404);
    expect(failed, 'failed loads logged to the console').toEqual([]);
    expect(questions.length, 'the delete asked first').toBeGreaterThan(mid);
  });
});

