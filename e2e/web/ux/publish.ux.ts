import { expect, test } from '../fixtures/test';
import { Tour } from './tour';
import { live, openCard, toTab, toThread } from './nav';

// Publishing: a session whose work is committed is pushed and opened as a
// pull request on GitHub (a stand-in: fake gh, and a bare repository served
// as github.com), then read on its pull-request tab. Can a person tell what
// will happen before they confirm, and what happened after?

let id: string;

test.use({ seed: { run: async (ws) => { await ws.addGitHubRemote(); id = await ws.seedReady('Add a wave helper'); } } });

test('open a pull request for a session', async ({ pairedPage: page, server }, info) => {
  test.setTimeout(180_000);
  await page.goto(server.url);
  const t = new Tour(page, info, 'publish', 'Publish a ready session: push it and open its pull request, then read it');

  await live(t);
  await openCard(t, id);
  await t.shot('ready', 'A committed session on a repository with GitHub: is opening a pull request offered, and beside landing?');
  await toTab(t, 'pr');
  await t.wait('the tab offers a pull request', () => expect(page.getByTestId('pr-publish-create')).toBeVisible({ timeout: 20_000 }));
  await t.shot('pr-tab', 'The pull request tab before there is one: what it offers');
  await t.click('press Open a pull request…', page.getByTestId('pr-publish-create'));
  await t.wait('the confirm opens', () => expect(page.getByTestId('publish-dialog')).toBeVisible());
  await t.shot('confirm', 'The confirm: what will be pushed where, and the words the pull request opens with');
  await t.click('confirm', page.getByTestId('publish-confirm'));
  await t.wait('the dialog closes', () => expect(page.getByTestId('publish-dialog')).toHaveCount(0, { timeout: 30_000 }));
  await t.shot('opened', 'After opening it: is it clear it worked and where the pull request is?');
  await toThread(t);
  await t.shot('thread-after', 'The thread and head once the session has a pull request: how does it land now?');
  t.finish();
});
