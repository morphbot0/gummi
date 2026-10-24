// views/doctor.js — the readiness checklist `gummi doctor` prints, from
// the command's own builder: each check's status, what it found, and what
// to do about it. The quick run never constructs a backend; deep checks
// (POST ?deep=1, the CLI's --deep) probe every model the profiles name,
// which contacts those backends and can take a while.

import { h, clear, plural } from '../dom.js?v=__ASSET_V__'
import { registerView } from '../views.js?v=__ASSET_V__'
import { errorBox, confirmStrip } from './kit.js?v=__ASSET_V__'

const MARK = { ok: '✓', warn: '!', fail: '✕', unknown: '?' }
const WORD = { ok: 'ok', warn: 'warning', fail: 'failing', unknown: 'not probed' }

registerView('doctor', {
  title: 'Doctor',
  css: 'views/doctor.css',
  mount (body, ctx) {
    const v = { rep: null, err: null, loading: false, deep: false, asking: false, alive: true }
    body.classList.add('vdoctor')

    async function run (deep) {
      v.loading = true
      v.deep = deep
      v.asking = false
      draw()
      try {
        // the deep run spends model turns, so it is a write (POST), which
        // the server holds to the page's own origin
        v.rep = deep ? await ctx.api.post('/api/doctor?deep=1', {}) : await ctx.api.get('/api/doctor')
        v.err = null
      } catch (err) { v.err = err }
      v.loading = false
      if (v.alive) draw()
    }

    function draw () {
      clear(body)
      const r = v.rep
      const checks = r?.checks || []
      const count = (s) => checks.filter(c => c.status === s).length
      body.append(h('div', { class: 'vhead dhead' },
        r ? h('span', { class: ['verdict', r.ready ? 'ready' : 'not', r.ready && count('warn') && 'warn'], testid: 'doctor-ready', data: { ready: String(!!r.ready) } },
          h('span', { 'aria-hidden': 'true' }, r.ready ? '✓' : '✕'),
          // ready with warnings is not the same as ready: it says so first
          r.ready ? (count('warn') ? `Ready, with ${count('warn')} warning${count('warn') === 1 ? '' : 's'}` : 'Ready') : 'Not ready') : null,
        r ? h('span', { class: 'tally' },
          [['ok', 'ok'], ['warn', 'warning', 'warnings'], ['fail', 'failing'], ['unknown', 'not probed']]
            .filter(([s]) => count(s)).map(([s, one, many]) => `${count(s)} ${count(s) === 1 || !many ? one : many}`).join(' · ')) : null,
        h('span', { class: 'grow' }),
        v.loading ? h('span', { class: 'vbusy' }, h('span', { class: 'spinner' }), v.deep ? 'probing the backends…' : 'checking…') : null,
        h('button', { type: 'button', class: 'btn', testid: 'doctor-rerun', disabled: v.loading, onclick: () => run(false) }, 'Check again'),
        h('button', { type: 'button', class: 'btn', testid: 'doctor-deep', disabled: v.loading, onclick: () => { v.asking = true; draw() } }, 'Run deep checks')))
      if (v.asking) {
        body.append(confirmStrip({
          question: 'Probe every model backend?',
          detail: 'Deep checks start a short session on each model the profiles name, so they contact those backends (and may spend a little). They can take a minute.',
          yes: 'Run deep checks',
          testid: 'doctor-deep-confirm',
          onYes: () => run(true),
          onNo: () => { v.asking = false; draw() }
        }))
      }
      if (v.err) body.append(errorBox(v.err))
      if (!r) {
        if (v.loading) body.append(h('div', { class: 'empty', testid: 'doctor-loading' }, h('span', { class: 'spinner' })))
        return
      }
      body.append(h('ul', { class: ['dchecks', v.loading && 'stale'], testid: 'doctor-checks' }, checks.map(c =>
        h('li', { class: ['dcheck', c.status], testid: `doctor-check-${c.name}`, data: { status: c.status } },
          h('span', { class: 'mk', role: 'img', 'aria-label': WORD[c.status] || c.status }, MARK[c.status] || '·'),
          h('span', { class: 'n' }, c.name),
          h('span', { class: 'd' }, c.detail),
          c.remediation && c.status !== 'ok' ? h('span', { class: 'fix' }, c.remediation) : null))))
      if (v.deep && !v.loading) body.append(h('p', { class: 'vnote' }, `Deep run · ${plural(checks.filter(c => c.name.startsWith('reach:')).length, 'model probe')}. Results are cached for a while, as the CLI’s are.`))
    }

    run(false)
    return () => { v.alive = false }
  }
})
