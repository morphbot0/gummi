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
  if (d.attach?.length) out.attach = d.attach
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
    else {
      if (d.base) req.base = d.base
      const others = (d.attach || []).filter(n => n !== d.repo)
      if (others.length) req.attach = others
    }
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
        ? null
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
      h('b', null, 'What is this agent for?'))
  }
  return h('div', { class: 'draft-hero', testid: 'draft-hero' },
    h('b', null, `What are we working on in ${where}?`),
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
  // an agent was chosen when the draft opened: it has no worktree to pick
  if (!d.home) {
    row.append(h('button', {
      class: 'dsel dbtn', type: 'button', testid: 'draft-main',
      title: d.mainCheckout ? 'The session works in the main checkout: no branch, no worktree, its changes left uncommitted' : 'The session works in its own branch worktree',
      // the base stays picked across it: the main checkout only sets it
      // aside (startSession sends no base with it)
      onclick: () => set({ sessionDraft: { ...state.sessionDraft, mainCheckout: !d.mainCheckout } })
    }, d.mainCheckout ? h('b', null, 'the main checkout') : 'own worktree'))
  }
  if (!d.mainCheckout && !d.home) {
    row.append(h('label', { class: 'dsel' }, h('span', { class: 'lbl' }, 'from'),
      h('select', {
        testid: 'draft-base', 'aria-label': 'Branch the session forks from',
        onchange: (e) => set({ sessionDraft: { ...state.sessionDraft, base: e.target.value } })
      }, h('option', { value: '', selected: !d.base }, 'the default branch'), branches.map(b => h('option', { value: b, selected: b === d.base }, b)))))
    // repositories to attach beside the session's own: the same branch is
    // cut in each (DESIGN §27). One chip, its list in a popover: a
    // workspace of many repositories would otherwise flood the row
    const others = (form?.repos || []).filter(r => r !== d.repo)
    if (others.length) {
      const picked = attachedOf(d)
      row.append(h('button', {
        class: 'dsel dbtn', type: 'button', testid: 'draft-attach', 'aria-haspopup': 'dialog',
        title: picked.length ? `Also attached: ${picked.join(', ')}` : 'Attach other repositories: the session’s branch is cut there too',
        // pinned to the row, not the chip: every tick redraws the row's
        // chips, and a popover held to a replaced one would lose its place
        onclick: () => (popKind() === 'attach' ? closePop() : openAttach(row))
      }, h('span', { class: 'lbl' }, 'also'), picked.length
        ? [h('b', { class: 'cut' }, picked[0]), picked.length > 1 ? h('span', { class: 'lbl' }, `+${picked.length - 1}`) : null]
        : 'attach…'))
    }
  }
  const bud = h('button', {
    class: 'dsel dbtn', type: 'button', testid: 'draft-budget', 'aria-haspopup': 'dialog',
    onclick: () => (popKind() === 'budget' ? closePop() : openBudget(bud))
  }, 'budget ', h('b', null, budgetWord(d.envelope)))
  row.append(bud)
}

// ---- popovers ----

// attachedOf is the draft's attached repositories, less its own: a repo
// picked before the person switched the draft onto it is not "also".
function attachedOf (d) {
  return (d.attach || []).filter(r => r !== d.repo)
}

// openAttach lists the other repositories to cut the session's branch in.
// A tick applies at once and the list stays open, so several are picked in
// one go; the filter is there for a workspace of dozens.
function openAttach (anchor) {
  const others = (form?.repos || []).filter(r => r !== state.sessionDraft.repo)
  const list = h('div', { class: 'alist', role: 'group', 'aria-label': 'Repositories to attach' })
  const items = others.map(r => {
    const box = h('input', {
      type: 'checkbox', testid: `draft-attach-${r}`, checked: attachedOf(state.sessionDraft).includes(r),
      onchange: () => {
        const cur = attachedOf(state.sessionDraft)
        set({ sessionDraft: { ...state.sessionDraft, attach: box.checked ? [...cur, r] : cur.filter(x => x !== r) } })
      }
    })
    const el = h('label', { class: 'aitem' }, box, h('span', { class: 'mono' }, r))
    list.append(el)
    return { r, el }
  })
  const filter = h('input', {
    class: 'inp', type: 'text', placeholder: 'Filter repositories', 'aria-label': 'Filter repositories', testid: 'draft-attach-filter',
    oninput: () => {
      const q = filter.value.trim().toLowerCase()
      for (const it of items) it.el.hidden = !!q && !it.r.toLowerCase().includes(q)
    }
  })
  const el = h('div', { class: 'spop apop', role: 'dialog', 'aria-label': 'Also attach repositories', testid: 'draft-attach-pop' },
    h('div', { class: 'fl' }, 'Also attach'),
    others.length > 6 ? filter : null,
    list,
    h('p', { class: 'fh' }, 'The session’s branch is cut in each, in a worktree beside its own.'))
  openPop('attach', anchor, el, { alignLeft: true })
  if (others.length > 6) filter.focus()
}

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
