import { expect, GummiServer, test } from '../fixtures/test';

// These prove the harness, not the web UI: the scripted agent, the seeding
// helpers and fake gh, all through the real CLI. No browser, no server
// (except the last test, which waits for `gummi web` to exist).

test.describe.configure({ mode: 'serial' });

test("a session's turn edits its tree, and the diff is real", async ({ workspace: ws }) => {
  const id = await ws.seedSession('Add a wave helper');
  expect(id).toMatch(/^FF-\d+$/);

  const st = await ws.status(id);
  expect(st.kind).toBe('freeform');
  expect(st.running).toBe(false);
  expect(await ws.git('-C', ws.worktree(id), 'status', '--porcelain')).toContain('NOTES.md');
  const diff = await ws.gummiOK(['diff', id]);
  expect(diff.stdout).toContain('Add a wave helper');
});

test('[workflow] a run inside the pre-authorisation runs to its report, with real checks', async ({ workspace: ws }) => {
  const r = await ws.drive(['run', '--envelope', '50', '[workflow] make the workflow file']);
  expect(r.code, r.stdout + r.stderr).toBe(0);
  const kinds = r.events.map((e) => e.event);
  expect(kinds.at(-1)).toBe('done');
  expect(kinds).not.toContain('question');

  // the run's states ride the stream, and it ends done with its report
  const runs = r.events.filter((e) => e.event === 'workflow');
  expect(runs.map((e) => e.state)).toContain('running');
  const last = runs.at(-1)!;
  expect(last.state).toBe('done');
  expect(last.recipe).toBe('deliver');
  expect(String(last.report)).not.toBe('');
  // what the done line spent is the pool's: the session's turns and the steps'
  const done = r.events.at(-1)!;
  expect(Number(done.spent_credits)).toBeGreaterThan(0);

  // the spec's done-when check ran against what the steps wrote
  const spec = await ws.gummiOK(['spec', r.id]);
  expect(spec.stdout).toContain('```gummi-done-when');
  expect(spec.stdout).toContain('test -f made.txt');
  expect(await ws.git('-C', ws.worktree(r.id), 'status', '--porcelain')).toContain('made.txt');
  expect(ws.agentLog()).toContain('You are step ');
});

test("[step-asks] a step's question needs you, and a resume answers it", async ({ workspace: ws }) => {
  const r = await ws.drive(['run', '--envelope', '50', '[workflow] [step-asks] make the workflow file']);
  const q = r.events.find((e) => e.event === 'question');
  expect(q, r.stdout).toBeTruthy();
  expect(q!.kind).toBe('workflow_ask');
  expect(q!.run).toBe(1);
  expect(String(q!.step)).toMatch(/implement$/);
  expect(q!.options).toEqual(['Keep it plain (recommended)', 'Give it a header']);

  const answered = await ws.answer(r.id, 'Keep it plain (recommended)');
  expect(answered.events.at(-1)?.event).toBe('done');
  // the process that asked ended with `gummi run`; the answer reaches the
  // step's card as a turn of its own, and the step does its task with it
  expect(ws.agentLog()).toContain('The person said: Keep it plain (recommended).');
  expect(answered.events.filter((e) => e.event === 'workflow').at(-1)?.state).toBe('done');
});

test('[ask] stops on an open question, and the answer reaches the session', async ({ workspace: ws }) => {
  const ask = await ws.seedAsk('Add a choosy helper');
  expect(ask.question).toBe('Which way?');
  expect(ask.options).toEqual(['Left', 'Right']);
  let st = await ws.status(ask.id);
  expect(st.escalation?.kind).toBe('ask');

  const r = await ws.answer(ask.id, 'Right');
  expect(r.events.at(-1)?.event).toBe('done');
  // a resume is a new process: the answer reaches the session as a turn
  expect(r.events.some((e) => e.event === 'reply' && String(e.text).includes('Decided with the user: Right'))).toBe(true);
  st = await ws.status(ask.id);
  expect(st.escalation).toBeFalsy();
});

test('a landed card is done and on main', async ({ workspace: ws }) => {
  const id = await ws.seedLanded('Add a nod helper', 'feat: add a nod helper');
  const st = await ws.status(id);
  expect(st.stage).toBe('done');
  expect(await ws.git('log', '-1', '--format=%s', 'main')).toContain('feat: add a nod helper');
});

test('session cards', async ({ workspace: ws }) => {
  const sessions = await ws.seedSessions(['Add a wave helper', 'Add a shrug helper']);
  expect(sessions).toHaveLength(2);
  for (const id of sessions) {
    const st = await ws.status(id);
    expect(st.kind).toBe('freeform');
    expect(st.running).toBe(false);
  }
});

test('fake gh answers pr link/status/comments', async ({ workspace: ws }) => {
  const id = await ws.seedReady('Add a farewell helper');
  await ws.linkPR(id, 7);
  const status = await ws.gummiOK(['pr', 'status', id]);
  expect(status.stdout).toContain('e2e/tiny#7');
  expect(status.stdout).toContain('Comments: 1');

  const comments = await ws.gummiOK(['pr', 'comments', id, '--json']);
  const threads = JSON.parse(comments.stdout);
  expect(threads.threads).toHaveLength(1); // the resolved one is dropped
  expect(threads.threads[0].root_body_first_line).toBe('Should this helper take a name?');

  const calls = ws.ghCalls().map((c) => c.slice(0, 2).join(' '));
  expect(calls).toEqual(expect.arrayContaining(['pr view', 'api repos/e2e/tiny', 'api graphql']));

  // a test can swap an answer for its own workspace
  await ws.setGh('repo.json', { allow_squash_merge: false });
  await ws.gummiOK(['pr', 'unlink', id]);
  const relink = await ws.gummiOK(['pr', 'link', id, '7']);
  expect(relink.stderr).toContain('does not allow squash-merge');
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
    expect(server.code).toMatch(/^\d{6}$/);
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
