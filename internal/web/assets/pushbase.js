// pushbase.js — closing a card by push: once it has landed, the base is
// pushed to the remote its repository closes cards on (the repositories
// view sets which). The page decides nothing: the facts are the server's
// (GET /api/repos/pushbase), shown with the commands they run, and the
// confirm carries their fingerprint, so a base or remote that moved in
// between refuses the push rather than running it unseen. It is a
// fast-forward or nothing; gummi never forces a base.

import { h, cardName } from './dom.js?v=__ASSET_V__'
import { get, post } from './api.js?v=__ASSET_V__'
import { openModal } from './views.js?v=__ASSET_V__'
import { toast } from './toast.js?v=__ASSET_V__'

// pushAfterControl is the landing dialog's "push afterwards" switch, on by
// default where the repository closes cards by push.
export function pushAfterControl (remote, base) {
  const box = h('input', { type: 'checkbox', checked: true, testid: 'push-after' })
  const el = h('label', { class: 'keep-branch', testid: 'push-after-label' }, box, ` Then push ${base || 'the base'} to ${remote}`)
  return { el, value: () => box.checked }
}

const short = (s) => (s || '').slice(0, 7)

// openBasePush reads what pushing repo's base would do and asks. card is
// the landed card it closes (its branch is deleted on the remote too, if
// the remote has it); done runs after a push that went through.
export async function openBasePush ({ repo = '', card = '', returnTo, done } = {}) {
  const title = card ? `Push the landing · ${cardName(card)}` : 'Push the base'
  let gone = false
  const reading = openModal({
    title,
    testid: 'pushbase-dialog',
    returnTo,
    onClose: () => { gone = true },
    body: h('p', { class: 'aq pubwait', role: 'status' }, h('span', { class: 'spinner' }), 'Reading the remote…'),
    actions: [{ label: 'Cancel', testid: 'pushbase-cancel' }]
  })
  let f
  try {
    f = await get(`/api/repos/pushbase?repo=${encodeURIComponent(repo)}${card ? `&card=${encodeURIComponent(card)}` : ''}`)
  } catch (err) {
    reading.close()
    toast(err.message, { err: true })
    return
  }
  if (gone) return
  reading.close()
  const target = `${f.remote}/${f.base}`
  const rows = [
    ['push', `${f.base} → ${target}`],
    [`${f.base} here`, short(f.local)],
    [`${target} now`, f.remoteTip ? short(f.remoteTip) + (f.refusal ? '' : ' · fast-forward') : 'not there yet — a new branch'],
    ['push URL', f.url || '—'],
    f.hook ? ['hooks', `pre-push (${f.hook})`] : null,
    f.card ? ['afterwards', `delete ${f.card} on ${f.remote}`] : null
  ].filter(Boolean)
  const lede = f.refusal
    ? h('p', { class: 'aerr', testid: 'pushbase-refusal', role: 'alert' }, f.refusal)
    : h('p', { class: 'aq', testid: 'pushbase-lede' }, `Push ${f.base} to ${f.remote} as a fast-forward${f.card ? `, then delete ${f.card} there` : ''}.`)
  const body = [
    lede,
    h('dl', { class: 'pb-facts', testid: 'pushbase-facts' }, rows.map(([k, v]) => h('div', null, h('dt', null, k), h('dd', { class: 'mono' }, v)))),
    f.commands.length ? h('pre', { class: 'pb-cmds mono', testid: 'pushbase-commands' }, f.commands.join('\n')) : null,
    f.refusal ? null : h('p', { class: 'sub' }, `Refused if ${f.base} or ${target} moves before you confirm.`)
  ]
  openModal({
    title,
    testid: 'pushbase-dialog',
    returnTo,
    body,
    actions: [
      { label: f.refusal ? 'Close' : 'Not now', testid: 'pushbase-cancel' },
      f.refusal
        ? null
        : {
            label: `Push to ${f.remote}`,
            primary: true,
            testid: 'pushbase-confirm',
            onClick: async () => {
              try {
                const res = await post('/api/repos/pushbase', { repo, card, fingerprint: f.fingerprint })
                toast(res?.text || `Pushed ${f.base} to ${f.remote}`)
                done?.()
                return true
              } catch (err) {
                toast(err.message, { err: true })
                return false
              }
            }
          }
    ].filter(Boolean)
  })
}
