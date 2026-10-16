// tree.js — the Working tree tab: a person's hands on the card's branch.
// What is staged and what is not, staging a file, a hunk or some of a
// hunk's lines, discarding, committing (or amending), the stashes made on
// the card's branch, a fetch, and the push the PR tab's strip makes.
//
// The page decides nothing: every write is the server's to refuse, and it
// refuses all of them while an agent is working in the tree (Tree.why) —
// a hand-staged index under a running agent would fight its commits. A
// hunk is named by the digest of the diff it was chosen from, so one
// chosen from a diff that has since changed is refused and read again.

import { h, plural } from './dom.js?v=__ASSET_V__'
import { get, post, cardPath } from './api.js?v=__ASSET_V__'
import { toast } from './toast.js?v=__ASSET_V__'
import { openModal } from './views.js?v=__ASSET_V__'
import { openPublish } from './publish.js?v=__ASSET_V__'

export const treeTab = {
  name: 'tree',
  // an agent has no branch, a research card a throwaway tree, and a
  // finished card no tree at all
  hidden: (c) => !!c?.home || !!c?.scratch || c?.stage === 'done',
  label: 'Working tree',
  key: 'g w',
  fetch: (id) => get(cardPath(id, 'tree')),
  empty: (d) => !d || (!d.staged?.length && !d.changes?.length && !d.conflicts?.length && !d.stashes?.length),
  count: (d) => (d?.staged?.length || 0) + (d?.changes?.length || 0) + (d?.conflicts?.length || 0),
  render
}

// per card: the files opened to their diff (key "s:path" staged, "u:path"
// not) with the lines picked in each, the message being written, amend
const local = new Map()

function localFor (id) {
  let l = local.get(id)
  if (!l) { l = { open: new Map(), msg: '', amend: false }; local.set(id, l) }
  return l
}

const keyOf = (staged, path) => `${staged ? 's' : 'u'}:${path}`

const STATUS = { M: 'modified', A: 'added', D: 'deleted', R: 'renamed', T: 'type changed', U: 'conflicted', '?': 'untracked' }

function render (pane, entry, ctx) {
  const d = entry.data
  if (!d) return
  const l = localFor(ctx.id)
  const locked = !!d.why

  // write sends one write and shows the tree it answers with
  const write = async (req, { quiet = false } = {}) => {
    try {
      const res = await post(cardPath(ctx.id, 'tree'), req)
      if (!quiet) toast(res.done)
      for (const o of l.open.values()) o.diff = null
      ctx.swap(res.tree)
      return res
    } catch (err) {
      if (err.status === 409 && err.message === 'moved') {
        toast('The tree changed since it was read — showing it again')
        for (const o of l.open.values()) { o.diff = null; o.sel.clear() }
        ctx.swap(null)
      } else {
        toast(err.message, { err: true })
      }
      return null
    }
  }

  const confirm = (title, text, label, go) => openModal({
    title,
    testid: 'tree-confirm',
    returnTo: '[data-testid="tree-commit"]',
    body: h('p', { class: 'aq' }, text),
    actions: [{ label: 'Cancel', testid: 'tree-confirm-cancel' }, { label, danger: true, primary: true, testid: 'tree-confirm-go', onClick: () => go() }]
  })

  pane.append(h('div', { class: 'tr-bar', testid: 'tree-bar' },
    h('span', { class: 'tr-branch mono', title: 'the card’s branch' }, d.branch),
    d.tracked ? h('span', { class: 'tr-ab mono', testid: 'tree-ahead', title: `against ${d.remote}/${d.branch}` }, h('span', { class: 'tr-ahead' }, `↑${d.ahead || 0}`), ' ', h('span', { class: 'tr-behind' }, `↓${d.behind || 0}`)) : null,
    h('span', { class: 'tr-remote' }, d.remote ? (d.tracked ? d.remote : `not on ${d.remote} yet`) : 'no remote'),
    h('span', { class: 'sp' }),
    ...fetchControl(),
    h('button', { class: 'btn', type: 'button', testid: 'tree-push', disabled: locked, onclick: () => openPublish(ctx.card, 'push', { returnTo: '[data-testid="tree-push"]', done: () => ctx.swap(null) }) }, 'Push'),
    h('button', { class: 'btn', type: 'button', testid: 'tree-stash', disabled: locked || (!d.staged.length && !d.changes.length), title: 'git stash push --include-untracked', onclick: () => write({ op: 'stash', message: l.msg.split('\n')[0] }) }, 'Stash')))

  if (locked) pane.append(h('div', { class: 'tr-lock', testid: 'tree-locked', role: 'status' }, h('span', { class: 'spinner' }), d.why))

  if (d.conflicts.length) {
    pane.append(group('Conflicts', d.conflicts.length, 'tree-conflicts', []))
    pane.append(h('ul', { class: 'tr-files' }, d.conflicts.map(f => fileRow(f, false, { note: 'resolve in the terminal or ask the agent' }))))
  }

  pane.append(group('Staged', d.staged.length, 'tree-staged', [
    d.staged.length ? h('button', { class: 'btn ghost', type: 'button', testid: 'tree-unstage-all', disabled: locked, onclick: () => write({ op: 'unstage', paths: d.staged.map(f => f.path) }) }, 'Unstage all') : null
  ]))
  if (!d.staged.length) pane.append(h('p', { class: 'tr-none' }, 'Nothing staged. Tick a file below, or a hunk in its diff.'))
  else pane.append(h('ul', { class: 'tr-files' }, d.staged.map(f => fileRow(f, true))))

  pane.append(group('Changes', d.changes.length, 'tree-changes', d.changes.length
    ? [
        h('button', { class: 'btn ghost', type: 'button', testid: 'tree-stage-all', disabled: locked, onclick: () => write({ op: 'stage', paths: d.changes.map(f => f.path) }) }, 'Stage all'),
        h('button', { class: 'btn ghost danger', type: 'button', testid: 'tree-discard-all', disabled: locked, onclick: () => confirm('Discard every change', `Throw away the unstaged changes to ${plural(d.changes.length, 'file')}? Untracked files are deleted. What is staged is kept.`, 'Discard', () => write({ op: 'discard', paths: d.changes.map(f => f.path) })) }, 'Discard all…')
      ]
    : []))
  if (!d.changes.length) pane.append(h('p', { class: 'tr-none' }, 'No unstaged changes.'))
  else pane.append(h('ul', { class: 'tr-files' }, d.changes.map(f => fileRow(f, false))))

  if (d.stashes.length) {
    pane.append(group('Stashes', d.stashes.length, 'tree-stashes', []))
    pane.append(h('ul', { class: 'tr-files' }, d.stashes.map((s, i) => h('li', { class: 'tr-stash', testid: `tree-stash-${i}` },
      h('span', { class: 'tr-path' }, s.subject.replace(/^(WIP on|On) [^:]+: /, '')),
      s.at ? h('span', { class: 'tr-when' }, new Date(s.at * 1000).toLocaleString()) : null,
      h('button', { class: 'btn ghost', type: 'button', testid: `tree-stash-apply-${i}`, disabled: locked, onclick: () => write({ op: 'stash-apply', stash: s.sha }) }, 'Apply'),
      h('button', { class: 'btn ghost danger', type: 'button', testid: `tree-stash-drop-${i}`, disabled: locked, onclick: () => confirm('Drop the stash', `Drop “${s.subject}”? Its changes are gone afterwards.`, 'Drop', () => write({ op: 'stash-drop', stash: s.sha })) }, 'Drop')))))
  }

  pane.append(commitBox())

  // fetchControl is the Fetch button and, with more than one remote, the
  // remote it fetches: the branch's own by default, any other, or all
  function fetchControl () {
    const remotes = d.remotes || []
    if (!remotes.includes(l.fetchFrom) && l.fetchFrom !== '*') l.fetchFrom = remotes.includes(d.remote) ? d.remote : remotes[0]
    const cmd = () => l.fetchFrom === '*' ? 'git fetch --all --prune' : `git fetch --prune ${l.fetchFrom}`
    const btn = h('button', { class: 'btn', type: 'button', testid: 'tree-fetch', disabled: !remotes.length, title: remotes.length ? cmd() : 'this repository has no remote', onclick: () => write({ op: 'fetch', remote: l.fetchFrom }) }, 'Fetch')
    if (remotes.length < 2) return [btn]
    const sel = h('select', {
      class: 'tr-fetch-from',
      testid: 'tree-fetch-remote',
      'aria-label': 'Remote to fetch',
      onchange: () => { l.fetchFrom = sel.value; btn.title = cmd() }
    },
    remotes.map(r => h('option', { value: r, selected: r === l.fetchFrom }, r === d.remote ? `${r} (this branch)` : r)),
    h('option', { value: '*', selected: l.fetchFrom === '*' }, 'all remotes'))
    return [sel, btn]
  }

  function group (title, n, testid, buttons) {
    return h('div', { class: 'tr-grp', testid }, h('span', null, title), h('span', { class: 'n' }, String(n)), h('span', { class: 'sp' }), ...buttons)
  }

  function fileRow (f, staged, { note } = {}) {
    const k = keyOf(staged, f.path)
    const o = l.open.get(k)
    const untracked = f.status === '?'
    const toggle = h('input', {
      type: 'checkbox',
      class: 'tr-cb',
      checked: staged,
      disabled: locked || f.status === 'U',
      'aria-label': `${staged ? 'Unstage' : 'Stage'} ${f.path}`,
      testid: `tree-${staged ? 'unstage' : 'stage'}-${f.path}`,
      onchange: () => write({ op: staged ? 'unstage' : 'stage', paths: [f.path] }, { quiet: true })
    })
    const canDiff = !untracked && !f.binary && f.status !== 'U'
    const name = h('button', {
      class: ['tr-path', 'mono', o && 'on'],
      type: 'button',
      disabled: !canDiff,
      'aria-expanded': canDiff ? String(!!o) : null,
      testid: `tree-file-${staged ? 's' : 'u'}-${f.path}`,
      title: canDiff ? 'show its diff' : null,
      onclick: () => { if (o) l.open.delete(k); else l.open.set(k, { diff: null, sel: new Set() }); ctx.rerender() }
    }, dirOf(f.path) ? h('span', { class: 'dir' }, dirOf(f.path)) : null, baseOf(f.path), f.from ? h('span', { class: 'dir' }, ` ← ${f.from}`) : null)
    const li = h('li', { class: 'tr-file' },
      h('div', { class: 'tr-row' },
        toggle,
        h('span', { class: ['tr-st', `s-${f.status === '?' ? 'u' : f.status}`], title: STATUS[f.status] || f.status }, f.status),
        name,
        note ? h('span', { class: 'tr-when' }, note) : null,
        f.binary ? h('span', { class: 'tr-when' }, 'binary') : h('span', { class: 'tr-lines mono' }, f.add ? h('span', { class: 'a' }, `+${f.add}`) : null, f.del ? h('span', { class: 'd' }, ` −${f.del}`) : null),
        !staged && f.status !== 'U'
          ? h('button', { class: 'btn ghost danger tr-x', type: 'button', disabled: locked, testid: `tree-discard-${f.path}`, title: untracked ? 'delete this untracked file' : 'throw away its unstaged changes', 'aria-label': `Discard ${f.path}`, onclick: () => confirm('Discard changes', untracked ? `Delete the untracked file ${f.path}?` : `Throw away the unstaged changes to ${f.path}?`, 'Discard', () => write({ op: 'discard', paths: [f.path] })) }, '✕')
          : null))
    if (o) li.append(diffOf(f, staged, o))
    return li
  }

  function diffOf (f, staged, o) {
    const box = h('div', { class: 'tr-diff', testid: `tree-diff-${f.path}` })
    if (!o.diff) {
      box.append(h('p', { class: 'tr-none' }, h('span', { class: 'spinner' }), 'Reading the diff…'))
      get(cardPath(ctx.id, 'tree/diff') + `?path=${encodeURIComponent(f.path)}${staged ? '&staged=1' : ''}`)
        .then(diff => { o.diff = diff; ctx.rerender() })
        .catch(err => { box.replaceChildren(h('p', { class: 'tr-none' }, err.message)) })
      return box
    }
    const hunks = o.diff.file?.hunks || []
    if (!hunks.length) box.append(h('p', { class: 'tr-none' }, 'No textual change to show.'))
    hunks.forEach((hk, n) => {
      const picked = hk.lines.filter(ln => ln.t !== ' ' && o.sel.has(ln.idx)).map(ln => ln.idx)
      const what = picked.length ? plural(picked.length, 'line') : 'hunk'
      const hunkWrite = (hunkOp) => write({ op: 'hunk', path: f.path, hunk: n, lines: picked, digest: o.diff.digest, hunkOp }).then(r => { if (r) o.sel.clear() })
      box.append(h('div', { class: 'hunk' },
        h('div', { class: 'hh tr-hh' }, h('span', null, hk.header), h('span', { class: 'sp' }),
          staged
            ? h('button', { class: 'btn ghost', type: 'button', disabled: locked, testid: `tree-hunk-unstage-${n}`, onclick: () => hunkWrite('unstage') }, `Unstage ${what}`)
            : [
                h('button', { class: 'btn ghost', type: 'button', disabled: locked, testid: `tree-hunk-stage-${n}`, onclick: () => hunkWrite('stage') }, `Stage ${what}`),
                h('button', { class: 'btn ghost danger', type: 'button', disabled: locked, testid: `tree-hunk-discard-${n}`, onclick: () => confirm('Discard', `Throw away this ${what} from ${f.path}?`, 'Discard', () => hunkWrite('discard')) }, `Discard ${what}`)
              ]),
        hk.lines.map(ln => {
          const cls = ln.t === '+' ? 'a' : ln.t === '-' ? 'd' : ''
          const change = ln.t !== ' '
          const on = o.sel.has(ln.idx)
          return h('div', {
            class: ['ln', 'tr-ln', cls, on && 'picked'],
            testid: `tree-line-${ln.idx}`,
            role: change ? 'checkbox' : null,
            'aria-checked': change ? String(on) : null,
            tabindex: change && !locked ? '0' : null,
            title: change ? 'pick this line, then stage only what is picked' : null,
            onclick: change && !locked ? () => { if (on) o.sel.delete(ln.idx); else o.sel.add(ln.idx); ctx.rerender() } : null,
            onkeydown: change && !locked ? (e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); e.currentTarget.click() } } : null
          }, h('span', { class: 'o' }, ln.old ? String(ln.old) : ''), h('span', { class: 'n' }, ln.new ? String(ln.new) : ''), h('span', { class: 'm' }, change ? ln.t : ''), h('span', null, ln.text || ' '))
        })))
    })
    return box
  }

  function commitBox () {
    const ta = h('textarea', {
      class: 'tr-msg',
      rows: 3,
      testid: 'tree-message',
      placeholder: 'Commit message — the first line is the subject',
      disabled: locked,
      spellcheck: 'true',
      value: l.msg,
      oninput: (e) => { l.msg = e.target.value; sync() }
    })
    ta.dataset.draft = 'tree-message'
    const amend = h('input', { type: 'checkbox', checked: l.amend, disabled: locked, testid: 'tree-amend', onchange: (e) => { l.amend = e.target.checked; sync() } })
    const commit = async (push) => {
      const res = await write({ op: 'commit', message: l.msg, amend: l.amend })
      if (!res) return
      l.msg = ''
      l.amend = false
      ctx.rerender()
      if (push) openPublish(ctx.card, 'push', { returnTo: '[data-testid="tree-commit"]', done: () => ctx.swap(null) })
    }
    const go = h('button', { class: 'btn', type: 'button', testid: 'tree-commit', onclick: () => commit(false) }, 'Commit')
    const goPush = h('button', { class: 'btn pri', type: 'button', testid: 'tree-commit-push', onclick: () => commit(true) }, 'Commit & push')
    function sync () {
      const can = !locked && !!l.msg.trim() && (l.amend || d.staged.length > 0)
      go.disabled = goPush.disabled = !can
      go.textContent = l.amend ? 'Amend' : 'Commit'
      goPush.textContent = l.amend ? 'Amend & push' : 'Commit & push'
    }
    sync()
    return h('div', { class: 'tr-commit', testid: 'tree-commit-box' }, ta,
      h('div', { class: 'tr-crow' },
        h('label', { class: 'tr-tog' }, amend, 'Amend the last commit'),
        d.signing ? h('span', { class: 'tr-chip', title: 'the SSH key stored in settings signs it' }, 'signed') : null,
        h('span', { class: 'sp' }),
        go, goPush))
  }
}

const dirOf = (p) => p.includes('/') ? p.slice(0, p.lastIndexOf('/') + 1) : ''
const baseOf = (p) => p.slice(p.lastIndexOf('/') + 1)
