// views/firstrun.js — the first-run setup: the start that seeded
// .gummi/profiles.yaml offers one short wizard (an agent, two models, a
// confirm) that rewrites the file to a single default profile. It decides
// nothing: every agent, hint, preselection and role comes from
// GET /api/firstrun, and the answer is posted back to be checked there.
// Closing it any other way posts a skip, which keeps the starter file.

import { h, clear } from '../dom.js?v=__ASSET_V__'
import { registerView } from '../views.js?v=__ASSET_V__'
import { choose, errorBox } from './kit.js?v=__ASSET_V__'

registerView('firstrun', {
  title: 'Set up gummi',
  css: 'views/firstrun.css',
  wide: false,
  mount (body, ctx) {
    const wiz = ctx.params.wizard
    const usable = wiz.agents.filter(a => a.status === 'usable')
    const v = {
      screen: wiz.askAgent ? 'agent' : 'models',
      agent: wiz.agent,
      strong: null,
      everyday: null,
      err: null,
      busy: false,
      done: false
    }
    body.classList.add('frs')
    const cur = () => wiz.agents.find(a => a.name === v.agent)
    function pick () { const a = cur(); v.strong = a?.strong || null; v.everyday = a?.everyday || null }
    pick()

    const steps = wiz.askAgent ? 3 : 2
    const stepNo = () => ({ agent: 1, models: wiz.askAgent ? 2 : 1, confirm: steps }[v.screen])

    async function send (req, onDone) {
      v.busy = true
      v.err = null
      draw()
      try {
        const r = await ctx.api.post('/api/firstrun', req)
        v.done = true
        ctx.toast?.(r.notice)
        ctx.refreshBoard?.()
        ctx.close()
        onDone?.()
      } catch (err) {
        v.err = err
        v.busy = false
        draw()
      }
    }
    const skip = () => send({ skip: true })
    const save = () => send({ agent: v.agent, strong: v.strong, everyday: v.everyday })

    function agentScreen () {
      if (!usable.length) {
        return [
          h('p', { class: 'frs-lede' }, 'No agent is usable yet, so gummi keeps its starter profiles.'),
          agentList(),
          foot({ label: 'Continue', testid: 'firstrun-continue', onclick: skip })
        ]
      }
      return [
        h('p', { class: 'frs-lede' }, 'Which agent should gummi run?'),
        agentList(),
        foot({ label: 'Continue', testid: 'firstrun-continue', onclick: () => { pick(); v.screen = 'models'; draw() } })
      ]
    }

    function agentList () {
      return h('div', { class: 'frs-agents', role: 'radiogroup', 'aria-label': 'Agent' },
        wiz.agents.map(a => h('label', { class: ['frs-agent', a.status !== 'usable' && 'off'], testid: `firstrun-agent-${a.name}` },
          h('input', {
            type: 'radio', name: 'agent', value: a.name, checked: a.name === v.agent, disabled: a.status !== 'usable',
            onchange: () => { v.agent = a.name }
          }),
          h('b', null, a.name),
          h('span', { class: 'frs-status' }, a.status),
          a.hint ? h('small', { class: 'frs-hint' }, a.hint) : null)))
    }

    function modelsScreen () {
      const a = cur()
      const sel = (label, key, testid) => h('label', { class: 'field' }, h('span', null, label),
        choose(a.models, { value: v[key], testid, label, onchange: (e) => { v[key] = e.target.value } }))
      return [
        h('p', { class: 'frs-lede' }, `Which models should ${a.name} run?`),
        sel('strong (architect, planner, reviewer)', 'strong', 'firstrun-strong'),
        sel('everyday (implementer, tester, scribe, scout)', 'everyday', 'firstrun-everyday'),
        foot({ label: 'Continue', testid: 'firstrun-continue', onclick: () => { v.screen = 'confirm'; draw() } },
          wiz.askAgent ? () => { v.screen = 'agent'; draw() } : null)
      ]
    }

    function confirmScreen () {
      return [
        h('p', { class: 'frs-lede' }, `Profile ${wiz.profile} will be written:`),
        h('dl', { class: 'frs-roles', testid: 'firstrun-roles' },
          wiz.roles.flatMap(r => [h('dt', null, r.role), h('dd', null, `${v.agent}/${r.tier === 'strong' ? v.strong : v.everyday}`)])),
        h('p', { class: 'frs-note' }, 'profiles.yaml keeps only this profile.'),
        foot({ label: 'Save', testid: 'firstrun-save', onclick: save }, () => { v.screen = 'models'; draw() })
      ]
    }

    // foot: Skip on the left, Back when there is one, the primary last.
    function foot (primary, back) {
      return h('div', { class: 'frs-foot' },
        h('button', { class: 'btn', type: 'button', testid: 'firstrun-skip', disabled: v.busy, onclick: skip }, 'Skip'),
        h('span', { class: 'grow' }),
        back ? h('button', { class: 'btn', type: 'button', testid: 'firstrun-back', disabled: v.busy, onclick: back }, 'Back') : null,
        h('button', { class: 'btn pri', type: 'button', testid: primary.testid, disabled: v.busy, onclick: primary.onclick }, primary.label))
    }

    function draw () {
      clear(body)
      body.append(
        h('div', { class: 'frs-step', testid: 'firstrun-step' }, `${stepNo()}/${steps}`),
        ...{ agent: agentScreen, models: modelsScreen, confirm: confirmScreen }[v.screen](),
        ...(v.err ? [errorBox(v.err, 'firstrun-error')] : []))
      body.querySelector('.btn.pri')?.focus()
    }

    // Enter is the primary button, wherever focus is but on a button
    const onKey = (e) => {
      if (e.key !== 'Enter' || e.target.closest?.('button') || v.busy) return
      e.preventDefault()
      body.querySelector('.btn.pri')?.click()
    }
    body.addEventListener('keydown', onKey)
    draw()

    return () => {
      body.removeEventListener('keydown', onKey)
      // closed any other way: a skip, so the board stops offering it
      if (!v.done && !v.busy) ctx.api.post('/api/firstrun', { skip: true }).catch(() => {})
    }
  }
})
