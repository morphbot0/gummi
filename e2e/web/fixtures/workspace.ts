import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { agentScript, fakeGh, fakeGhData, fakeSsh, gummiBin } from './paths';

/**
 * A throwaway gummi workspace: a git repository holding a tiny Go module,
 * initialised with `gummi init` and configured so every role runs on the
 * scripted agent (scripts/web-e2e-agent.py) and every `gh` call lands on
 * fixtures/fake-gh.
 *
 * Layout under `root` (one temp dir, removed by dispose()):
 *
 *   repo/    the git repository and its .gummi workspace (cwd of every gummi)
 *   tmp/     TMPDIR for gummi and everything it runs
 *   gh/      this workspace's copy of fixtures/gh — fake-gh's answers
 *   bin/gh   a symlink to fake-gh, first on PATH (for anything that runs plain `gh`)
 *   logs/    agent.log (every protocol frame), gh.log (every gh argv),
 *            server logs
 *   config/  XDG_CONFIG_HOME, so the user's own gummi config never leaks in
 *
 * Seeding helpers drive the real CLI (`gummi run`, `resume`, `merge`) or,
 * where there is no verb, a board of their own (viaBoard), so a seeded card
 * is in exactly the state the product puts it in. They need no web server and are safe to call while
 * one runs (the CLI takes only per-card locks), though a running board
 * picks the change up on its own schedule.
 */
export class Workspace {
  readonly root: string;
  readonly repo: string;
  readonly logs: string;
  readonly ghData: string;
  readonly env: NodeJS.ProcessEnv;

  private constructor(root: string, extraEnv: NodeJS.ProcessEnv) {
    this.root = root;
    this.repo = path.join(root, 'repo');
    this.logs = path.join(root, 'logs');
    this.ghData = path.join(root, 'gh');
    const bin = path.join(root, 'bin');
    this.env = {
      ...process.env,
      PATH: `${bin}${path.delimiter}${process.env.PATH ?? ''}`,
      TMPDIR: path.join(root, 'tmp'),
      XDG_CONFIG_HOME: path.join(root, 'config'),
      GUMMI_AGENT: 'headless',
      GUMMI_AGENT_CMD: agentScript,
      GUMMI_ENVELOPE: '20',
      GUMMI_NOTIFY: 'off',
      GUMMI_GH_CMD: fakeGh,
      FAKE_GH_DATA: path.join(root, 'gh'),
      FAKE_GH_LOG: path.join(root, 'logs', 'gh.log'),
      // publishing: git's ssh is fake-ssh, serving the bare repositories
      // under remote/ as github.com (see addGitHubRemote)
      FAKE_GH_REMOTE: path.join(root, 'remote'),
      GIT_SSH_COMMAND: fakeSsh,
      GUMMI_E2E_FAST: '1',
      GUMMI_E2E_AGENT_LOG: path.join(root, 'logs', 'agent.log'),
      // a stray web address in the caller's shell must not leak into a test
      GUMMI_WEB_ADDR: '',
      // test-only: let `gummi web` accept and dial push endpoints on
      // loopback/private addresses, for a stand-in push service (a real
      // board refuses them: a paired device could otherwise aim the host's
      // POSTs at anything on its network)
      GUMMI_WEB_PUSH_ALLOW_PRIVATE: '1',
      ...extraEnv,
    };
  }

  /**
   * Create a fresh workspace. `env` adds or overrides environment for every
   * gummi process this workspace starts (e.g. GUMMI_E2E_SLOW_SECONDS).
   */
  static async create(opts: { env?: NodeJS.ProcessEnv; name?: string } = {}): Promise<Workspace> {
    const root = await fs.promises.mkdtemp(path.join(os.tmpdir(), `gummi-e2e-${opts.name ?? 'ws'}-`));
    const ws = new Workspace(root, opts.env ?? {});
    await ws.scaffold();
    return ws;
  }

  private async scaffold(): Promise<void> {
    for (const dir of ['repo/cmd/tiny', 'tmp', 'bin', 'logs', 'config']) {
      await fs.promises.mkdir(path.join(this.root, dir), { recursive: true });
    }
    await fs.promises.cp(fakeGhData, this.ghData, { recursive: true });
    await fs.promises.symlink(fakeGh, path.join(this.root, 'bin', 'gh'));

    const files: Record<string, string> = {
      'go.mod': 'module example.com/tiny\n\ngo 1.22\n',
      // A library package at the root and the command under cmd/: with a
      // single main package `go build ./...` would drop a binary into the
      // worktree, and gummi rightly blocks a branch that ships one.
      'greet.go': 'package tiny\n\n// Greet says hello.\nfunc Greet(name string) string { return "hello, " + name }\n',
      'greet_test.go':
        'package tiny\n\nimport "testing"\n\nfunc TestGreet(t *testing.T) {\n' +
        '\tif got := Greet("x"); got != "hello, x" {\n\t\tt.Fatalf("got %q", got)\n\t}\n}\n',
      'cmd/tiny/main.go':
        'package main\n\nimport (\n\t"fmt"\n\n\t"example.com/tiny"\n)\n\nfunc main() { fmt.Println(tiny.Greet("world")) }\n',
      '.gitignore': '.gummi/\n',
      'README.md': '# tiny\n\nA module small enough that its checks run in a second.\n',
    };
    for (const [name, body] of Object.entries(files)) {
      await fs.promises.writeFile(path.join(this.repo, name), body);
    }
    await this.git('init', '-q', '-b', 'main');
    await this.git('config', 'user.name', 'E2E Tester');
    await this.git('config', 'user.email', 'e2e@example.invalid');
    await this.git('config', 'commit.gpgsign', 'false');
    await this.git('add', '-A');
    await this.git('commit', '-q', '-m', 'init: a tiny module');

    await this.gummiOK(['init']);
    await this.writeGummiFile(
      'config.yaml',
      [
        '# gummi e2e workspace',
        'permissions: allow-all',
        'sandbox: off',
        '',
      ].join('\n'),
    );
    await this.writeGummiFile(
      'profiles.yaml',
      [
        'default: e2e',
        'profiles:',
        '  e2e: # every role on the scripted agent (GUMMI_AGENT_CMD)',
        '    architect: { backend: headless, model: e2e-architect }',
        '    implementer: { backend: headless, model: e2e-implementer }',
        '    reviewer: { backend: headless, model: e2e-reviewer }',
        '    scribe: { backend: headless, model: e2e-scribe }',
        '  e2e-alt: # a second profile, for the profile picker',
        '    architect: { backend: headless, model: e2e-alt-architect }',
        '    implementer: { backend: headless, model: e2e-alt-implementer }',
        '    reviewer: { backend: headless, model: e2e-alt-reviewer }',
        '    scribe: { backend: headless, model: e2e-alt-scribe }',
        '',
      ].join('\n'),
    );
  }

  /** Write a file under .gummi/ (config.yaml, profiles.yaml, …). */
  async writeGummiFile(name: string, body: string): Promise<void> {
    await fs.promises.writeFile(path.join(this.repo, '.gummi', name), body);
  }

  /** Remove the whole workspace. Kept when GUMMI_E2E_KEEP=1, for poking at. */
  async dispose(): Promise<void> {
    if (process.env.GUMMI_E2E_KEEP === '1') {
      console.log(`[e2e] kept workspace ${this.root}`);
      return;
    }
    await fs.promises.rm(this.root, { recursive: true, force: true });
  }

  // ------------------------------------------------------------------
  // running things
  // ------------------------------------------------------------------

  /** Run a command in the repository with the workspace's environment. */
  exec(cmd: string, args: string[], opts: { timeoutMs?: number; input?: string; cwd?: string } = {}): Promise<Exec> {
    return run(cmd, args, { cwd: opts.cwd ?? this.repo, env: this.env, timeoutMs: opts.timeoutMs, input: opts.input });
  }

  /** Run `gummi <args>`; never throws on a non-zero exit (see gummiOK). */
  gummi(args: string[], opts: { timeoutMs?: number; input?: string } = {}): Promise<Exec> {
    return this.exec(gummiBin, args, { timeoutMs: opts.timeoutMs ?? 120_000, input: opts.input });
  }

  /** Run `gummi <args>` and throw, with its output, unless it exits 0. */
  async gummiOK(args: string[], opts: { timeoutMs?: number; input?: string } = {}): Promise<Exec> {
    const r = await this.gummi(args, opts);
    if (r.code !== 0) throw new Error(`gummi ${args.join(' ')} exited ${r.code}\n${r.stdout}\n${r.stderr}`);
    return r;
  }

  /** Run git in the repository (or in `cwd`), throwing on failure. */
  async git(...args: string[]): Promise<string> {
    const r = await this.exec('git', args);
    if (r.code !== 0) throw new Error(`git ${args.join(' ')} exited ${r.code}\n${r.stderr}`);
    return r.stdout;
  }

  /** `gummi status <id> --json`, parsed. */
  async status(id: string): Promise<CardStatus> {
    const r = await this.gummiOK(['status', id, '--json']);
    return JSON.parse(r.stdout) as CardStatus;
  }

  /** The card's worktree under .gummi/worktrees. */
  worktree(id: string): string {
    return path.join(this.repo, '.gummi', 'worktrees', id);
  }

  /** The agent protocol log: one line per frame, `IN|OUT <pid> <json>`. */
  agentLog(): string {
    return readIfExists(path.join(this.logs, 'agent.log'));
  }

  /** Every argv fake-gh was called with, oldest first. */
  ghCalls(): string[][] {
    return readIfExists(path.join(this.logs, 'gh.log'))
      .split('\n')
      .filter(Boolean)
      .map((l) => JSON.parse(l) as string[]);
  }

  /** Replace one of fake-gh's canned answers for this workspace (see fake-gh). */
  async setGh(file: string, data: unknown): Promise<void> {
    await fs.promises.writeFile(path.join(this.ghData, file), JSON.stringify(data, null, 2));
  }

  /**
   * Make fake-gh slow to answer `gh <cmd> <sub>`: the call waits until the
   * returned function is called (see fake-gh).
   */
  async holdGh(cmd: string, sub: string): Promise<() => Promise<void>> {
    const file = path.join(this.ghData, `hold-${cmd}-${sub}`);
    await fs.promises.writeFile(file, '');
    return () => fs.promises.rm(file, { force: true });
  }

  // ------------------------------------------------------------------
  // seeding — each returns the card id and leaves the card parked
  // ------------------------------------------------------------------

  /**
   * A session started by `gummi run` from title and driven until its first
   * turn is answered: the scripted agent's turn edits a file, so its diff
   * is not empty. Returns the session's id.
   */
  async seedSession(title: string, opts: RunOpts = {}): Promise<string> {
    const r = await this.drive(['run', ...runFlags(opts), title]);
    expectEvent(r, 'done');
    return r.id;
  }

  /**
   * A session whose work is committed on its branch — ready to land.
   * `gummi run`, then `gummi commit` of what its turn left in the tree.
   */
  async seedReady(title: string, opts: RunOpts = {}): Promise<string> {
    const id = await this.seedSession(title, opts);
    await this.gummiOK(['commit', id, '-m', commitSubject(title)]);
    return id;
  }

  /**
   * A session whose agent is waiting on an ask_user question with two
   * options ("Left", "Right"). The [ask] keyword rides a line of its own
   * under the title, so the card keeps the title it was given.
   */
  async seedAsk(title: string, opts: RunOpts = {}): Promise<{ id: string; question: string; options: string[]; decision: string }> {
    const r = await this.drive(['run', ...runFlags(opts), title.includes('[ask]') ? title : `${title}\n\n[ask]`]);
    const q = expectEvent(r, 'question');
    return { id: r.id, question: String(q.q), options: q.options as string[], decision: String(q.decision) };
  }

  /** A ready session landed on main with `gummi merge` (stage done). */
  async seedLanded(title: string, message = commitSubject(title), opts: RunOpts = {}): Promise<string> {
    const id = await this.seedReady(title, opts);
    await this.gummiOK(['merge', id, '-m', message]);
    return id;
  }

  /**
   * Sessions (freeform cards), one per title, each with its title as its
   * opening turn and that turn answered. A session has no CLI verb of its
   * own yet, so this mints them through a board of its own (viaBoard).
   * `extra` is sent with each card (e.g. `{ repo: 'lxd' }`).
   */
  async seedSessions(titles: string[], extra: Record<string, unknown> = {}): Promise<string[]> {
    return this.viaBoard(titles.map((title) => ({ kind: 'freeform', title, ...extra })));
  }

  /**
   * Make cards through `POST /api/cards`, as the new-card form does, on a
   * board started for it on a free port with no pairing; wait for every
   * card's opening turn (a session's) to settle; stop the board again.
   * Call it before the test's server starts (a seed's run), never beside
   * it: the board's instance lock admits one.
   */
  private async viaBoard(cards: Array<Record<string, unknown>>): Promise<string[]> {
    const { GummiServer } = await import('./server');
    const server = await GummiServer.start(this, { args: ['--no-pairing'] });
    try {
      const call = async (method: string, pathname: string, body?: unknown) => {
        const res = await fetch(server.url + pathname, {
          method,
          headers: { Origin: server.url, 'Content-Type': 'application/json' },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
        const text = await res.text();
        if (!res.ok) throw new Error(`${method} ${pathname} → ${res.status}: ${text}`);
        return text ? JSON.parse(text) : undefined;
      };
      const ids: string[] = [];
      for (const card of cards) {
        const c = await call('POST', '/api/cards', card);
        ids.push(String(c?.id ?? c?.card?.id));
      }
      const deadline = Date.now() + 60_000;
      for (;;) {
        const rows: Array<{ id: string; status: string }> = (await call('GET', '/api/board')).rows;
        const busy = ids.filter((id) => rows.find((r) => r.id === id)?.status === 'running');
        if (busy.length === 0) break;
        if (Date.now() > deadline) throw new Error(`cards still running their opening turn: ${busy.join(', ')}`);
        await new Promise((r) => setTimeout(r, 100));
      }
      return ids;
    } finally {
      await server.stop();
    }
  }

  /** Answer a stopped session's open question with `gummi resume --answer`. */
  async answer(id: string, text: string): Promise<DriveResult> {
    return this.drive(['resume', id, '--answer', text]);
  }

  /**
   * Give the repository a github.com remote that works: `origin` is
   * git@github.com:e2e/tiny.git, and fake-ssh serves it from a bare
   * repository under remote/ that starts with main pushed. With it (and
   * fake-gh signed in) the board offers its publish acts.
   */
  async addGitHubRemote(repo = 'e2e/tiny'): Promise<void> {
    const bare = this.remotePath(repo);
    await fs.promises.mkdir(path.dirname(bare), { recursive: true });
    await this.git('init', '-q', '--bare', '-b', 'main', bare);
    await this.git('remote', 'add', 'origin', `git@github.com:${repo}.git`);
    await this.git('push', '-q', 'origin', 'main');
  }

  /** The bare repository standing in for github.com/<repo>. */
  remotePath(repo = 'e2e/tiny'): string {
    return path.join(this.root, 'remote', `${repo}.git`);
  }

  /** Link a card to fake-gh's PR (7 by default) with `gummi pr link`. */
  async linkPR(id: string, number = 7): Promise<void> {
    await this.gummiOK(['pr', 'link', id, String(number)]);
  }

  /**
   * Run a driving verb and parse its NDJSON stream. Throws when it printed
   * nothing parseable (it crashed before driving anything).
   */
  async drive(args: string[]): Promise<DriveResult> {
    const r = await this.gummi(args);
    const events = parseEvents(r.stdout);
    const created = events.find((e) => e.event === 'created' || e.event === 'resumed');
    if (!created) throw new Error(`gummi ${args.join(' ')} exited ${r.code} without a created/resumed event\n${r.stdout}\n${r.stderr}`);
    return { ...r, id: String(created.id), events };
  }
}

// ----------------------------------------------------------------------
// types and helpers
// ----------------------------------------------------------------------

export interface Exec {
  code: number | null;
  stdout: string;
  stderr: string;
}

export type DriverEvent = { event: string; id?: string; stage?: string; [k: string]: unknown };

export interface DriveResult extends Exec {
  id: string;
  events: DriverEvent[];
}

/** Flags every seeding helper forwards to `gummi run`. */
export interface RunOpts {
  envelope?: number;
  repo?: string;
  profile?: string;
  base?: string;
  ref?: string;
}

/** The fields of `gummi status --json` the suite reads (it carries more). */
export interface CardStatus {
  id: string;
  kind: string;
  title: string;
  stage: string;
  branch?: string;
  branch_state?: string;
  verified: boolean;
  running: boolean;
  spend: { credits: number; envelope: number };
  escalation?: { kind: string; reason: string; stage: string; at: string };
  blockers: { open_questions: number; open_diff: number };
  [k: string]: unknown;
}

function runFlags(o: RunOpts): string[] {
  const f: string[] = [];
  if (o.envelope !== undefined) f.push('--envelope', String(o.envelope));
  if (o.profile) f.push('--profile', o.profile);
  if (o.base) f.push('--base', o.base);
  if (o.repo) f.push('--repo', o.repo);
  if (o.ref) f.push('--ref', o.ref);
  return f;
}

/** A Conventional Commits subject a landing or commit accepts, from a title. */
export function commitSubject(title: string): string {
  const words = title.toLowerCase().replace(/\[[^\]]*\]/g, ' ').replace(/[^a-z0-9 ]+/g, ' ').replace(/\s+/g, ' ').trim();
  return `feat: ${words || 'work'}`;
}

function withKeyword(title: string, keyword: string): string {
  return title.includes(keyword) ? title : `${keyword} ${title}`;
}

export function parseEvents(stdout: string): DriverEvent[] {
  const out: DriverEvent[] = [];
  for (const line of stdout.split('\n')) {
    const t = line.trim();
    if (!t.startsWith('{')) continue;
    try {
      out.push(JSON.parse(t) as DriverEvent);
    } catch {
      /* not an event line */
    }
  }
  return out;
}

function expectEvent(r: DriveResult, name: string): DriverEvent {
  const e = r.events.findLast((ev) => ev.event === name);
  if (!e) throw new Error(`expected a "${name}" event from ${r.id}, got:\n${r.stdout}\n${r.stderr}`);
  return e;
}

function readIfExists(p: string): string {
  try {
    return fs.readFileSync(p, 'utf8');
  } catch {
    return '';
  }
}

export function run(
  cmd: string,
  args: string[],
  opts: { cwd: string; env: NodeJS.ProcessEnv; timeoutMs?: number; input?: string },
): Promise<Exec> {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { cwd: opts.cwd, env: opts.env, stdio: ['pipe', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      reject(new Error(`${cmd} ${args.join(' ')} timed out after ${opts.timeoutMs}ms\n${stdout}\n${stderr}`));
    }, opts.timeoutMs ?? 60_000);
    child.on('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });
    child.on('close', (code) => {
      clearTimeout(timer);
      resolve({ code, stdout, stderr });
    });
    // a child that exits before reading its stdin makes the write fail
    // with EPIPE; that is not the command's failure, its exit code is
    child.stdin.on('error', () => {});
    child.stdin.end(opts.input ?? '');
  });
}
