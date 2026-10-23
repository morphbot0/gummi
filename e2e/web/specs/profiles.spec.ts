import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';

// The profiles view against a real `gummi web`: the workspace's two
// profiles list, a third is added through the model picker, made the
// default, and one is deleted through its confirm — each written to
// .gummi/profiles.yaml and offered by the new-card form at once. A claude
// role told to run a gpt model is refused with the board's sentence.

async function openProfiles(page: Page) {
  await page.getByTestId('rail-more').click();
  await page.getByTestId('menu-profiles').click();
  await expect(page.getByTestId('view-profiles')).toBeVisible();
}

async function pickModel(page: Page, testid: string, model: string, backend: string) {
  await page.getByTestId(testid).click();
  await page.getByTestId('model-search').fill(model);
  const row = page.getByTestId(`model-typed-${backend}`);
  await row.click();
}

test('profiles are added, defaulted and deleted', async ({ pairedPage: page, api, workspace }, info) => {
  test.setTimeout(90_000);
  await openProfiles(page);
  await expect(page.getByTestId('profiles-count')).toHaveText('2 profiles');
  await expect(page.getByTestId('profile-e2e-default')).toBeVisible();
  await expect(page.getByTestId('profile-e2e-alt')).toBeVisible();
  await shot(page, info, 'profiles');

  // a claude role on a gpt model is refused, and nothing is written
  const file = path.join(workspace.repo, '.gummi', 'profiles.yaml');
  const refused = await api('PUT', '/api/profiles/cheap', { roles: [{ role: 'implementer', backend: 'claude', model: 'gpt-5' }] });
  expect(refused.status).toBe(400);
  expect(refused.text).toContain('only runs Anthropic models');
  expect(fs.readFileSync(file, 'utf8')).not.toContain('cheap');

  await page.getByTestId('profiles-new').click();
  await page.getByTestId('profile-form-name').fill('cheap');
  // a profile on the scripted agent saves
  await pickModel(page, 'profile-form-implementer', 'e2e-cheap', 'headless');
  await pickModel(page, 'profile-form-step-cheap', 'e2e-tiny', 'headless');
  await shot(page, info, 'profiles-form');
  await page.getByTestId('profile-form-save').click();
  await expect(page.getByTestId('profile-form')).toHaveCount(0);
  await expect(page.getByTestId('profile-cheap')).toContainText('e2e-tiny');
  await expect(page.getByTestId('profiles-count')).toHaveText('3 profiles');
  expect(fs.readFileSync(file, 'utf8')).toContain('step-cheap: {backend: headless, model: e2e-tiny}');

  await page.getByTestId('profile-cheap-make-default').click();
  await expect(page.getByTestId('profile-cheap-default')).toBeVisible();
  const form = await api('GET', '/api/form');
  expect(form.json.profiles[0]).toBe('cheap');

  await page.getByTestId('profile-e2e-alt-delete').click();
  await page.getByTestId('profile-e2e-alt-confirm-yes').click();
  await expect(page.getByTestId('profile-e2e-alt')).toHaveCount(0);
  await expect(page.getByTestId('profiles-count')).toHaveText('2 profiles');
  expect(fs.readFileSync(file, 'utf8')).not.toContain('e2e-alt');
  await shot(page, info, 'profiles-after');
});
