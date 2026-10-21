import { expect, test } from '../fixtures/test';
import type { Page } from '@playwright/test';
import { shot } from '../fixtures/shots';

// The new-card form against a real `gummi web`: the choices come from
// GET /api/form, the card from POST /api/cards (the TUI's own form,
// filled in and submitted), and a refusal is shown beside its field in the
// form's own words. The form makes a session: it offers no kind.

async function openForm(page: Page) {
  await expect(page.getByTestId('conn')).toHaveAttribute('data-state', 'live');
  await page.getByTestId('rail-new-menu').click();
  await page.getByTestId('rail-new').click();
  await expect(page.getByTestId('view-newcard')).toBeVisible();
  await expect(page.getByTestId('newcard-title')).toBeVisible();
}

const phone = (info: { project: { name: string } }) => info.project.name === 'phone';

test('a session from the form, with its budget and profile', async ({ pairedPage: page, api }, info) => {
  await openForm(page);
  // one kind is no choice: there is no picker
  await expect(page.getByTestId('newcard-kinds')).toHaveCount(0);
  // nothing typed: the form's own refusal, beside the title
  await page.getByTestId('newcard-create').click();
  await expect(page.getByTestId('newcard-error-title')).toHaveText('A card needs a title');
  await page.getByTestId('newcard-title').fill('Add a farewell helper');
  await page.getByTestId('newcard-desc').fill('Greet has no goodbye. Add Farewell(name) beside it, with a test.');
  await expect(page.getByTestId('newcard-envelope')).toHaveValue('20');
  // a budget the form refuses, in its own words, beside the budget
  await page.getByTestId('newcard-envelope').fill('-5');
  await page.getByTestId('newcard-create').click();
  await expect(page.getByTestId('newcard-error-envelope')).toContainText('negative');
  await page.getByTestId('newcard-envelope').fill('3');
  await page.getByTestId('newcard-profile').selectOption('e2e-alt');
  await shot(page, info, 'newcard-session');
  await page.getByTestId('newcard-create').click();
  await expect(page.getByTestId('view-newcard')).toHaveCount(0);
  await expect(page.getByTestId('card-title')).toHaveText('Add a farewell helper');
  const id = (await page.getByTestId('card-id').getAttribute('data-id'))!;
  expect(id).toMatch(/^FF-/);
  const card = (await api('GET', `/api/cards/${id}`)).json;
  expect(card).toMatchObject({ kind: 'freeform', stage: 'open', envelope: 300, profile: 'e2e-alt' });
});

test('a session opens at once', async ({ pairedPage: page }, info) => {
  await openForm(page);
  await expect(page.getByTestId('newcard-autopilot')).toHaveCount(0);
  await page.getByTestId('newcard-title').fill('Tidy the readme');
  await page.getByTestId('newcard-create').click();
  await expect(page.getByTestId('card-title')).toHaveText('Tidy the readme');
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', /^FF-/);
  // an open session has no stages at all, so it shows no badge for them
  await expect(page.getByTestId('card-stages')).toHaveCount(0);
  if (phone(info)) await page.getByTestId('tab-thread').click();
  await page.getByTestId('composer-input').fill('Add a line about the command');
  await expect(page.getByTestId('composer-says')).toContainText('a turn for this card');
  await shot(page, info, 'newcard-freeform');
});

// A title in any script names a card: Latin letters with marks fold into
// the branch name, and a script that does not fold still gets one.
test('a title in any script makes a card', async ({ pairedPage: page, api }, info) => {
  for (const [title, branch] of [['Привет мир', /\/card-[0-9a-f]{8}$/], ['Café résumé', /\/cafe-resume$/]] as const) {
    await openForm(page);
    await page.getByTestId('newcard-title').fill(title);
    await page.getByTestId('newcard-desc').fill('Say hello in the reader’s own words.');
    await page.getByTestId('newcard-create').click();
    await expect(page.getByTestId('card-title')).toHaveText(title);
    const id = (await page.getByTestId('card-id').getAttribute('data-id'))!.trim();
    expect((await api('GET', `/api/cards/${id}`)).json.branch).toMatch(branch);
    // the created card opened; back to the cards for the next one
    if (phone(info)) await page.getByTestId('card-back').click();
  }
});
