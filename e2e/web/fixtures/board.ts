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
  // a session whose work is committed, ready to land
  ready: string;
  // another, carrying a diff comment once seedLive has run
  commented: string;
  ask: string;
  landed: string;
  sessions: string[];
  stacked: string[];
  // filled by seedLive
  freeform?: string;
  running?: string;
}

export async function seedBoard(ws: Workspace): Promise<Board> {
  const ready = await ws.seedReady('Add a wave helper');
  const commented = await ws.seedReady('Add a shout helper');
  const ask = (await ws.seedAsk('Pick where the greeting lives')).id;
  const landed = await ws.seedLanded('Add a farewell helper');
  const sessions = await ws.seedSessions(['Add a shrug helper', 'Add a nod helper']);
  const bottom = await ws.seedReady('Add a bow helper');
  await ws.gummiOK(['stack', 'new', bottom, '--name', 'greetings']);
  await ws.gummiOK(['stack', 'add', 'greetings', ready]);
  return { ready, commented, ask, landed, sessions, stacked: [bottom, ready] };
}

/** The half that needs the running board: a session, a running one, a diff comment. */
export async function seedLive(request: APIRequestContext, server: GummiServer, b: Board, opts: { running?: boolean } = {}): Promise<Board> {
  const call = (method: string, path: string, body?: unknown) => api(request, server, method, path, body);
  b.freeform = await seedFreeform(request, server, 'Tidy the README');

  // a comment on the commented card's first added line
  const diff = (await call('GET', `/api/cards/${b.commented}/diff`)).json;
  const line = diff.files.flatMap((f: any) => f.hunks.flatMap((hk: any) => hk.lines)).find((l: any) => l.t === '+');
  const ann = await call('POST', `/api/cards/${b.commented}/diff/annotations`, { idx: line.idx, comment: 'Name the helper after what it returns.' });
  expect(ann.status, ann.text).toBe(200);

  if (opts.running !== false) {
    // a session whose turn streams slowly (the workspace sets GUMMI_E2E_SLOW_SECONDS)
    const id = await seedFreeform(request, server, '[slow] Add a lazy helper');
    await expect.poll(async () => (await call('GET', `/api/cards/${id}`)).json.status).toBe('running');
    b.running = id;
  }
  return b;
}
