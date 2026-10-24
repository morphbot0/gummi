// keys.js — the board's keyboard: j/k walk the rail, n opens the next card
// that needs you, g then s/m/d/p/r picks a panel tab, [ and ] fold the rail
// and the panel, / filters the rail, c writes, ? lists the keys, ⌘K/Ctrl-K jumps. The open
// card's actions are . (its menu), L (land) and C (commit). Digits pick
// an answer and enter gives it. Letter keys never fire while typing, and
// nothing uses alt (alt+d is the address bar on Windows and Linux).

import { state } from './store.js?v=__ASSET_V__'
import { overlayOpen } from './views.js?v=__ASSET_V__'
import { openDecision, highlight, answer } from './decision.js?v=__ASSET_V__'
import { openActions } from './head.js?v=__ASSET_V__'

// headAct presses the open card's head button for an act (folded or not,
// a phone's included), or opens its menu with that act first when the head
// has none — where the menu says why it is not on offer by its absence.
function headAct (testids, word) {
  const btn = testids.map(t => document.querySelector(`[data-testid="${t}"]`)).find(Boolean)
  if (btn) btn.click()
  else openActions(word)
}

export function initKeys (a) {
  let gAt = 0
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      if (!overlayOpen()) a.palette()
      return
    }
    if (overlayOpen() || e.metaKey || e.ctrlKey || e.altKey) return
    const t = e.target
    if (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable) return
    if (e.key === 'Enter' && /^(BUTTON|A|SUMMARY|LABEL)$/.test(t.tagName)) return
    if (t.getAttribute?.('role') === 'button' && (e.key === 'Enter' || e.key === ' ')) return
    if (gAt && Date.now() - gAt < 1200) {
      gAt = 0
      const tab = { s: 'spec', m: 'memory', d: 'diff', l: 'log', p: 'pr', r: 'stats', t: 'terminal' }[e.key]
      if (tab) { e.preventDefault(); a.setTab(tab) }
      return
    }
    const d = openDecision()
    switch (e.key) {
      case 'g': gAt = Date.now(); break
      case 'j': e.preventDefault(); a.step(1); break
      case 'k': e.preventDefault(); a.step(-1); break
      case 'n': e.preventDefault(); a.nextNeeding(); break
      case '[': e.preventDefault(); a.toggleRail(); break
      case ']': e.preventDefault(); a.togglePanel(); break
      case '/': e.preventDefault(); a.focusFilter(); break
      case 'c': e.preventDefault(); a.focusComposer(); break
      case '?': e.preventDefault(); a.keysHelp(); break
      case '.': if (state.card) { e.preventDefault(); openActions() } break
      case 'L': if (state.card) { e.preventDefault(); headAct(['session-land', 'session-land-held'], 'merge') } break
      case 'C': if (state.card) { e.preventDefault(); headAct(['session-commit'], 'commit') } break
      case 'Enter': if (d && state.conn === 'live') { e.preventDefault(); answer() } break
      default:
        if (d && /^[1-9]$/.test(e.key) && +e.key <= d.options.length) { e.preventDefault(); highlight(+e.key - 1) }
    }
  })
}
