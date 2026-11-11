(() => {
  'use strict';

  const toggle = document.querySelector('#page-companion');
  const errorLabel = document.querySelector('#page-companion-error');
  const retryButton = document.querySelector('#page-companion-retry');
  const locale = window.TailSplitI18n;
  const preferences = window.TailSplitCompanionPreferences;
  const leasesByPet = new WeakMap();
  const activeHosts = new Map();
  const wiredHosts = new WeakSet();
  const hostPrefix = 'tailsplit-peetty-' + (window.crypto?.randomUUID?.() || Math.random().toString(36).slice(2)) + '-';
  let hostSerial = 0;
  const timeoutMS = 10000;
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
    const pending = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      const previous = window.TailSplitBundledPeetty;
      let settled = false;
      let timer = null;
      const fail = () => {
        if (settled) return;
        settled = true;
        window.clearTimeout(timer);
        script.remove();
        reject(new Error('Companion fallback unavailable.'));
      };
      script.src = '/vendor/peetty/fallback.js';
      script.onload = () => {
        if (settled) return;
        const loaded = window.TailSplitBundledPeetty;
        if (!loaded || loaded === previous || typeof loaded.createPeetty !== 'function') { fail(); return; }
        settled = true;
        window.clearTimeout(timer);
        sdk = loaded;
        bundled = true;
        resolve();
      };
      script.onerror = fail;
      timer = window.setTimeout(fail, timeoutMS);
      try { document.head.append(script); } catch (_) { fail(); }
    });
    fallbackLoading = pending;
    void pending.catch(() => { if (fallbackLoading === pending) fallbackLoading = null; });
    return pending;
  }

  // The SDK facade gives each instance a private tag. Native cloning/importing
  // preserves that tag, so copied hosts can be adopted without a creation hook.
  function registerHost(lease, host) {
    if (host.localName !== lease.tag) {
      host.remove();
      throw new Error('Unisolated companion host.');
    }
    if (lease.cancelled) void Promise.resolve().then(() => { host.remove(); });
    else lease.hosts.add(host);
  }

  function wireHost(host) {
    if (wiredHosts.has(host)) return;
    // Capture composed clicks on the host, including controls created later
    // inside its open shadow tree. SDK handlers may stop propagation or remove
    // the UI, so persist after dispatch and recheck the instance's ownership.
    host.addEventListener('click', (event) => {
      const owner = activeHosts.get(host.localName);
      if (!owner || owner.cancelled || !host.isConnected) return;
      let hide = false;
      for (const node of event.composedPath()) {
        if (node === host) break;
        if (node?.localName === 'peetty-companion' || node?.localName?.startsWith('tailsplit-peetty-')) return;
        if (node?.getAttribute?.('data-action') === 'hide') hide = true;
      }
      if (hide) void Promise.resolve().then(() => {
        if (activeHosts.get(host.localName) === owner && !owner.cancelled) void save(false);
      });
    }, true);
    wiredHosts.add(host);
  }

  function adoptHost(host) {
    if (host.localName === 'peetty-companion') {
      host.remove();
    } else if (host.localName?.startsWith(hostPrefix)) {
      const owner = activeHosts.get(host.localName);
      if (!owner || owner.cancelled) host.remove();
      else { owner.hosts.add(host); wireHost(host); }
    }
  }

  function adoptHosts(root) {
    if (root.nodeType === 1) adoptHost(root);
    if (root.querySelectorAll) {
      for (const host of root.querySelectorAll('*')) adoptHost(host);
    }
  }

  function disposeLease(lease, retryCleanup = false) {
    lease.cancelled = true;
    activeHosts.delete(lease.tag);
    window.clearTimeout(lease.timer);
    lease.release();
    if (!lease.disposed || retryCleanup) {
      lease.disposed = true;
      try { if (typeof lease.instance?.destroy === 'function') lease.instance.destroy(); }
      catch (_) { /* Host ownership does not depend on successful SDK cleanup. */ }
    }
    for (const host of lease.hosts) host.remove();
    lease.hosts.clear();
    leasesByPet.delete(lease.instance);
  }

  function dispose(instance) {
    const lease = leasesByPet.get(instance);
    if (lease) disposeLease(lease);
  }

  async function mountOne(current) {
    if (current !== generation || !ready || !enabled || suspended || document.hidden || saving) return;
    const lease = { tag: hostPrefix + (++hostSerial), instance: null, hosts: new Set(), cancelled: false, disposed: false, timer: null, release: null };
    activeHosts.set(lease.tag, lease);
    const cancelled = new Promise((resolve) => { lease.release = resolve; });
    let committed = false;
    try {
      const next = sdk.createPeetty({
        figure: 'miru', size: 88, activity: 'lively',
        language: locale.language, greet: false, chat: false, zIndex: 20,
      }, (host) => registerHost(lease, host), lease.tag);
      lease.instance = next;
      if (!next || typeof next.mount !== 'function' || typeof next.destroy !== 'function' || typeof next.setLanguage !== 'function') {
        throw new Error('Incompatible companion instance.');
      }
      leasesByPet.set(next, lease);
      pet = next;
      const deadline = new Promise((_, reject) => {
        lease.timer = window.setTimeout(() => reject(new Error('Companion mount timed out.')), timeoutMS);
      });
      const mounting = Promise.resolve(next.mount());
      const settled = () => {
        // Observe the original mount after the cancellation/deadline race has
        // returned, so late listeners and animation work get another teardown.
        if (lease.cancelled) disposeLease(lease, true);
      };
      void mounting.then(settled, settled);
      await Promise.race([mounting, cancelled, deadline]);
      if (lease.cancelled || current !== generation || pet !== next) return;
      adoptHosts(document.documentElement);
      const host = [...lease.hosts].find((candidate) => candidate.isConnected && candidate.shadowRoot);
      if (!host) throw new Error('Missing companion host.');
      wireHost(host);
      committed = true;
    } finally {
      window.clearTimeout(lease.timer);
      if (!committed || lease.cancelled || current !== generation || pet !== lease.instance) {
        disposeLease(lease);
        if (pet === lease.instance) pet = null;
      }
    }
  }

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
      await mountOne(current);
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
    new window.MutationObserver((records) => {
      for (const record of records) {
        for (const node of record.addedNodes) adoptHosts(node);
      }
      checkCompatibility();
    }).observe(document.documentElement, { childList: true, characterData: true, subtree: true });
  }
  adoptHosts(document.documentElement);

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
