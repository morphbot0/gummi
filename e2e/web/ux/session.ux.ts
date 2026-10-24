import { expect, test } from '../fixtures/test';
import { Tour } from './tour';
import { cardAction, live, send, settle, toTab, toThread } from './nav';

// The session loop on a fresh board: start a session, talk to it, read its
// diff, comment on a line, have it addressed, commit and land. The core
// loop gummi exists for, from a person who has never used it before.

test('a session from nothing to landed', async ({ pairedPage: page, server }, info) => {
  test.setTimeout(240_000);
  await page.goto(server.url);
  const t = new Tour(page, info, 'session', 'First use: start a session, talk to it, review its diff, comment, commit and land it');

  await live(t);
  await t.shot('empty-board', 'A board with nothing on it: is it clear what gummi is and how to start?');

  await t.click('open the new-session menu', page.getByTestId('rail-new-menu'));
  await t.shot('new-menu', 'The ways to start something: are they told apart clearly?');
  await t.click('pick New session…', page.getByTestId('rail-new'));
  await t.wait('the form opens', () => expect(page.getByTestId('newcard-title')).toBeVisible());
  await t.shot('new-form', 'The new-session form: which fields matter, and are the defaults right?');
  await t.fill('give it a title', page.getByTestId('newcard-title'), 'Add a farewell helper');
  await t.fill('say what to do', page.getByTestId('newcard-desc'), 'Greet has no goodbye. Add Farewell(name) beside it.');
  await t.click('create it', page.getByTestId('newcard-create'));
  await t.wait('the session opens', () => expect(page.getByTestId('card-title')).toHaveText('Add a farewell helper'));
  await toThread(t);
  await t.shot('first-turn', 'The session just started: is it clear the agent is working, and on what?');
  await t.wait('the first turn ends', () => expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 45_000 }));
  await settle(t);
  await t.shot('first-reply', 'The first reply: what changed, and what can you do next?');

  await send(t, 'Also mention it in the README.');
  await t.shot('second-reply', 'A second exchange: is the thread easy to follow?');

  // the work so far: the agent stages, it does not commit
  await toTab(t, 'tree');
  await t.shot('working-tree', 'What the agent changed but did not commit: is it clear this is uncommitted?');
  await toTab(t, 'diff');
  await t.shot('diff', 'The diff against the base: is it clear what will land?');
  const line = page.locator('[data-testid^="diff-line-"]').first();
  if (await t.maybe('click a line number to comment', () => line.locator('.n').click({ timeout: 5_000 }))) {
    await t.fill('write the comment', page.getByTestId('annotation-input'), 'Name it after what it returns.');
    await t.click('save the comment', page.getByTestId('annotation-save'));
    await t.shot('commented', 'A comment on a line: is it clear how it reaches the agent?');
    await t.maybe('request changes', () => page.getByTestId('diff-request-changes').click({ timeout: 5_000 }));
    await t.maybe('confirm the request', () => page.getByTestId('changes-go').click({ timeout: 5_000 }));
    await t.shot('changes-requested', 'After asking for changes: is it clear the agent has the comment?');
  }

  await toThread(t);
  await t.wait('any turn ends', () => expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 45_000 }));
  await t.shot('before-commit', 'Ready to keep the work: how does the person commit it?');
  await cardAction(t, 'commit', 'session-commit');
  await t.shot('commit-dialog', 'Committing: what is asked, and is the message prefilled sensibly?');
  await t.maybe('write a commit message', () => page.getByTestId('action-dialog').locator('textarea').fill('feat: add a farewell helper', { timeout: 5_000 }));
  await t.maybe('confirm the commit', () => page.getByTestId('action-confirm').click({ timeout: 5_000 }));
  await t.shot('committed', 'Committed: is landing now the obvious next step?');
  await cardAction(t, 'merge', 'session-land');
  await t.shot('land-dialog', 'Landing: what it will do, and what holds it (an unresolved comment)?');
  await t.maybe('confirm the landing', () => page.getByTestId('action-confirm').click({ timeout: 5_000 }));
  await t.shot('after-land', 'After landing (or the refusal): is the outcome clear?');
  t.finish();
});
