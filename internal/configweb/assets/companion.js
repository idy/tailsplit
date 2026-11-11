(() => {
  'use strict';

  const toggle = document.querySelector('#page-companion');
  const errorLabel = document.querySelector('#page-companion-error');
  const retryButton = document.querySelector('#page-companion-retry');
  const locale = window.TailSplitI18n;
  const preferences = window.TailSplitCompanionPreferences;
  let enabled = false;
  let ready = false;
  let saving = false;
  let suspended = false;
  let loading = null;
  let errorKey = '';
  let retryAction = null;
  let pet = null;
  let generation = 0;
  let bundled = !document.querySelector('script[data-peetty-release]');
  let fallbackLoading = null;
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)');
  toggle.checked = false;
  toggle.disabled = true;

  function renderError() {
    errorLabel.textContent = errorKey ? locale.t(errorKey) : '';
    errorLabel.hidden = !errorKey;
    retryButton.hidden = !retryAction;
  }

  function showError(key, retry = null) {
    errorKey = key;
    retryAction = retry;
    renderError();
  }

  function readEnabled(value) {
    if (typeof value?.enabled !== 'boolean') throw new Error('Invalid companion preference.');
    return value.enabled;
  }

  function useBundled() {
    if (bundled) return Promise.resolve();
    if (fallbackLoading) return fallbackLoading;
    fallbackLoading = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = '/vendor/peetty/peetty.global.js';
      script.onload = () => { bundled = true; resolve(); };
      script.onerror = () => { script.remove(); fallbackLoading = null; reject(new Error('Companion fallback unavailable.')); };
      document.head.append(script);
    });
    return fallbackLoading;
  }

  // The embedded build retains TailSplit's accessibility and long-log fixes
  // until those changes are available in the upstream CDN distribution.
  function needsBundled() {
    return reducedMotion?.matches || (document.querySelector('#desktop-log-text')?.textContent?.length || 0) > 16384;
  }

  async function sync() {
    if (!ready || !enabled || suspended || document.hidden) {
      generation++;
      pet?.destroy();
      pet = null;
      return;
    }
    if (pet || saving) return;
    const current = ++generation;
    let next = null;
    let fallbackAttempted = false;
    try {
      if (!bundled && needsBundled()) {
        fallbackAttempted = true;
        await useBundled();
      }
      if (current !== generation) return;
      next = window.Peetty.createPeetty({
        figure: 'miru', size: 88, activity: 'lively',
        language: locale.language, greet: false, chat: false, zIndex: 20,
      });
      pet = next;
      await next.mount();
      if (current !== generation || pet !== next) {
        next.destroy();
        return;
      }
      document.querySelector('peetty-companion').shadowRoot
        .querySelector('[data-action="hide"]').addEventListener('click', () => { void save(false); });
    } catch (_) {
      next?.destroy();
      if (current !== generation) return;
      if (pet === next) pet = null;
      if (!bundled && !fallbackAttempted) {
        try {
          await useBundled();
          if (current === generation) void sync();
          return;
        } catch (_) { /* A retry can reload the embedded fallback. */ }
      }
      showError('无法显示页面小人，请重试。', sync);
    }
  }

  function checkCompatibility() {
    if (bundled || !needsBundled()) return;
    generation++;
    pet?.destroy();
    pet = null;
    void sync();
  }

  reducedMotion?.addEventListener('change', checkCompatibility);
  const log = document.querySelector('#desktop-log-text');
  if (log && window.MutationObserver) {
    new window.MutationObserver(checkCompatibility).observe(log, { childList: true, characterData: true, subtree: true });
  }

  async function save(value) {
    if (!ready || saving) { toggle.checked = enabled; return; }
    const previous = enabled;
    enabled = value;
    toggle.checked = enabled;
    saving = true;
    toggle.disabled = true;
    showError('');
    void sync();
    try {
      enabled = readEnabled(await preferences.save(value));
    } catch (_) {
      enabled = previous;
      showError('无法保存页面小人偏好，请重试。', () => save(value));
    } finally {
      saving = false;
      toggle.disabled = false;
      toggle.checked = enabled;
      void sync();
    }
  }

  function load() {
    if (ready || loading) return loading;
    loading = (async () => {
      try {
        enabled = readEnabled(await preferences.load());
        ready = true;
        toggle.checked = enabled;
        toggle.disabled = false;
        showError('');
        void sync();
      } catch (_) {
        showError('无法读取页面小人偏好，请重试。', load);
      } finally { loading = null; }
    })();
    return loading;
  }

  toggle.addEventListener('change', () => { void save(toggle.checked); });
  retryButton.addEventListener('click', () => { const retry = retryAction; showError(''); void retry?.(); });
  locale.subscribe(() => { pet?.setLanguage(locale.language); renderError(); });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && !ready) void load();
    else void sync();
  });
  window.addEventListener('pagehide', () => { suspended = true; void sync(); });
  window.addEventListener('pageshow', () => { suspended = false; if (ready) void sync(); else void load(); });
  void load();
})();
