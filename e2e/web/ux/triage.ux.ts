import { expect, test } from '../fixtures/test';
import { seedBoard, seedLive, type Board } from '../fixtures/board';
import { Tour } from './tour';
import { cardAction, live, openCard, settle, toRail, toTab, toThread } from './nav';

// Triage: a person comes back to a busy board. Can they tell what needs
// them, get to it, settle it, and see which card is ready to land and what
// landing it takes — without reading every card?

let board: Board;

test.use({
  workspaceEnv: { GUMMI_E2E_SLOW_SECONDS: '900' },
  seed: { run: async (ws) => { board = await seedBoard(ws); } },
});

test('triage a busy board', async ({ pairedPage: page, server }, info) => {
  test.setTimeout(240_000);
  await seedLive(page.context().request, server, board);
  await page.goto(server.url);
  const t = new Tour(page, info, 'triage', 'Coming back to a busy board: find what needs you, settle it, find what is ready and land it');
  const b = board;

  await live(t);
  await t.shot('cold-open', 'The first screen on coming back: is it clear what needs you and what is ready?');

  // what needs me: the pill, or the key
  await t.click('press the "need you" pill', page.getByTestId('pill-needs'));
  await settle(t);
  await toThread(t);
  await t.shot('needs-you', 'The card that needs you: is the question and how to answer it obvious? Is the primary action the right one?');

  // answer it in the person's own words when no option is offered
  const option = page.getByTestId('decision-option-0');
  if (await option.isVisible()) {
    await t.click('pick the first answer', option);
  } else {
    await t.fill('type an answer', page.getByTestId('composer-input'), 'Put it in greet.go');
    await t.press('send the answer', 'Enter');
  }
  await t.wait('the decision clears', () => expect(page.getByTestId('pill-needs')).toContainText('0', { timeout: 30_000 }));
  await t.shot('answered', 'After answering: is it clear the answer landed and what happens next?');

  // what is ready: the person looks for a card to land
  await toRail(t);
  await t.shot('rail-after', 'The cards again: which one is ready to land, which has an open comment, which is stacked?');
  await openCard(t, b.sessions[0]);
  await t.shot('ready-card', 'A session whose work is committed: is "land it" the obvious next step, and what will it do?');
  if (!t.phone) {
    await t.press('open the card’s actions with .', '.');
    await t.shot('actions-by-key', 'The card’s actions from the keyboard: is landing among them, named as the head names it?');
    await page.keyboard.press('Escape');
  }
  await toTab(t, 'diff');
  await t.shot('ready-diff', 'Reading what it changed before landing it');
  await toThread(t);
  await cardAction(t, 'merge', 'session-land');
  await t.shot('land-dialog', 'The landing dialog: what it will do, and is anything holding it?');
  await t.maybe('cancel the landing', () => page.getByTestId('action-cancel').click());

  // a card with an open comment, and one stacked on another
  await openCard(t, b.commented);
  await t.shot('commented-card', 'A session with an unresolved comment on its diff: does the card say so before you open the diff?');
  await openCard(t, b.stacked[1]);
  await t.shot('stacked-card', 'The top of a stack: can it land, and if not, why not and what to do?');
  await openCard(t, b.running!);
  await t.shot('running-card', 'A session working right now: what is it doing, and how do you steer or stop it?');
  await openCard(t, b.landed);
  await t.shot('landed-card', 'A landed session: is it clear it is finished and where the work went?');
  t.finish();
});
