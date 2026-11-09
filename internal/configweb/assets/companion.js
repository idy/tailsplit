(() => {
  'use strict';

  const toggle = document.querySelector('#page-companion');
  const locale = window.TailSplitI18n;
  const storageKey = 'tailsplit.page-companion';
  let enabled = true;
  let pet = null;
  let generation = 0;
  try { enabled = localStorage.getItem(storageKey) !== 'off'; } catch (_) { /* Storage is optional. */ }
  toggle.checked = enabled;

  function remember() {
    try { localStorage.setItem(storageKey, enabled ? 'on' : 'off'); } catch (_) { /* Keep the session preference. */ }
  }

  async function sync() {
    if (!enabled || document.hidden) {
      generation++;
      pet?.destroy();
      pet = null;
      return;
    }
    if (pet) return;
    const current = ++generation;
    const next = window.Peetty.createPeetty({
      figure: 'miru', size: 88, activity: 'lively',
      language: locale.language, greet: false, chat: false, zIndex: 20,
    });
    pet = next;
    try {
      await next.mount();
      if (current !== generation || pet !== next) {
        next.destroy();
        return;
      }
      // The companion's own Hide action shares the General settings switch.
      document.querySelector('peetty-companion').shadowRoot
        .querySelector('[data-action="hide"]').addEventListener('click', () => {
          enabled = false;
          toggle.checked = false;
          remember();
          void sync();
        });
    } catch (error) {
      next.destroy();
      if (pet === next) {
        pet = null;
        enabled = false;
        toggle.checked = false;
        console.warn('TailSplit companion could not start.', error);
      }
    }
  }

  toggle.addEventListener('change', () => {
    enabled = toggle.checked;
    remember();
    void sync();
  });
  locale.subscribe(() => pet?.setLanguage(locale.language));
  document.addEventListener('visibilitychange', () => { void sync(); });
  window.addEventListener('pagehide', () => { generation++; pet?.destroy(); pet = null; });
  window.addEventListener('pageshow', () => { void sync(); });
  void sync();
})();
