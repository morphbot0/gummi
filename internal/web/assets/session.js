// session.js — a session from the page's side (DESIGN §19.8). A session is
// a freeform card: one agent in its own worktree, on the model its person
// picked, with no stages. Two things live here.
//
// The draft: "New session" opens no dialog. The conversation column becomes
// an empty session whose repository, base and budget sit in the composer,
// and the first message sent is what creates it (POST /api/cards) and its
// first turn.
//
// The model picker beside Send, on a draft and on an open session: the
// agents this host can run, the models each agent says it provides (the
// server asked them live) merged with the ids the workspace's profiles
// run, the pairs sessions on the board use, and any id typed in. On a
// session that exists, a pick is the card's "model" action. A card in the
// workflow shows no picker: its stages take their models from its profile.

import { $, h, clear, isMobile, cr, dollarsInput, parseDollars, cardName } from './dom.js?v=__ASSET_V__'
import { get, post, cardPath } from './api.js?v=__ASSET_V__'
import { on, set, state } from './store.js?v=__ASSET_V__'
import { toast } from './toast.js?v=__ASSET_V__'
import { openModal } from './views.js?v=__ASSET_V__'
import { openPop, closePop, popKind, openModelPicker as openPairPicker } from './modelpick.js?v=__ASSET_V__'

let ctx = {}
let form = null // webapi.Form: the draft's choices and the picker's catalog
let formLoad = null // the /api/form fetch in flight, for a send that cannot wait on nothing

const BUDGETS = [500, 1500, 5000, 0]

export function initSession (c) {
  ctx = c
  // opening a card leaves the draft; what was typed stays in the composer
  // only while the draft is what it was typed into
  on(['sel'], () => { if (state.sel && state.sessionDraft) set({ sessionDraft: null }) })
  on(['sessionDraft', 'card', 'sel', 'conn'], render)
  render()
}

async function loadForm (repo = '') {
  const mine = (formLoad = (async () => {
    try {
      form = await get('/api/form' + (repo ? `?repo=${encodeURIComponent(repo)}` : ''))
    } catch (err) {
      toast(`The model list did not load: ${err.message}`, { err: true })
    }
    return form
  })())
  try { return await mine } finally { if (formLoad === mine) formLoad = null }
}

// budgetOf reads a budget typed by a person in dollars, 0 for uncapped,
// and answers it in credits. Anything else — empty, negative, finer than a
// cent — is refused with a sentence, never quietly read as 0 (which would
// be no cap at all).
export function budgetOf (raw) {
  if (String(raw ?? '').trim() === '') return { err: 'Say a budget in dollars — 0 is uncapped.' }
  const b = parseDollars(raw)
  return b.err ? { err: b.err } : { n: b.credits }
}

// budgetWord is how a draft says its budget: none picked yet and the
// board's default still on its way says so, rather than "uncapped".
function budgetWord (env) {
  if (env == null) return 'the board default'
  return env ? cr(env) : 'uncapped'
}

// newSession opens an empty draft in the conversation column. cameFrom
// is the card open before it, which Cancel goes back to.
let cameFrom = null
// opts.home opens the draft as an agent's: a home of its own, no repository.
export async function newSession (opts) {
  const home = opts?.home === true
  closePop()
  if (state.sel) cameFrom = state.sel
  // the draft opens at once, on what the page already knows; the form's
  // defaults fill it when they arrive, without touching what was typed
  const known = form
  // envelope null is "the board's default, not here yet": never 0, which
  // would start the session uncapped
  const draftOf = (f) => {
    const d = f?.sessions?.default || {}
    return { repo: f?.repos?.[0] || '', base: '', envelope: f ? (f.envelope || 0) : null, backend: d.backend || '', model: d.model || '', mainCheckout: false, home }
  }
  set({
    sel: null,
    card: null,
    cardErr: null,
    thread: null,
    live: null,
    gone: null,
    sessionDraft: draftOf(known)
  })
  if (isMobile()) set({ view: 'thread' })
  ctx.clearComposer?.()
  $('#composer-input')?.focus()
  const f = await loadForm()
  if (!known && f && state.sessionDraft) set({ sessionDraft: { ...draftOf(f), ...pickedOnly(state.sessionDraft) } })
}

// pickedOnly is what a person changed in a draft before its defaults came.
function pickedOnly (d) {
  const out = {}
  for (const k of ['repo', 'base', 'backend', 'model']) if (d[k]) out[k] = d[k]
  if (d.envelope != null) out.envelope = d.envelope
  if (d.mainCheckout) out.mainCheckout = true
  if (d.home) out.home = true
  return out
}

// startSession sends the draft's first message: the card is created with
// it, the page moves onto the card, and its first turn is already running.
// A refusal is thrown back to the composer, which keeps the line. atts
// are the ids of images already uploaded (the composer's paperclip) —
// they ride the description the card is seeded with, and its first turn
// carries them natively when the agent it starts on can take images.
export async function startSession (text, atts = []) {
  let d = state.sessionDraft
  if (d.envelope == null) {
    // sent before the form came: its default budget is what the session
    // starts on, so wait for it — and refuse rather than start uncapped
    await (formLoad || loadForm(d.repo || ''))
    d = state.sessionDraft || d
    if (d.envelope == null && form) d = { ...d, envelope: form.envelope || 0 }
    if (d.envelope == null) throw new Error('The board’s default budget did not load. Set a budget beside the composer, then send again.')
  }
  const req = { kind: 'freeform', description: text, backend: d.backend, model: d.model, envelope: d.envelope }
  // an agent is in no repository: its draft sends neither a repo nor a base
  if (d.home) req.home = true
  else {
    if (d.repo) req.repo = d.repo
    if (d.mainCheckout) req.mainCheckout = true
    else if (d.base) req.base = d.base
  }
  if (atts.length) req.attachments = atts
  const c = await post('/api/cards', req)
  set({ sessionDraft: null })
  await ctx.refreshBoard?.()
  await ctx.select(c.id)
  toast(`${cardName(c.id)} started on ${pairLabel(c.session || d)}`)
  return c
}

export function pairLabel (p) {
  return `${p.model || 'the default model'} · ${p.backend || 'default agent'}`
}

// draftHead is the conversation's head while a draft is open.
export function draftHead () {
  const d = state.sessionDraft
  return [
    h('div', { class: 'head-row' },
      h('span', { class: ['kind', d.home && 'ag'], testid: 'card-kind' }, d.home ? 'agent' : 'session'),
      h('h1', { testid: 'card-title' }, d.home ? 'New agent' : 'New session'),
      h('div', { class: 'head-actions' },
        h('button', { class: 'btn', type: 'button', testid: 'draft-cancel', onclick: cancelDraft }, 'Cancel'))),
    h('div', { class: 'subline' },
      d.home
        ? h('span', { testid: 'draft-where' }, 'a home of its own · no repository')
        : h('span', null, d.repo || state.board?.repo || 'this repository', d.mainCheckout ? ' · the main checkout' : (d.base ? [' · from ', h('span', { class: 'mono' }, d.base)] : ' · a new worktree')),
      h('span', null, `budget ${budgetWord(d.envelope)}`))
  ]
}

// draftHero is the empty conversation a draft shows.
export function draftHero () {
  const d = state.sessionDraft
  const where = d.repo || state.board?.repo || 'this repository'
  const starters = ['Fix a failing test', 'Explain how this part of the code works', 'Review this branch before I merge it']
  if (d.home) {
    return h('div', { class: 'draft-hero', testid: 'draft-hero' },
      h('b', null, 'What is this agent for?'),
      h('p', null, 'An agent is a session with a home of its own instead of a repository. Its home persists across conversations, so it can keep notes and skills there. It is budgeted like any card, and it starts when you send the first message.'))
  }
  return h('div', { class: 'draft-hero', testid: 'draft-hero' },
    h('b', null, `What are we working on in ${where}?`),
    h('p', null, 'A session is one agent in its own worktree, with no stages and no gates. It starts when you send the first message. The model is next to Send, and you can switch it later.'),
    h('div', { class: 'starters' }, starters.map(s => h('button', {
      class: 'btn', type: 'button', testid: 'draft-starter', onclick: () => { ctx.restoreComposer?.(s + ' '); $('#composer-input')?.focus() }
    }, s))))
}

function cancelDraft () {
  closePop()
  set({ sessionDraft: null })
  ctx.clearComposer?.()
  const back = state.board?.rows?.some(r => r.id === cameFrom) ? cameFrom : state.board?.rows?.[0]?.id
  if (back) ctx.select(back)
}

// draftTakesImages is whether the session this draft would start can
// take a turn's images: the agent its pair names, the form's default
// pair answering for one that has not picked (or has not loaded its
// form) — the backend-level half of the live answer a card's composer
// state reports once the session exists. The first message's attachments
// ride the description the card is seeded with, and an agent that cannot
// take them natively still gets each one by its path, so nothing here
// can be refused.
export function draftTakesImages () {
  const d = state.sessionDraft
  if (!d) return false
  const backend = d.backend || form?.sessions?.default?.backend || ''
  const a = (form?.sessions?.agents || []).find((x) => x.name === backend)
  return !!a?.images
}

// sessionPair is what the picker shows: the draft's pick, or the pair an
// open session runs on. null hides the picker.
function sessionPair () {
  if (state.sessionDraft) return state.sessionDraft
  const c = state.card
  if (c && c.session && c.stage === 'open' && c.actions?.some(a => a.id === 'model')) return c.session
  return null
}

function render () {
  const slot = $('#composer-model')
  const row = $('#composer-draft')
  if (!slot || !row) return
  clear(slot)
  clear(row)
  const p = sessionPair()
  if (p) {
    const btn = h('button', {
      class: 'mbtn', type: 'button', testid: 'model-picker-btn',
      title: 'The agent and model this session runs on',
      'aria-haspopup': 'dialog', 'aria-expanded': 'false',
      onclick: () => (popKind() === 'model' ? closePop() : openPicker(btn))
    }, h('span', { class: 'mono m' }, p.model || 'default model'), h('span', { class: 'mono a' }, p.backend || 'agent'), h('span', { class: 'caret', 'aria-hidden': 'true' }, '▴'))
    slot.append(btn)
  }
  const d = state.sessionDraft
  row.hidden = !d
  if (!d) return
  const repos = form?.repos || []
  if (repos.length > 1 && !d.home) {
    row.append(h('label', { class: 'dsel' }, h('span', { class: 'sr-only' }, 'Repository'),
      h('select', {
        testid: 'draft-repo',
        onchange: async (e) => {
          await loadForm(e.target.value)
          set({ sessionDraft: { ...state.sessionDraft, repo: e.target.value, base: '' } })
        }
      }, repos.map(r => h('option', { value: r, selected: r === d.repo }, r)))))
  }
  const branches = form?.branches || []
  row.append(h('button', {
    class: 'dsel dbtn', type: 'button', testid: 'draft-main',
    title: d.home
      ? 'An agent: it works in a home of its own, in no repository, and keeps what it builds there'
      : d.mainCheckout ? 'The session works in the main checkout: no branch, no worktree, its changes left uncommitted' : 'The session works in its own branch worktree',
    // the row cycles its three answers: own worktree, the main checkout, a
    // home of its own. The base stays picked across it: the other two only
    // set it aside (startSession sends no base with them)
    onclick: () => set({ sessionDraft: { ...state.sessionDraft, mainCheckout: !d.mainCheckout && !d.home, home: d.mainCheckout } })
  }, d.home ? h('b', null, 'a home of its own') : d.mainCheckout ? h('b', null, 'the main checkout') : 'own worktree'))
  if (!d.mainCheckout && !d.home) {
    row.append(h('label', { class: 'dsel' }, h('span', { class: 'lbl' }, 'from'),
      h('select', {
        testid: 'draft-base', 'aria-label': 'Branch the session forks from',
        onchange: (e) => set({ sessionDraft: { ...state.sessionDraft, base: e.target.value } })
      }, h('option', { value: '', selected: !d.base }, 'the default branch'), branches.map(b => h('option', { value: b, selected: b === d.base }, b)))))
    row.append(h('span', { class: 'dnote-s' }, 'new worktree'))
  }
  const bud = h('button', {
    class: 'dsel dbtn', type: 'button', testid: 'draft-budget', 'aria-haspopup': 'dialog',
    onclick: () => (popKind() === 'budget' ? closePop() : openBudget(bud))
  }, 'budget ', h('b', null, budgetWord(d.envelope)))
  row.append(bud)
}

// ---- popovers ----

function openBudget (anchor) {
  const d = state.sessionDraft
  const input = h('input', { class: 'inp', type: 'text', inputmode: 'decimal', value: dollarsInput(d.envelope ?? form?.envelope ?? 0), testid: 'draft-budget-input', 'aria-label': 'Budget in dollars', 'aria-describedby': 'draft-budget-err' })
  const err = h('p', { class: 'ferr', id: 'draft-budget-err', testid: 'draft-budget-error', role: 'alert', hidden: true })
  // a preset is already credits; only what was typed is dollars to read
  const apply = (v) => {
    const b = typeof v === 'number' ? { n: v } : budgetOf(v)
    if (b.err) { err.textContent = b.err; err.hidden = false; input.focus(); return }
    set({ sessionDraft: { ...state.sessionDraft, envelope: b.n } })
    closePop()
  }
  input.addEventListener('input', () => { err.hidden = true })
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); apply(input.value) } })
  const el = h('div', { class: 'spop', role: 'dialog', 'aria-label': 'Budget for the new session', testid: 'draft-budget-pop' },
    h('div', { class: 'fl' }, 'Budget for the new session'),
    h('div', { class: 'presets' }, BUDGETS.map(v => h('button', {
      class: ['btn', v === d.envelope && 'pri'], type: 'button', testid: `draft-budget-${v}`, onclick: () => apply(v)
    }, v ? cr(v) : 'uncapped'))),
    h('div', { class: 'row' }, input, h('button', { class: 'btn', type: 'button', testid: 'draft-budget-set', onclick: () => apply(input.value) }, 'Set')),
    err,
    h('p', { class: 'fh' }, 'When it runs out the session stops and asks. Raise it from the session’s head and the same conversation carries on.'))
  openPop('budget', anchor, el, { alignLeft: true })
  input.focus()
  input.select()
}

export function openModelPicker () {
  const btn = document.querySelector('[data-testid=model-picker-btn]')
  if (btn) openPicker(btn)
}

async function openPicker (anchor) {
  if (!form) await loadForm(state.sessionDraft?.repo || '')
  openPairPicker(anchor, {
    catalog: form?.sessions,
    current: sessionPair() || {},
    onPick: choose,
    note: state.sessionDraft
      ? 'Any model an installed agent runs — the ones it names itself, plus the ids this workspace has used. Profiles do not apply to sessions.'
      : 'Switching keeps the conversation: the next turn runs on the new model with what was said so far.'
  })
}

// choose applies a pick: to the draft, or as the open session's "model"
// action, whose refusal (a turn in flight, an id the agent would refuse)
// is said where the picker was.
async function choose (backend, model) {
  closePop()
  if (state.sessionDraft) {
    set({ sessionDraft: { ...state.sessionDraft, backend, model } })
    return
  }
  const id = state.sel
  if (!id) return
  try {
    const c = await post(cardPath(id, 'actions/model'), { backend, model })
    if (c && state.sel === id) set({ card: c })
    toast(`${cardName(id)} now runs on ${pairLabel({ backend, model })}`)
    ctx.refresh?.(id)
  } catch (err) {
    const e = err.data || {}
    toast(e.error === 'busy' ? 'The session is mid-turn — switch once this turn ends.' : (e.error || err.message), { err: true })
  }
}

// ---- write a spec ----

// writeSpecButton is the session head's way into the workflow: shown while
// the card's menu offers "writespec" (an open session nobody else drives).
export function writeSpecButton (card) {
  const a = card?.actions?.find(x => x.id === 'writespec')
  if (!a) return null
  return h('button', { class: 'btn hide-s', type: 'button', testid: 'write-spec', title: a.detail, onclick: () => openWriteSpec(card, a) }, 'Write a spec')
}

// openWriteSpec opens the dialog at once and starts the handoff brief. On a
// live session the start IS the brief turn, gummi asking the session to
// write what the next card's architect will read: it runs in the
// background, the thread's busy marker names it, and the dialog reads it
// again when the card's update says it has landed — so leaving the dialog,
// or the page, loses nothing. The brief is an editable field filled with
// the draft, labeled by where it came from. Nothing mints before the brief
// has landed and the person confirms, and the start button stays off until
// it has.
export async function openWriteSpec (card, a) {
  if (!form) await loadForm(card.repo || '')
  const title = h('input', { value: card.title, testid: 'spec-title', autocomplete: 'off' })
  const brief = h('textarea', { rows: 10, testid: 'spec-brief', disabled: true })
  const note = h('span', { class: 'fh', testid: 'spec-brief-note' }, 'drafting the handoff brief…')
  const profile = h('select', { testid: 'spec-profile' },
    (a.choices || []).map((c, i) => h('option', { value: c.value, selected: i === 0 }, c.detail ? `${c.label} — ${c.detail}` : c.label)))
  const budget = h('input', { type: 'text', inputmode: 'decimal', value: form ? dollarsInput(form.envelope || 0) : '', testid: 'spec-budget' })
  const err = h('p', { class: 'spec-err', role: 'alert', testid: 'spec-error', hidden: true })
  const body = h('div', { class: 'mbody spec-body' },
    h('p', { class: 'spec-about' }, `${cardName(card.id)} ends here and keeps its branch. A feature continues its work on a branch cut from it: the profile’s architect plans it from this conversation and what the branch already holds, and it lands only once its critique and checks pass.`),
    h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Title'), title),
    h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Brief'), brief, note),
    (a.choices || []).length ? h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Profile'), profile, h('span', { class: 'fh' }, 'Every stage takes its agent and model from the profile.')) : null,
    h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Budget'), budget, h('span', { class: 'fh' }, 'Dollars for the whole spec, apart from what the session spent. 0 is uncapped.')),
    err)
  const startAction = { label: 'Start the spec', primary: true, testid: 'spec-start', disabled: true, onClick: mint }
  // filled: the brief has landed in the field, which is when a mint may
  // read it. Set once and never cleared — a later read cannot unfill it.
  let filled = false
  let reading = false
  // apply takes the brief as the page last read it: drafting keeps the
  // dialog waiting, a brief fills the field, a failed read says why.
  const apply = (d) => {
    if (filled || !d || d.drafting) return
    filled = true
    // the draft's own label: the session's words, or what the conversation
    // alone could lay out — a degraded brief is never mistaken for the
    // session's own words
    brief.value = d.brief || ''
    brief.disabled = false
    if (startAction.el) startAction.el.disabled = false
    note.textContent = d.source === 'assembled'
      ? 'assembled from the conversation — no live session could answer it'
      : 'the session’s own words — edit what the next card’s architect will read'
  }
  const fail = (e) => { note.textContent = e.data?.text || e.data?.error || e.message }
  // read GETs the brief as it stands; it never starts a turn. An update that
  // arrives while a read is in flight sets again, and the read settles by
  // reading once more: the brief can land during the GET, and the update
  // that says so must not be the one dropped.
  let again = false
  const read = async () => {
    if (filled) return
    if (reading) { again = true; return }
    reading = true
    try {
      apply(await get(cardPath(card.id, 'writespec-draft')))
    } catch (e) {
      fail(e)
    } finally {
      reading = false
      if (again) { again = false; read() }
    }
  }
  async function mint () {
    err.hidden = true
    if (!filled) {
      // the brief is still drafting: nothing mints off an empty field
      err.textContent = 'still drafting the handoff brief…'
      err.hidden = false
      return false
    }
    const b = budgetOf(budget.value)
    if (b.err) { err.textContent = b.err; err.hidden = false; budget.focus(); return false }
    const req = { message: title.value.trim(), brief: brief.value, number: b.n }
    if (profile.value) req.profile = profile.value
    if (state.card?.decision?.against?.token) req.against = state.card.decision.against.token
    try {
      const c = await post(cardPath(card.id, 'actions/writespec'), req)
      await ctx.refreshBoard?.()
      if (c?.id) await ctx.select(c.id)
      toast(`${cardName(card.id)} continues as ${c?.id ? cardName(c.id) : 'a spec'}`)
    } catch (e) {
      err.textContent = e.data?.text || e.data?.error || e.message
      err.hidden = false
      return false
    }
  }
  // the card's update is the signal the brief has landed: read it then. The
  // listener waits for the start to answer without error: a refused start
  // started no brief, so an update that arrives meanwhile must not read an
  // assembled draft into the dialog as though it were the session's own.
  let unwatch = () => {}
  let closed = false
  openModal({
    title: 'Write a spec',
    testid: 'write-spec-dialog',
    bodyEl: body,
    card: card.id,
    onClose: () => { closed = true; unwatch() },
    actions: [{ label: 'Cancel' }, startAction]
  })
  // the dialog is up: start the brief once, then show what it answered
  try {
    apply(await post(cardPath(card.id, 'writespec-draft')))
    if (!closed) {
      unwatch = on(['card'], () => { read() })
      // a brief that landed before the listener attached has no update
      // left to say so: one read covers it
      read()
    }
  } catch (e) {
    fail(e)
  }
}

// ---- delegation ----

// delegateButton opens the session's delegation: the credits it may give
// workflow cards it creates. Off until the person sets a budget — a session
// without one is not offered the card tools at all.
export function delegateButton (card) {
  // an agent holds no branch a delegated card could fork from
  if (!card?.session || card.stage === 'done' || card.home) return null
  const d = card.delegation
  const label = d ? `Delegating · ${cr(Math.max(0, d.left))} left` : 'Delegate'
  return h('button', { class: ['btn', 'hide-s', d && 'on'], type: 'button', testid: 'delegate', title: 'Let this session create workflow cards under a budget', onclick: () => openDelegate(card) }, label)
}

export function openDelegate (card) {
  const d = card.delegation || { budget: 0, confirmAll: false }
  const budget = h('input', { type: 'text', inputmode: 'decimal', value: d.budget ? dollarsInput(d.budget) : '', testid: 'delegate-budget' })
  const all = h('input', { type: 'checkbox', testid: 'delegate-all' })
  all.checked = !!d.confirmAll
  const err = h('p', { class: 'spec-err', role: 'alert', testid: 'delegate-error', hidden: true })
  const body = h('div', { class: 'mbody spec-body' },
    h('p', { class: 'spec-about' }, 'The session may then create feature and bug cards that fork from this branch, run the whole workflow, and land back here when it asks. Each card is put to you first.'),
    h('label', { class: 'field' }, h('span', { class: 'fl' }, 'Budget'), budget, h('span', { class: 'fh' }, 'Dollars across every card it creates. 0 turns delegation off.')),
    h('label', { class: 'field' }, all, h('span', null, ' Don’t ask me about each card')),
    err)
  openModal({
    title: 'Delegate to cards',
    testid: 'delegate-dialog',
    bodyEl: body,
    card: card.id,
    actions: [{ label: 'Cancel' }, {
      label: 'Save',
      primary: true,
      testid: 'delegate-save',
      onClick: async () => {
        const b = budget.value.trim() === '' ? { credits: 0 } : parseDollars(budget.value)
        if (b.err) { err.hidden = false; err.textContent = b.err; return false }
        try {
          await post(cardPath(card.id, 'delegation'), { budget: b.credits, confirm_all: all.checked })
          return true
        } catch (e) {
          err.hidden = false
          err.textContent = e.data?.error || e.message
          return false
        }
      }
    }]
  })
}
