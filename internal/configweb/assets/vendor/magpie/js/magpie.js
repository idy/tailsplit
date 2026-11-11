// Magpie Design System: behaviour for the components in css/.
// A dependency-free ES module. Nothing runs on import; call what you need.

export { initSelect } from './select.js';

const THEMES = new Set(['light', 'dark']);

/**
 * Sets an explicit theme on an element (the root by default), or follows the
 * system again when `theme` is anything other than 'light' or 'dark'.
 */
export function setTheme(theme, root = document.documentElement) {
  if (THEMES.has(theme)) root.dataset.theme = theme;
  else delete root.dataset.theme;
}

let toastTimer = 0;

/**
 * Shows a short status message in a single shared `.mds-toast`, replacing
 * the previous one. The element is created on first use unless you pass one.
 */
export function toast(message, { duration = 3200, element } = {}) {
  let node = element || document.querySelector('.mds-toast');
  if (!node) {
    node = document.createElement('div');
    node.className = 'mds-toast';
    node.setAttribute('role', 'status');
    document.body.append(node);
  }
  clearTimeout(toastTimer);
  node.textContent = message;
  // Hide and reflow so the @starting-style entrance replays for back-to-back messages.
  node.hidden = true;
  void node.offsetWidth;
  node.hidden = false;
  toastTimer = setTimeout(() => { node.hidden = true; }, duration);
  return node;
}

/**
 * Opens a `<dialog>` modally and resolves with its returnValue when it
 * closes. Escape and backdrop dismissal resolve with `fallback`.
 * Buttons inside `<form method="dialog">` set the value with `value="…"`.
 */
export function openDialog(dialog, { fallback = 'cancel' } = {}) {
  return new Promise((resolve) => {
    dialog.returnValue = fallback;
    dialog.addEventListener('close', () => resolve(dialog.returnValue || fallback), { once: true });
    dialog.showModal();
  });
}

/**
 * Wires a `.mds-tabs[role=tablist]`: roving tabindex, Arrow/Home/End keys,
 * aria-selected and the `hidden` state of each tab's aria-controls panel.
 * `onSelect(tab)` may return (or resolve to) false to keep the current tab,
 * for example while a draft cannot be converted.
 */
export function initTabs(tablist, { onSelect } = {}) {
  const tabs = () => [...tablist.querySelectorAll('[role="tab"]')].filter((tab) => !tab.disabled);
  const current = () => tablist.querySelector('[role="tab"][aria-selected="true"]');

  function paint(selected) {
    for (const tab of tablist.querySelectorAll('[role="tab"]')) {
      const on = tab === selected;
      tab.setAttribute('aria-selected', String(on));
      tab.tabIndex = on ? 0 : -1;
      const panel = tab.getAttribute('aria-controls');
      if (panel) document.getElementById(panel)?.toggleAttribute('hidden', !on);
    }
  }

  async function select(tab, focus = false) {
    if (!tab || tab === current()) { if (focus) tab?.focus(); return true; }
    if (onSelect && (await onSelect(tab)) === false) return false;
    paint(tab);
    if (focus) tab.focus();
    return true;
  }

  tablist.addEventListener('click', (event) => {
    const tab = event.target.closest('[role="tab"]');
    if (tab && tablist.contains(tab) && !tab.disabled) select(tab);
  });
  tablist.addEventListener('keydown', (event) => {
    const list = tabs();
    const index = list.indexOf(document.activeElement);
    if (index < 0) return;
    const next = {
      ArrowRight: list[(index + 1) % list.length],
      ArrowLeft: list[(index - 1 + list.length) % list.length],
      Home: list[0],
      End: list[list.length - 1],
    }[event.key];
    if (!next) return;
    event.preventDefault();
    select(next, true);
  });

  paint(current() || tabs()[0]);
  return { select: (tab) => select(tab) };
}

/**
 * Marks one navigable `.mds-nav-item` as the current page and calls
 * `onSelect(item)`; a false result keeps the previous item.
 * `.mds-nav-parent` buttons disclose their aria-controls subnav without
 * navigating. Active ancestors receive data-active; selecting a descendant
 * expands its path. Only the current leaf receives aria-current="page".
 */
export function initNav(nav, { onSelect } = {}) {
  const items = () => [...nav.querySelectorAll('.mds-nav-item:not(.mds-nav-parent)')];
  const parents = () => [...nav.querySelectorAll('.mds-nav-parent')];
  const panelFor = (parent) => {
    const panel = nav.ownerDocument.getElementById(parent.getAttribute('aria-controls'));
    return panel && nav.contains(panel) ? panel : null;
  };
  function syncParents(reveal = false) {
    for (const parent of parents()) {
      const panel = panelFor(parent);
      if (!panel) continue;
      const current = panel.querySelector('.mds-nav-item[aria-current="page"]');
      parent.toggleAttribute('data-active', Boolean(current));
      if (current && reveal) parent.setAttribute('aria-expanded', 'true');
      panel.hidden = parent.getAttribute('aria-expanded') !== 'true';
    }
  }
  async function select(item) {
    if (!item || item.disabled || !items().includes(item)) return false;
    if (item.getAttribute('aria-current')) { syncParents(true); return true; }
    if (onSelect && (await onSelect(item)) === false) return false;
    for (const other of items()) {
      if (other === item) other.setAttribute('aria-current', 'page');
      else other.removeAttribute('aria-current');
    }
    syncParents(true);
    return true;
  }
  nav.addEventListener('click', (event) => {
    const item = event.target.closest('.mds-nav-item');
    if (!item || !nav.contains(item) || item.disabled) return;
    if (item.classList.contains('mds-nav-parent')) {
      if (!panelFor(item)) return;
      item.setAttribute('aria-expanded', String(item.getAttribute('aria-expanded') !== 'true'));
      syncParents();
    } else if (item.tagName === 'BUTTON') select(item);
  });
  syncParents(true);
  return { select };
}

/**
 * Finds the native window bridge, if the page runs inside one.
 * macOS: a WKWebView script message handler named `macHandler`.
 * Windows: WebView2's `window.chrome.webview`.
 * Returns `{ platform, host }` or null in an ordinary browser tab.
 */
export function detectWindowHost(macHandler) {
  const mac = macHandler && window.webkit?.messageHandlers?.[macHandler];
  if (mac) return { platform: 'macos', host: mac };
  const windows = window.chrome?.webview;
  if (windows) return { platform: 'windows', host: windows };
  return null;
}

/**
 * Makes `.mds-titlebar` behave like native chrome inside a frameless window:
 * a real drag (4px or more) posts 'drag', double-click posts 'zoom', and
 * `[data-window-action]` buttons post their action ('minimize', 'zoom',
 * 'close'). Sets `data-window-chrome` on the root and shows
 * `.mds-window-controls` on Windows only, where the page draws them.
 */
export function initWindowChrome(titlebar, bridge) {
  if (!bridge) return;
  const { platform, host } = bridge;
  document.documentElement.dataset.windowChrome = platform;
  for (const controls of titlebar.querySelectorAll('.mds-window-controls')) controls.hidden = platform !== 'windows';

  const isBackground = (event) => !event.target.closest('button, a, input, select, textarea, [role="button"]');
  let press = null;
  titlebar.addEventListener('mousedown', (event) => {
    press = null;
    if (event.button !== 0 || event.detail > 1 || !isBackground(event)) return;
    event.preventDefault();
    press = { x: event.clientX, y: event.clientY };
  });
  // Hand off only an actual drag: entering a native move loop on mouse-down
  // would swallow the click sequence that double-click zoom needs.
  window.addEventListener('mousemove', (event) => {
    if (!(event.buttons & 1)) press = null;
    if (!press || Math.hypot(event.clientX - press.x, event.clientY - press.y) < 4) return;
    press = null;
    host.postMessage('drag');
  });
  window.addEventListener('mouseup', () => { press = null; });
  window.addEventListener('blur', () => { press = null; });
  titlebar.addEventListener('dblclick', (event) => {
    if (event.button !== 0 || !isBackground(event)) return;
    event.preventDefault();
    host.postMessage('zoom');
  });
  for (const button of titlebar.querySelectorAll('[data-window-action]')) {
    button.addEventListener('click', () => host.postMessage(button.dataset.windowAction));
  }
}
