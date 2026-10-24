import type { Page } from '@playwright/test';

/**
 * What a machine can read off one screen for a UX review: text a box cuts
 * off, controls too small to hit, controls with no name, the primary
 * actions on offer, what covers what, and the words the person reads in the
 * card's head, its dock and the open surface. None of it fails anything:
 * the tour records it beside the screenshot for a reviewer to weigh.
 */
export interface Probe {
  viewport: { width: number; height: number };
  view: string | null;
  dialog: string | null;
  sideways: boolean;
  leaks: string[];
  clipped: string[];
  smallTargets: string[];
  unnamed: string[];
  covered: string[];
  lowContrast: string[];
  primaries: string[];
  text: Record<string, string>;
}

export async function probe(page: Page, phone: boolean): Promise<Probe> {
  return page.evaluate((phone) => {
    const cap = <T>(xs: T[], n = 30) => xs.slice(0, n);
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const describe = (el: Element) => {
      const id = el.getAttribute('data-testid');
      const words = ((el as HTMLElement).innerText || el.getAttribute('aria-label') || el.getAttribute('title') || '').trim().replace(/\s+/g, ' ').slice(0, 60);
      return `${id ? `[${id}]` : el.tagName.toLowerCase()}${words ? ` "${words}"` : ''}`;
    };
    const visible = (el: Element) => {
      const r = el.getBoundingClientRect();
      if (r.width < 1 || r.height < 1) return false;
      if (r.bottom < 0 || r.right < 0 || r.top > vh || r.left > vw) return false;
      if (el.closest('[hidden], [inert]')) return false;
      const s = getComputedStyle(el);
      return s.visibility !== 'hidden' && s.display !== 'none' && Number(s.opacity) > 0.05;
    };

    const dialogEl = [...document.querySelectorAll('[role="dialog"], dialog[open]')].find(visible) || null;
    // an open menu covers what is under it on purpose, like a dialog
    const menuEl = [...document.querySelectorAll('[role="menu"], [role="listbox"]')].find(visible) || null;
    const INTERACTIVE = 'button, a[href], input:not([type="hidden"]), select, textarea, summary, [role="button"], [role="tab"], [role="menuitem"], [role="option"], [tabindex="0"]';
    const interactive = [...document.querySelectorAll(INTERACTIVE)].filter(visible);

    // text cut off by its own box: an ellipsis at work, or a control whose
    // words overflow it
    const clipped: string[] = [];
    for (const el of document.querySelectorAll('body *')) {
      if (!(el instanceof HTMLElement) || el.children.length > 3 || !el.innerText?.trim()) continue;
      if (el.matches('textarea, input, select, option, pre, code, .xterm *')) continue;
      // a screen reader's label is a 1px box on purpose
      if (el.clientWidth <= 2 || el.scrollWidth <= el.clientWidth + 1 || !visible(el)) continue;
      const s = getComputedStyle(el);
      const cuts = s.textOverflow === 'ellipsis' || (s.overflowX !== 'visible' && s.overflowX !== 'auto' && s.overflowX !== 'scroll');
      if (cuts) clipped.push(`${describe(el)} shows ${el.clientWidth}px of ${el.scrollWidth}px`);
    }

    const smallTargets: string[] = [];
    const unnamed: string[] = [];
    const covered: string[] = [];
    const floor = phone ? 44 : 24;
    for (const el of interactive) {
      const r = el.getBoundingClientRect();
      // a resizer is a thin edge by design
      if (!el.matches('[role="separator"]') && (r.width < floor || r.height < floor)) smallTargets.push(`${describe(el)} ${Math.round(r.width)}×${Math.round(r.height)}`);
      const name = (el.getAttribute('aria-label') || el.getAttribute('title') || (el as HTMLElement).innerText || el.getAttribute('aria-labelledby') || (el as HTMLInputElement).placeholder || '').trim();
      // a <details> summary is named by its content, which may be an icon
      // the browser draws itself
      if (!name && !el.matches('input[type="checkbox"], input[type="radio"], summary') && !el.closest('label')) unnamed.push(describe(el));
      // something else on top of a control's middle (a toast, a pinned bar);
      // with a dialog open everything behind it is covered on purpose
      if (!dialogEl || dialogEl.contains(el)) {
        const x = r.left + r.width / 2;
        const y = r.top + r.height / 2;
        if (x >= 0 && y >= 0 && x < vw && y < vh) {
          const top = document.elementFromPoint(x, y);
          if (top && top !== el && !el.contains(top) && !top.contains(el) && !(menuEl && menuEl.contains(top)) && !el.matches('summary')) covered.push(`${describe(el)} under ${describe(top)}`);
        }
      }
    }

    // text whose contrast against what is behind it is under WCAG AA:
    // 4.5:1, or 3:1 for large text (24px, or 18.66px bold)
    const rgb = (c: string) => (c.match(/[\d.]+/g) || []).map(Number);
    const lum = ([r, g, b]: number[]) => {
      const f = (v: number) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const backdrop = (el: Element | null): number[] => {
      for (; el; el = el.parentElement) {
        const c = rgb(getComputedStyle(el).backgroundColor);
        if (c.length >= 3 && (c.length < 4 || c[3] > 0.5)) return c.slice(0, 3);
      }
      return rgb(getComputedStyle(document.body).backgroundColor).slice(0, 3);
    };
    const lowContrast: string[] = [];
    const seenText = new Set<string>();
    for (const el of document.querySelectorAll('body *')) {
      if (!(el instanceof HTMLElement) || !visible(el) || el.clientWidth <= 2) continue;
      const own = [...el.childNodes].filter((n) => n.nodeType === Node.TEXT_NODE).map((n) => n.textContent || '').join('').trim();
      if (!own || el.closest('[disabled], [aria-disabled="true"], .xterm, pre, code')) continue;
      const s = getComputedStyle(el);
      const fg = rgb(s.color);
      if (fg.length >= 4 && fg[3] < 0.5) continue;
      const a = lum(fg.slice(0, 3)), b = lum(backdrop(el));
      const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
      const size = parseFloat(s.fontSize);
      const large = size >= 24 || (size >= 18.66 && Number(s.fontWeight) >= 700);
      if (ratio < (large ? 3 : 4.5)) {
        const key = `${describe(el)} ${ratio.toFixed(1)}:1`;
        if (!seenText.has(key)) { seenText.add(key); lowContrast.push(key); }
      }
    }

    const primaries = [...document.querySelectorAll('.btn.pri, button.pri, .send')]
      .filter(visible).filter((el) => !dialogEl || dialogEl.contains(el)).map(describe);

    const LEAK = /\b(undefined|null|NaN|\[object Object\])\b/;
    const leaks = document.body.innerText.split('\n').filter((l) => LEAK.test(l)).map((l) => l.trim().slice(0, 120));

    const text: Record<string, string> = {};
    const read = (key: string, sel: string) => {
      const el = [...document.querySelectorAll(sel)].find(visible) as HTMLElement | undefined;
      if (el?.innerText?.trim()) text[key] = el.innerText.trim().replace(/\n{2,}/g, '\n').slice(0, 800);
    };
    read('dialog', '[role="dialog"], dialog[open]');
    read('head', '[data-testid="card-head"]');
    read('mobileHead', '[data-testid="mobile-card"]');
    read('dock', '[data-testid="dock"]');
    read('mobileDecision', '[data-testid="mobile-decision"]');
    read('surface', '[data-testid="surface-head"]');
    read('pane', '[data-testid="panel-pane"]');
    read('toasts', '[data-testid="toasts"]');
    read('topbar', '[data-testid="topbar"]');
    read('rail', '[data-testid="rail-cards"]');
    // the thread's newest words: what the person last read there
    const thread = document.querySelector('[data-testid="thread-items"]') as HTMLElement | null;
    if (thread && visible(thread) && thread.innerText.trim()) text.thread = thread.innerText.trim().replace(/\n{2,}/g, '\n').slice(-800);

    return {
      viewport: { width: vw, height: vh },
      view: document.getElementById('app')?.getAttribute('data-view') ?? null,
      dialog: dialogEl ? describe(dialogEl).slice(0, 80) : null,
      sideways: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
      leaks: cap(leaks),
      clipped: cap(clipped),
      smallTargets: cap(smallTargets),
      unnamed: cap(unnamed),
      covered: cap(covered),
      lowContrast: cap(lowContrast),
      primaries,
      text,
    };
  }, phone);
}
