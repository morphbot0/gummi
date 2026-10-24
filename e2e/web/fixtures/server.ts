import { spawn, type ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import type { APIRequestContext, BrowserContext, Page } from '@playwright/test';
import { gummiBin } from './paths';
import { run, type Exec, type Workspace } from './workspace';

/**
 * One `gummi web` process serving a Workspace's board on a loopback port.
 *
 * Coded against the web command's contract (docs/DESIGN.md §20 and the web
 * brief): `gummi web --addr 127.0.0.1:<port>` prints
 * `pairing code NNNNNN` when no device is paired yet, answers
 * `GET /api/session` without auth, redeems `POST /api/pair {code, name}`
 * into an HttpOnly cookie, and `gummi web pair` asks the running server
 * for a fresh code.
 */
export class GummiServer {
  readonly ws: Workspace;
  readonly port: number;
  readonly url: string;
  readonly logFile: string;
  /** The code the server printed at start, until something redeems it. */
  code: string | undefined;
  /**
   * The request client of the first device pair() let in: it approves a
   * later device that waits to be let in (see pair()).
   */
  approver: APIRequestContext | undefined;
  private proc: ChildProcess | undefined;
  private output = '';

  private constructor(ws: Workspace, port: number, logFile: string) {
    this.ws = ws;
    this.port = port;
    this.url = `http://127.0.0.1:${port}`;
    this.logFile = logFile;
  }

  /**
   * Start a server for ws and wait until it answers /api/session. `args`
   * adds flags (e.g. ['--no-pairing']); `port` pins the port (restart).
   */
  static async start(ws: Workspace, opts: { port?: number; args?: string[]; timeoutMs?: number } = {}): Promise<GummiServer> {
    const port = opts.port ?? (await freePort());
    const logFile = path.join(ws.logs, `server-${port}-${Date.now()}.log`);
    const s = new GummiServer(ws, port, logFile);
    await s.launch(opts.args ?? [], opts.timeoutMs ?? 30_000);
    return s;
  }

  private async launch(args: string[], timeoutMs: number): Promise<void> {
    const log = fs.createWriteStream(this.logFile, { flags: 'a' });
    this.output = '';
    // detached: the server leads its own process group, so stop() takes
    // the headless agents it spawned down with it.
    const proc = spawn(gummiBin, ['web', '--addr', `127.0.0.1:${this.port}`, ...args], {
      cwd: this.ws.repo,
      env: this.ws.env,
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: true,
    });
    this.proc = proc;
    const onData = (d: Buffer) => {
      const text = d.toString();
      this.output += text;
      log.write(text);
      const m = /pairing code (\d{6})/.exec(this.output);
      if (m && this.code === undefined) this.code = m[1];
    };
    proc.stdout!.on('data', onData);
    proc.stderr!.on('data', onData);
    proc.on('close', () => log.end());

    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (proc.exitCode !== null || proc.signalCode !== null) {
        throw new Error(`gummi web exited (${proc.exitCode ?? proc.signalCode}) before serving:\n${this.output}`);
      }
      try {
        const res = await fetch(`${this.url}/api/session`);
        if (res.ok) {
          // the pairing code is printed just after the board answers: wait
          // a moment for it, so freshCode never asks `gummi web pair` for a
          // second one while this one is still on its way
          if (!args.includes('--no-pairing')) {
            const until = Date.now() + 2_000;
            while (this.code === undefined && Date.now() < until) await sleep(25);
          }
          return;
        }
      } catch {
        /* not listening yet */
      }
      await sleep(100);
    }
    await this.stop();
    throw new Error(`gummi web did not answer /api/session within ${timeoutMs}ms:\n${this.output}`);
  }

  /** Everything the server has printed so far (also in logFile). */
  get log(): string {
    return this.output;
  }

  /** Whether the process is still running. */
  get running(): boolean {
    return !!this.proc && this.proc.exitCode === null && this.proc.signalCode === null;
  }

  /**
   * A pairing code nobody has used: the one printed at start the first
   * time, then a fresh one from `gummi web pair` (codes are single-use).
   */
  async freshCode(): Promise<string> {
    if (this.code) {
      const c = this.code;
      this.code = '';
      return c;
    }
    const r = await this.ws.gummi(['web', 'pair']);
    const m = /pairing code (\d{6})/.exec(r.stdout + r.stderr);
    if (!m) throw new Error(`gummi web pair printed no code (exit ${r.code}):\n${r.stdout}\n${r.stderr}`);
    return m[1];
  }

  /** Stop the server: SIGTERM to its process group, SIGKILL after 5s. */
  async stop(): Promise<void> {
    const proc = this.proc;
    if (!proc || !this.running) return;
    const closed = new Promise<void>((r) => proc.once('close', () => r()));
    killGroup(proc, 'SIGTERM');
    const t = setTimeout(() => killGroup(proc, 'SIGKILL'), 5_000);
    await closed;
    clearTimeout(t);
    this.proc = undefined;
  }

  /** Stop and start again on the same port (paired cookies stay valid). */
  async restart(opts: { args?: string[]; timeoutMs?: number } = {}): Promise<void> {
    await this.stop();
    this.code = undefined;
    await this.launch(opts.args ?? [], opts.timeoutMs ?? 30_000);
  }

  /**
   * Run a second `gummi web` against the same workspace while this one
   * holds the board, and return what it printed and its exit code: the
   * instance lock must refuse it and name the holder.
   */
  async startSecond(opts: { args?: string[]; timeoutMs?: number } = {}): Promise<Exec> {
    const port = await freePort();
    return run(gummiBin, ['web', '--addr', `127.0.0.1:${port}`, ...(opts.args ?? [])], {
      cwd: this.ws.repo,
      env: this.ws.env,
      timeoutMs: opts.timeoutMs ?? 15_000,
    });
  }

  /** Run the TUI (`gummi` with no args) while this server holds the board. */
  async startTUI(opts: { timeoutMs?: number } = {}): Promise<Exec> {
    return run(gummiBin, [], { cwd: this.ws.repo, env: this.ws.env, timeoutMs: opts.timeoutMs ?? 15_000 });
  }
}

/**
 * Pair a browser context with the server as `name`.
 *
 * via "api" (default) redeems a fresh code with `POST /api/pair` through the
 * context's own request client, so the cookie lands in the context's jar,
 * then opens the board. via "ui" opens the page and types the code into the
 * pairing form (a text box labelled "code", one labelled "name", a "Pair"
 * button) — use it in the pairing spec; everything else should use "api".
 */
export async function pair(page: Page, server: GummiServer, name = 'Tester', via: 'api' | 'ui' = 'api'): Promise<void> {
  const code = await server.freshCode();
  if (via === 'api') {
    const request = page.context().request;
    const res = await request.post(`${server.url}/api/pair`, {
      data: { code, name },
      headers: { Origin: server.url },
    });
    if (!res.ok()) throw new Error(`POST /api/pair → ${res.status()}: ${await res.text()}`);
    const body = await res.json();
    if (body.pending) {
      // A second device paired with `gummi web pair`'s code waits to be
      // let in (DESIGN §20.3): the first device paired here lets it in.
      if (!server.approver) throw new Error(`${name} waits to be let in, and no device paired before it can approve it`);
      const ok = await server.approver.post(`${server.url}/api/devices/${encodeURIComponent(body.deviceId)}/approve`, {
        headers: { Origin: server.url },
      });
      if (!ok.ok()) throw new Error(`approving ${name} → ${ok.status()}: ${await ok.text()}`);
    } else {
      server.approver ??= request;
    }
    await page.goto(server.url);
    return;
  }
  await page.goto(server.url);
  await page.getByLabel(/code/i).fill(code);
  await page.getByLabel(/name/i).fill(name);
  await page.getByRole('button', { name: /pair/i }).click();
}

/** A paired context's session cookie(s), e.g. to copy into another context. */
export async function sessionCookies(context: BrowserContext, server: GummiServer) {
  return context.cookies(server.url);
}

export interface ApiResponse<T = any> {
  status: number;
  ok: boolean;
  json: T;
  text: string;
  headers: Record<string, string>;
}

/**
 * Call the server's JSON API through `request` (a context's request client
 * carries that context's cookie). Sends `Origin: <server>` so writes pass
 * the same-origin check; pass `origin` to send another (or null for none)
 * when testing the refusal. Never throws on an HTTP error status.
 */
export async function api<T = any>(
  request: APIRequestContext,
  server: GummiServer,
  method: string,
  pathname: string,
  body?: unknown,
  opts: { origin?: string | null; headers?: Record<string, string> } = {},
): Promise<ApiResponse<T>> {
  const headers: Record<string, string> = { ...(opts.headers ?? {}) };
  const origin = opts.origin === undefined ? server.url : opts.origin;
  if (origin !== null) headers.Origin = origin;
  const res = await request.fetch(server.url + pathname, {
    method,
    headers,
    data: body === undefined ? undefined : body,
    failOnStatusCode: false,
  });
  const text = await res.text();
  let json: any = undefined;
  try {
    json = text ? JSON.parse(text) : undefined;
  } catch {
    /* not JSON */
  }
  return { status: res.status(), ok: res.ok(), json, text, headers: res.headers() };
}

/**
 * Create a freeform card through the test server's web API, paired: what
 * the Workspace seeders do on a board of their own before it starts.
 */
export async function seedFreeform(request: APIRequestContext, server: GummiServer, title: string, extra: Record<string, unknown> = {}): Promise<string> {
  const r = await api(request, server, 'POST', '/api/cards', { kind: 'freeform', title, ...extra });
  if (!r.ok) throw new Error(`POST /api/cards (freeform) → ${r.status}: ${r.text}`);
  return String(r.json?.id ?? r.json?.card?.id);
}

/** An ephemeral loopback port nobody is listening on right now. */
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      srv.close(() => (typeof addr === 'object' && addr ? resolve(addr.port) : reject(new Error('no port'))));
    });
  });
}

function killGroup(proc: ChildProcess, sig: NodeJS.Signals): void {
  try {
    if (proc.pid) process.kill(-proc.pid, sig);
  } catch {
    try {
      proc.kill(sig);
    } catch {
      /* already gone */
    }
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
