// crew.js — a session's crew (DESIGN §24.9): the Crew tab (the budget
// strip, the plan as waves of node cards, the crew's branch with what has
// landed on it, the planner strip and the node inspector), the plan a crew
// decision shows in the dock, the rail's squares, the head's verified mark
// and the "Start a crew" dialog. The server decides everything drawn here
// (internal/ui/webcrew.go); this only draws it and sends the person's acts.

import { h, cr, plural, dollarsInput } from './dom.js?v=__ASSET_V__'
import { get, post, cardPath } from './api.js?v=__ASSET_V__'
import { markdown } from './markdown.js?v=__ASSET_V__'
import { toast } from './toast.js?v=__ASSET_V__'
import { openModal } from './views.js?v=__ASSET_V__'
import { budgetOf } from './session.js?v=__ASSET_V__'

export const crewTab = {
  name: 'crew',
  label: 'Crew',
  key: 'g c',
  fetch: (id) => get(cardPath(id, 'crew')),
  empty: (c) => !c || c.none,
  count: (c) => (c?.asks ? 1 : 0),
  // a session that has (or had) a crew: the rail's row says so
  hidden: (c) => !c || c.kind !== 'freeform' || !!c.home || !c.crew,
  render
}

// the words a node's state is said in, and the colour class it takes
const NODE_WORD = { waiting: 'waiting', running: 'working', checking: 'checking', reviewing: 'in review', ready: 'ready to land', landed: 'landed', stuck: 'stuck', exhausted: 'far past its estimate', dropped: 'dropped' }
const nodeTone = (st) => ({ landed: 'ok', ready: 'ok', running: 'run', checking: 'run', reviewing: 'run', stuck: 'bad', exhausted: 'bad', dropped: 'off' })[st] || 'wait'

const CREW_WORD = { proposed: 'proposed', running: 'running', paused: 'paused', reviewing: 'in final review', finished: 'finished', partial: 'partial', declined: 'declined', landed: 'landed' }
export const crewWord = (s) => CREW_WORD[s] || s || ''

// squares is the rail's crew mark: one square per node, coloured by its
// state (§24.9 "On the board").
export function crewSquares (rc, testid) {
  if (!rc) return null
  const nodes = rc.nodes || []
  const landed = nodes.filter(s => s === 'landed').length
  return h('span', { class: 'crew-sq', testid, title: `crew ${rc.seq} ${crewWord(rc.state)}${nodes.length ? ` · ${landed} of ${nodes.length} landed` : ''}` },
    nodes.map(s => h('i', { class: `t-${nodeTone(s)}`, 'aria-hidden': 'true' })),
    h('span', { class: 'crew-sqw' }, nodes.length ? `crew ${landed}/${nodes.length}` : `crew ${crewWord(rc.state)}`))
}

// ---- the plan in a decision ----

// planEl draws a crew decision's payload: the plan's nodes by wave with
// what each owns and costs, its checks as ticks, and for a widening the
// plan's diff against what the person approved.
export function crewPlanEl (dc) {
  if (!dc) return null
  if (dc.kind === 'question') {
    return h('div', { class: 'crew-dplan', testid: 'crew-decision' },
      h('p', { class: 'crew-dsub' }, `node ${dc.node} asks · the planner passed it on: the spec does not settle it`))
  }
  const head = h('div', { class: 'crew-dhead' },
    h('span', null, 'planned ', h('b', { class: 'mono' }, cr(dc.planned)),
      dc.wasPlanned && dc.wasPlanned !== dc.planned ? h('span', { class: 'mono' }, ` (was ${cr(dc.wasPlanned)})`) : null,
      ' of ', h('b', { class: 'mono' }, cr(dc.budget)),
      dc.wasBudget && dc.wasBudget !== dc.budget ? h('span', { class: 'mono' }, ` (was ${cr(dc.wasBudget)})`) : null,
      ` · ${plural(dc.lanes || 1, 'lane')}`))
  const rows = []
  for (const w of dc.waves || []) {
    w.nodes.forEach((n, i) => rows.push(h('tr', { testid: `crew-plan-${n.id}` },
      h('td', { class: 'crew-wv' }, i === 0 ? `wave ${w.n}` : ''),
      h('td', { class: 'mono' }, n.id),
      h('td', null, roleChip(n.role)),
      h('td', { class: 'crew-tt' }, n.title),
      h('td', { class: 'mono crew-own' }, (n.owns || []).join('\n') || 'read-only'),
      h('td', { class: 'mono crew-env' }, cr(n.envelope)))))
  }
  const diff = dc.kind === 'widen'
    ? h('div', { class: 'crew-diff', testid: 'crew-widen' },
        (dc.reasons || []).length ? h('ul', { class: 'crew-why' }, dc.reasons.map(r => h('li', null, r))) : null,
        (dc.changes || []).map(c => h('div', { class: ['crew-chg', `op-${opName(c.op)}`], testid: `crew-change-${c.node.id}` },
          h('span', { class: 'mono' }, `${c.op} ${c.node.id}`), roleChip(c.node.role), h('span', null, c.node.title),
          h('span', { class: 'mono crew-own' }, c.what || (c.node.owns || []).join(' ')),
          h('span', { class: 'mono' }, cr(c.node.envelope)))),
        (dc.itemChanges || []).map(c => h('div', { class: ['crew-chg', `op-${opName(c.op)}`] },
          h('span', { class: 'mono' }, `${c.op} ${c.item.id}`), h('span', null, `done when: ${c.item.says}`))))
    : null
  return h('div', { class: 'crew-dplan', testid: 'crew-decision' },
    head,
    diff,
    rows.length ? h('table', { class: 'crew-ptab', testid: 'crew-plan' }, h('tbody', null, rows)) : null,
    (dc.ticks || []).length ? h('div', { class: 'crew-ticks', testid: 'crew-ticks' }, dc.ticks.map(t => h('span', { class: t.ok ? 'okc' : 'badc' }, `${t.ok ? '✓' : '✕'} ${t.text}`))) : null)
}

const opName = (op) => ({ '+': 'add', '~': 'chg', '-': 'del' })[op] || 'chg'

function roleChip (role) {
  return h('span', { class: ['crew-role', `r-${role || 'none'}`] }, role)
}

// ---- the head's verified mark ----

// markEl is §24.8's "crew verified at <sha> · N commits since", with the
// one action that moves it.
export function crewMarkEl (card) {
  const m = card?.crewMark
  if (!m) return null
  const sha = String(m.sha || '').slice(0, 7)
  return h('span', { class: ['crew-mark', m.since && 'stale'], testid: 'crew-mark' },
    h('span', null, `crew ${m.partial ? 'partially ' : ''}verified at `, h('b', { class: 'mono' }, sha)),
    h('span', { testid: 'crew-mark-since' }, ` · ${plural(m.since, 'commit')} since`),
    h('button', { class: 'link', type: 'button', testid: 'crew-recheck', title: 'Run the done-when checks on the tip and move the mark there', onclick: (e) => act(card.id, 'recheck', {}, e.currentTarget) }, 'Re-check'))
}

// landCrewButton is the head's way to land a crew that has ended.
export function landCrewButton (card) {
  const st = card?.crew?.state
  if (st !== 'finished' && st !== 'partial') return null
  return h('button', { class: 'btn pri hide-s', type: 'button', testid: 'crew-land', title: `Land crew ${card.crew.seq}'s branch on this session as one commit, then run its done-when checks`, onclick: (e) => act(card.id, 'land', {}, e.currentTarget) }, 'Land crew')
}

// act sends one crew action and says what it answered.
async function act (id, action, body, btn, after) {
  if (btn) btn.disabled = true
  try {
    const res = await post(cardPath(id, `crew/${action}`), body)
    toast(res?.text || 'done')
    after?.(res)
    return true
  } catch (err) {
    toast(err.data?.error || err.message, { err: true })
    return false
  } finally {
    if (btn) btn.disabled = false
  }
}

// ---- dialogs ----

// openCrewStart asks for a crew: the brief beyond the spec, and a budget.
export function openCrewStart (card, after) {
  const brief = h('textarea', { rows: 4, testid: 'crew-brief', placeholder: 'What the crew is for, beyond the spec: what matters, what to watch.' })
  const budget = h('input', { type: 'text', inputmode: 'decimal', value: dollarsInput(400), testid: 'crew-budget', autocomplete: 'off' })
  const err = h('p', { class: 'spec-err', role: 'alert', testid: 'crew-start-error', hidden: true })
  const env = card?.envelope ? ` The crew spends from the session's budget (${cr(card.envelope)}); you are asked before it runs out, and a crew that runs out pauses rather than throwing work away.` : ''
  openModal({
    title: 'Start a crew',
    testid: 'crew-start-dialog',
    card: card.id,
    body: [
      h('p', { class: 'spec-about' }, `A planner turns this session's spec into a plan of nodes, each checked and reviewed, on a branch of the crew's own. You approve the plan before anything runs. Starting freezes the spec's current revision.${env}`),
      h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Brief'), brief),
      h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Estimate'), budget, h('span', { class: 'fh' }, 'What you expect the whole crew to cost, in dollars: its nodes, the planner and the reviews.')),
      err
    ],
    actions: [{ label: 'Cancel' }, {
      label: 'Start the crew',
      primary: true,
      testid: 'crew-start',
      onClick: async () => {
        err.hidden = true
        const b = budgetOf(budget.value)
        if (b.err || !b.n) { err.textContent = b.err || 'a crew needs a budget'; err.hidden = false; return false }
        try {
          const res = await post(cardPath(card.id, 'crew/start'), { brief: brief.value.trim(), budget: b.n })
          toast(res?.text || 'the crew was asked for')
          after?.(res)
        } catch (e) {
          err.textContent = e.data?.error || e.message
          err.hidden = false
          return false
        }
      }
    }]
  })
}

// openWords asks for the words an act takes (a steer's note, a stop's
// reason, a pre-authorisation's amount).
function openWords (card, { title, label, about, action, field, testid, dollars, value = '' }, after) {
  const input = dollars
    ? h('input', { type: 'text', inputmode: 'decimal', value, testid: `${testid}-input`, autocomplete: 'off' })
    : h('textarea', { rows: 3, testid: `${testid}-input` })
  const err = h('p', { class: 'spec-err', role: 'alert', hidden: true })
  openModal({
    title,
    testid: `${testid}-dialog`,
    card: card.id,
    body: [about ? h('p', { class: 'spec-about' }, about) : null, h('label', { class: 'field' }, h('span', { class: 'fl' }, label), input), err],
    actions: [{ label: 'Cancel' }, {
      label: title,
      primary: true,
      testid: `${testid}-go`,
      onClick: async () => {
        let v = input.value.trim()
        if (dollars) {
          const b = budgetOf(v || '0')
          if (b.err) { err.textContent = b.err; err.hidden = false; return false }
          v = b.n
        } else if (!v && field === 'note') { err.textContent = 'say something'; err.hidden = false; return false }
        const ok = await act(card.id, action, { [field]: v }, null, after)
        if (!ok) return false
      }
    }]
  })
}

// ---- the tab ----

// picked is the node a person opened in the inspector, per card: the tab
// redraws on every change to the crew and must open the same one again.
const picked = new Map()

const ACT = {
  pause: { label: 'Pause crew', go: (c, ctx) => act(c.id, 'pause', {}, null, swap(ctx)) },
  resume: { label: 'Resume crew', primary: true, go: (c, ctx) => act(c.id, 'resume', {}, null, swap(ctx)) },
  steer: { label: 'Steer planner…', go: (c, ctx) => openWords(c, { title: 'Steer the planner', label: 'Note', about: 'A note to the crew’s planner, which wakes it. A change of scope still comes back to you as a widening.', action: 'steer', field: 'note', testid: 'crew-steer' }, swap(ctx)) },
  stop: { label: 'Stop crew…', danger: true, go: (c, ctx) => openWords(c, { title: 'Stop the crew', label: 'Why', about: 'The crew winds down. What has landed on its branch stays, and can be landed.', action: 'stop', field: 'reason', testid: 'crew-stop' }, swap(ctx)) },
  land: { label: 'Land crew', primary: true, go: (c, ctx) => act(c.id, 'land', {}, null, swap(ctx)) },
  recheck: { label: 'Re-check', go: (c, ctx) => act(c.id, 'recheck', {}, null, swap(ctx)) },
  start: { label: 'Start a crew…', go: (c, ctx) => openCrewStart(c, swap(ctx)) },
  preauth: { label: 'Pre-authorise…', go: (c, ctx, data) => openWords(c, { title: 'Pre-authorise', label: 'Up to', dollars: true, value: dollarsInput(data?.preauth || 0), about: 'A plan whose estimate is at most this runs without asking you. A widening, or a top-up of the session, always asks. 0: every plan asks.', action: 'preauth', field: 'credits', testid: 'crew-preauth' }, swap(ctx)) }
}
const swap = (ctx) => (res) => ctx.swap(res?.crew || null)

function render (pane, entry, ctx) {
  const c = entry.data
  const card = ctx.card || { id: ctx.id }
  const acts = h('div', { class: 'crew-acts' }, (c?.acts || []).map(a => ACT[a]
    ? h('button', { class: ['btn', ACT[a].primary && 'pri', ACT[a].danger && 'danger'], type: 'button', testid: `crew-act-${a}`, onclick: () => ACT[a].go(card, ctx, c) }, ACT[a].label)
    : null))
  if (!c || c.none) {
    pane.append(h('div', { class: 'crew', testid: 'crew' }, h('div', { class: 'empty', testid: 'crew-none' }, h('b', null, 'No crew'), c?.why || ''), acts))
    return
  }
  const box = h('div', { class: 'crew', testid: 'crew', data: { state: c.state } })
  box.append(h('div', { class: 'crew-top' },
    h('span', { class: ['crew-state', `cs-${c.state}`], testid: 'crew-state' }, `crew ${c.seq} ${crewWord(c.state)}`),
    c.branch ? h('span', { class: 'mono crew-br', title: `lands on ${c.onto}` }, c.branch) : null,
    c.specRev ? h('span', { class: 'crew-dim' }, `spec revision ${c.specRev}`) : null,
    acts))
  box.append(budgetStrip(c))
  if (c.asks) box.append(h('div', { class: 'crew-asks', testid: 'crew-asks' }, h('b', null, 'Waits on you: '), c.asks, h('span', { class: 'crew-dim' }, ' · answer it in the thread')))
  const sel = picked.get(ctx.id)
  const nodes = (c.waves || []).flatMap(w => w.nodes)
  box.append(h('div', { class: 'crew-canvas', testid: 'crew-canvas' }, (c.waves || []).map(w => h('div', { class: 'crew-wave', testid: `crew-wave-${w.n}` },
    h('div', { class: 'crew-wh' }, `wave ${w.n}`),
    w.nodes.map(n => nodeCard(n, n.id === sel, () => { picked.set(ctx.id, n.id === sel ? null : n.id); ctx.rerender() })))),
  (c.waves || []).length ? h('div', { class: 'crew-wave prove' }, h('div', { class: 'crew-wh' }, 'prove'),
    h('div', { class: ['crew-node', 'gate', c.state === 'finished' || c.state === 'landed' ? 't-ok' : c.state === 'reviewing' ? 't-run' : 't-wait'] },
      h('div', { class: 'crew-nh' }, roleChip('review')),
      h('div', { class: 'crew-nt' }, plural((c.items || []).length, 'done-when item')),
      h('div', { class: 'crew-nf' }, h('span', { class: 'crew-st' }, c.state === 'reviewing' ? 'reviewing' : c.state === 'finished' || c.state === 'landed' ? 'passed' : c.state === 'partial' ? 'partial' : 'then you land')))) : null))
  box.append(h('div', { class: 'crew-lane', testid: 'crew-lane' },
    h('span', { class: 'mono crew-br' }, c.branch || 'crew branch'),
    (c.landed || []).map(l => h('span', { class: 'crew-cm', title: l.title, testid: `crew-landed-${l.node}` }, h('i', { 'aria-hidden': 'true' }), l.node)),
    h('span', { class: 'crew-dim' }, laneTail(c, nodes))))
  box.append(h('div', { class: 'crew-planner', testid: 'crew-planner' },
    h('b', null, 'Planner'),
    c.planner?.last ? h('span', null, ` · ${c.planner.at ? `last at ${c.planner.at}: ` : ''}`, h('i', null, c.planner.last)) : h('span', null, ' · nothing yet'),
    (c.planner?.waiting || []).length ? h('span', { class: 'crew-dim' }, ` · wakes on: ${c.planner.waiting.join('; ')}`) : null))
  const node = nodes.find(n => n.id === sel)
  if (node) box.append(inspector(node, c))
  if ((c.items || []).length) {
    box.append(h('section', { class: 'crew-sec' }, h('h5', null, 'Done when'),
      c.items.map(it => h('div', { class: 'crew-item', testid: `crew-item-${it.id}` }, h('span', { class: 'mono' }, it.id), h('span', null, it.says), it.check ? h('span', { class: 'mono crew-dim' }, it.check) : it.judge ? h('span', { class: 'crew-dim' }, 'judged') : null))))
  }
  if (c.report) box.append(h('section', { class: 'crew-sec', testid: 'crew-report' }, h('h5', null, 'Report'), markdown(c.report)))
  if ((c.log || []).length) {
    box.append(h('details', { class: 'crew-sec crew-log', testid: 'crew-log' }, h('summary', null, `Run log (${c.log.length})`),
      h('ol', null, c.log.map(l => h('li', { class: 'mono' }, l)))))
  }
  if ((c.crews || []).length > 1) {
    box.append(h('p', { class: 'crew-dim crew-hist' }, 'Earlier crews: ', c.crews.slice(0, -1).map(x => `crew ${x.seq} ${crewWord(x.state)}`).join(' · ')))
  }
  pane.append(box)
}

function laneTail (c, nodes) {
  const left = nodes.filter(n => n.state && n.state !== 'landed' && n.state !== 'dropped').map(n => n.id)
  if (c.state === 'landed') return `landed on ${c.onto}${c.verified ? ` · verified at ${String(c.verified).slice(0, 7)}` : ''}`
  if (left.length) return `${left.join(', ')} land here in order`
  if (!nodes.some(n => n.state)) return 'nothing runs until you approve the plan'
  return `lands on ${c.onto}`
}

function budgetStrip (c) {
  const b = c.budget || {}
  const pct = (n) => b.budget ? Math.min(100, (n / b.budget) * 100) + '%' : '0%'
  const elapsed = c.started ? Math.max(0, ((c.state === 'landed' || c.state === 'finished' || c.state === 'partial' ? new Date(c.updated) : Date.now()) - new Date(c.started)) / 60000) : 0
  return h('div', { class: 'crew-budget', testid: 'crew-budget' },
    h('span', null, 'spent ', h('b', { class: 'mono', testid: 'crew-spent' }, cr(b.spent))),
    h('span', { class: 'crew-bar', 'aria-hidden': 'true', style: { '--pl': pct(b.planned), '--sp': pct(b.spent) } }, h('i', { class: 'pl' }), h('i', { class: 'sp' })),
    h('span', null, 'planned ', h('b', { class: 'mono' }, cr(b.planned)), ' · estimate ', h('b', { class: 'mono' }, cr(b.budget)),
      b.toFinish ? [' · to finish ~', h('b', { class: 'mono', testid: 'crew-to-finish' }, cr(b.toFinish))] : null,
      b.left != null ? [' · session has ', h('b', { class: 'mono', testid: 'crew-left' }, cr(b.left)), ' left'] : null),
    h('span', null, 'lanes ', h('b', null, `${b.live || 0}/${b.lanes || 1}`)),
    h('span', null, 'rework ', h('b', null, String(b.rework || 0))),
    elapsed ? h('span', null, 'elapsed ', h('b', null, `${Math.round(elapsed)}m`)) : null)
}

function nodeCard (n, sel, onPick) {
  const tone = n.asks ? 'you' : nodeTone(n.state)
  const checks = (n.checks || []).length
  return h('button', {
    class: ['crew-node', `t-${tone}`, sel && 'sel'],
    type: 'button',
    testid: `crew-node-${n.id}`,
    data: { state: n.state || 'planned' },
    'aria-pressed': String(!!sel),
    title: n.reason || n.title,
    onclick: onPick
  },
  h('span', { class: 'crew-nh' }, roleChip(n.role), h('span', { class: 'mono crew-id' }, n.id)),
  h('span', { class: 'crew-nt' }, n.title),
  checks ? h('span', { class: 'crew-checks', 'aria-hidden': 'true' }, Array.from({ length: checks }, () => h('i', { class: n.state === 'landed' || n.state === 'ready' ? 'ok' : '' }))) : null,
  n.asks ? h('span', { class: 'crew-act' }, `asks: ${n.asks}`) : n.reason ? h('span', { class: 'crew-act' }, n.reason) : null,
  h('span', { class: 'crew-nf' },
    h('span', { class: 'mono' }, n.state ? cr(n.spent) : `~${cr(n.envelope)}`),
    n.reworks ? h('span', { class: 'crew-rw' }, `rework ${n.reworks}`) : null,
    h('span', { class: 'crew-st', testid: `crew-node-state-${n.id}` }, n.asks ? 'asks you' : n.state ? (NODE_WORD[n.state] || n.state) : (n.needs || []).length ? `needs ${n.needs.join(', ')}` : 'planned')))
}

function inspector (n, c) {
  const sec = (title, ...kids) => h('section', { class: 'crew-sec' }, h('h5', null, title), ...kids)
  const list = (xs) => h('ul', { class: 'crew-hand' }, xs.map(x => h('li', null, x)))
  return h('div', { class: 'crew-insp', testid: 'crew-inspector' },
    h('div', { class: 'crew-ih' }, roleChip(n.role), h('h4', null, `${n.id} · ${n.title}`)),
    h('div', { class: 'crew-dim' }, `spent ${cr(n.spent || 0)} of ${cr(n.envelope)}`, n.tier ? ` · ${n.tier}` : '', n.branch ? [' · ', h('span', { class: 'mono' }, n.branch)] : ''),
    n.asks ? sec('Asks you', h('p', null, n.asks), h('p', { class: 'crew-dim' }, 'The answers are pinned above the composer.')) : null,
    n.reason ? sec('Why it stopped', h('p', null, n.reason)) : null,
    n.brief ? sec('Brief from the planner', markdown(n.brief)) : null,
    sec('Owns', h('span', { class: 'mono' }, (n.owns || []).join(' · ') || 'nothing: it reads and reports')),
    (n.checks || []).length ? sec('Checks', n.checks.map(ch => h('div', { class: ['crew-chk', (n.state === 'landed' || n.state === 'ready') && 'ok'] }, h('span', { class: 'mono' }, ch)))) : null,
    (n.serves || []).length ? sec('Serves', h('span', null, n.serves.map(id => {
      const it = (c.items || []).find(x => x.id === id)
      return it ? `${id}: ${it.says}` : id
    }).join(' · '))) : null,
    n.handoff?.decisions?.length ? sec('DECISION: lines', list(n.handoff.decisions)) : null,
    n.handoff?.done?.length ? sec('Done', list(n.handoff.done)) : null,
    n.handoff?.open?.length ? sec('Left open', list(n.handoff.open)) : null)
}
