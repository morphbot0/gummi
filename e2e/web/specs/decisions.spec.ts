import { expect, ref, pair, showTab, test, type GummiServer } from '../fixtures/test';
import type { Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { shot } from '../fixtures/shots';

// Answering a card's pinned decision against a real `gummi web` and the
// scripted agent: an agent's question (an option, and "chat about this"),
// one that outlived its call or its backend, and the next card waiting.
// The phone answers from the docked decision bar.

const isPhone = (info: { project: { name: string } }) => info.project.name === 'phone';

async function open(page: Page, server: GummiServer, id: string) {
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
}

// answerOption answers the pinned decision with one option: on a phone
// from the docked bar (the documents view shows it), elsewhere from the
// pinned block. One press gives it: what asks first (a landing's message,
// a yes) is the server's to ask.
async function answerOption(page: Page, phone: boolean, option: string) {
  if (phone) {
    await showTab(page, 'diff');
    await page.getByTestId('mdec-toggle').click();
    await page.getByTestId(`mdec-option-${option}`).click();
    return;
  }
  await page.getByTestId(`decision-option-${option}`).click();
}

async function thread(page: Page, phone: boolean) {
  if (phone) await page.getByTestId('tab-thread').click();
  // a session's turns are drawn in its live conversation, under the items
  return page.getByTestId('thread');
}

test.describe('two sessions waiting on their questions', () => {
  let ids: string[];
  test.use({ seed: { run: async (ws) => { ids = [(await ws.seedAsk('Add a wave helper')).id, (await ws.seedAsk('Add a nod helper')).id]; } } });

  // the chip names the next card's stop in the rail's words, never a
  // command line
  test('the next card waiting is offered where the answer was given', async ({ pairedPage: page, server }, info) => {
    const [a, b] = ids;
    await open(page, server, a);
    // the question outlived the run that asked it: its words answer it
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    await page.getByTestId('decision-option-chat').click();
    await page.getByTestId('composer-input').fill('Left, beside Greet');
    await page.keyboard.press('Enter');
    const chip = page.locator('[data-testid="nextup"]:visible').first();
    await expect(chip).toBeVisible({ timeout: 30_000 });
    await expect(chip).toContainText(/Add a nod helper” also needs you/);
    await expect(chip).not.toContainText('gummi resume');
    await shot(page, info, 'nextup');
    await chip.click();
    await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', b);
  });
});

test.describe('a session linked to a pull request', () => {
  let id: string;
  test.use({ seed: { run: async (ws) => { id = await ws.seedReady('Add a farewell helper'); await ws.linkPR(id); } } });

  // it lands on GitHub: the menu offers no local landing for it, and the
  // way to land it here anyway is the menu's own unlink
  test('offers no local landing', async ({ pairedPage: page, server }, info) => {
    await open(page, server, id);
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    await page.getByTestId('card-actions').click();
    await expect(page.getByTestId('card-actions-menu')).toBeVisible();
    await expect(page.getByTestId('action-merge')).toHaveCount(0);
    await expect(page.getByTestId('action-prunlink')).toBeVisible();
    await expect(page.getByTestId('landing-dialog')).toHaveCount(0);
    await shot(page, info, 'pr-merge-refused');
  });
});

test.describe('an agent’s question', () => {
  // A question's options live only in the process that asked it, so the
  // card is started from this board rather than seeded on the seeding
  // board, whose agents stop with it.
  async function ask(api: any, title = '[ask-where] Add a choosy helper'): Promise<string> {
    const c = (await api('POST', '/api/cards', { kind: 'freeform', title })).json;
    await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.decision?.kind).toBe('ask');
    return c.id;
  }

  test('an option answers it', async ({ pairedPage: page, server, api }, info) => {
    const id = await ask(api);
    await open(page, server, id);
    await expect(page.getByTestId('decision-question')).toContainText('Where should');
    await expect(page.getByTestId('decision-option-chat')).toBeVisible();
    await shot(page, info, 'ask');
    await answerOption(page, isPhone(info), '1');
    const items = await thread(page, isPhone(info));
    await expect(items).toContainText('Extend the existing file');
    await expect.poll(async () => (await api('GET', `/api/cards/${id}`)).json.decision?.kind).not.toBe('ask');
  });

  // A question long enough to overflow the decision's cap on every
  // viewport: the question yields first (scrolling inside its own region,
  // down to a floor of about two lines) so the answers list never gets
  // pushed past the box's bottom edge.
  test('a long question keeps its answers in reach', async ({ pairedPage: page, server, api }, info) => {
    const id = await ask(api, '[ask-long] Add a wordy helper');
    await open(page, server, id);
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    const decision = page.getByTestId('decision');
    await expect(decision).toHaveAttribute('data-kind', 'ask');
    const q = page.getByTestId('decision-question');

    // (a) the answers list's rect lies inside the decision box's rect, and
    // is at least as tall as its first two answers; (b) the box itself
    // never scrolls
    const geo = await decision.evaluate((box) => {
      const opts = box.querySelector('.opts')!;
      const first = opts.querySelector('.opt')!.getBoundingClientRect();
      const b = box.getBoundingClientRect();
      const o = opts.getBoundingClientRect();
      return { top: o.top - b.top, bottom: b.bottom - o.bottom, height: o.height, floor: first.height * 2, scrollTop: box.scrollTop };
    });
    expect(geo.top, 'the answers list starts inside the box').toBeGreaterThanOrEqual(-0.5);
    expect(geo.bottom, 'the answers list ends inside the box').toBeGreaterThanOrEqual(-0.5);
    expect(geo.height, 'the answers list keeps room for two answers').toBeGreaterThanOrEqual(geo.floor - 0.5);
    expect(geo.scrollTop, 'the box itself is never scrolled').toBe(0);

    // (c) every answer, scrolled into view within the list, is the element
    // at its own centre point (reachable and tappable)
    const options = decision.locator('.opt');
    const count = await options.count();
    for (let i = 0; i < count; i++) {
      const opt = options.nth(i);
      await opt.scrollIntoViewIfNeeded();
      const hit = await opt.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return !!at && (at === el || el.contains(at));
      });
      expect(hit, `option ${i} is reachable and tappable`).toBe(true);
    }

    // (d) the question region is scrollable, and scrolled to its end its
    // last line is inside the box
    expect(await q.evaluate((el) => el.scrollHeight > el.clientHeight), 'the question scrolls').toBe(true);
    const qEnd = await q.evaluate((el) => { el.scrollTop = el.scrollHeight; return el.getBoundingClientRect().bottom; });
    const boxBox = (await decision.boundingBox())!;
    expect(qEnd, 'the question, scrolled to its end, is inside the box').toBeLessThanOrEqual(boxBox.y + boxBox.height + 0.5);
    await shot(page, info, 'ask-long');

    // the question's scroll position survives a redraw of the decision (a
    // highlight move), as the answers list's already does
    await q.evaluate((el) => { el.scrollTop = 10 });
    if (isPhone(info)) {
      // tapping the chat answer only highlights it: it needs words before
      // it answers, so this is a redraw with no answer given
      await page.getByTestId('decision-option-chat').click();
    } else {
      // a digit only moves the highlight; enter is what answers
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
      await page.keyboard.press('1');
    }
    await expect.poll(() => q.evaluate((el) => el.scrollTop)).toBe(10);
    await expect(decision).toHaveAttribute('data-kind', 'ask');

    // (e) tapping the second answer answers the question (card leaves ask)
    await page.getByTestId('decision-option-2').click();
    await expect.poll(async () => (await api('GET', `/api/cards/${id}`)).json.decision?.kind).not.toBe('ask');
  });

  test('chat about this answers in your words', async ({ pairedPage: page, server, api }, info) => {
    const id = await ask(api);
    await open(page, server, id);
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    // the chat row with nothing typed asks for the words
    await page.getByTestId('decision-option-chat').click();
    await expect(page.getByTestId('decision-needs')).toContainText('Type your answer');
    await page.getByTestId('composer-input').fill('Put it beside Greet, in greet.go');
    await expect(page.getByTestId('composer-says')).toHaveText('answers “Chat about this”');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('thread')).toContainText('Put it beside Greet, in greet.go');
    await expect.poll(async () => (await api('GET', `/api/cards/${id}`)).json.decision?.kind).not.toBe('ask');
  });

  // A reply being written to the question while another device answers
  // it: the words stay, and the page says the card moved.
  test('words typed for a question someone else answered are not re-aimed unannounced', async ({ pairedPage: page, server, api }, info) => {
    test.skip(isPhone(info), 'enter answers from the composer on a keyboard');
    const id = await ask(api);
    await open(page, server, id);
    await expect(page.getByTestId('decision')).toHaveAttribute('data-kind', 'ask');
    await page.getByTestId('composer-input').fill('Put it beside Greet, in greet.go');
    await expect(page.getByTestId('composer-says')).toHaveText('answers “Chat about this”');
    // the other device picks an option
    const d = (await api('GET', `/api/cards/${id}`)).json.decision;
    await api('POST', `/api/cards/${id}/answer`, { ref: d.ref, option: '0', against: d.against.token });
    await expect(page.locator('[data-testid="decision"][data-kind="ask"]')).toHaveCount(0, { timeout: 30_000 });
    await expect(page.getByTestId('decision-moved')).toContainText('while you were writing');
    await expect(page.getByTestId('composer-input')).toHaveValue('Put it beside Greet, in greet.go');
  });

});

// A backend's own MCP client bounds a tool call (a minute, on several of
// them), and a question is a tool call that waits on a person. When the
// bound passes the agent is told its call failed, says so and ends its
// turn, with the question still on the page. A turn that ended on an open
// question has not finished the stage: the card waits where it is, and
// the answer reaches the architect as its next turn.
test.describe('a question the agent stopped waiting on', () => {
  async function ask(api: any): Promise<string> {
    const c = (await api('POST', '/api/cards', { kind: 'freeform', title: '[ask-gives-up] Add a patient helper' })).json;
    await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.decision?.kind).toBe('ask');
    return c.id;
  }

  test('stays open, and its answer still reaches the architect', async ({ pairedPage: page, server, api }, info) => {
    const id = await ask(api);
    await open(page, server, id);
    await expect(page.getByTestId('decision-question')).toContainText('Where should');
    // the agent gives up on its call and ends its turn while the reader
    // is still looking at the question
    // (a running stage's turns are drawn under the settled items, so
    // this reads the whole thread)
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    await expect(page.getByTestId('thread')).toContainText('The ask timed out');
    // long enough for a critique to have started, had the stage been
    // taken for finished
    await page.waitForTimeout(3000);
    const card = (await api('GET', `/api/cards/${id}`)).json;
    expect(card.decision?.kind).toBe('ask');
    expect(card.status).toBe('needs');
    await expect(page.getByTestId('thread')).not.toContainText('critique');
    await expect(page.getByTestId('thread')).not.toContainText('superseded');
    await shot(page, info, 'ask-outlived-its-call');
    if (!isPhone(info)) {
      await expect(page.getByTestId('decision')).toHaveAttribute('data-kind', 'ask');
      await expect(page.getByTestId('decision-question')).toContainText('Where should');
    }

    await answerOption(page, isPhone(info), '1');
    await expect(await thread(page, isPhone(info))).toContainText('Extend the existing file');
    // the architect finishes the plan with the answer
    await expect.poll(async () => JSON.stringify((await api('GET', `/api/cards/${id}/thread`)).json), { timeout: 30_000 }).toContain('Decided with the user: Extend the existing file');
  });
});

// A question outlives more than its call: the backend behind it can die
// while the person reads, or the host can restart under it. Either way the
// question stays on the page, and answering it is what brings the card
// back — a backend is started for the answer, which reaches the architect
// as a turn. Both used to refuse every answer (the web has no attach step)
// while writing each try to the log, so the question vanished at the next
// restart and the spec recorded choices nobody had delivered.
test.describe('a question whose backend went away', () => {
  async function ask(api: any, title: string): Promise<string> {
    const c = (await api('POST', '/api/cards', { kind: 'freeform', title })).json;
    await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.decision?.kind).toBe('ask');
    return c.id;
  }

  test('a backend that died behind it is replaced by the answer', async ({ pairedPage: page, server, api }, info) => {
    const id = await ask(api, '[ask-dies] Add a fragile helper');
    await open(page, server, id);
    await expect(page.getByTestId('decision-question')).toContainText('Where should');
    // the process dies while the reader is looking at the question
    await expect.poll(async () => (await api('GET', `/api/cards/${id}/live`)).json.freeform?.err ?? '', { timeout: 15_000 }).not.toBe('');
    expect((await api('GET', `/api/cards/${id}`)).json.decision?.kind).toBe('ask');
    await shot(page, info, 'ask-backend-died');

    await answerOption(page, isPhone(info), '1');
    // the answer opens the new backend's session, a running one: its turns
    // are drawn under the settled items, so this reads the whole thread
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    await expect(page.getByTestId('thread')).toContainText('Extend the existing file');
    await expect.poll(async () => JSON.stringify((await api('GET', `/api/cards/${id}/thread`)).json), { timeout: 30_000 }).toContain('Decided with the user: Extend the existing file');
    expect(JSON.stringify((await api('GET', `/api/cards/${id}/thread`)).json)).not.toContain('A new file (recommended)');
  });

  test('a restart keeps it, and its answer carries the card on', async ({ pairedPage: page, server, api }, info) => {
    const id = await ask(api, '[ask-where] Add a durable helper');
    await server.restart();
    await open(page, server, id);
    // the options died with the process that asked; the words are left
    await expect(page.getByTestId('decision-question')).toContainText('Where should');
    await expect(page.getByTestId('decision-option-chat')).toBeVisible();
    await shot(page, info, 'ask-restored');
    if (isPhone(info)) await page.getByTestId('tab-thread').click();
    await page.getByTestId('decision-option-chat').click();
    await page.getByTestId('composer-input').fill('Put it beside Greet, in greet.go');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('thread')).toContainText('Put it beside Greet, in greet.go');
    await expect.poll(async () => JSON.stringify((await api('GET', `/api/cards/${id}/thread`)).json), { timeout: 30_000 }).toContain('Decided with the user: Put it beside Greet, in greet.go');
  });
});

// A question that takes several answers, on a phone: tapping answers in
// the docked bar only ticks them, and a phone has no enter, so the bar
// must carry its own way to send them.
test('a phone sends a multi-pick answer from the docked bar', async ({ pairedPage: page, server, api }, info) => {
  test.skip(!isPhone(info), 'the docked bar is the phone’s');
  const c = (await api('POST', '/api/cards', { kind: 'freeform', title: '[ask-multi] Add a plural helper' })).json;
  await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.decision?.multi).toBe(true);
  await open(page, server, c.id);
  await showTab(page, 'diff');
  await page.getByTestId('mdec-toggle').click();
  await expect(page.getByTestId('mdec-send')).toBeDisabled();
  await page.getByTestId('mdec-option-0').click();
  await page.getByTestId('mdec-option-1').click();
  await expect(page.getByTestId('mdec-send')).toContainText('2 choices');
  await shot(page, info, 'mdec-multi');
  await page.getByTestId('mdec-send').click();
  await expect.poll(async () => (await api('GET', `/api/cards/${c.id}`)).json.decision?.kind, { timeout: 30_000 }).not.toBe('ask');
});
