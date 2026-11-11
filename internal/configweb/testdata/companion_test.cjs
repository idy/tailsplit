const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const { join } = require('node:path');
const { runInNewContext } = require('node:vm');

const source = readFileSync(join(__dirname, '../assets/companion.js'), 'utf8');
const flush = () => new Promise(setImmediate);

async function setup(options = {}) {
  const toggle = new EventTarget();
  const error = {};
  const retry = new EventTarget();
  const document = new EventTarget();
  document.hidden = false;
  let notifyHosts = () => {};
  const overlays = new class extends Set {
    add(instance) { super.add(instance); queueMicrotask(() => notifyHosts(instance.host)); return this; }
    delete(instance) { const removed = super.delete(instance); if (removed) queueMicrotask(() => notifyHosts(instance.host)); return removed; }
  }();
  const log = { textContent: options.log || '' };
  const preview = { textContent: options.yaml || '' };
  const content = [log, preview, ...(options.texts || []).map((textContent) => ({ textContent }))];
  document.body = { nodeType: 1, localName: 'body' };
  document.documentElement = { nodeType: 1, localName: 'html', querySelectorAll: () => [...overlays].map((instance) => instance.host) };
  document.querySelectorAll = () => [...overlays].map((instance) => instance.host);
  document.createTreeWalker = () => {
    let index = 0;
    return { nextNode() {
      const element = content[index++];
      return element ? { data: element.textContent, parentElement: { closest: () => null } } : null;
    } };
  };
  document.querySelector = (selector) => selector === '#page-companion' ? toggle : selector === '#page-companion-error' ? error : selector === '#page-companion-retry' ? retry : selector === '#desktop-log-text' ? log : selector === '#yaml-preview' ? preview : selector === 'script[data-peetty-release]' ? (options.updated ? {} : null) : [...overlays][0]?.host;
  const saved = options.saved || { enabled: true };
  const saves = [];
  const preferences = options.preferences || {
    load: async () => ({ enabled: saved.enabled }),
    save: async (enabled) => { saves.push(enabled); saved.enabled = enabled; return { enabled }; },
  };
  const instances = [];
  const resources = new Set();
  const window = new EventTarget();
  window.TailSplitI18n = { language: 'en', t: (key) => key, subscribe() {} };
  window.TailSplitCompanionPreferences = preferences;
  const timers = new Map();
  let nextTimer = 0;
  window.setTimeout = (callback) => { timers.set(++nextTimer, callback); return nextTimer; };
  window.clearTimeout = (id) => { timers.delete(id); };
  const expireTimers = () => { for (const [id, callback] of [...timers]) { timers.delete(id); callback(); } };
  window.Peetty = { createPeetty(_options, onHost = () => {}, hostTag = 'peetty-companion') {
    let finish;
    const pending = new Promise((resolve) => { finish = resolve; });
    const hide = new EventTarget();
    let settled = false;
    const instance = {
      cleanupCalls: 0,
      host: { nodeType: 1, localName: options.unscopedRoot && instances.length === 0 ? 'peetty-companion' : hostTag, get isConnected() { return overlays.has(instance); }, querySelectorAll: () => [], insertionRoot: options.outsideBody ? document.documentElement : document.body, shadowRoot: { querySelector: () => hide }, remove: () => { overlays.delete(instance); } }, hide, finish,
      // Model a mount whose asynchronous work completes after early disposal.
      mount: () => pending.then(() => {
        settled = true;
        if (options.installLateResources && instances[0] === instance) resources.add(instance);
        if (options.mountRejects && instances[0] === instance) {
          if (options.partialMount) overlays.add(instance);
          throw new Error('Mount failed');
        }
        instance.rendered = options.cloneTemplate ? instance.copyHost() : instance;
        if (options.cloneTemplate) instance.hide = instance.rendered.hide;
        overlays.add(instance.rendered);
      }),
      destroy: () => {
        instance.cleanupCalls++;
        if (options.destroyWhilePending && !settled && instances[0] === instance) throw new Error('Initialization still pending');
        resources.delete(instance);
        if (options.destroyBeforeRemoval && instances[0] === instance) throw new Error('Cleanup failed before removing host');
        overlays.delete(instance.rendered || instance);
        if (options.destroyThrows && instances[0] === instance) throw new Error('Cleanup failed');
      },
      setLanguage() {},
    };
    if (options.destroyMissing && instances.length === 0) instance.destroy = undefined;
    if (options.destroyNonCallable && instances.length === 0) instance.destroy = 'invalid';
    instance.copyHost = () => {
      const copy = { hide: new EventTarget() };
      copy.host = { ...instance.host, shadowRoot: { querySelector: () => copy.hide }, get isConnected() { return overlays.has(copy); }, remove: () => { overlays.delete(copy); } };
      return copy;
    };
    if (!(options.unscopedRoot && instances.length === 0)) onHost(instance.host);
    instances.push(instance);
    return instance;
  } };
  const scripts = [];
  const motion = new EventTarget();
  motion.matches = !!options.reducedMotion;
  window.matchMedia = () => motion;
  let logChanged = () => {};
  const observations = [];
  window.MutationObserver = class {
    constructor(callback) { this.callback = callback; logChanged = () => callback([{ addedNodes: [document.documentElement] }]); }
    observe(root) {
      observations.push(root);
      notifyHosts = (host) => {
        if (root === document.documentElement || host.insertionRoot === root) this.callback([{ addedNodes: [host] }]);
      };
    }
  };
  document.createElement = () => ({ remove() {} });
  document.head = { append(script) {
    script.finishLoad = () => {
      window.TailSplitBundledPeetty = { createPeetty: factory };
      script.onload();
    };
    scripts.push(script);
  } };
  const factory = window.Peetty.createPeetty;
  if (options.createThrows) window.Peetty.createPeetty = () => { throw new Error('Incompatible SDK'); };
  runInNewContext(source, { window, document, console });
  const setEnabled = (enabled) => { toggle.checked = enabled; toggle.dispatchEvent(new Event('change')); };
  const setHidden = (hidden) => { document.hidden = hidden; document.dispatchEvent(new Event('visibilitychange')); };
  await flush();
  return { toggle, error, retry, document, window, instances, overlays, saves, saved, setEnabled, setHidden, scripts, motion, log, preview, logChanged, factory, timers, expireTimers, observations, resources };
}

for (const interruption of ['toggle off', 'document hidden', 'page hidden']) {
  test(`late mount is removed after ${interruption}`, async () => {
    const state = await setup();
    if (interruption === 'toggle off') state.setEnabled(false);
    else if (interruption === 'document hidden') state.setHidden(true);
    else state.window.dispatchEvent(new Event('pagehide'));
    state.instances[0].finish();
    await flush();
    assert.equal(state.overlays.size, 0, 'a canceled companion must not reappear after mount completes');
  });
}

test('late old mount cannot claim the replacement Hide action', async () => {
  const state = await setup();
  state.setEnabled(false);
  await flush();
  state.setEnabled(true);
  await flush();
  assert.equal(state.instances.length, 2, 'replacement must not wait for a canceled mount');
  const [old, active] = state.instances;
  active.finish();
  await flush();
  old.finish();
  await flush();
  assert.deepEqual([...state.overlays], [active]);
  active.hide.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(state.toggle.checked, false);
  assert.equal(state.saved.enabled, false);
  assert.equal(state.overlays.size, 0);
});

test('a page restored before its old mount settles keeps only the restored companion', async () => {
  const state = await setup();
  state.window.dispatchEvent(new Event('pagehide'));
  state.window.dispatchEvent(new Event('pageshow'));
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.equal(state.toggle.checked, true);
});

test('saved disabled preference prevents mounting before or after a new page opens', async () => {
  const saved = { enabled: true };
  const first = await setup({ saved });
  first.setEnabled(false);
  await flush();
  const next = await setup({ saved });
  assert.equal(next.toggle.checked, false);
  assert.equal(next.instances.length, 0);
  assert.equal(next.toggle.disabled, false);
});

test('an unfinished initial preference read cannot mount into a hidden page', async () => {
  let resolve;
  const state = await setup({ preferences: { load: () => new Promise((done) => { resolve = done; }) } });
  assert.equal(state.toggle.disabled, true);
  assert.equal(state.instances.length, 0);
  state.window.dispatchEvent(new Event('pagehide'));
  resolve({ enabled: true });
  await flush();
  assert.equal(state.instances.length, 0);
  state.window.dispatchEvent(new Event('pageshow'));
  await flush();
  assert.equal(state.instances.length, 1);
});

test('failed saves restore the persisted visibility and expose a retryable error', async () => {
  const state = await setup({ preferences: {
    load: async () => ({ enabled: true }),
    save: async () => { throw new Error('disk full'); },
  } });
  state.setEnabled(false);
  await flush();
  assert.equal(state.toggle.checked, true);
  assert.equal(state.toggle.disabled, false);
  assert.equal(state.error.hidden, false);
  assert.match(state.error.textContent, /无法保存/);
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.match(state.error.textContent, /无法保存/);
});

for (const recovery of ['retry button', 'visible panel']) {
  test(`a failed preference read recovers through the ${recovery}`, async () => {
    let reads = 0;
    const state = await setup({ preferences: { load: async () => {
      if (++reads === 1) throw new Error('transient failure');
      return { enabled: false };
    } } });
    assert.equal(state.retry.hidden, false);
    if (recovery === 'retry button') state.retry.dispatchEvent(new Event('click'));
    else { state.setHidden(true); state.setHidden(false); }
    await flush();
    assert.equal(reads, 2);
    assert.equal(state.toggle.disabled, false);
    assert.equal(state.toggle.checked, false);
    assert.equal(state.error.hidden, true);
    assert.equal(state.retry.hidden, true);
  });
}

test('save retry repeats the failed user choice instead of the restored value', async () => {
  const writes = [];
  const state = await setup({ preferences: {
    load: async () => ({ enabled: true }),
    save: async (enabled) => {
      writes.push(enabled);
      if (writes.length === 1) throw new Error('disk temporarily unavailable');
      return { enabled };
    },
  } });
  state.setEnabled(false);
  await flush();
  assert.equal(state.toggle.checked, true);
  state.retry.dispatchEvent(new Event('click'));
  await flush();
  assert.deepEqual(writes, [false, false]);
  assert.equal(state.toggle.checked, false);
  assert.equal(state.error.hidden, true);
});

test('failed or invalid preference reads do not flash an unwanted companion', async () => {
  for (const load of [async () => { throw new Error('unavailable'); }, async () => ({ enabled: 'false' })]) {
    const state = await setup({ preferences: { load } });
    assert.equal(state.instances.length, 0);
    assert.equal(state.toggle.checked, false);
    assert.equal(state.toggle.disabled, true);
    assert.equal(state.error.hidden, false);
  }
});

for (const options of [{ reducedMotion: true }, { log: 'x'.repeat(16385) }]) {
  test(`updated runtime preserves embedded compatibility for ${Object.keys(options)[0]}`, async () => {
    const state = await setup({ updated: true, ...options });
    assert.equal(state.instances.length, 0);
    assert.equal(state.scripts.length, 1);
    assert.equal(state.scripts[0].src, '/vendor/peetty/fallback.js');
    state.scripts[0].finishLoad();
    await flush();
    assert.equal(state.instances.length, 1);
    state.instances[0].finish();
    await flush();
    assert.equal(state.overlays.size, 1);
  });
}

for (const failure of ['mountRejects', 'createThrows']) {
  test(`updated SDK ${failure} falls back without exposing an error`, async () => {
    const state = await setup({ updated: true, [failure]: true });
    if (failure === 'mountRejects') {
      state.instances[0].finish();
      await flush();
    }
    assert.equal(state.scripts.length, 1);
    state.window.Peetty.createPeetty = state.factory;
    state.scripts[0].finishLoad();
    await flush();
    state.instances.at(-1).finish();
    await flush();
    assert.equal(state.overlays.size, 1);
    assert.equal(state.error.hidden, true);
  });
}

test('late updated mount is removed when diagnostic logs trigger compatibility fallback', async () => {
  const state = await setup({ updated: true });
  const old = state.instances[0];
  state.log.textContent = '😀'.repeat(9000);
  state.logChanged();
  state.logChanged();
  assert.equal(state.scripts.length, 1, 'concurrent fallback requests share one script load');
  state.scripts[0].finishLoad();
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  old.finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
});

test('a hidden page cannot mount after its fallback script finishes loading', async () => {
  const state = await setup({ updated: true, reducedMotion: true });
  state.window.dispatchEvent(new Event('pagehide'));
  state.scripts[0].finishLoad();
  await flush();
  assert.equal(state.instances.length, 0);
  state.window.dispatchEvent(new Event('pageshow'));
  await flush();
  assert.equal(state.instances.length, 1);
});

test('a failed fallback download exposes an error and retry reloads the embedded script', async () => {
  const state = await setup({ updated: true, reducedMotion: true });
  state.scripts[0].onerror();
  await flush();
  assert.equal(state.error.hidden, false);
  assert.equal(state.retry.hidden, false);
  state.retry.dispatchEvent(new Event('click'));
  assert.equal(state.scripts.length, 2);
  state.scripts[1].finishLoad();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.equal(state.overlays.size, 1);
  assert.equal(state.error.hidden, true);
});

test('oversized YAML text uses the embedded runtime before mounting', async () => {
  const state = await setup({ updated: true, yaml: '# ' + 'x'.repeat(16385) });
  assert.equal(state.instances.length, 0);
  assert.equal(state.scripts.length, 1);
  state.scripts[0].finishLoad();
  await flush();
  assert.equal(state.instances.length, 1);
});

test('YAML changes interrupt a pending CDN mount and select embedded compatibility', async () => {
  const state = await setup({ updated: true });
  state.preview.textContent = 'x'.repeat(16385);
  state.logChanged();
  assert.equal(state.scripts.length, 1);
  state.scripts[0].finishLoad();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
});

test('many small text nodes do not select the large-text fallback', async () => {
  const state = await setup({ updated: true, texts: ['x'.repeat(10000), 'y'.repeat(10000)] });
  assert.equal(state.scripts.length, 0);
  assert.equal(state.instances.length, 1);
});

for (const failure of ['destroyMissing', 'destroyNonCallable', 'destroyThrows']) {
  test(`${failure} does not prevent embedded recovery`, async () => {
    const state = await setup({ updated: true, [failure]: true, mountRejects: failure === 'destroyThrows' });
    if (failure === 'destroyThrows') { state.instances[0].finish(); await flush(); }
    assert.equal(state.scripts.length, 1);
    state.scripts[0].finishLoad();
    await flush();
    state.instances[1].finish();
    await flush();
    assert.equal(state.overlays.size, 1);
    assert.equal(state.error.hidden, true);
  });
}

for (const interruption of ['disabled', 'document hidden', 'page hidden']) {
  test(`a failed fallback cannot report an error after ${interruption}`, async () => {
    const state = await setup({ updated: true, mountRejects: true });
    state.instances[0].finish();
    await flush();
    if (interruption === 'disabled') state.setEnabled(false);
    else if (interruption === 'document hidden') state.setHidden(true);
    else state.window.dispatchEvent(new Event('pagehide'));
    await flush();
    state.scripts[0].onerror();
    await flush();
    assert.equal(state.error.hidden, true);
    assert.equal(state.retry.hidden, true);
  });
}

test('a canceled fallback error cannot replace a preference-save error or retry', async () => {
  let writes = 0;
  const state = await setup({ updated: true, mountRejects: true, preferences: {
    load: async () => ({ enabled: true }),
    save: async () => { writes++; throw new Error('disk full'); },
  } });
  state.instances[0].finish();
  await flush();
  state.setEnabled(false);
  await flush();
  state.scripts[0].onerror();
  await flush();
  assert.match(state.error.textContent, /无法保存/);
  state.retry.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(writes, 2);
});

test('script load without a fresh embedded SDK stays retryable', async () => {
  const state = await setup({ updated: true, reducedMotion: true });
  state.scripts[0].onload();
  await flush();
  assert.equal(state.instances.length, 0);
  assert.equal(state.error.hidden, false);
  state.retry.dispatchEvent(new Event('click'));
  assert.equal(state.scripts.length, 2);
  state.scripts[1].finishLoad();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.equal(state.overlays.size, 1);
  assert.equal(state.error.hidden, true);
});

test('cleanup throwing before host removal cannot leave the fallback wired to a stale host', async () => {
  const state = await setup({ updated: true, destroyBeforeRemoval: true });
  state.instances[0].finish();
  await flush();
  const oldHost = state.instances[0].host;
  state.motion.matches = true;
  state.motion.dispatchEvent(new Event('change'));
  assert.equal(state.overlays.size, 0, 'adapter must remove the old host even when SDK cleanup throws');
  state.scripts[0].finishLoad();
  await flush();
  state.instances[1].finish();
  await flush();
  assert.notEqual(state.document.querySelector('peetty-companion'), oldHost);
  state.instances[1].hide.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(state.saved.enabled, false, 'the embedded hide action must save the preference');
  assert.equal(state.overlays.size, 0);
});

test('partial failed mounts are removed even if cleanup throws before removal', async () => {
  const state = await setup({ updated: true, mountRejects: true, partialMount: true, destroyBeforeRemoval: true });
  state.instances[0].finish();
  await flush();
  assert.equal(state.overlays.size, 0);
  state.scripts[0].finishLoad();
  await flush();
  state.instances[1].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
});

test('a consistently failing SDK and shared fallback load preserve a failed preference save', async () => {
  const values = [];
  const state = await setup({ updated: true, createThrows: true, preferences: {
    load: async () => ({ enabled: true }),
    save: async (value) => { values.push(value); throw new Error('disk full'); },
  } });
  assert.equal(state.scripts.length, 1);
  state.setEnabled(false);
  await flush();
  assert.match(state.error.textContent, /无法保存/);
  assert.equal(state.scripts.length, 1, 'new generation must wait on the shared fallback load');
  state.scripts[0].onerror();
  await flush();
  assert.match(state.error.textContent, /无法保存/);
  state.retry.dispatchEvent(new Event('click'));
  await flush();
  assert.deepEqual(values, [false, false], 'retry must repeat the preference write, not a mount');
});

for (const interruption of ['disabled', 'document hidden', 'page hidden']) {
  test(`canceled mount that inserts a host and rejects after ${interruption} removes it despite failed destruction`, async () => {
    const state = await setup({ updated: true, mountRejects: true, partialMount: true, destroyBeforeRemoval: true });
    if (interruption === 'disabled') state.setEnabled(false);
    else if (interruption === 'document hidden') state.setHidden(true);
    else state.window.dispatchEvent(new Event('pagehide'));
    await flush();
    state.instances[0].finish();
    await flush();
    assert.equal(state.overlays.size, 0, 'late rejected mount must not leave its host visible');
    assert.equal(state.error.hidden, true);
    assert.equal(state.scripts.length, 0, 'canceled mounts do not start fallback loads');
  });
}

test('replacement starts without waiting for a canceled mount and keeps only its own host', async () => {
  const state = await setup({ updated: true, mountRejects: true, partialMount: true, destroyBeforeRemoval: true });
  state.setHidden(true);
  state.setHidden(false);
  await flush();
  assert.equal(state.instances.length, 2, 'a canceled SDK promise must not block replacement');
  state.instances[1].finish();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  state.instances[1].hide.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(state.saved.enabled, false);
  assert.equal(state.overlays.size, 0);
});

test('body observation removes hosts added by canceled mounts before they settle', async () => {
  const state = await setup({ updated: true, mountRejects: true, partialMount: true, destroyBeforeRemoval: true });
  state.setHidden(true);
  state.overlays.add(state.instances[0]);
  state.logChanged();
  assert.equal(state.overlays.size, 0, 'a canceled mount cannot display a host while still pending');
  state.instances[0].finish();
  await flush();
  assert.equal(state.overlays.size, 0);
});

test('multiple stalled canceled mounts cannot remove the current companion', async () => {
  const state = await setup({ updated: true, destroyBeforeRemoval: true });
  for (let i = 0; i < 3; i++) { state.setHidden(true); state.setHidden(false); }
  await flush();
  assert.equal(state.instances.length, 4);
  const active = state.instances.at(-1);
  active.finish();
  await flush();
  for (const old of state.instances.slice(0, -1)) old.finish();
  await flush();
  assert.deepEqual([...state.overlays], [active]);
  assert.equal(state.timers.size, 0);
});

test('document-root observation removes late hosts outside body without waiting for a canceled promise', async () => {
  const state = await setup({ updated: true, outsideBody: true, destroyBeforeRemoval: true });
  assert.deepEqual(state.observations, [state.document.documentElement]);
  state.setHidden(true);
  state.overlays.add(state.instances[0]);
  await flush();
  assert.equal(state.overlays.size, 0);
  state.setHidden(false);
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  state.overlays.add(state.instances[0]);
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]], 'old root must not claim or remove the current host');
  assert.equal(state.timers.size, 0);
});

test('a stalled CDN mount times out into the embedded SDK and late failures stay isolated', async () => {
  const state = await setup({ updated: true, outsideBody: true, mountRejects: true, partialMount: true, destroyBeforeRemoval: true });
  state.expireTimers();
  await flush();
  assert.equal(state.scripts.length, 1);
  state.scripts[0].finishLoad();
  await flush();
  state.instances[1].finish();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.equal(state.error.hidden, true);
  assert.equal(state.timers.size, 0);
});

test('an embedded mount timeout exposes a retry that does not wait for its stalled predecessor', async () => {
  const state = await setup({ outsideBody: true, destroyBeforeRemoval: true });
  state.expireTimers();
  await flush();
  assert.equal(state.error.hidden, false);
  assert.equal(state.retry.hidden, false);
  state.retry.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  state.instances[0].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.equal(state.error.hidden, true);
  assert.equal(state.timers.size, 0);
});

test('fallback script timeout is retryable and ignores a late load', async () => {
  const state = await setup({ updated: true, reducedMotion: true });
  state.expireTimers();
  await flush();
  assert.equal(state.error.hidden, false);
  state.retry.dispatchEvent(new Event('click'));
  assert.equal(state.scripts.length, 2);
  state.scripts[1].finishLoad();
  await flush();
  state.instances[0].finish();
  await flush();
  state.scripts[0].finishLoad();
  await flush();
  assert.equal(state.instances.length, 1);
  assert.equal(state.overlays.size, 1);
  assert.equal(state.error.hidden, true);
  assert.equal(state.timers.size, 0);
});

for (const earlyThrow of [false, true]) {
  for (const lateReject of [false, true]) {
    test(`late initialization cleanup retries after cancellation (early throw=${earlyThrow}, rejection=${lateReject})`, async () => {
      const state = await setup({ updated: true, outsideBody: true, installLateResources: true, destroyWhilePending: earlyThrow, mountRejects: lateReject, partialMount: true });
      const old = state.instances[0];
      state.setHidden(true);
      state.setHidden(false);
      await flush();
      assert.equal(state.instances.length, 2, 'replacement must not wait for late cleanup');
      state.instances[1].finish();
      await flush();
      assert.equal(old.cleanupCalls, 1);
      old.finish();
      await flush();
      assert.equal(old.cleanupCalls, 2, 'cleanup must run after late initialization installs resources');
      assert.equal(state.resources.size, 0, 'late listeners and animation work must be released');
      assert.deepEqual([...state.overlays], [state.instances[1]]);
      assert.equal(state.error.hidden, true);
    });
  }
}

test('late initialization cleanup retries after a mount timeout and fallback recovery', async () => {
  const state = await setup({ updated: true, installLateResources: true, destroyWhilePending: true });
  const old = state.instances[0];
  state.expireTimers();
  await flush();
  state.scripts[0].finishLoad();
  await flush();
  state.instances[1].finish();
  await flush();
  old.finish();
  await flush();
  assert.equal(old.cleanupCalls, 2);
  assert.equal(state.resources.size, 0);
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.equal(state.error.hidden, true);
});

test('a copied template host retains its instance namespace and Hide preference action', async () => {
  const state = await setup({ updated: true, cloneTemplate: true });
  const instance = state.instances[0];
  instance.finish();
  await flush();
  assert.equal(instance.host.isConnected, false, 'the original template stays detached');
  assert.equal(state.overlays.size, 1);
  assert.equal(instance.rendered.host.localName, instance.host.localName);
  assert.equal(state.error.hidden, true);
  instance.hide.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(state.saved.enabled, false);
  assert.equal(state.overlays.size, 0);
});

test('a late copied host from a canceled template cannot claim a replacement namespace', async () => {
  const state = await setup({ updated: true, outsideBody: true, destroyBeforeRemoval: true });
  const old = state.instances[0];
  state.setHidden(true);
  state.setHidden(false);
  await flush();
  state.instances[1].finish();
  await flush();
  const copy = old.copyHost();
  assert.notEqual(copy.host.localName, state.instances[1].host.localName);
  state.overlays.add(copy);
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  old.finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
});

test('unscoped legacy hosts recover into an isolated fallback and late legacy copies are removed', async () => {
  const state = await setup({ updated: true, outsideBody: true, unscopedRoot: true, destroyBeforeRemoval: true });
  const old = state.instances[0];
  old.finish();
  await flush();
  assert.equal(state.overlays.size, 0);
  assert.equal(state.scripts.length, 1);
  state.scripts[0].finishLoad();
  await flush();
  state.instances[1].finish();
  await flush();
  const copy = old.copyHost();
  assert.equal(copy.host.localName, 'peetty-companion');
  state.overlays.add(copy);
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.equal(state.error.hidden, true);
  state.instances[1].hide.dispatchEvent(new Event('click'));
  await flush();
  assert.equal(state.saved.enabled, false);
  assert.equal(state.overlays.size, 0);
});

test('copies adopted after the initial mount receive their own Hide handler exactly once', async () => {
  const state = await setup({ updated: true });
  const instance = state.instances[0];
  instance.finish();
  await flush();
  const copy = instance.copyHost();
  instance.host.remove();
  state.overlays.add(copy);
  await flush();
  state.logChanged();
  state.logChanged();
  copy.hide.dispatchEvent(new Event('click'));
  await flush();
  assert.deepEqual(state.saves, [false]);
  assert.equal(state.saved.enabled, false);
  assert.equal(state.overlays.size, 0);
});

test('changing reduced motion switches an active CDN companion to the embedded runtime', async () => {
  const state = await setup({ updated: true });
  state.instances[0].finish();
  await flush();
  state.motion.matches = true;
  state.motion.dispatchEvent(new Event('change'));
  assert.equal(state.overlays.size, 0);
  assert.equal(state.scripts.length, 1);
  state.scripts[0].finishLoad();
  await flush();
  state.instances[1].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
});
