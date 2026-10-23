// views/profiles.js — the board's profiles (.gummi/profiles.yaml): which
// agent and model each role runs on. A person adds, edits, deletes and
// picks the default profile here; the terminal's profiles view (P) writes
// the same file through the same checks. A file the board refuses is
// still listed, beside why, so the profile that breaks it can be fixed or
// deleted without opening an editor on the host.

import { h, clear, plural } from '../dom.js?v=__ASSET_V__'
import { registerView } from '../views.js?v=__ASSET_V__'
import { field, choose, errorBox, confirmStrip } from './kit.js?v=__ASSET_V__'
import { pairField } from '../modelpick.js?v=__ASSET_V__'

const SANDBOXES = [
  { value: '', label: 'workspace default' },
  { value: 'enforce', label: 'enforce' },
  { value: 'warn', label: 'warn' },
  { value: 'off', label: 'off' }
]

// pairText is a role as one line: its model, on its backend.
function pairText (r, def) {
  return `${r.model} · ${r.backend || `${def || 'default agent'} (default)`}`
}

registerView('profiles', {
  title: 'Profiles',
  css: 'views/profiles.css',
  mount (body, ctx) {
    const v = { data: null, err: null, notice: null, editing: undefined, formEl: null, asking: null, catalog: null, busy: false, alive: true }
    body.classList.add('prf')

    const bar = h('div', { class: 'prf-bar' })
    const formSlot = h('div')
    const list = h('div', { class: 'prf-list', testid: 'profiles-list' })
    body.append(bar,
      h('p', { class: 'prf-lede' },
        'A profile maps each role gummi runs to an agent and a model. A card uses its own profile, or the default; an edit reaches the next session started, never one already running.'),
      formSlot, list)

    async function load () {
      try { v.data = await ctx.api.get('/api/profiles'); v.err = null } catch (err) { v.err = err }
      if (v.alive) draw()
    }

    // act runs one edit; its answer is the profiles as they now read. A
    // refusal is handed to onErr when given (the form shows its own),
    // else shown above the list.
    async function act (method, path, req, said, onErr) {
      v.busy = true
      v.notice = null
      try {
        v.data = await ctx.api.api(method, path, req)
        v.err = null
        v.asking = null
        ctx.toast?.(said)
        ctx.refreshBoard?.()
        return true
      } catch (err) {
        if (onErr) onErr(err); else v.notice = err
        return false
      } finally {
        v.busy = false
        if (v.alive) draw()
      }
    }

    async function openForm (p) {
      v.editing = p
      v.formEl = null
      v.asking = null
      if (!v.catalog) {
        try { v.catalog = await ctx.api.get('/api/schedules/catalog') } catch {
          // the form still opens; a pair it cannot offer is typed instead
          v.catalog = { agents: [], recent: [] }
        }
      }
      draw()
    }

    function closeForm () { v.editing = undefined; v.formEl = null; draw() }

    function drawBar () {
      clear(bar)
      const n = v.data?.profiles?.length || 0
      bar.append(
        h('span', { class: 'prf-count', testid: 'profiles-count' }, plural(n, 'profile')),
        h('button', { class: 'btn pri', type: 'button', testid: 'profiles-new', disabled: v.busy, onclick: () => openForm(null) }, '+ New profile'))
    }

    function profileCard (p) {
      const d = v.data
      const isDef = p.name === d.default
      const slotOf = new Map(d.slots.map(s => [s.role, s]))
      const asking = v.asking === p.name
      return h('section', { class: ['prf-card', isDef && 'def'], testid: `profile-${p.name}` },
        h('div', { class: 'prf-head' },
          h('b', { class: 'prf-name' }, p.name),
          isDef ? h('span', { class: 'prf-badge', testid: `profile-${p.name}-default` }, 'default') : null,
          p.sandbox ? h('span', { class: 'prf-meta' }, `sandbox ${p.sandbox}`) : null,
          h('span', { class: 'grow' }),
          h('button', { class: 'btn', type: 'button', testid: `profile-${p.name}-edit`, disabled: v.busy, onclick: () => openForm(p) }, 'Edit'),
          isDef ? null : h('button', { class: 'btn', type: 'button', testid: `profile-${p.name}-make-default`, disabled: v.busy, onclick: () => act('POST', `/api/profiles/${encodeURIComponent(p.name)}/default`, {}, `${p.name} is the default profile`) }, 'Make default'),
          h('button', { class: 'btn', type: 'button', testid: `profile-${p.name}-delete`, disabled: v.busy, onclick: () => { v.asking = p.name; draw() } }, 'Delete')),
        asking
          ? confirmStrip({
            question: `Delete ${p.name}?`,
            detail: isDef ? 'It is the default: make another the default first, unless it is the last profile.' : `A card that uses ${p.name} runs on the default profile from its next session.`,
            yes: 'Delete',
            danger: true,
            testid: `profile-${p.name}-confirm`,
            onYes: () => act('DELETE', `/api/profiles/${encodeURIComponent(p.name)}`, undefined, `${p.name} deleted`),
            onNo: () => { v.asking = null; draw() }
          })
          : null,
        h('dl', { class: 'prf-roles' },
          p.roles.map(r => [
            h('dt', { title: slotOf.get(r.role)?.help || 'a role gummi does not read' }, r.role, slotOf.has(r.role) ? null : h('span', { class: 'prf-unread' }, ' unread')),
            h('dd', { class: 'mono' }, pairText(r, d.defaultBackend))
          ])))
    }

    function profileForm (p) {
      const d = v.data
      const editing = !!p
      const err = h('div', { role: 'alert', class: 'prf-form-error', testid: 'profile-form-error' })
      const name = h('input', { type: 'text', testid: 'profile-form-name', value: p?.name || '', placeholder: 'e.g. thrifty', autocomplete: 'off', spellcheck: 'false', readonly: editing })
      const sandbox = choose(SANDBOXES, { value: p?.sandbox || '', testid: 'profile-form-sandbox', label: 'Sandbox' })
      const mkDef = h('input', { type: 'checkbox', testid: 'profile-form-default', checked: !editing && !(d.profiles || []).length })
      // a pick always names an agent and a model: a role runs on one
      const catalog = { ...v.catalog, agents: (v.catalog.agents || []).map(a => ({ ...a, needsModel: true })) }
      const have = new Map((p?.roles || []).map(r => [r.role, r]))
      const roles = [...d.slots.map(s => s.role), ...[...have.keys()].filter(r => !d.slots.some(s => s.role === r))]
      const pairs = roles.map(role => {
        const was = have.get(role)
        // a role written with no backend follows the default agent; it
        // keeps doing so unless the person picks something else for it
        const shown = was ? { backend: was.backend || d.defaultBackend || '', model: was.model } : {}
        const pair = pairField({
          catalog,
          value: shown,
          testid: `profile-form-${role}`,
          blank: { label: 'not set', sub: 'falls back as the slot describes' },
          note: d.slots.find(s => s.role === role)?.help || ''
        })
        const value = () => {
          const cur = pair.value()
          if (!cur.backend && !cur.model) return null
          if (was && !was.backend && cur.backend === shown.backend && cur.model === shown.model) return { role, model: was.model, outputTokenMax: was.outputTokenMax }
          return { role, backend: cur.backend, model: cur.model, outputTokenMax: was?.outputTokenMax }
        }
        const help = d.slots.find(s => s.role === role)?.help || 'a role gummi does not read'
        return { el: h('div', { class: 'prf-slot' }, h('div', { class: 'prf-slot-l' }, h('b', { class: 'mono' }, role), h('small', null, help)), pair.el), value }
      })
      return h('form', {
        class: 'prf-form',
        testid: 'profile-form',
        onsubmit: async (e) => {
          e.preventDefault()
          clear(err)
          const n = name.value.trim()
          const req = { roles: pairs.map(x => x.value()).filter(Boolean), sandbox: sandbox.value, makeDefault: mkDef.checked }
          if (!n) { err.append(errorBox('Name the profile.', 'profile-form-error-detail')); return }
          if (!req.roles.length) { err.append(errorBox('Set at least one role.', 'profile-form-error-detail')); return }
          if (!editing && (d.profiles || []).some(x => x.name === n)) { err.append(errorBox(`There is a profile ${n} already: edit it instead.`, 'profile-form-error-detail')); return }
          const ok = await act('PUT', `/api/profiles/${encodeURIComponent(n)}`, req, editing ? `${n} saved` : `${n} added`,
            (e) => err.append(errorBox(e, 'profile-form-error-detail')))
          if (ok) closeForm()
        }
      },
      h('div', { class: 'prf-form-row' },
        field('Name', name, { hint: editing ? 'a profile is renamed by adding it under the new name and deleting this one' : 'letters, digits, . _ and -' }),
        field('Sandbox', sandbox)),
      pairs.map(x => x.el),
      h('label', { class: 'prf-check' }, mkDef, h('span', null, 'Make this the default profile')),
      err,
      h('div', { class: 'prf-form-foot' },
        h('button', { type: 'button', class: 'btn', testid: 'profile-form-cancel', onclick: closeForm }, 'Cancel'),
        h('button', { type: 'submit', class: 'btn pri', testid: 'profile-form-save', disabled: v.busy }, editing ? 'Save' : 'Add')))
    }

    function draw () {
      drawBar()
      clear(formSlot)
      clear(list)
      if (v.err) { list.append(errorBox(v.err)); return }
      const d = v.data
      if (!d) return
      if (d.problem) list.append(h('div', { class: 'prf-problem', role: 'alert', testid: 'profiles-problem' }, h('b', null, 'This file is not in force. '), d.problem))
      if (v.notice) list.append(errorBox(v.notice, 'profiles-notice'))
      // the form outlives a redraw: a refused save keeps what was typed
      if (v.editing !== undefined) formSlot.append(v.formEl ||= profileForm(v.editing))
      if (!d.profiles.length) {
        list.append(h('div', { class: 'empty', testid: 'profiles-empty' },
          h('b', null, 'No profiles'),
          `Every role runs on ${d.defaultBackend || 'the default agent'}. Add a profile to choose a model per role.`))
      }
      for (const p of d.profiles) list.append(profileCard(p))
      list.append(h('p', { class: 'prf-path mono', testid: 'profiles-path' }, d.path))
    }

    load()
    return () => { v.alive = false }
  }
})
