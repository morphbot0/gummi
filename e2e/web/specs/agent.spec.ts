import { expect, test } from '../fixtures/test';

// An agent, against a real `gummi web`: New agent in the rail's new menu opens
// the session draft as an agent's, the first message mints it into a home
// of its own, and its page is a session's without anything a branch would
// give it.
test('an agent starts in a home of its own and has no branch surfaces', async ({ pairedPage: page }, info) => {
  test.skip(info.project.name === 'phone', 'the rail foot is the desktop entry');
  await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
  await page.getByTestId('rail-new-menu').click();
  await page.getByTestId('rail-new-agent').click();
  await expect(page.getByTestId('card-title')).toHaveText('New agent');
  await expect(page.getByTestId('draft-where')).toHaveText('a home of its own · no repository');

  const input = page.getByTestId('composer-input');
  await input.fill('Watch the nightly CI.');
  await input.press('Enter');
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', /^FF-/, { timeout: 20_000 });
  const id = (await page.getByTestId('card-id').getAttribute('data-id'))!;

  await expect(page.getByTestId('card-kind')).toHaveText('Agent');
  await expect(page.getByTestId('card-home')).toHaveText(`⌂.gummi/homes/${id}`);
  await expect(page.getByTestId('rail-group-agents').getByTestId(`rail-row-${id}`)).toBeVisible();

  // no branch, so no Changes surface to diff, log or publish from (its
  // icon is tab-diff; the log and PR tabs live inside it), and no memory
  await expect(page.getByTestId('tab-stats')).toBeVisible();
  for (const t of ['diff', 'memory']) await expect(page.getByTestId(`tab-${t}`)).toHaveCount(0);
  await expect(page.getByTestId('session-land')).toHaveCount(0);
  await expect(page.getByTestId('delegate')).toHaveCount(0);
});
