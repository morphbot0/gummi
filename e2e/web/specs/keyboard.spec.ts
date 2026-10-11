import type { Page } from '@playwright/test';
import { expect, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';

// The board without a mouse: pair, walk the rail with j and k, open the
// documents with the g keys, answer a decision with a digit and enter,
// jump with the palette, open a view from it and close it with esc — and
// wherever the focus lands on the way, it can be seen.

let ids: { wave: string; nod: string };

test.use({
  seed: {
    run: async (ws) => {
      ids = { wave: await ws.seedDesignGate('Add a wave helper'), nod: await ws.seedDesignGate('Add a nod helper') };
    },
  },
});

/** What has the focus, and whether it shows: an outline, or (for a text field) its own frame. */
async function focus(page: Page) {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return { id: 'body', shown: true };
    const cs = getComputedStyle(el);
    const outline = cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0;
    const field = /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName);
    return { id: el.dataset.testid || el.id || el.tagName, shown: outline || field };
  });
}

test('the whole loop, from the keyboard alone', async ({ page, server }, info) => {
  test.skip(info.project.name === 'phone', 'a phone has no keyboard shortcuts');

  // pairing: the name field has the focus; tab reaches the code, enter pairs
  await page.goto(server.url);
  await expect(page.getByTestId('pair-name')).toBeFocused();
  await page.keyboard.type('Keys');
  await page.keyboard.press('Tab');
  await expect(page.getByTestId('pair-code')).toBeFocused();
  await page.keyboard.type(await server.freshCode());
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
  await expect(page.getByTestId('card-id')).toHaveText(/FD-/);

  // tabbing through the page: every stop shows where it is
  const unseen: string[] = [];
  for (let i = 0; i < 25; i++) {
    await page.keyboard.press('Tab');
    const f = await focus(page);
    if (!f.shown) unseen.push(f.id);
  }
  expect(unseen, 'focused but not visibly').toEqual([]);
  await page.keyboard.press('Escape');
  await page.evaluate(() => (document.activeElement as HTMLElement)?.blur());

  // j and k walk the rail
  const first = await page.getByTestId('card-id').textContent();
  await page.keyboard.press('j');
  await expect(page.getByTestId('card-id')).not.toHaveText(first!);
  await page.keyboard.press('k');
  await expect(page.getByTestId('card-id')).toHaveText(first!);

  // g then a letter picks a document tab
  await page.keyboard.press('g');
  await page.keyboard.press('d');
  await expect(page.getByTestId('tab-diff')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('g');
  await page.keyboard.press('r');
  await expect(page.getByTestId('tab-stats')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('g');
  await page.keyboard.press('s');
  await expect(page.getByTestId('tab-spec')).toHaveAttribute('aria-selected', 'true');

  // a digit highlights an answer and enter gives it
  const d = page.getByTestId('decision');
  await expect(d).toHaveAttribute('data-kind', 'gate');
  await page.keyboard.press('3');
  await expect(page.getByTestId('decision-option-pause')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('composer-says')).toHaveText('stop here');
  await page.keyboard.press('1');
  await expect(page.getByTestId('decision-option-advance')).toHaveAttribute('aria-pressed', 'true');
  await shot(page, info, 'keyboard-answer');
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('toast').filter({ hasText: 'Answered: approve' })).toBeVisible();

  // the palette jumps to a card by its id
  const other = first === ids.wave ? ids.nod : ids.wave;
  await page.keyboard.press('Control+k');
  await expect(page.getByTestId('palette-input')).toBeFocused();
  await page.keyboard.type(other);
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('palette')).toHaveCount(0);
  await expect(page.getByTestId('card-id')).toHaveText(other);

  // …and opens a view by name; esc closes it and the keys work again
  await page.keyboard.press('Control+k');
  await page.keyboard.type('doctor');
  await expect(page.getByTestId('palette-view-doctor')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('view-doctor')).toBeVisible();
  expect((await focus(page)).shown).toBe(true);
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('view-doctor')).toHaveCount(0);

  // / filters the rail (esc clears it and leaves), c writes, esc leaves
  // the composer, ? lists the keys
  await page.keyboard.press('/');
  await expect(page.getByTestId('rail-filter')).toBeFocused();
  await page.keyboard.type('no such card');
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('rail-filter')).toHaveValue('');
  await expect(page.getByTestId('rail-filter')).not.toBeFocused();
  await page.keyboard.press('[');
  await page.keyboard.press('/');
  await expect(page.getByTestId('rail-filter'), 'a folded rail opens for it').toBeFocused();
  await page.keyboard.press('Escape');
  await page.keyboard.press('c');
  await expect(page.getByTestId('composer-input')).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('composer-input')).not.toBeFocused();
  await page.keyboard.press('?');
  await expect(page.getByTestId('keys-help')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('keys-help')).toHaveCount(0);
});
