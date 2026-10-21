import { expect, type APIRequestContext } from '@playwright/test';
import { api, seedFreeform, type GummiServer } from './server';
import type { Workspace } from './workspace';

/**
 * A board with one card in every state a person meets, for the specs that
 * look at the whole page (visual QA, accessibility, the keyboard walk).
 * `seedBoard` runs before the server starts (the CLI half); `seedLive`
 * after, through the API (what has no CLI verb, or needs a live board).
 */
export interface Board {
  gate: string;
  failed: string;
  ask: string;
  landed: string;
  backlog: string[];
  bug: string;
  research: string;
  stacked: string[];
  // filled by seedLive
  freeform?: string;
  running?: string;
}

export async function seedBoard(ws: Workspace): Promise<Board> {
  const gate = await ws.seedDesignGate('Add a wave helper');
  const failed = await ws.seedVerifyFailed('Add a shout helper');
  const ask = (await ws.seedAsk('Pick where the greeting lives')).id;
  const landed = await ws.seedLanded('Add a farewell helper');
  const backlog = await ws.seedBacklog(['Add a shrug helper', 'Add a nod helper']);
  const bug = await ws.seedBug('Greet panics on an empty name', { severity: 'high' });
  const research = await ws.seedResearch('Compare greeting libraries');
  const bottom = await ws.seedVerified('Add a bow helper');
  await ws.gummiOK(['stack', 'new', bottom, '--name', 'greetings']);
  await ws.gummiOK(['stack', 'add', 'greetings', gate]);
  return { gate, failed, ask, landed, backlog, bug, research, stacked: [bottom, gate] };
}

/** The half that needs the running board: a freeform card, a running card, a diff comment, a dependency. */
export async function seedLive(request: APIRequestContext, server: GummiServer, b: Board, opts: { running?: boolean } = {}): Promise<Board> {
  const call = (method: string, path: string, body?: unknown) => api(request, server, method, path, body);
  b.freeform = await seedFreeform(request, server, 'Tidy the README');

  // the second backlog card waits on the first
  const deps = await call('POST', `/api/cards/${b.backlog[1]}/actions/deps`, { cards: [b.backlog[0]] });
  expect(deps.status, deps.text).toBe(200);

  // a comment on the failed card's first added line, pending with its next answer
  const diff = (await call('GET', `/api/cards/${b.failed}/diff`)).json;
  const line = diff.files.flatMap((f: any) => f.hunks.flatMap((hk: any) => hk.lines)).find((l: any) => l.t === '+');
  const ann = await call('POST', `/api/cards/${b.failed}/diff/annotations`, { idx: line.idx, comment: 'Name the helper after what it returns.' });
  expect(ann.status, ann.text).toBe(200);

  if (opts.running !== false) {
    // a card whose architect streams slowly (the workspace sets GUMMI_E2E_SLOW_SECONDS)
    const c = (await call('POST', '/api/cards', { kind: 'feature', title: '[slow] Add a lazy helper' })).json;
    let card = (await call('POST', `/api/cards/${c.id}/answer`, { ref: c.decision.ref, option: 'advance', against: c.decision.against.token })).json;
    card = (await call('POST', `/api/cards/${c.id}/answer`, { ref: card.decision.ref, option: 'run', against: card.decision.against.token })).json;
    await expect.poll(async () => (await call('GET', `/api/cards/${c.id}`)).json.status).toBe('running');
    b.running = c.id;
  }
  return b;
}
