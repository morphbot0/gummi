// workflow.js — a session's workflow runs on the page (DESIGN §25). Three
// things live here, each drawn from what the server folded, never decided
// here:
//
// The run's block (§25.9): one per run, inline in the thread where the run
// was proposed, built from the thread's run item (threadfold) and redrawn
// only when its Rev moves. Its head says the recipe, the spec, the spend
// (a run estimates nothing) and the elapsed time, with Pause, Resume and Stop;
// a strip draws the recipe's top-level entries; a row per step that has
// run says what it did, with Open; a step's question is answered under
// its row; the footer lists the files changed and offers Revert run….
//
// The step panel (§25.10): Open shows the step beside the thread, on the
// surface the documents use (a phone's full screen). Its Transcript is the
// step card's own conversation, live; Diff its own diff; Brief the prompt
// as filled in and the artifacts it reads and writes. A step has no
// terminal of its own: it works in the session's tree, whose terminal is
// the session's. A message typed under it goes to the step's agent.
//
// The proposal's card (§25.5): the pinned decision that asks to allow a
// run draws its step tree, the checks it will run and what it costs
// against the session's pool, with a box for feedback.

import { $, h, clear, cr, dur, plural, isMobile } from './dom.js?v=__ASSET_V__'
import { get, post, cardPath } from './api.js?v=__ASSET_V__'
import { on, set, state } from './store.js?v=__ASSET_V__'
import { toast } from './toast.js?v=__ASSET_V__'
import { openModal } from './views.js?v=__ASSET_V__'
import { onEvent } from './events.js?v=__ASSET_V__'
import { hl } from './diff.js?v=__ASSET_V__'

let ctx = {}

export function initWorkflow (c) {
  ctx = c
  // a step is opened on its session: another card closes it
  on(['sel'], () => { if (state.step) set({ step: null }) })
  // the step's own conversation moves with its card's events, which name
  // the step's card, not the session
  onEvent('live', (e) => { if (state.step && e.id === state.step) loadTranscript() })
  onEvent('card', (e) => { if (state.step && e.id === state.step) { loadTranscript(); stepDiff = null } })
  on(['step'], () => { stepDiff = null; transcript = null; loadTranscript() })
  // the step's transcript follows its newest line while the reader is at
  // it, as the thread does: scrolling up lets go, coming back down takes
  // hold again. A scroll the page made redrawing the pane is not the
  // reader's (stickStep).
  const pane = $('#pane')
  pane.addEventListener('scroll', () => {
    if (!redrawing && state.tab === 'step' && transcriptBox?.isConnected) stepFollowing = atEnd(pane)
  }, { passive: true })
  // an open run's elapsed time is counted in place, never by a redraw
  setInterval(tickElapsed, 15000)
}

// ---------------------------------------------------------------- words

const STATE_WORD = {
  proposed: 'waits on your answer',
  queued: 'queued behind the open run',
  running: 'running',
  paused: 'paused',
  done: 'done',
  partial: 'partial',
  stopped: 'stopped',
  denied: 'not run'
}

const MARK = { done: '✓', failed: '✕', stopped: '–', asking: '?', running: '', waiting: '' }

function elapsed (r) {
  if (!r.started) return ''
  const from = new Date(r.started).getTime()
  const to = r.ended ? new Date(r.ended).getTime() : Date.now()
  return to > from ? dur(to - from) : ''
}

function tickElapsed () {
  const runs = (state.thread?.items || []).filter(it => it.t === 'run')
  for (const el of document.querySelectorAll('[data-elapsed-run]')) {
    const it = runs.find(x => String(x.run.n) === el.dataset.elapsedRun)
    if (!it || it.run.ended) continue
    const t = elapsed(it.run)
    if (el.textContent !== t) el.textContent = t
  }
}

// ---------------------------------------------------------------- the block

// stepsOpen remembers, per card and run, a person's unfolding of an ended
// run's steps.
const stepsOpen = new Set()
// replies are what a person is typing to a step's question, per step card:
// a block redrawn under them keeps the words.
const replies = new Map()

// runSig is what the thread caches a run's block by: its revision and the
// page's own state of it.
export function runSig (it) {
  return `${it.run.rev}|${stepsOpen.has(`${state.sel} ${it.run.n}`)}|${state.card?.stage || ''}`
}

// runBlock draws one run's block.
export function runBlock (it) {
  const r = it.run
  const id = state.sel
  const ended = ['done', 'partial', 'stopped'].includes(r.state)
  const live = ['running', 'paused'].includes(r.state)
  const block = h('section', {
    class: ['rb', `rb-${r.state}`, live && 'rb-on'],
    testid: `run-${r.n}`,
    data: { run: String(r.n), state: r.state, rev: r.rev },
    'aria-label': `${r.recipe} run ${r.n}, ${STATE_WORD[r.state] || r.state}`
  }, head(id, r), r.state === 'proposed' || r.state === 'denied' ? null : body(id, r, ended))
  return block
}

function head (id, r) {
  const act = (what, label, danger) => h('button', {
    class: ['btn', danger && 'danger'], type: 'button', testid: `run-${what}`,
    onclick: async (e) => {
      const b = e.currentTarget
      b.disabled = true
      try {
        await post(cardPath(id, `runs/${r.n}/${what}`))
        toast(`Run ${r.n}: ${label.toLowerCase()}`)
        ctx.refresh(id)
      } catch (err) {
        b.disabled = false
        toast(err.data?.error || err.message, { err: true })
      }
    }
  }, label)
  const money = cr(r.spent)
  return h('div', { class: 'rb-head', testid: 'run-head' },
    h('span', { class: 'k' }, 'workflow'),
    h('b', { testid: 'run-title' }, `${r.recipe} · run ${r.n}`),
    r.specTitle ? h('span', { class: 'rb-spec', title: `spec ${r.spec}${r.specRev ? `, revision ${r.specRev}` : ''}` }, h('span', { class: 'k' }, 'spec'), ' ', r.specTitle) : null,
    h('span', { class: 'rb-r' },
      h('span', { class: ['rb-state', `s-${r.state}`], testid: 'run-state' }, r.stopping ? 'stopping' : (STATE_WORD[r.state] || r.state)),
      r.state === 'proposed' || r.state === 'denied' ? null : h('span', { class: 'mono', testid: 'run-spent' }, money),
      r.started ? h('span', { class: 'mono rb-el', testid: 'run-elapsed', data: { elapsedRun: String(r.n) } }, elapsed(r)) : null,
      r.canPause ? act('pause', 'Pause') : null,
      r.canResume ? act('resume', 'Resume') : null,
      r.canStop ? act('stop', 'Stop', true) : null),
    r.reason && r.state !== 'done' ? h('span', { class: 'rb-why', testid: 'run-reason' }, r.reason) : null)
}

function body (id, r, ended) {
  const parts = [strip(r)]
  if (r.pending) {
    parts.push(h('div', { class: 'rb-pending', testid: 'run-pending' },
      h('b', null, 'Waits on you'), ' · ', r.pending.question.split('\n')[0],
      h('span', { class: 'hint' }, ' — answer it below the thread')))
  }
  const rows = h('div', { class: 'rb-rows', testid: 'run-rows' }, r.rows.map(row => [stepRow(id, row), row.question ? question(id, row) : null]))
  if (ended) {
    if (r.results?.length) parts.push(results(r))
    const key = `${id} ${r.n}`
    const d = h('details', { class: 'rb-fold', testid: 'run-steps', open: stepsOpen.has(key) },
      h('summary', null, plural(r.rows.length, 'step'), ' · ', r.rows.map(x => x.folded ? `${x.id} ↺ ${x.iter}` : x.id).join(' · ')), rows)
    d.addEventListener('toggle', () => { if (d.open) stepsOpen.add(key); else stepsOpen.delete(key) })
    parts.push(d)
    if (r.commits?.length) {
      parts.push(h('div', { class: 'rb-commits', testid: 'run-commits' }, h('span', { class: 'k' }, 'commits made while it ran'),
        r.commits.map(c => h('span', { class: 'mono' }, c))))
    }
  } else {
    parts.push(rows)
  }
  parts.push(foot(id, r, ended))
  return parts
}

function strip (r) {
  const chip = (e) => {
    const word = e.kind === 'repeat'
      ? `${e.id} ↺ ${e.iter || 0}/${e.max}`
      : e.kind === 'parallel' ? (e.branches || []).map(b => b.id).join(' ∥ ') : e.id
    return h('span', { class: ['rb-chip', `s-${e.state}`], testid: `run-strip-${e.id}`, title: `${e.id}: ${e.state}` },
      word, MARK[e.state] ? h('span', { class: 'm', 'aria-hidden': 'true' }, ' ' + MARK[e.state]) : null)
  }
  return h('div', { class: 'rb-strip', testid: 'run-strip' },
    r.strip.map((e, i) => [i ? h('i', { 'aria-hidden': 'true' }, '→') : null, chip(e)]))
}

function stepRow (id, row) {
  const open = row.card && !row.folded
  const where = row.iter ? `↺ ${row.iter}` : ''
  return h('div', {
    class: ['rb-row', `s-${row.state}`, row.waiting && 'waiting', row.question && 'asking', row.folded && 'folded'],
    testid: `run-row-${row.key}`,
    data: { card: row.card || null, state: row.state }
  },
  h('span', { class: 'dot', 'aria-label': row.state }),
  h('span', { class: 'at mono', title: row.path }, where),
  h('span', { class: ['wrole', row.role && `r-${row.role}`] }, row.role || row.repeat || ''),
  h('span', { class: 'ti' },
    h('span', { class: 'n' }, row.folded ? row.steps : row.id),
    row.line ? h('small', { testid: 'run-row-line' }, row.line) : null,
    row.flag ? h('small', { class: 'flag', testid: 'run-row-flag' }, row.flag) : null),
  h('span', { class: 'cost mono' }, row.spent ? cr(row.spent) : '–'),
  open
    ? h('button', { class: 'link rb-open', type: 'button', testid: 'run-open', 'aria-label': `Open step ${row.path}`, onclick: () => openStep(row.card) }, 'Open')
    : h('span'))
}

function question (id, row) {
  const q = row.question
  const send = async (answer, btn) => {
    if (!String(answer || '').trim()) return
    if (btn) btn.disabled = true
    try {
      await post(cardPath(id, `steps/${encodeURIComponent(row.card)}/answer`), { answer })
      replies.delete(row.card)
      toast(`Answered ${row.id}: ${answer}`)
      ctx.refresh(id)
    } catch (err) {
      if (btn) btn.disabled = false
      toast(err.data?.error || err.message, { err: true })
    }
  }
  const input = h('input', { type: 'text', class: 'rb-reply', testid: 'run-question-reply', placeholder: 'Reply…', 'aria-label': `Reply to ${row.id} in your own words`, value: replies.get(row.card) || '' })
  input.addEventListener('input', () => replies.set(row.card, input.value))
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); send(input.value, null) } })
  return h('div', { class: 'rb-ask', testid: 'run-question', data: { card: row.card } },
    h('b', null, `${row.id} asks`),
    h('div', { class: 'rb-q' }, q.question),
    h('div', { class: 'rb-opts' }, (q.options || []).map((o, i) => h('button', {
      class: ['btn', o.recommended && 'pri'], type: 'button', testid: `run-question-option-${i}`, title: o.detail || o.label,
      onclick: (e) => send(o.label, e.currentTarget)
    }, o.label))),
    h('div', { class: 'rb-replyrow' }, input,
      h('button', { class: 'btn', type: 'button', testid: 'run-question-send', onclick: (e) => send(input.value, e.currentTarget) }, 'Send')))
}

function results (r) {
  const word = { met: '✓', unmet: '✕', unjudged: '–' }
  return h('div', { class: 'rb-results', testid: 'run-results' }, r.results.map(x => h('span', {
    class: ['rb-res', `v-${x.verdict}`], testid: `run-result-${x.item}`, title: [x.checked ? 'its check' : 'judged', x.detail].filter(Boolean).join(': ')
  }, h('span', { 'aria-label': x.verdict }, word[x.verdict] || '·'), ` ${x.item}`)))
}

function foot (id, r, ended) {
  const files = r.files || []
  return h('div', { class: 'rb-foot', testid: 'run-files' },
    files.length
      ? h('span', { title: files.join('\n') }, h('b', null, plural(files.length, 'file')), ended ? ' left changed in the tree' : ' changed in the tree', ' · uncommitted unless committed since')
      : h('span', null, ended ? 'the run left the tree as it found it' : 'nothing changed yet'),
    h('span', { class: 'sp' }),
    r.report ? h('button', { class: 'link', type: 'button', testid: 'run-report', onclick: () => openSpec(r.spec) }, 'report ▸') : null,
    r.canRevert ? h('button', { class: 'btn', type: 'button', testid: 'run-revert', onclick: () => openRevert(id, r.n) }, 'Revert run…') : null)
}

// openSpec shows a spec on the Specs tab: a run's report is written there.
export let openSpec = () => {}
export function setOpenSpec (fn) { openSpec = fn }

// markStep rings the row of the step open beside the thread, in place.
export function markStep (root) {
  for (const el of root.querySelectorAll('.rb-row[data-card]')) el.classList.toggle('sel', el.dataset.card === state.step)
}

// ---------------------------------------------------------------- revert

async function openRevert (id, n) {
  let pre
  try {
    pre = await get(cardPath(id, `runs/${n}/revert`))
  } catch (err) {
    toast(err.data?.error || err.message, { err: true })
    return
  }
  const paths = pre.paths || []
  openModal({
    title: `Revert run ${n}?`,
    testid: 'revert-dialog',
    card: id,
    bodyEl: h('div', { class: 'mbody' },
      h('p', null, paths.length ? `${plural(paths.length, 'path')} go back to how they were when run ${n} started:` : 'Nothing differs from where the run started.'),
      paths.length ? h('ul', { class: 'rb-paths mono', testid: 'revert-paths' }, paths.map(p => h('li', null, p))) : null,
      h('p', { class: 'hint', testid: 'revert-note' }, pre.note)),
    actions: [
      { label: 'Cancel', testid: 'revert-cancel' },
      {
        label: 'Revert',
        danger: true,
        primary: true,
        testid: 'revert-confirm',
        disabled: !paths.length,
        onClick: async () => {
          try {
            // the revert is of what this preview listed: the server refuses it if the tree has moved since
            const done = await post(cardPath(id, `runs/${n}/revert`), { tree: pre.tree })
            toast(`Run ${n} reverted: ${plural((done.paths || []).length, 'path')} put back`)
            ctx.refresh(id)
          } catch (err) {
            toast(err.data?.error || err.message, { err: true })
            return false
          }
        }
      }
    ]
  })
}

// ---------------------------------------------------------------- the step panel

let transcript = null // { card, thread } as last read
let stepDiff = null // { card, data } as last read
let transcriptBusy = false
let transcriptAgain = false
const inner = new Map() // step card -> the inner tab shown
const drafts = new Map() // step card -> what is typed to it

export function openStep (card) {
  if (!card) return
  set({ step: card })
  ctx.setTab('step')
}

export const stepTab = {
  name: 'step',
  label: 'Step',
  key: 'g o',
  hidden: () => !state.step,
  fetch: (id) => state.step ? get(cardPath(id, `steps/${encodeURIComponent(state.step)}`)) : Promise.resolve(null),
  empty: (d) => !d,
  render: renderStep
}

async function loadTranscript () {
  const card = state.step
  if (!card) return
  if (transcriptBusy) { transcriptAgain = true; return }
  transcriptBusy = true
  try {
    const t = await get(cardPath(card, 'thread'))
    if (state.step !== card) return
    const was = transcript
    transcript = { card, thread: t }
    if (!was || was.card !== card || JSON.stringify(was.thread) !== JSON.stringify(t)) drawTranscript()
  } catch (err) {
    if (state.step === card && !transcript) { transcript = { card, err }; drawTranscript() }
  } finally {
    transcriptBusy = false
    if (transcriptAgain) { transcriptAgain = false; loadTranscript() }
  }
}

// transcriptBox is the step's conversation, kept across the panel's
// redraws: the panel redraws its pane when the step's head moves, and the
// conversation under the reader is not drawn again for that.
let transcriptBox = null
// stepFollowing says the reader is at the transcript's newest line, and a
// redraw keeps them there; redrawing marks the pane's own redraw, whose
// scroll is not the reader's.
let stepFollowing = true
let redrawing = false
function atEnd (sc) { return sc.scrollHeight - sc.scrollTop - sc.clientHeight < 24 }

// stickStep keeps a following reader at the transcript's newest line once
// the pane has laid out.
function stickStep () {
  requestAnimationFrame(() => {
    const pane = $('#pane')
    if (stepFollowing && state.tab === 'step' && transcriptBox?.isConnected) pane.scrollTop = pane.scrollHeight
    redrawing = false
  })
}
let drawTurns = () => []
let reconcileInto = (box, els) => { clear(box); box.append(...els) }
export function setTranscriptDrawer (draw, reconcile) { drawTurns = draw; reconcileInto = reconcile }

function drawTranscript () {
  if (!transcriptBox) return
  const t = transcript
  if (!t || t.card !== state.step) { reconcileInto(transcriptBox, [h('div', { class: 'empty' }, h('span', { class: 'spinner' }))]); return }
  if (t.err) { reconcileInto(transcriptBox, [h('div', { class: 'empty err' }, h('b', null, 'The step’s conversation did not load'), t.err.message)]); return }
  const els = drawTurns(t.card, t.thread.live)
  reconcileInto(transcriptBox, els.length ? els : [h('div', { class: 'empty', testid: 'step-transcript-empty' }, 'Nothing said yet.')])
  stickStep()
}

function renderStep (pane, entry, pctx) {
  const s = entry.data
  if (!s) { pane.append(h('div', { class: 'empty' }, 'No step open.')); return }
  const tab = inner.get(s.card) || 'transcript'
  const setInner = (t) => { inner.set(s.card, t); pctx.rerender() }
  const crumb = h('nav', { class: 'stp-crumb', testid: 'step-crumb', 'aria-label': 'Where this step is' },
    s.crumb.map((c, i) => [i ? h('span', { 'aria-hidden': 'true' }, ' › ') : null,
      i === 0 ? h('button', { class: 'link', type: 'button', testid: 'step-close', onclick: backToSession }, c)
        : i === s.crumb.length - 1 ? h('b', null, c) : h('span', null, c)]))
  const money = cr(s.spent)
  const head = h('div', { class: 'stp-head', testid: 'step-head' },
    crumb,
    h('div', { class: 'stp-title' },
      h('span', { class: ['wrole', `r-${s.role}`] }, s.role),
      h('b', { testid: 'step-title' }, s.id)),
    h('div', { class: 'stp-meta' },
      s.live ? h('span', { class: 'stp-live', testid: 'step-live' }, 'live') : h('span', { class: ['rb-state', `s-${s.state}`], testid: 'step-state' }, s.state),
      [s.model, s.tier && `step-${s.tier}`].filter(Boolean).join(' · '),
      h('span', { class: 'mono', testid: 'step-spent' }, money),
      'in the session’s tree'),
    s.reason ? h('div', { class: 'rb-why' }, s.reason) : null)
  const tabs = [['transcript', 'Transcript'], ['diff', 'Diff'], ['brief', 'Brief']]
  const bar = h('div', { class: 'seg stp-tabs', role: 'tablist', 'aria-label': 'The step' }, tabs.map(([k, l]) => h('button', {
    class: tab === k && 'on', type: 'button', role: 'tab', 'aria-selected': String(tab === k), testid: `step-tab-${k}`, onclick: () => setInner(k)
  }, l)))
  pane.append(h('div', { class: 'stp', testid: 'step-panel', data: { card: s.card } }, head, bar, innerBody(tab, s), composer(s, pctx)))
}

// backToSession closes the step: the surface it was shown on closes with
// it, and a phone goes back to the thread.
function backToSession () {
  set({ step: null, tab: 'spec', ...(isMobile() ? { view: 'thread' } : { rightHidden: true }) })
}

function innerBody (tab, s) {
  if (tab === 'transcript') {
    if (!transcriptBox || transcriptBox.dataset.card !== s.card) {
      // a step opened lands on its newest line, as a thread does
      transcriptBox = h('div', { class: 'stp-transcript', testid: 'step-transcript', data: { card: s.card } })
      stepFollowing = true
      drawTranscript()
      if (!transcript || transcript.card !== s.card) loadTranscript()
    }
    redrawing = true
    stickStep()
    return transcriptBox
  }
  if (tab === 'diff') {
    const box = h('div', { class: 'stp-diff', testid: 'step-diff' })
    if (stepDiff?.card === s.card) drawDiff(box, stepDiff.data)
    else {
      box.append(h('div', { class: 'empty' }, h('span', { class: 'spinner' })))
      get(cardPath(s.session, `steps/${encodeURIComponent(s.card)}/diff`)).then((d) => {
        stepDiff = { card: s.card, data: d }
        if (state.step === s.card) { clear(box); drawDiff(box, d) }
      }).catch((err) => { clear(box); box.append(h('div', { class: 'empty err' }, err.message)) })
    }
    return box
  }
  return h('div', { class: 'stp-brief', testid: 'step-brief' },
    (s.artifacts || []).length
      ? h('div', { class: 'stp-arts', testid: 'step-artifacts' }, s.artifacts.map(a => h('div', { class: ['stp-art', a.exists && 'there'] },
        h('span', { class: 'k' }, a.writes ? 'writes' : 'reads'), h('b', null, a.name), h('span', { class: 'mono', title: a.path }, a.path),
        a.exists ? null : h('span', { class: 'hint' }, 'not there yet'))))
      : null,
    h('pre', { class: 'stp-prompt', tabindex: '0', 'aria-label': 'The prompt as filled in' }, s.brief || ''),
    s.output ? h('div', { class: 'stp-out' }, h('span', { class: 'k' }, 'its output'), h('pre', { tabindex: '0' }, s.output)) : null)
}

function drawDiff (box, d) {
  if (!d.files?.length) { box.append(h('div', { class: 'empty', testid: 'step-diff-none' }, d.why || 'No change.')); return }
  for (const f of d.files) {
    box.append(h('section', { class: 'file', testid: 'step-diff-file', 'aria-label': f.path, data: { path: f.path } },
      h('div', { class: 'fh' }, h('span', { class: 'p' }, f.path), h('span', { class: 'add' }, `+${f.add}`), h('span', { class: 'del' }, `−${f.del}`)),
      (f.hunks || []).map(hk => h('div', { class: 'hunk' }, h('div', { class: 'hh' }, hk.header),
        (hk.lines || []).map(ln => {
          const code = h('span')
          code.innerHTML = hl(ln.text) || ' ' // hl escapes everything it is given
          return h('div', { class: ['ln', ln.t === '+' ? 'a' : ln.t === '-' ? 'd' : ''] },
            h('span', { class: 'o ro' }, ln.old ? String(ln.old) : ''), h('span', { class: 'n ro' }, ln.new ? String(ln.new) : ''),
            h('span', { class: 'm', 'aria-hidden': 'true' }, ln.t === ' ' ? '' : ln.t), code)
        })))))
  }
}

// composer is the line to the step's agent: a turn while it runs, a
// follow-up once it has ended (the server's words say which).
function composer (s, pctx) {
  const ta = h('textarea', { class: 'stp-input', testid: 'step-input', rows: '2', placeholder: s.says, 'aria-label': `Message ${s.id}` })
  ta.value = drafts.get(s.card) || ''
  ta.addEventListener('input', () => drafts.set(s.card, ta.value))
  const send = async (btn) => {
    const text = ta.value.trim()
    if (!text) { ta.focus(); return }
    btn.disabled = true
    try {
      await post(cardPath(s.session, `steps/${encodeURIComponent(s.card)}/send`), { text })
      drafts.delete(s.card)
      ta.value = ''
      toast(`Sent to ${s.id}`)
      loadTranscript()
    } catch (err) {
      toast(err.data?.error || err.message, { err: true })
    } finally {
      btn.disabled = false
    }
  }
  const btn = h('button', { class: 'btn pri', type: 'button', testid: 'step-send', onclick: (e) => send(e.currentTarget) }, 'Send')
  ta.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(btn) } })
  return h('div', { class: 'stp-comp', testid: 'step-composer', data: { draft: `step:${s.card}` } },
    h('span', { class: 'to' }, `to ${s.id}`), ta, btn)
}

// ---------------------------------------------------------------- the proposal

// proposalEl is a run's proposal (or revised plan) as its decision draws
// it: the step tree with each step's role and tier, the spec it freezes,
// the checks it runs, the warnings, and what the session's pool has left —
// the run spends from it and estimates nothing.
export function proposalEl (p) {
  const left = p.bounded ? `${cr(p.poolLeft)} left in the session` : 'the session has no envelope'
  return h('div', { class: 'wfp', testid: 'run-proposal' },
    h('div', { class: 'wfp-head' },
      h('b', null, p.kind === 'plan' ? `Allow the revised plan · run ${p.run}?` : `Allow ${p.recipe} · run ${p.run}?`),
      h('span', { class: 'hint', testid: 'proposal-tree' }, ['in the session’s tree', p.branch, p.dirty ? 'holds uncommitted work' : 'nothing uncommitted'].filter(Boolean).join(' · '))),
    p.description ? h('div', { class: 'hint' }, p.description) : null,
    h('div', { class: 'wfp-spec', testid: 'proposal-spec' }, h('span', { class: 'k' }, 'spec'), ' ',
      `S${p.spec} ${p.specTitle || ''}`, p.specRev ? ` · revision ${p.specRev}, frozen when it starts` : ''),
    h('div', { class: 'wfp-steps', role: 'table', 'aria-label': 'Its steps' }, p.steps.map(s => h('div', {
      class: ['wfp-step', `k-${s.kind}`], role: 'row', testid: `proposal-step-${s.id}`, style: { '--depth': String(s.depth) }
    },
    h('span', { class: 'id mono', role: 'cell' }, s.kind === 'repeat' ? `${s.id} ↺≤${s.max}` : s.kind === 'parallel' ? `${s.id} ∥` : s.id),
    h('span', { class: ['wrole', s.role && `r-${s.role}`], role: 'cell' }, s.role || (s.kind === 'parallel' ? s.join : '')),
    h('span', { class: 'ti', role: 'cell' }, s.kind === 'repeat'
      ? `until ${s.until} · at the max: ${s.onMax}`
      : s.kind === 'parallel' ? `side by side, join ${s.join}` : [s.tier, s.prompt].filter(Boolean).join(' · '))))),
    p.checks?.length ? h('div', { class: 'wfp-checks', testid: 'proposal-checks' }, h('span', { class: 'k' }, 'runs in the tree when it ends'),
      p.checks.map(c => h('div', null, h('b', { class: 'mono' }, c.item), ' ', h('span', { class: 'mono' }, c.cmd)))) : null,
    h('div', { class: 'wfp-ticks' },
      p.warnings?.length
        ? h('div', { class: 'warnc', testid: 'proposal-warnings' }, p.warnings.map(w => h('div', null, `! ${w}`)))
        : h('span', { class: 'okc' }, '✓ recipe checks pass'),
      h('span', { class: p.bounded && p.poolLeft <= 0 ? 'warnc' : 'okc', testid: 'proposal-money' }, `${left}; the run spends from it`)),
    h('div', { class: 'hint' }, 'Changes stay uncommitted in the tree · a snapshot at every step'))
}
