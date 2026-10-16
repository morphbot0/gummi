// views/skills.js — the workspace skill pack and its curator (DESIGN
// §23). The curator writes skills with no person before it; a person
// acts afterwards (§23.6): pins a skill the curator would otherwise
// rewrite or archive, reverts a run that changed the pack, or asks for a
// run now. Everything shown is the board's one read (GET /api/curator);
// the page derives nothing — which skill may be pinned, which run may be
// reverted and each run's line all come from the server.

import { h, clear, plural, cr, clock, dollarsInput, parseDollars } from '../dom.js?v=__ASSET_V__'
import { registerView } from '../views.js?v=__ASSET_V__'
import { errorBox, confirmStrip, refetcher, field } from './kit.js?v=__ASSET_V__'
import { pairField } from '../modelpick.js?v=__ASSET_V__'

function errText (err) {
  return err?.data?.text || err?.message || String(err)
}

// when shows the next run as a phrase; "in 5h" is what a person reads.
function when (iso) {
  if (!iso) return null
  const ms = new Date(iso) - Date.now()
  if (ms <= 0) return 'due now'
  const m = Math.round(ms / 60000)
  if (m < 1) return 'in under a minute'
  if (m < 60) return `in ${m}m`
  const hr = Math.round(m / 60)
  if (hr < 24) return `in ${hr}h`
  return `in ${Math.round(hr / 24)}d`
}

const OWNER = { curator: 'curator', person: 'person', pinned: 'curator, pinned' }

const OUTCOME = { done: 'done', failed: 'failed', exhausted: 'ran out of budget', paused: 'paused' }

function outcomeClass (o) {
  if (o === 'done') return 't-ok'
  if (o === 'failed') return 't-err'
  return 't-warn'
}

registerView('skills', {
  title: 'Skills',
  css: 'views/skills.css',
  mount (body, ctx) {
    // form is the curator's settings form while it is open: built once from
    // the read it opened on, so a board event's redraw keeps what is typed.
    const v = { data: null, err: null, busy: false, asking: null, notice: null, noticeErr: false, form: null, catalog: null }
    body.classList.add('vskills')

    const head = h('div', { class: 'sk-head' })
    const runs = h('div', { class: 'sk-sect' })
    const skills = h('div', { class: 'sk-sect' })
    const staged = h('div', { class: 'sk-sect' })
    body.append(head,
      h('p', { class: 'vnote' },
        'The curator writes skills every session starts with; nothing waits on a person. Pin a curator skill to keep it as it is, or revert a run that changed the pack — the next session starts without it.'),
      skills, staged, runs)

    const load = refetcher(async () => {
      try {
        v.data = await ctx.api.get('/api/curator')
        v.err = null
      } catch (err) {
        if (!v.data) v.err = err
      }
      draw()
    })

    // act runs one write; its answer (or the board's refusal) is the line
    // the view shows, and the read is fetched again either way.
    async function act (path) {
      v.busy = true
      v.asking = null
      draw()
      try {
        const res = await ctx.api.post(path, {})
        v.notice = res?.text || 'done'
        v.noticeErr = false
      } catch (err) {
        v.notice = errText(err)
        v.noticeErr = true
      }
      v.busy = false
      await load()
    }

    function draw () {
      clear(head)
      clear(skills)
      clear(staged)
      clear(runs)
      if (v.err) { head.append(errorBox(v.err, 'skills-error')); return }
      const d = v.data
      if (!d) { head.append(h('div', { class: 'vbusy' }, h('span', { class: 'spinner' }), 'Reading the skill pack…')); return }
      drawHead(d)
      drawSkills(d)
      drawStaged(d)
      drawRuns(d)
    }

    function drawHead (d) {
      const c = d.config
      const cfg = c
        ? h('div', { class: 'sk-config', testid: 'skills-config' },
            h('b', null, 'Curator'), ' ',
            `${c.backend} on ${c.model || 'its default model'} · ${c.schedule} · ${cr(c.budget)} a run`)
        : h('div', { class: 'sk-config', testid: 'skills-config' },
            h('b', null, 'Curator off'), ' — set it up to have sessions’ lessons distilled into the pack. The pack still reaches sessions, and pin and revert still work.')
      const facts = h('div', { class: 'sk-facts' })
      if (c) {
        facts.append(h('span', { testid: 'skills-spend' },
          c.monthly ? `${cr(d.monthSpent)} of ${cr(c.monthly)} spent this month` : `${cr(d.monthSpent)} spent this month · no monthly cap`))
        if (d.running) facts.append(h('span', { class: 'sk-running', testid: 'skills-running' }, 'running now'))
        else if (d.next) facts.append(h('span', { class: 'sk-next', testid: 'skills-next' }, `next run ${when(d.next)}`))
      }
      facts.append(h('span', { class: 'mute', testid: 'skills-published' },
        d.published ? `published ${d.published.slice(0, 12)}` : 'pack not made yet'))
      const runBtn = h('button', {
        class: 'btn', type: 'button', testid: 'skills-run',
        disabled: v.busy || !c || d.running,
        title: !c ? 'No curator is configured' : d.running ? 'A curator run is already going' : 'Run the curator at the board’s next tick',
        onclick: () => { v.asking = { what: 'run' }; draw() }
      }, 'Run now')
      const setBtn = h('button', {
        class: 'btn', type: 'button', testid: 'skills-configure',
        disabled: v.busy || !!v.form,
        onclick: () => openForm(c)
      }, c ? 'Settings' : 'Set up')
      head.append(h('div', { class: 'sk-bar' }, h('div', { class: 'grow' }, cfg, facts), h('div', { class: 'sk-btns' }, setBtn, runBtn)))
      if (v.form) head.append(v.form)
      if (v.asking?.what === 'off') {
        head.append(confirmStrip({
          question: 'Turn the skill curator off?',
          detail: 'Nothing distils sessions into the pack or spends on it until it is set up again. The pack and its skills stay.',
          yes: 'Turn off',
          danger: true,
          testid: 'skills-off-confirm',
          onYes: () => saveConfig(null),
          onNo: () => { v.asking = null; draw() }
        }))
      }
      if (v.asking?.what === 'run') {
        head.append(confirmStrip({
          question: 'Run the skill curator now?',
          detail: `It starts at the board’s next tick and may spend up to ${cr(c?.budget)} of this month’s budget.`,
          yes: 'Run',
          testid: 'skills-run-confirm',
          onYes: () => act('/api/curator/run'),
          onNo: () => { v.asking = null; draw() }
        }))
      }
      if (v.notice) {
        head.append(h('div', { class: ['sk-notice', v.noticeErr && 'err'], role: 'status', testid: 'skills-notice' }, v.notice))
      }
    }

    // openForm fetches the agent catalog the schedule form uses (every agent
    // a session can run on and the models it names) before the first draw.
    async function openForm (c) {
      if (!v.catalog) {
        try { v.catalog = await ctx.api.get('/api/schedules/catalog') } catch (err) { v.catalog = { agents: [] } }
      }
      v.asking = null
      v.notice = null
      v.form = curatorForm(c)
      draw()
      v.form.querySelector('button, input')?.focus()
    }

    function closeForm () { v.form = null; draw() }

    // saveConfig writes the curator block (null: none). A refusal stays in
    // the form; the board's answer is the read the view draws next.
    async function saveConfig (body, errBox) {
      v.busy = true
      v.asking = null
      try {
        v.data = body ? await ctx.api.api('PUT', '/api/curator/config', body) : await ctx.api.api('DELETE', '/api/curator/config')
        v.notice = body ? 'curator settings saved — the next run uses them' : 'curator turned off'
        v.noticeErr = false
        v.form = null
      } catch (err) {
        if (errBox) { clear(errBox); errBox.append(errorBox(err, 'skills-form-error-detail')) } else { v.notice = errText(err); v.noticeErr = true }
      }
      v.busy = false
      draw()
    }

    function curatorForm (c) {
      // the composer's own picker: any installed agent, on its default model
      // or any it names; a fresh form starts on the first installed agent
      const first = (v.catalog?.agents || []).find(a => a.installed)?.name || ''
      const pair = pairField({
        catalog: v.catalog,
        value: c ? { backend: c.backend, model: c.model } : { backend: first },
        testid: 'skills-form-pair',
        note: 'The curator runs on this agent and model. It needs an agent that reaches gummi’s tools.'
      })
      const sched = h('input', { type: 'text', testid: 'skills-form-schedule', placeholder: '@daily', value: c?.schedule || '' })
      const budget = h('input', { type: 'text', inputmode: 'decimal', testid: 'skills-form-budget', value: dollarsInput(c?.budget || 100) })
      const monthly = h('input', { type: 'text', inputmode: 'decimal', testid: 'skills-form-monthly', placeholder: 'no cap', value: c?.monthly ? dollarsInput(c.monthly) : '' })
      const err = h('div', { role: 'alert', testid: 'skills-form-error' })

      return h('form', {
        class: 'skc-form',
        testid: 'skills-form',
        onsubmit: (ev) => {
          ev.preventDefault()
          clear(err)
          // A field left as it was shown keeps its exact credits: a
          // budget written by hand may be finer than a cent, and saving
          // the form unchanged must not be refused for it.
          const keep = (input, credits) => credits && input.value === dollarsInput(credits) ? { credits } : null
          const b = keep(budget, c?.budget) || parseDollars(budget.value)
          if (b.err) { err.append(errorBox(b.err, 'skills-form-error-detail')); return }
          const m = monthly.value.trim() === '' ? { credits: 0 } : keep(monthly, c?.monthly) || parseDollars(monthly.value)
          if (m.err) { err.append(errorBox(m.err, 'skills-form-error-detail')); return }
          saveConfig({
            backend: pair.value().backend,
            model: pair.value().model || undefined,
            schedule: sched.value.trim() || undefined,
            budget: b.credits,
            monthly: m.credits || undefined
          }, err)
        }
      },
      h('div', { class: 'vrow' },
        field('Agent and model', pair.el, { hint: 'the curator needs an agent that reaches gummi’s tools' })),
      h('div', { class: 'vrow' },
        field('Schedule', sched, { hint: '@daily, @weekly, 6h, or a cron line' }),
        field('Budget per run (dollars)', budget, { hint: 'what one run may spend' }),
        field('Monthly cap (dollars)', monthly, { hint: 'across every run; empty for none' })),
      err,
      h('div', { class: 'skc-foot' },
        c ? h('button', { class: 'btn danger', type: 'button', testid: 'skills-form-off', onclick: () => { v.form = null; v.asking = { what: 'off' }; draw() } }, 'Turn off') : null,
        h('span', { class: 'grow' }),
        h('button', { class: 'btn', type: 'button', testid: 'skills-form-cancel', onclick: closeForm }, 'Cancel'),
        h('button', { class: 'btn pri', type: 'submit', testid: 'skills-form-save' }, c ? 'Save' : 'Turn on')))
    }

    function drawSkills (d) {
      const rows = d.skills || []
      skills.append(h('h3', { class: 'shead' }, 'Skills', h('span', { testid: 'skills-count' }, plural(rows.length, 'skill'))))
      if (!rows.length) {
        skills.append(h('div', { class: 'empty', testid: 'skills-empty' },
          h('b', null, 'No skills yet'),
          'The curator writes one when sessions keep paying for the same lesson; a person may commit one to the pack too.'))
        return
      }
      skills.append(h('div', { class: 'tablewrap' },
        h('table', { class: 'passes sk-table', testid: 'skills-table' },
          h('thead', null, h('tr', null,
            h('th', null, 'Skill'), h('th', null, 'Scope'), h('th', null, 'Owner'),
            h('th', { class: 'num' }, 'Uses'), h('th', null, 'Standing'), h('th', null, ''))),
          h('tbody', null, rows.map(s => skillRow(s))))))
    }

    function skillRow (s) {
      const standing = s.due ? 'archived at the next sweep' : s.stale ? 'stale' : s.lastUsed ? `used ${clock(s.lastUsed)}` : ''
      const pin = s.pinnable
        ? h('button', {
            class: 'btn', type: 'button', testid: `skill-${s.name}-${s.pinned ? 'unpin' : 'pin'}`, disabled: v.busy,
            title: s.pinned ? 'Let the curator rewrite or archive it again' : 'Keep the curator and the timers off it',
            onclick: () => act(`/api/curator/skills/${encodeURIComponent(s.name)}/${s.pinned ? 'unpin' : 'pin'}`)
          }, s.pinned ? 'Unpin' : 'Pin')
        : null
      return h('tr', { testid: `skill-${s.name}`, data: { owner: s.owner } },
        h('td', { class: 'sk-name' }, h('b', null, s.name), s.description ? h('div', { class: 'sk-desc' }, s.description) : null),
        h('td', null, s.scope),
        h('td', { testid: `skill-${s.name}-owner` }, OWNER[s.owner] || s.owner),
        h('td', { class: 'num', title: `invoked ${s.uses || 0}, read ${s.views || 0}, edited ${s.edits || 0} times` }, String((s.uses || 0) + (s.views || 0))),
        h('td', { class: [s.due || s.stale ? 'sk-stale' : 'mute'], testid: `skill-${s.name}-standing` }, standing),
        h('td', { class: 'sk-act' }, pin))
    }

    // drawStaged lists what card sessions staged for the curator (§23.5):
    // only that card's sessions see a pending change until the curator
    // reviews it; a rejected one stays with the curator's reason.
    function drawStaged (d) {
      const rows = d.staged || []
      const pending = rows.filter(c => c.status === 'pending')
      const rejected = rows.filter(c => c.status === 'rejected')
      staged.append(h('h3', { class: 'shead' }, 'Staged by sessions',
        h('span', { testid: 'staged-count' }, `${pending.length} pending · ${rejected.length} rejected`)))
      if (!rows.length) {
        staged.append(h('div', { class: 'empty', testid: 'staged-empty' },
          h('b', null, 'Nothing staged'),
          'A session stages a skill it wrote or one it would archive; only its own card sees it until the curator reviews it.'))
        return
      }
      const list = h('ul', { class: 'sk-staged', testid: 'staged' })
      for (const c of [...pending, ...rejected]) {
        list.append(h('li', { testid: `staged-${c.card}-${c.name}`, data: { status: c.status } },
          h('div', { class: 'sk-staged-head' },
            h('span', { class: 'sk-staged-op' }, c.op === 'archive' ? 'archive' : 'write'),
            h('b', { class: 'sk-staged-name' }, c.name),
            h('span', { class: 'mute', testid: `staged-${c.card}-${c.name}-card` }, `from ${c.cardName || c.card}`),
            c.at ? h('span', { class: 'mute' }, clock(c.at)) : null,
            h('span', { class: 'grow' }),
            c.status === 'rejected'
              ? h('span', { class: 't-err' }, 'rejected')
              : h('span', { class: 't-warn' }, 'waiting for the curator')),
          c.why ? h('div', { class: 'sk-staged-why' }, c.why) : null,
          c.status === 'rejected' && c.verdict
            ? h('div', { class: 'sk-staged-verdict', testid: `staged-${c.card}-${c.name}-verdict` }, `the curator: ${c.verdict}`)
            : null))
      }
      staged.append(list)
    }

    // skillOf is a run's reviewed change, card/name, as its skill's name.
    const skillOf = (ref) => ref.slice(ref.indexOf('/') + 1)

    function drawRuns (d) {
      const rows = d.runs || []
      runs.append(h('h3', { class: 'shead' }, 'Curator runs', h('span', null, plural(rows.length, 'run'))))
      if (!rows.length) {
        runs.append(h('div', { class: 'empty', testid: 'curator-runs-empty' }, h('b', null, 'No curator runs yet')))
        return
      }
      const list = h('ul', { class: 'sk-runs', testid: 'curator-runs' })
      for (const r of rows) {
        const li = h('li', { testid: `curator-run-${r.id}`, data: { outcome: r.outcome } },
          h('div', { class: 'sk-run-head' },
            h('span', { class: 'sk-when' }, clock(r.start)),
            h('span', { class: ['sk-outcome', outcomeClass(r.outcome)] }, OUTCOME[r.outcome] || r.outcome),
            h('span', { class: 'mute' }, r.why === 'person' ? 'asked by a person' : 'on schedule'),
            h('span', { class: 'mute' }, cr(r.credits)),
            h('span', { class: 'grow' }),
            r.revertable
              ? h('button', {
                  class: 'btn', type: 'button', testid: `curator-run-${r.id}-revert`, disabled: v.busy,
                  onclick: () => { v.asking = { what: 'revert', id: r.id }; draw() }
                }, 'Revert')
              : r.reverted ? h('span', { class: 'mute', testid: `curator-run-${r.id}-reverted` }, 'reverted') : null),
          h('div', { class: 'sk-digest', testid: `curator-run-${r.id}-digest` }, r.digest),
          r.accepted?.length || r.rejected?.length
            ? h('div', { class: 'sk-reviewed mute', testid: `curator-run-${r.id}-reviewed` },
                [r.accepted?.length && `took ${r.accepted.map(skillOf).join(', ')}`,
                  r.rejected?.length && `rejected ${r.rejected.map(skillOf).join(', ')}`].filter(Boolean).join(' · '))
            : null)
        if (v.asking?.what === 'revert' && v.asking.id === r.id) {
          li.append(confirmStrip({
            question: 'Revert this curator run?',
            detail: 'One commit undoes everything it wrote and archived, and the next session starts without it.',
            yes: 'Revert',
            danger: true,
            testid: `curator-run-${r.id}-confirm`,
            onYes: () => act(`/api/curator/runs/${encodeURIComponent(r.id)}/revert`),
            onNo: () => { v.asking = null; draw() }
          }))
        }
        list.append(li)
      }
      runs.append(list)
    }

    draw()
    load()
    const off = ctx.onEvent('board', load)
    return () => { off() }
  }
})
