// threadcache.js — the threads (and live blocks) of the cards this browser
// opened last, kept so that opening one draws what was there a moment ago
// instead of a spinner while the read goes out. What it holds is only ever
// a stand-in: select draws it marked cached, and the full read that always
// follows replaces it. The head is never kept — its decision is what enter
// answers, and an answer is given to what the server says now.
//
// It is kept in memory and, a few cards' worth, in localStorage so that a
// reload on a slow link draws at once too. The key carries the board's repo
// and this build's asset version: another board on the same address, or a
// page whose item shapes changed, starts empty. Unpairing (or the server
// refusing this device) forgets all of it.

const PREFIX = 'gummi-web:threads:'
const KEEP = 6 // cards held across a reload
const MAX_BYTES = 1500000 // and their weight, in characters
const VERSION = '__ASSET_V__'

let key = ''
let held = new Map() // card id -> { thread, live }, newest last
let saveTimer = 0

// openCache reads what this repo's board left behind on this browser.
export function openCache (repo) {
  key = PREFIX + VERSION + ':' + (repo || '')
  held = new Map()
  try {
    // an older build's (or another board's) cache is dead weight
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const k = localStorage.key(i)
      if (k?.startsWith(PREFIX) && !k.startsWith(PREFIX + VERSION + ':')) localStorage.removeItem(k)
    }
    const v = JSON.parse(localStorage.getItem(key) || '[]')
    if (Array.isArray(v)) for (const [id, e] of v) if (id && e?.thread) held.set(id, e)
  } catch { /* nothing kept */ }
}

// cached is what was last drawn for a card, or null.
export function cached (id) { return (id && held.get(id)) || null }

// keep records a card's thread and live block as the server last said
// them; a thread that failed or whose card is gone is not kept.
export function keep (id, thread, live) {
  if (!id || !thread || thread.cached || thread.err || thread.gone || thread.unavailable) return
  held.delete(id)
  held.set(id, { thread: { items: thread.items, lastSeq: thread.lastSeq }, live: live || null })
  while (held.size > KEEP * 4) held.delete(held.keys().next().value)
  clearTimeout(saveTimer)
  saveTimer = setTimeout(save, 2000)
}

// drop forgets one card (deleted, here or elsewhere).
export function drop (id) {
  if (!held.delete(id)) return
  clearTimeout(saveTimer)
  saveTimer = setTimeout(save, 2000)
}

// forgetAll empties the cache, here and on disk: the device is no longer
// paired, and what it was shown is not its to keep.
export function forgetAll () {
  clearTimeout(saveTimer)
  held = new Map()
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const k = localStorage.key(i)
      if (k?.startsWith(PREFIX)) localStorage.removeItem(k)
    }
  } catch { /* nothing kept */ }
}

// save writes the newest cards that fit, dropping the oldest until the
// browser takes it; a browser that takes nothing keeps nothing.
function save () {
  if (!key) return
  let list = [...held].slice(-KEEP)
  while (list.length) {
    const text = JSON.stringify(list)
    if (text.length <= MAX_BYTES) {
      try { localStorage.setItem(key, text); return } catch { /* over the quota: fewer */ }
    }
    list = list.slice(1)
  }
  try { localStorage.removeItem(key) } catch { /* nothing kept */ }
}
