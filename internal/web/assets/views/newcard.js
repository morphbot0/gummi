// views/newcard.js — the new-card form: a session (the board's one
// kind; an agent is a session's "where"), with the choices GET /api/form
// offers — profiles, the default budget,
// repositories and their branches, the cards a new one can stack on.
// Submitting is POST /api/cards, which fills in and submits the
// TUI's own card form: its validation is the one that runs, and its
// refusal comes back as a sentence, shown beside the field it is about.

import { h, clear, dollarsInput, parseDollars, cardName } from '../dom.js?v=__ASSET_V__'
import { registerView } from '../views.js?v=__ASSET_V__'
import { hush } from '../toast.js?v=__ASSET_V__'

const MAC = /Mac|iPhone|iPad/.test(navigator.platform || '')

// What a session is, under the form's head.
const ABOUT = 'No stages and no gates: an agent works on a branch while you talk to it. It opens at once.'

// Which field a refusal is about, read from the sentence the form wrote.
const FIELD_OF = [
  [/title|first line/i, 'title'],
  [/description/i, 'desc'],
  [/budget|envelope|credits/i, 'envelope'],
  [/profile/i, 'profile'],
  [/repositor/i, 'repo'],
  [/stack/i, 'stack'],
  [/branch/i, 'base']
]

function fieldFor (msg) {
  for (const [re, f] of FIELD_OF) if (re.test(msg)) return f
  return ''
}

registerView('newcard', {
  title: 'New card',
  css: 'views/newcard.css',
  mount (body, ctx) {
    const seed = String(ctx.params?.text || '').trim()
    const [seedTitle, ...seedRest] = seed.split('\n')
    const st = {
      kind: 'freeform',
      form: null,
      repo: '',
      busy: false,
      // attachments are stored by reference, so the description always
      // offers the control — every stage gets at least the file's path.
      attachments: []
    }
    clear(body).append(h('div', { class: 'empty', testid: 'newcard-loading' }, h('span', { class: 'spinner' })))

    const el = {}
    const errs = {}

    const load = async (repo = '') => {
      try {
        st.form = await ctx.api.get('/api/form' + (repo ? `?repo=${encodeURIComponent(repo)}` : ''))
      } catch (err) {
        clear(body).append(h('div', { class: 'empty err', testid: 'newcard-error' }, h('b', null, 'The form did not load'), err.message))
        return false
      }
      return true
    }

    const errEl = (name) => (errs[name] = h('span', { class: 'ferr', testid: `newcard-error-${name}`, role: 'alert', hidden: true }))
    const field = (name, label, control, hint) => h('label', { class: 'field', data: { f: name } },
      h('span', { class: 'fl' }, label), control, hint ? h('span', { class: 'fh' }, hint) : null, errEl(name))

    const draw = () => {
      const f = st.form
      clear(body)
      el.title = h('input', { testid: 'newcard-title', placeholder: 'A short title', autocomplete: 'off', value: st.title ?? seedTitle ?? '' })
      el.desc = h('textarea', { testid: 'newcard-desc', placeholder: 'Say what you want done', rows: 5, value: st.desc ?? seedRest.join('\n').trim() })
      const chips = h('div', { class: 'chips', testid: 'newcard-chips', hidden: st.attachments.length === 0 })
      const renderChips = () => {
        clear(chips)
        chips.hidden = st.attachments.length === 0
        for (const a of st.attachments) {
          chips.append(h('span', { class: ['chip', a.error && 'err', a.pending && 'pending'] },
            a.pending ? 'Uploading…' : (a.error || a.name),
            h('button', { type: 'button', title: 'Remove', onclick: () => { st.attachments = st.attachments.filter((x) => x !== a); renderChips() } }, '×')))
        }
      }
      renderChips()
      const file = h('input', {
        type: 'file', testid: 'newcard-file', accept: 'image/png,image/jpeg,image/gif,image/webp', multiple: true, hidden: true,
        onchange: async (e) => {
          const files = [...e.target.files]
          e.target.value = ''
          for (const f of files) {
            const chip = { id: null, name: f.name || 'image', pending: true, error: null }
            st.attachments.push(chip)
            renderChips()
            try {
              Object.assign(chip, await ctx.api.uploadAttachment(f), { pending: false })
            } catch (err) {
              chip.pending = false
              chip.error = (err.data && err.data.error) || err.message || 'upload failed'
            }
            renderChips()
          }
        }
      })
      const attach = h('button', { class: 'attach', type: 'button', testid: 'newcard-attach', title: 'Attach an image', onclick: () => file.click() }, '📎')
      const main = [
        h('div', { class: 'nc-kindrow' }, h('p', { class: 'nc-about', testid: 'newcard-about' }, ABOUT)),
        field('title', 'Title', el.title),
        field('desc', 'What to do', el.desc),
        h('div', { class: 'nc-attach' }, attach, file, chips)
      ]

      const side = []
      el.profile = h('select', { testid: 'newcard-profile' }, f.profiles.map(p => h('option', { value: p, selected: p === (st.profile || f.profiles[0]) }, p)))
      side.push(field('profile', 'Profile', el.profile, 'Which models play each role.'))
      el.envelope = h('input', { testid: 'newcard-envelope', type: 'text', inputmode: 'decimal', value: st.envelope ?? (f.envelope ? dollarsInput(f.envelope) : '') })
      side.push(field('envelope', 'Budget', el.envelope, 'Dollars this card may spend. 0 is uncapped.'))
      if (f.repos?.length) {
        el.repo = h('select', { testid: 'newcard-repo', onchange: async () => { keep(); st.repo = el.repo.value; if (await load(st.repo)) draw() } },
          f.repos.map(r => h('option', { value: r, selected: r === st.repo }, r)))
        side.push(field('repo', 'Repository', el.repo))
      }
      const branches = f.branches || []
      el.base = h('select', { testid: 'newcard-base' }, h('option', { value: '' }, 'the default branch'), branches.map(b => h('option', { value: b, selected: b === st.base }, b)))
      const baseHint = h('span', { class: 'fh', testid: 'newcard-base-hint' })
      side.push(h('label', { class: 'field', data: { f: 'base' } }, h('span', { class: 'fl' }, 'Base branch'), el.base, baseHint, errEl('base')))
      if (f.stackable?.length) {
        el.stack = h('select', { testid: 'newcard-stack' }, h('option', { value: '' }, 'no stack'), f.stackable.map(c => h('option', { value: c.id, selected: c.id === st.stack }, `${c.title}`)))
        side.push(field('stack', 'Stack onto', el.stack, 'Fork from that card’s branch. It never waits for it.'))
      }
      // a stacked card forks from the card below it, whatever base is
      // picked (cardmint ignores it there): the base says so and steps
      // aside rather than being dropped without a word
      const syncBase = () => {
        const stacked = !!el.stack?.value
        el.base.disabled = stacked
        baseHint.textContent = stacked
          ? `Set by the stack: it forks from ${el.stack.value}’s branch and lands with it.`
          : 'What the branch forks from and lands on.'
      }
      el.stack?.addEventListener('change', syncBase)
      syncBase()

      const err = h('p', { class: 'aerr', testid: 'newcard-error', role: 'alert', hidden: true })
      errs._ = err
      const create = h('button', { class: 'btn pri', type: 'submit', testid: 'newcard-create' }, 'Create and open')
      const form = h('form', { class: 'nc', testid: 'newcard-form', novalidate: true, onsubmit: (e) => { e.preventDefault(); submit() } },
        h('div', { class: 'nc-grid' }, h('div', { class: 'nc-main' }, main), h('div', { class: 'nc-side' }, side)),
        h('div', { class: 'nc-foot' }, err, h('span', { class: 'nc-keys' }, h('kbd', null, MAC ? '⌘' : 'Ctrl'), h('kbd', null, 'enter'), ' creates'),
          h('button', { class: 'btn', type: 'button', testid: 'newcard-cancel', onclick: () => ctx.close() }, 'Cancel'), create))
      form.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submit() } })
      // a refusal is about what was sent: editing the field clears it
      const clearField = (e) => {
        const f = e.target.closest?.('.field')
        if (!f || !f.classList.contains('bad')) return
        f.classList.remove('bad')
        const fe = f.querySelector('.ferr')
        if (fe) fe.hidden = true
      }
      form.addEventListener('input', clearField)
      form.addEventListener('change', clearField)
      el.create = create
      body.append(form)
      el.title.focus()
    }

    // keep holds what was typed across a redraw (a repository change
    // redraws the form).
    const keep = () => {
      if (!el.title) return
      st.title = el.title.value
      st.desc = el.desc.value
      st.profile = el.profile?.value
      st.envelope = el.envelope?.value
      st.base = el.base?.disabled ? st.base : el.base?.value
      st.stack = el.stack?.value
    }

    const showErr = (msg) => {
      for (const e of Object.values(errs)) { e.hidden = true; e.closest('.field')?.classList.remove('bad') }
      if (!msg) return
      const name = fieldFor(msg)
      const target = (name && errs[name]) || errs._
      // said here, beside its field: the server's broadcast of the same
      // refusal would stand over the form as a toast as well
      hush(msg)
      clear(target).append(sentence(msg, st.form?.branches || []))
      target.hidden = false
      target.closest('.field')?.classList.add('bad')
      target.closest('.field')?.querySelector('input,textarea,select')?.focus()
    }

    // sentence capitalises a refusal's first word — unless that word is a
    // name (a branch: "main is the branch…", "free-1 has no commits…"),
    // which is shown as it is spelled
    const sentence = (msg, names) => {
      const first = msg.split(/\s/)[0]
      if (!/^[a-z]+$/.test(first) || names.includes(first)) return msg
      return msg[0].toUpperCase() + msg.slice(1)
    }

    const submit = async () => {
      if (st.busy) return
      if (st.attachments.some((a) => a.pending)) { showErr('still uploading an image — wait a moment and try again'); return }
      if (st.attachments.some((a) => a.error)) { showErr('remove the failed attachment before creating'); return }
      keep()
      // typed in dollars, sent in credits
      const env = el.envelope.value.trim() === '' ? null : parseDollars(el.envelope.value)
      if (env?.err) { showErr(env.err); return }
      const req = {
        kind: st.kind,
        title: el.title.value.trim(),
        description: el.desc.value.trim() || undefined,
        profile: el.profile.value || undefined,
        envelope: env ? env.credits : undefined,
        repo: st.repo || undefined,
        base: (!el.base.disabled && el.base.value) || undefined,
        stackOn: el.stack?.value || undefined,
        attachments: st.attachments.length ? st.attachments.map((a) => a.id) : undefined
      }
      showErr('')
      st.busy = true
      el.create.disabled = true
      try {
        const card = await ctx.api.post('/api/cards', req)
        ctx.close()
        ctx.toast(`Created ${cardName(card.id)}`)
        await ctx.refreshBoard?.()
        ctx.select(card.id)
      } catch (err) {
        showErr(err.data?.text || err.message)
      } finally {
        st.busy = false
        if (el.create.isConnected) el.create.disabled = false
      }
    }

    load().then(ok => { if (ok) draw() })
  }
})
