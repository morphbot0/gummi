// A session an agent dispatched, in the agent's thread (DESIGN §26.9): one
// block per child, folded server-side (threadfold.Dispatch) — its state,
// its run's progress, its spend and the last thing it said. The words are
// the server's; this file only draws them. Clicking the block selects the
// child's own card.

import { h, cr } from './dom.js?v=__ASSET_V__'

// dispatchSig is what the thread caches a block by: its revision alone.
export function dispatchSig (it) {
  return it.dispatch.rev
}

// stateClass colours the state word.
function stateClass (s) {
  if (s === 'working' || s.endsWith(' running')) return 'run'
  if (s === 'waiting for the person' || s === 'envelope spent' || s.endsWith(' paused')) return 'warn'
  if (s.endsWith(' done')) return 'ok'
  return 'idle'
}

// dispatchBlock draws one dispatched session; open(card) selects it.
export function dispatchBlock (it, open) {
  const d = it.dispatch
  const spend = d.envelope ? `${cr(d.spent)} of ${cr(d.envelope)}` : cr(d.spent)
  const facts = [
    d.run && d.of ? `run ${d.run} · ${d.done}/${d.of} steps` : null,
    d.step || null
  ].filter(Boolean).join(' · ')
  return h('button', {
    class: ['dispatch-block', `st-${stateClass(d.state)}`], type: 'button', testid: `dispatch-block-${d.card}`,
    'aria-label': `Open session ${d.title}: ${d.state}`,
    onclick: () => open(d.card)
  },
  h('span', { class: 'db-head' },
    h('span', { class: 'db-mark', 'aria-hidden': 'true' }, '⇢'),
    h('b', { class: 'db-title' }, d.title),
    d.repo ? h('span', { class: 'db-repo' }, d.repo) : null,
    h('span', { class: 'db-state', testid: `dispatch-state-${d.card}` }, d.state)),
  h('span', { class: 'db-facts' }, h('span', null, facts), h('span', { class: 'db-spend' }, spend)),
  d.last ? h('span', { class: 'db-last' }, h('i', null, 'last: '), d.last) : null)
}
