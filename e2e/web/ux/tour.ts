import fs from 'node:fs';
import path from 'node:path';
import type { Locator, Page, TestInfo } from '@playwright/test';
import { probe, type Probe } from './probe';

/** Where a tour writes: ux-out/<project>/<journey>/, gitignored. */
export const OUT = path.join(__dirname, '..', 'ux-out');

export interface Step {
  n: number;
  name: string;
  // what the person is trying to do or should see here, for the reviewer
  intent: string;
  file: string;
  // the clicks, keys and fills spent since the journey began
  actions: number;
  errors: string[];
  probe: Probe;
}

export interface JourneyRecord {
  journey: string;
  goal: string;
  project: string;
  theme: 'light' | 'dark';
  steps: Step[];
  // the act a tour could not do, in its own words: a missing control is a
  // finding as often as it is a tour to fix
  blocked: string | null;
  actions: number;
  seconds: number;
}

/**
 * A journey through the page as a person takes it: every act counted, a
 * screenshot and a probe at every step that matters. An act that cannot be
 * done blocks the journey (recorded, with a screenshot) instead of failing
 * the test, so one tour run always yields every journey it could walk.
 */
export class Tour {
  private steps: Step[] = [];
  private errors: string[] = [];
  private actions = 0;
  private blocked: string | null = null;
  private started = Date.now();
  readonly dir: string;

  constructor(readonly page: Page, readonly info: TestInfo, readonly journey: string, readonly goal: string, readonly theme: 'light' | 'dark' = 'light') {
    this.dir = path.join(OUT, info.project.name, journey);
    fs.rmSync(this.dir, { recursive: true, force: true });
    fs.mkdirSync(this.dir, { recursive: true });
    page.setDefaultTimeout(10_000);
    page.on('console', (m) => { if (m.type() === 'error') this.errors.push(m.text().slice(0, 200)); });
    page.on('pageerror', (e) => this.errors.push(`pageerror: ${e.message.slice(0, 200)}`));
  }

  get phone(): boolean { return this.info.project.name.endsWith('phone'); }
  get isBlocked(): boolean { return this.blocked !== null; }

  /** A screenshot and a probe, named for what the person meets. */
  async shot(name: string, intent: string): Promise<void> {
    if (this.blocked) return;
    await this.snap(name, intent);
  }

  private async snap(name: string, intent: string): Promise<void> {
    await this.page.waitForTimeout(250);
    const n = this.steps.length + 1;
    const file = `${String(n).padStart(2, '0')}-${name}.png`;
    await this.page.screenshot({ path: path.join(this.dir, file) });
    this.steps.push({ n, name, intent, file, actions: this.actions, errors: this.errors.splice(0), probe: await probe(this.page, this.phone) });
  }

  /** One act the person does; `cost` is how many clicks or keys it takes. */
  async act(label: string, fn: () => Promise<unknown>, cost = 1): Promise<boolean> {
    if (this.blocked) return false;
    try {
      await fn();
      this.actions += cost;
      return true;
    } catch (e) {
      this.blocked = `${label}: ${String((e as Error).message).split('\n')[0]}`;
      await this.snap('blocked', `The tour could not: ${label}`).catch(() => {});
      return false;
    }
  }

  /** Something to wait for, which costs the person nothing. */
  wait(label: string, fn: () => Promise<unknown>): Promise<boolean> { return this.act(label, fn, 0); }

  click(label: string, loc: Locator): Promise<boolean> { return this.act(label, () => loc.click()); }
  press(label: string, key: string): Promise<boolean> { return this.act(label, () => this.page.keyboard.press(key)); }
  fill(label: string, loc: Locator, text: string): Promise<boolean> { return this.act(label, () => loc.fill(text)); }

  /** An act that may be absent on this face without blocking the rest. */
  async maybe(label: string, fn: () => Promise<unknown>): Promise<boolean> {
    if (this.blocked) return false;
    try { await fn(); this.actions += 1; return true; } catch { return false; }
  }

  finish(): void {
    const rec: JourneyRecord = {
      journey: this.journey,
      goal: this.goal,
      project: this.info.project.name,
      theme: this.theme,
      steps: this.steps,
      blocked: this.blocked,
      actions: this.actions,
      seconds: Math.round((Date.now() - this.started) / 1000),
    };
    fs.writeFileSync(path.join(this.dir, 'journey.json'), JSON.stringify(rec, null, 2));
  }
}
