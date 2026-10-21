import { expect, showTab, test } from '../fixtures/test';

// A session calls a crew (DESIGN §24): the scripted session writes a spec
// with one done-when item and asks for a crew; the planner's plan is the
// card's decision, drawn as its node table; running it lands the node on
// the crew's branch, and the report comes back as the crew's turn. The
// person lands the crew from the head, which pins the verified mark to
// the session's tip, and the Spec tab lists the revisions the crew froze.
test('a session runs a crew from its plan to its verified mark', async ({ pairedPage: page, server, api }, info) => {
  test.skip(info.project.name === 'phone', 'the crew canvas is the wide face; a phone reads the same fold as a list');
  test.setTimeout(150_000);
  const made = await api('POST', '/api/cards', { kind: 'freeform', title: 'Make the crew file', envelope: 2000 });
  expect(made.status, made.text).toBe(201);
  const id = String(made.json?.id);
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  // the session's opening turn runs and ends
  await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });

  const input = page.getByTestId('composer-input');
  await input.click();
  await input.fill('[crew] make the crew file');
  await page.keyboard.press('Enter');

  // the plan is the pinned decision, with its nodes, what they own and cost
  const plan = page.getByTestId('crew-decision');
  await expect(plan).toBeVisible({ timeout: 45_000 });
  await expect(page.getByTestId('decision')).toContainText('crew plan');
  await expect(plan.getByTestId('crew-plan-t1')).toContainText('implementer');
  await expect(plan.getByTestId('crew-plan-t1')).toContainText('crew.txt');
  await expect(plan.getByTestId('crew-ticks')).toContainText('all 1 done-when item served');
  await expect(page.getByTestId(`rail-row-crew-${id}`)).toBeAttached();
  await expect(page.getByTestId('decision-option-0')).toContainText('Run the crew');
  await page.getByTestId('decision-option-0').click();

  // the crew runs, and its report comes back as the crew's own turn
  const report = page.getByTestId('thread-crew');
  await expect(report.last()).toContainText('The crew has reported', { timeout: 90_000 });
  await expect(report.last().locator('.who b')).toHaveText('crew');

  // the Crew tab reads the node landed on the crew's branch
  await showTab(page, 'crew');
  await expect(page.getByTestId('crew-state')).toContainText('finished', { timeout: 30_000 });
  await expect(page.getByTestId('crew-node-state-t1')).toHaveText('landed');
  await expect(page.getByTestId('crew-landed-t1')).toBeVisible();
  await page.getByTestId('crew-node-t1').click();
  await expect(page.getByTestId('crew-inspector')).toContainText('plain text, one line');
  await expect(page.getByTestId('crew-report')).toContainText('DW-1');

  // the session's own loose work (its opening turn's notes) is committed
  // first: a crew lands only on a clean tree
  const head = (await api('GET', `/api/cards/${id}`)).json;
  if (head.actions?.some((a: any) => a.id === 'commit')) {
    const c = await api('POST', `/api/cards/${id}/actions/commit`, { message: 'docs: the session notes' });
    expect(c.status, c.text).toBe(200);
  }
  // the head offers it too; on a narrow head it folds into the menu, and
  // the Crew tab's own button is always there
  await expect(page.getByTestId('crew-land')).toBeAttached();
  await page.getByTestId('crew-act-land').click();
  await expect(page.getByTestId('crew-mark-since')).toContainText('0 commits since', { timeout: 30_000 });
  await expect(page.getByTestId('crew-mark')).toContainText('crew verified at');
  await expect(page.getByTestId('crew-state')).toContainText('landed', { timeout: 15_000 });

  // the spec lists its revisions, and the one the crew froze says so
  await showTab(page, 'spec');
  const revs = page.getByTestId('spec-revisions');
  await expect(revs).toBeVisible();
  await expect(revs.getByTestId('spec-revision-1')).toBeVisible();
  await expect(revs).toContainText('frozen for crew 1');
});

// A person starts a session's spec on its Spec tab, edits it whole, and
// each save is a revision of its own.
test('a session spec is started, edited and saved as a revision', async ({ pairedPage: page, server, api }, info) => {
  test.setTimeout(90_000);
  const id = String((await api('POST', '/api/cards', { kind: 'freeform', title: 'Write down the plan' })).json?.id);
  await page.goto(`${server.url}/#${id}`);
  await expect(page.getByTestId('card-id')).toHaveAttribute('data-id', id);
  if (info.project.name !== 'phone') await expect(page.getByTestId('composer-says')).not.toContainText('stop this turn', { timeout: 30_000 });
  await showTab(page, 'spec');
  await expect(page.getByTestId('spec-none')).toContainText('No spec');
  await page.getByTestId('spec-start-session').click();
  await expect(page.getByTestId('spec-revision-1')).toBeVisible();
  await expect(page.getByTestId('spec-ready')).toContainText('no done-when item yet');

  await page.getByTestId('spec-edit').click();
  const ed = page.getByTestId('spec-editor');
  await expect(ed).toBeVisible();
  const text = await ed.inputValue();
  expect(text).toContain('## Problem');
  await ed.fill(text.replace('## Problem\n', '## Problem\n\nThe plan lives only in the conversation.\n'));
  await page.getByTestId('spec-save').click();
  await expect(page.getByTestId('spec-revision-2')).toBeVisible();
  await expect(page.getByTestId('spec-doc')).toContainText('The plan lives only in the conversation.');
  await expect(page.getByTestId('spec-revision-2')).toContainText('Tester');
  const spec = (await api('GET', `/api/cards/${id}/spec`)).json;
  expect(spec.revisions?.length).toBe(2);
  expect(spec.markdown).toContain('The plan lives only in the conversation.');
});
