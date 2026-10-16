// modelpick.js — the model picker and the popovers it opens in. One picker
// for every place the page asks for an agent and a model: the composer's
// button beside Send (session.js), and the curator's and a schedule's form
// (pairField). The catalog is webapi.SessionModels: the agents this host
// can run, the models each says it provides merged with the ids the
// workspace's profiles run, and the pairs sessions on the board use.

import { h, clear, append, setVars } from './dom.js?v=__ASSET_V__'

let pop = null // the open popover: { kind, el, close }

// popKind is what the open popover is, '' for none: a button that opened
// one closes it on a second press.
export function popKind () { return pop?.kind || '' }

export function closePop () {
  if (!pop) return
  const p = pop
  pop = null
  p.close()
}

// openPop pins a popover to anchor: above it, its right edge to the
// anchor's (left, for one opened from the left of the composer) — below
// it instead where there is no room above and more below, as for a form
// field near the top of a view.
export function openPop (kind, anchor, el, { alignLeft = false } = {}) {
  closePop()
  // the composer redraws its buttons on every card and connection change:
  // one replaced while the form loaded is gone from the page, and a
  // popover placed against it would hang off the top of the window
  if (!anchor.isConnected && anchor.dataset.testid) anchor = document.querySelector(`[data-testid="${anchor.dataset.testid}"]`)
  if (!anchor) return
  document.body.append(el)
  // kept inside the window whichever edge it hangs from: on a phone the
  // picker is wider than the space left of Send
  const place = () => {
    const r = anchor.getBoundingClientRect()
    const w = el.offsetWidth
    const left = alignLeft ? r.left : r.right - w
    const below = r.top - 14 < el.offsetHeight && window.innerHeight - r.bottom > r.top
    setVars(el, {
      bottom: below ? null : Math.max(8, window.innerHeight - r.top + 6) + 'px',
      top: below ? r.bottom + 6 + 'px' : null,
      left: Math.max(8, Math.min(left, window.innerWidth - w - 8)) + 'px',
      right: null
    })
  }
  place()
  anchor.setAttribute('aria-expanded', 'true')
  const outside = (e) => { if (!el.contains(e.target) && !anchor.contains(e.target)) closePop() }
  // every key typed in a popover is the popover's: the page's shortcuts
  // (] hides the panel, [ the rail) would move the layout out from under it
  const keys = (e) => {
    e.stopPropagation()
    if (e.key === 'Escape') { closePop(); anchor.focus() }
  }
  // a resize here is usually the on-screen keyboard, raised by this same
  // popover's own search/budget input grabbing focus below — not the page
  // moving out from under it, so it must not close what focus just opened
  const moved = () => { if (!el.contains(document.activeElement)) closePop() }
  // a view scrolled under a popover pinned to the window would leave it
  // behind: it follows its anchor instead (closing would also take it from
  // under a phone's keyboard, which scrolls the page as it rises)
  const scrolled = (e) => { if (!el.contains(e.target)) place() }
  // added a tick later, so the click that opened it does not close it —
  // and only if it is still open then: a popover closed within that tick
  // would leave the listener behind, closing every later one on mousedown
  setTimeout(() => { if (pop?.el === el) document.addEventListener('mousedown', outside) }, 0)
  el.addEventListener('keydown', keys)
  window.addEventListener('resize', moved)
  document.addEventListener('scroll', scrolled, true)
  pop = {
    kind,
    el,
    close: () => {
      document.removeEventListener('mousedown', outside)
      document.removeEventListener('scroll', scrolled, true)
      window.removeEventListener('resize', moved)
      anchor.setAttribute('aria-expanded', 'false')
      el.remove()
    }
  }
}

// modelShownMax bounds how many of one agent's rows the picker draws
// before pointing at the rest: an agent that enumerates its catalog can
// offer hundreds of ids, and every one of them as a row is exactly what
// the search box was put there to prevent. The rows are the filtered
// list's head; the note names how many wait behind the next character
// typed.
const modelShownMax = 30

// openModelPicker opens the picker against anchor. current is the pair
// shown as picked; onPick gets the pair chosen. blank, when given, is a
// first row for "no pair at all" ({ label, sub }: the profile's default,
// say), picked as backend and model both ''. note is the footer's words.
export function openModelPicker (anchor, { catalog, current = {}, onPick, blank, note = '' }) {
  const cat = catalog || { agents: [], recent: [] }
  const search = h('input', { class: 'inp', testid: 'model-search', placeholder: 'Search, or type any model id', 'aria-label': 'Search models', autocomplete: 'off' })
  const list = h('div', { class: 'mlist', role: 'listbox', 'aria-label': 'Models' })
  const foot = h('div', { class: 'mfoot-s' })
  const el = h('div', { class: 'mpick', role: 'dialog', 'aria-label': 'Choose a model', testid: 'model-picker' },
    h('div', { class: 'msearch' }, search), list, foot)

  const pick = (backend, model) => { closePop(); onPick(backend, model) }
  const isCur = (backend, model) => backend === (current.backend || '') && model === (current.model || '')
  const rowEl = (backend, model, sub, testid, label) => h('button', {
    class: ['mrow', isCur(backend, model) && 'on'],
    type: 'button', role: 'option', testid, data: { model },
    'aria-selected': String(isCur(backend, model)),
    onclick: () => pick(backend, model)
  }, h('span', { class: 'mono m' }, label || model || 'default model'), h('span', { class: 'sub' }, sub))

  // enter takes what was typed at its word: the row that is exactly that
  // id, else the typed id itself — a suggestion that merely contains it
  // (claude-sonnet-4-6 for "sonnet-4") only once nothing else is left
  const enterRow = () => {
    const q = search.value.trim()
    const rows = [...list.querySelectorAll('.mrow')]
    if (!q) return rows[0]
    return rows.find(r => r.dataset.model === q) || list.querySelector('[data-testid^="model-typed-"]') || rows[0]
  }

  const draw = () => {
    const q = search.value.trim()
    const ql = q.toLowerCase()
    const hit = (s) => !ql || String(s).toLowerCase().includes(ql)
    clear(list)
    if (blank && (hit(blank.label) || hit(blank.sub || ''))) {
      list.append(rowEl('', '', blank.sub || '', 'model-blank', blank.label))
    }
    const recent = (cat.recent || []).filter(p => hit(p.model) || hit(p.backend))
    if (recent.length) {
      append(list, [h('div', { class: 'mgh' }, 'Recent'), recent.map(p => rowEl(p.backend, p.model, p.backend, `model-recent-${p.backend}-${p.model}`))])
    }
    const missing = []
    const installed = []
    let exact = false
    for (const a of cat.agents || []) {
      if (!a.installed) { missing.push(a.name); continue }
      installed.push(a)
      const models = [...(a.needsModel ? [] : ['']), ...(a.models || [])]
      if (q && models.includes(q)) exact = true
      const shown = models.filter(m => hit(m) || hit(a.name))
      if (!shown.length) continue
      const cap = shown.slice(0, modelShownMax)
      append(list, [h('div', { class: 'mgh' }, a.name, a.hint ? h('span', { class: 'hint' }, a.hint) : null),
        cap.map(m => rowEl(a.name, m, m ? '' : `${a.name} picks`, `model-${a.name}-${m || 'default'}`)),
        shown.length > cap.length
          ? h('div', { class: 'mgh more' }, `+${shown.length - cap.length} more — search to narrow`)
          : null])
    }
    // an id nobody suggested is still a model: offered once, on each agent
    // that could run it, below everything the workspace already uses
    const fits = installed.filter(a => { try { return !a.pattern || new RegExp(a.pattern, 'i').test(q) } catch { return true } })
    if (q && !exact && fits.length) {
      append(list, [h('div', { class: 'mgh' }, 'Use this id'),
        fits.map(a => rowEl(a.name, q, `on ${a.name}`, `model-typed-${a.name}`))])
    }
    if (!list.children.length) list.append(h('div', { class: 'mempty' }, 'No agent on this host can run a session.'))
    clear(foot)
    append(foot, [note,
      missing.length ? h('span', { class: 'missing' }, ` Not installed here: ${missing.join(', ')}.`) : null])
  }
  search.addEventListener('input', draw)
  search.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      enterRow()?.click()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      list.querySelector('.mrow')?.focus()
    }
  })
  list.addEventListener('keydown', (e) => {
    const rows = [...list.querySelectorAll('.mrow')]
    const i = rows.indexOf(document.activeElement)
    if (e.key === 'ArrowDown' && i >= 0) { e.preventDefault(); rows[Math.min(rows.length - 1, i + 1)].focus() }
    if (e.key === 'ArrowUp' && i >= 0) { e.preventDefault(); (i === 0 ? search : rows[i - 1]).focus() }
  })
  draw()
  openPop('model', anchor, el)
  search.focus()
}

// pairField is a form's agent-and-model control: one button showing the
// pair, opening the picker. value() is the pair picked ({ backend, model },
// '' for unset); onchange hears every pick. A pair the catalog does not
// name (a backend this host lacks) still shows, and stays until replaced.
export function pairField ({ catalog, value = {}, testid, blank, note, onchange }) {
  const cur = { backend: value.backend || '', model: value.model || '' }
  const m = h('span', { class: 'mono m' })
  const a = h('span', { class: 'mono a' })
  const btn = h('button', {
    class: 'mbtn pairf', type: 'button', testid,
    'aria-haspopup': 'dialog', 'aria-expanded': 'false',
    onclick: (e) => {
      // the field is a <label>: its click would land here a second time
      e.preventDefault()
      if (popKind() === 'model') { closePop(); return }
      openModelPicker(btn, {
        catalog,
        current: cur,
        blank,
        note,
        onPick: (backend, model) => {
          cur.backend = backend
          cur.model = model
          draw()
          onchange?.(cur)
          btn.focus()
        }
      })
    }
  }, m, a, h('span', { class: 'caret', 'aria-hidden': 'true' }, '▾'))
  function draw () {
    btn.dataset.backend = cur.backend
    btn.dataset.model = cur.model
    m.textContent = cur.backend || !blank ? (cur.model || 'default model') : blank.label
    a.textContent = cur.backend || (blank ? '' : 'pick an agent')
  }
  draw()
  return { el: btn, value: () => ({ ...cur }) }
}
