import { expect, showTab, test } from '../fixtures/test';
import { shot } from '../fixtures/shots';

// A notice stands low at the right, just above what is docked at the
// bottom — clear of the head's buttons and the surface's tabs at the top,
// and of the composer — whatever the dock holds while the notice is up.

let id: string;
test.use({ seed: { run: async (ws) => { id = await ws.seedReady('Add a noticed helper'); } } });

test('a notice stands low at the right, clear of the dock and the head', async ({ pairedPage: page }, info) => {
  test.skip(info.project.name === 'phone', 'the phone docks a folded decision bar');
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  await showTab(page, 'diff');
  await page.locator('[data-testid^="diff-line-"]').first().locator('.n').click();
  await page.getByTestId('annotation-input').fill('Name it after what it does');
  await page.getByTestId('annotation-save').click();
  await expect(page.locator('[data-testid^="annotation-resolve-"]').first()).toBeVisible();
  await expect(page.getByTestId('toast')).toHaveCount(0, { timeout: 10_000 });

  const dockTop = async () => (await page.locator('.dock').boundingBox())!.y;
  await page.locator('[data-testid^="annotation-resolve-"]').first().click();
  const toast = page.getByTestId('toast').filter({ hasText: 'resolved' });
  await expect(toast).toBeVisible();
  await expect.poll(async () => (await toast.boundingBox())!.y + (await toast.boundingBox())!.height).toBeLessThanOrEqual(await dockTop());
  const at = (await toast.boundingBox())!;
  const top = (await page.locator('.top').boundingBox())!;
  expect(at.y, 'under the top bar').toBeGreaterThanOrEqual(top.y + top.height);
  const head = (await page.getByTestId('card-head').boundingBox())!;
  expect(at.y, 'below the card head').toBeGreaterThanOrEqual(head.y + head.height);
  expect(at.x + at.width, 'at the right').toBeGreaterThan(page.viewportSize()!.width - 40);
  await shot(page, info, 'toast-above-dock');

  // and a plain notice never takes a press meant for what is under it
  expect(await toast.evaluate((e) => getComputedStyle(e).pointerEvents)).toBe('none');
});
