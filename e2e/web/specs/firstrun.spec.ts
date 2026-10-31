import fs from 'node:fs';
import path from 'node:path';
import { expect, test } from '../fixtures/test';
import type { Workspace } from '../fixtures/workspace';
import { shot } from '../fixtures/shots';

// The first-run setup against a real `gummi web`: a workspace with no
// profiles.yaml, so this start seeds it and offers the wizard. What the
// machine holds is pinned: PATH is the system's own bin directories and a
// stub directory, so copilot is not installed, claude is a stub that answers
// its model list and is signed in through a fake key, and opencode is a stub
// whose catalog probe fails (it is offered the seed's own ids).

const claudeStub = `#!/usr/bin/env python3
import json, sys
for line in sys.stdin:
    req = json.loads(line)
    if req.get("type") == "control_request":
        print(json.dumps({"type": "control_response", "response": {
            "subtype": "success", "request_id": req["request_id"],
            "response": {"models": [{"value": "default"}, {"value": "opus"}, {"value": "sonnet"}, {"value": "haiku"}]}}}), flush=True)
        break
`;

async function firstRun(ws: Workspace): Promise<void> {
  const stubs = path.join(ws.root, 'stubs');
  await fs.promises.mkdir(stubs, { recursive: true });
  await fs.promises.writeFile(path.join(stubs, 'claude'), claudeStub, { mode: 0o755 });
  await fs.promises.writeFile(path.join(stubs, 'opencode'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  Object.assign(ws.env, {
    PATH: [path.join(ws.root, 'bin'), stubs, '/usr/local/bin', '/usr/bin', '/bin'].join(path.delimiter),
    GUMMI_CLAUDE_BIN: path.join(stubs, 'claude'),
    GUMMI_OPENCODE_BIN: path.join(stubs, 'opencode'),
    ANTHROPIC_API_KEY: 'e2e-not-a-key',
  });
  // no profiles.yaml: the server's start seeds it, and that start is the first run
  await fs.promises.rm(path.join(ws.repo, '.gummi', 'profiles.yaml'), { force: true });
}

const profilesFile = (ws: Workspace) => path.join(ws.repo, '.gummi', 'profiles.yaml');

test.describe('first run', () => {
  test.use({ seed: { run: firstRun } });

  test('the wizard writes one default profile', async ({ pairedPage: page, api, workspace }, info) => {
    const view = page.getByTestId('view-firstrun');
    await expect(view).toBeVisible();
    await expect(page.getByTestId('firstrun-step')).toHaveText('1/3');
    await expect(page.getByTestId('firstrun-agent-copilot')).toContainText('not installed');
    await expect(page.getByTestId('firstrun-agent-copilot')).toContainText('install the copilot CLI — "copilot" is not on PATH');
    // nothing but the screen itself: no stray text after the buttons
    await expect(page.getByTestId('view-firstrun-body')).not.toContainText('null');
    await expect(page.getByTestId('firstrun-agent-copilot').locator('input')).toBeDisabled();
    await expect(page.getByTestId('firstrun-agent-claude')).toContainText('usable');
    await expect(page.getByTestId('firstrun-agent-opencode')).toContainText('usable');
    await shot(page, info, 'firstrun-agent');

    await page.getByTestId('firstrun-agent-claude').click();
    await page.getByTestId('firstrun-continue').click();
    await expect(page.getByTestId('firstrun-step')).toHaveText('2/3');
    // the claude stub's list (the board's model lists are sorted),
    // preselected as the seed would have it
    await expect(page.getByTestId('firstrun-strong')).toHaveValue('opus');
    await expect(page.getByTestId('firstrun-everyday')).toHaveValue('haiku');
    await expect(page.getByTestId('firstrun-everyday').locator('option')).toHaveText(['haiku', 'opus', 'sonnet']);
    await page.getByTestId('firstrun-everyday').selectOption('sonnet');
    await shot(page, info, 'firstrun-models');

    // back keeps the agent, forward keeps nothing stale
    await page.getByTestId('firstrun-back').click();
    await expect(page.getByTestId('firstrun-step')).toHaveText('1/3');
    await page.getByTestId('firstrun-continue').click();
    await page.getByTestId('firstrun-everyday').selectOption('sonnet');
    await page.getByTestId('firstrun-continue').click();

    await expect(page.getByTestId('firstrun-step')).toHaveText('3/3');
    const roles = page.getByTestId('firstrun-roles');
    await expect(roles).toContainText('architectclaude/opus');
    await expect(roles).toContainText('implementerclaude/sonnet');
    await expect(roles).toContainText('scoutclaude/sonnet');
    await shot(page, info, 'firstrun-confirm');

    await page.getByTestId('firstrun-save').click();
    await expect(view).toHaveCount(0);
    await expect(page.getByTestId('toast').filter({ hasText: 'default' })).toBeVisible();
    await shot(page, info, 'firstrun-saved');

    const file = fs.readFileSync(profilesFile(workspace), 'utf8');
    expect(file).toContain('default: default');
    expect(file).toContain('architect: {backend: claude, model: opus}');
    expect(file).toContain('implementer: {backend: claude, model: sonnet}');
    // the starter profiles are gone (the header comment may still say "premium")
    expect(file).not.toMatch(/^\s+(thrifty|premium):/m);

    // done: the board offers it no more, and a second answer is refused
    expect((await api('GET', '/api/firstrun')).json.pending).toBe(false);
    expect((await api('POST', '/api/firstrun', { skip: true })).status).toBe(409);
    await page.reload();
    await expect(page.getByTestId('app')).toBeVisible();
    await expect(view).toHaveCount(0);

    // the profile is the board's now
    const form = await api('GET', '/api/form');
    expect(form.json.profiles).toEqual(['default']);
  });

  test('skip keeps the starter profiles untouched', async ({ pairedPage: page, api, workspace }) => {
    await expect(page.getByTestId('view-firstrun')).toBeVisible();
    const seeded = fs.readFileSync(profilesFile(workspace), 'utf8');
    expect(seeded).toContain('thrifty');
    await page.getByTestId('firstrun-skip').click();
    await expect(page.getByTestId('view-firstrun')).toHaveCount(0);
    expect(fs.readFileSync(profilesFile(workspace), 'utf8')).toBe(seeded);
    expect((await api('GET', '/api/firstrun')).json.pending).toBe(false);
    await page.reload();
    await expect(page.getByTestId('app')).toBeVisible();
    await expect(page.getByTestId('view-firstrun')).toHaveCount(0);
  });

  test('closing the wizard is a skip', async ({ pairedPage: page, api, workspace }) => {
    await expect(page.getByTestId('view-firstrun')).toBeVisible();
    const seeded = fs.readFileSync(profilesFile(workspace), 'utf8');
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('view-firstrun')).toHaveCount(0);
    await expect.poll(async () => (await api('GET', '/api/firstrun')).json.pending).toBe(false);
    expect(fs.readFileSync(profilesFile(workspace), 'utf8')).toBe(seeded);
  });
});

test('a workspace that has its profiles is not offered the wizard', async ({ pairedPage: page, api }) => {
  await expect(page.getByTestId('app')).toBeVisible();
  expect((await api('GET', '/api/firstrun')).json.pending).toBe(false);
  await expect(page.getByTestId('view-firstrun')).toHaveCount(0);
});
