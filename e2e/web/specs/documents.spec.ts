import { expect, showCard, showTab, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';

// The card page against a real `gummi web` and a session the scripted
// agent gave a spec and a commit, with a linked pull request (fake gh):
// the spec, diff, PR and stats tabs as the server serves them.

let id: string;
test.use({ seed: { run: async (ws) => { id = await ws.seedReady('[spec] Add a noted helper'); await ws.linkPR(id); } } });

test('the spec, diff, PR and stats tabs read the real card', async ({ pairedPage: page }, info) => {
  await showCard(page, id);
  await showTab(page, 'spec');
  // the outline is a menu, on a phone as well; picking a section goes to it
  await page.getByTestId('spec-toc').click();
  const outline = page.getByTestId('spec-toc-menu');
  await expect(outline).toContainText('Approach');
  await outline.getByRole('menuitem', { name: /Approach/ }).click();
  await expect(outline).toHaveCount(0);
  await expect(page.getByTestId('spec-doc').getByRole('heading', { name: /Approach/ })).toBeInViewport();
  await shot(page, info, 'real-spec');

  await showTab(page, 'diff');
  await expect(page.getByTestId('diff-files')).toContainText('NOTES.md');
  const line = page.locator('[data-testid^="diff-line-"]').first();
  await line.locator('.n').click();
  await page.getByTestId('annotation-input').fill('Say what the helper returns');
  await page.getByTestId('annotation-save').click();
  await expect(page.locator('[data-testid^="annotation-"]').filter({ hasText: 'Say what the helper' }).first()).toBeVisible();
  await expect(page.getByTestId('diff-pending')).toContainText('comment');
  await shot(page, info, 'real-diff');

  await showTab(page, 'pr');
  await expect(page.getByTestId('pr-state')).toContainText('open');
  await expect(page.getByTestId('pr-push-cmd')).toContainText('git push');
  await showTab(page, 'stats');
  await expect(page.getByTestId('stats-spent')).toBeVisible();
  await shot(page, info, 'real-stats');
});
