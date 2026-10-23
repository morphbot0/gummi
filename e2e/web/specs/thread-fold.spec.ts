import { expect, test } from '../fixtures/test';

// Once a person toggles a foldable row of the card thread, the row keeps
// their open state across every later redraw for the rest of the page
// session: an activity row is rebuilt when a call appends or its status
// settles, the pinned task list is rebuilt on every live delta, and a stage
// divider is rebuilt by a refetch — a rebuilt <details> starts closed, so
// each builder re-applies what the person last set. Rows they never touched
// keep the defaults, and the memory is scoped per card. Page session only:
// a reload forgets, so cards are switched with rail clicks, never reloads.

test.describe('the fold a person set on a thread row survives redraws', () => {
  test('a folded task list stays folded across the next live delta', async ({ pairedPage: page, server, api }, info) => {
    test.skip(info.project.name !== 'desktop', 'the fold memory is page state; one viewport is enough');
    test.setTimeout(90_000);
    const made = await api('POST', '/api/cards', { kind: 'freeform', title: 'Fold the checklist' });
    const id = String(made.json?.id);
    await page.goto(`${server.url}/#${id}`);
    await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
    await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });

    // the checklist is pinned open while anything is left; folding it is
    // the person's choice
    const tasks = page.getByTestId('tasks');
    await expect(tasks).toContainText('tasks 1/2');
    await expect(tasks).toHaveAttribute('open', '');
    await tasks.locator('summary').click();
    await expect(tasks).not.toHaveAttribute('open', '');

    // the next turn rebuilds the pinned list on every live delta; it stays
    // folded through them and after the turn ends
    const input = page.getByTestId('composer-input');
    await input.click();
    await input.fill('note the second fold probe too');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('thread')).toContainText('note the second fold probe too', { timeout: 30_000 });
    await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
    await expect(tasks).not.toHaveAttribute('open', '');
  });

  // The pinned list opens by rule while anything is left; that default is
  // the page's own doing, never a person's choice, so a list nobody has
  // touched still folds once everything completes.
  test('a task list nobody touched folds when everything completes', async ({ pairedPage: page, server, api }, info) => {
    test.skip(info.project.name !== 'desktop', 'the fold memory is page state; one viewport is enough');
    test.setTimeout(90_000);
    const made = await api('POST', '/api/cards', { kind: 'freeform', title: 'Complete the checklist' });
    const id = String(made.json?.id);
    await page.goto(`${server.url}/#${id}`);
    await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
    await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });

    const tasks = page.getByTestId('tasks');
    await expect(tasks).toContainText('tasks 1/2');
    await expect(tasks).toHaveAttribute('open', '');
    const input = page.getByTestId('composer-input');
    await input.click();
    await input.fill('[tasks-done] wrap it up');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('thread')).toContainText('wrap it up', { timeout: 30_000 });
    await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
    await expect(tasks).toContainText('tasks 2/2');
    await expect(tasks).not.toHaveAttribute('open', '');
  });
});

