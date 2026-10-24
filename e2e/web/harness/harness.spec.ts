import { expect, GummiServer, test } from '../fixtures/test';

// These prove the harness, not the web UI: the scripted agent, the seeding
// helpers and fake gh, all through the workspace's seeding board. No browser.

test.describe.configure({ mode: 'serial' });

test("a session's turn edits its tree, and the diff is real", async ({ workspace: ws }) => {
  const id = await ws.seedSession('Add a wave helper');
  expect(id).toMatch(/^FF-\d+$/);

  const c = await ws.card(id);
  expect(c.kind).toBe('freeform');
  expect(c.status).not.toBe('running');
  expect(await ws.git('-C', ws.worktree(id), 'status', '--porcelain')).toContain('NOTES.md');
  const diff = await ws.call('GET', `/api/cards/${id}/diff`);
  expect(JSON.stringify(diff)).toContain('Add a wave helper');
});

test('a ready session has its work committed on its branch', async ({ workspace: ws }) => {
  const id = await ws.seedReady('Add a bow helper');
  expect(await ws.git('-C', ws.worktree(id), 'status', '--porcelain')).toBe('');
  expect(await ws.git('-C', ws.worktree(id), 'log', '-1', '--format=%s')).toContain('feat: add a bow helper');
});

test('[ask] stops on an open question, and the answer reaches the session', async ({ workspace: ws }) => {
  const ask = await ws.seedAsk('Add a choosy helper');
  expect(ask.question).toBe('Which way?');
  expect(ask.options).toEqual(['Left', 'Right']);
  expect((await ws.card(ask.id)).decision?.kind).toBe('ask');

  await ws.answer(ask.id, 'Right');
  expect(ws.agentLog()).toContain('Right');
  expect((await ws.card(ask.id)).decision?.kind).not.toBe('ask');
});

test('a landed card is done and on main', async ({ workspace: ws }) => {
  const id = await ws.seedLanded('Add a nod helper', 'feat: add a nod helper');
  expect((await ws.card(id)).stage).toBe('done');
  expect(await ws.git('log', '-1', '--format=%s', 'main')).toContain('feat: add a nod helper');
});

test('session cards', async ({ workspace: ws }) => {
  const sessions = await ws.seedSessions(['Add a wave helper', 'Add a shrug helper']);
  expect(sessions).toHaveLength(2);
  for (const id of sessions) {
    const c = await ws.card(id);
    expect(c.kind).toBe('freeform');
    expect(c.status).not.toBe('running');
  }
});

test('fake gh answers a linked PR', async ({ workspace: ws }) => {
  const id = await ws.seedReady('Add a farewell helper');
  await ws.linkPR(id, 7);
  const pr = await ws.call('GET', `/api/cards/${id}/pr?refresh=1`);
  expect(JSON.stringify(pr)).toContain('e2e/tiny');
  expect(JSON.stringify(pr)).toContain('Should this helper take a name?');

  const calls = ws.ghCalls().map((c) => c.slice(0, 2).join(' '));
  expect(calls).toEqual(expect.arrayContaining(['pr view', 'api repos/e2e/tiny', 'api graphql']));
});

test.describe('slow', () => {
  test.use({ workspaceEnv: { GUMMI_E2E_SLOW_SECONDS: '1' } });

  test('[slow] streams its reply in deltas', async ({ workspace: ws }) => {
    const started = Date.now();
    await ws.seedSession('[slow] Add a lazy helper');
    expect(Date.now() - started).toBeGreaterThan(500); // one turn, paced over the slow second
    const deltas = ws.agentLog().split('\n').filter((l) => l.startsWith('OUT') && l.includes('"type": "text"'));
    expect(deltas.length).toBeGreaterThan(3); // its reply in pieces, not one message
  });
});

test('gummi web serves, pairs and refuses a second instance', async ({ workspace: ws }) => {
  const help = await ws.gummi(['web', '--help']);
  test.skip(help.code !== 0 || !/--addr/.test(help.stdout + help.stderr), '`gummi web` does not exist in this build yet');

  const server = await GummiServer.start(ws);
  try {
    // the code is printed just after the server starts answering
    await expect.poll(() => server.code ?? '').toMatch(/^\d{6}$/);
    const session = await (await fetch(`${server.url}/api/session`)).json();
    expect(session.authed).toBe(false);

    const res = await fetch(`${server.url}/api/pair`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Origin: server.url },
      body: JSON.stringify({ code: await server.freshCode(), name: 'Harness' }),
    });
    expect(res.status).toBe(200);
    expect(res.headers.get('set-cookie')).toBeTruthy();

    const second = await server.startSecond();
    expect(second.code).not.toBe(0);
    expect(second.stdout + second.stderr).toContain('served by gummi web');
  } finally {
    await server.stop();
  }
});
