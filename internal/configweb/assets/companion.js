(() => {
  'use strict';

  const toggle = document.querySelector('#page-companion');
  const errorLabel = document.querySelector('#page-companion-error');
  const retryButton = document.querySelector('#page-companion-retry');
  const locale = window.TailSplitI18n;
  const preferences = window.TailSplitCompanionPreferences;
  const hostsByPet = new WeakMap();
  let mountTail = Promise.resolve();
  let mountLease = null;
  let enabled = false;
  let ready = false;
  let saving = false;
  let suspended = false;
  let loading = null;
  let errorKey = '';
  let errorKind = '';
  let retryAction = null;
  let pet = null;
  let generation = 0;
  let bundled = !document.querySelector('script[data-peetty-release]');
  let fallbackLoading = null;
  let sdk = window.Peetty;
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)');
  toggle.checked = false;
  toggle.disabled = true;

  function renderError() {
    errorLabel.textContent = errorKey ? locale.t(errorKey) : '';
    errorLabel.hidden = !errorKey;
    retryButton.hidden = !retryAction;
  }

  function showError(key, retry = null, kind = 'companion') {
    // Saving a preference is the user's outstanding operation. A subsequent
    // failed mount must not replace its explanation or its retry action.
    if (key && kind === 'companion' && errorKind === 'preference') return;
    errorKey = key;
    errorKind = key ? kind : '';
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
      const previous = window.TailSplitBundledPeetty;
      const fail = () => { script.remove(); fallbackLoading = null; reject(new Error('Companion fallback unavailable.')); };
      script.src = '/vendor/peetty/fallback.js';
      script.onload = () => {
        const loaded = window.TailSplitBundledPeetty;
        if (!loaded || loaded === previous || typeof loaded.createPeetty !== 'function') { fail(); return; }
        sdk = loaded;
        bundled = true;
        resolve();
      };
      script.onerror = fail;
      document.head.append(script);
    });
    return fallbackLoading;
  }

  function rememberHosts(lease) {
    for (const host of document.querySelectorAll('peetty-companion')) {
      if (!lease.previous.has(host)) lease.hosts.add(host);
    }
  }

  function removeHosts(hosts) {
    for (const host of hosts || []) host.remove();
  }

  function dispose(instance) {
    if (mountLease && mountLease.instance === instance) rememberHosts(mountLease);
    try { if (typeof instance?.destroy === 'function') instance.destroy(); }
    catch (_) { /* A broken CDN cleanup must not prevent embedded recovery. */ }
    finally {
      removeHosts(hostsByPet.get(instance));
      hostsByPet.delete(instance);
    }
  }

  // Only one SDK mount may own new DOM hosts at a time. Its lease survives
  // cancellation until the mount settles and its additions have been removed.
  // This makes cleanup independent of the SDK's private instance fields and
  // prevents an old mount from claiming or deleting a replacement's host.
  async function mountOne(current) {
    if (current !== generation || !ready || !enabled || suspended || document.hidden || saving) return;
    const lease = { current, instance: null, previous: new Set(document.querySelectorAll('peetty-companion')), hosts: new Set() };
    mountLease = lease;
    let next = null;
    let committed = false;
    try {
      next = sdk.createPeetty({
        figure: 'miru', size: 88, activity: 'lively',
        language: locale.language, greet: false, chat: false, zIndex: 20,
      });
      lease.instance = next;
      if (!next || typeof next.mount !== 'function' || typeof next.destroy !== 'function' || typeof next.setLanguage !== 'function') {
        throw new Error('Incompatible companion instance.');
      }
      hostsByPet.set(next, lease.hosts);
      pet = next;
      await next.mount();
      rememberHosts(lease);
      if (current !== generation || pet !== next) return;
      const live = new Set(document.querySelectorAll('peetty-companion'));
      const host = [...lease.hosts].find((candidate) => live.has(candidate) && candidate.shadowRoot?.querySelector('[data-action="hide"]'));
      if (!host) throw new Error('Missing companion host.');
      host.shadowRoot.querySelector('[data-action="hide"]').addEventListener('click', () => { void save(false); });
      committed = true;
    } finally {
      rememberHosts(lease);
      if (!committed || current !== generation || pet !== next) {
        dispose(next);
        removeHosts(lease.hosts);
        if (pet === next) pet = null;
      }
      mountLease = null;
    }
  }

  // Upstream can abandon a scan on any oversized text node, including YAML
  // comments and values. Inspect text nodes rather than total container text.
  function needsBundled() {
    if (reducedMotion?.matches) return true;
    const walker = document.createTreeWalker(document.body, 4 /* SHOW_TEXT */);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (node.data.length > 16384 && !node.parentElement?.closest('script,style,noscript')) return true;
    }
    return false;
  }

  async function sync() {
    if (!ready || !enabled || suspended || document.hidden) {
      generation++;
      dispose(pet);
      pet = null;
      return;
    }
    if (pet || saving) return;
    const current = ++generation;
    let fallbackAttempted = false;
    try {
      if (!bundled && needsBundled()) {
        fallbackAttempted = true;
        await useBundled();
      }
      if (current !== generation) return;
      const task = mountTail.then(() => mountOne(current));
      mountTail = task.catch(() => {});
      await task;
    } catch (_) {
      if (current !== generation) return;
      if (!bundled && !fallbackAttempted) {
        try {
          await useBundled();
          if (current === generation) void sync();
          return;
        } catch (_) { /* A retry can reload the embedded fallback. */ }
      }
      if (current !== generation) return;
      showError('无法显示页面小人，请重试。', sync);
    }
  }

  function checkCompatibility() {
    if (bundled || !pet || !needsBundled()) return;
    generation++;
    dispose(pet);
    pet = null;
    void sync();
  }

  reducedMotion?.addEventListener('change', checkCompatibility);
  if (window.MutationObserver) {
    new window.MutationObserver(() => {
      if (mountLease) {
        rememberHosts(mountLease);
        if (mountLease.current !== generation) removeHosts(mountLease.hosts);
      }
      checkCompatibility();
    }).observe(document.body, { childList: true, characterData: true, subtree: true });
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
      showError('无法保存页面小人偏好，请重试。', () => save(value), 'preference');
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
        showError('无法读取页面小人偏好，请重试。', load, 'preference');
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
