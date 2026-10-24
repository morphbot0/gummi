import { expect } from '@playwright/test';
import type { Tour } from './tour';

// The moves every journey makes, the way a person makes them on each face:
// on a phone the cards, a card's thread and its documents are separate
// screens, so getting to one costs a tap the desktop does not.

/** Waits for the board to be live, at no cost. */
export async function live(t: Tour): Promise<boolean> {
  return t.wait('the board goes live', () => expect(t.page.getByTestId('conn')).toHaveAttribute('data-state', 'live', { timeout: 30_000 }));
}

/** Waits for the thread and the surface to finish loading, at no cost. */
export async function settle(t: Tour): Promise<boolean> {
  return t.wait('the card settles', async () => {
    await expect(t.page.getByTestId('thread-loading')).toHaveCount(0);
    await expect(t.page.getByTestId('panel-loading')).toHaveCount(0);
  });
}

/** Back to the list of cards (a phone's first screen). */
export async function toRail(t: Tour): Promise<boolean> {
  const back = t.page.getByTestId('card-back');
  if (t.phone && (await back.isVisible())) return t.click('go back to the cards', back);
  return true;
}

/** Opens a card from its row, as a person would. */
export async function openCard(t: Tour, id: string): Promise<boolean> {
  if (!(await toRail(t))) return false;
  if (!(await t.click(`open card ${id} from the rail`, t.page.getByTestId(`rail-row-${id}`)))) return false;
  if (!(await t.wait('the card opens', () => expect(t.page.getByTestId('card-id')).toHaveAttribute('data-id', id)))) return false;
  if (!(await toThread(t))) return false;
  return settle(t);
}

/** The card's thread on screen (on a phone a document may cover it). */
export async function toThread(t: Tour): Promise<boolean> {
  if (!t.phone) return true;
  if ((await t.page.getByTestId('app').getAttribute('data-view')) === 'thread') return true;
  return t.click('show the thread', t.page.getByTestId('tab-thread'));
}

/**
 * One of a card's documents on screen: Spec, Memory, Run, Terminal have an
 * icon each; the diff, log, tree and pull request share the Changes one,
 * behind the switch in its header.
 */
export async function toTab(t: Tour, name: string, opts: { soft?: boolean } = {}): Promise<boolean> {
  const changes = ['diff', 'log', 'tree', 'pr'].includes(name);
  const icon = t.page.getByTestId(`tab-${changes ? 'diff' : name}`);
  const label = `open the ${name} document`;
  const open = async () => {
    if ((await icon.getAttribute('aria-selected', { timeout: 3_000 })) !== 'true') await icon.click();
    if (changes) {
      const leaf = t.page.getByTestId(name === 'diff' ? 'changes-diff' : `tab-${name}`);
      if ((await leaf.getAttribute('aria-selected', { timeout: 3_000 })) !== 'true') await leaf.click();
    }
    await expect(t.page.getByTestId('panel-loading')).toHaveCount(0);
  };
  return opts.soft ? t.maybe(label, open) : t.act(label, open, changes ? 2 : 1);
}

/** Sends one message from the composer and waits for the turn to end. */
export async function send(t: Tour, text: string, opts: { wait?: boolean } = {}): Promise<boolean> {
  const input = t.page.getByTestId('composer-input');
  if (!(await t.fill('type a message', input, text))) return false;
  if (!(await t.press('send it with enter', 'Enter'))) return false;
  if (opts.wait === false) return true;
  return t.wait('the turn ends', async () => {
    await expect(t.page.getByTestId('composer-says')).toContainText('stop this turn', { timeout: 15_000 }).catch(() => {});
    await expect(t.page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 45_000 });
  });
}

/**
 * A card action from its head: the button when the head shows one, else
 * the card's actions menu (a phone, or a head too narrow for the button),
 * which costs a click more. Records which way it went in the label.
 */
export async function cardAction(t: Tour, action: string, button: string): Promise<boolean> {
  const btn = t.page.getByTestId(button);
  if (await btn.isVisible()) return t.click(`press ${action} on the head`, btn);
  if (!(await t.click(`${action} is not on the head: open the card actions`, t.page.getByTestId('card-actions')))) return false;
  return t.click(`pick ${action} from the menu`, t.page.getByTestId(`action-${action}`));
}
