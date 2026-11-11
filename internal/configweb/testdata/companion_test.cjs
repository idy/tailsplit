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
  const overlays = new Set();
  const log = { textContent: options.log || '' };
  const preview = { textContent: options.yaml || '' };
  const content = [log, preview, ...(options.texts || []).map((textContent) => ({ textContent }))];
  document.body = {};
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
  const window = new EventTarget();
  window.TailSplitI18n = { language: 'en', t: (key) => key, subscribe() {} };
  window.TailSplitCompanionPreferences = preferences;
  window.Peetty = { createPeetty() {
    let finish;
    const pending = new Promise((resolve) => { finish = resolve; });
    const hide = new EventTarget();
    const instance = {
      host: { shadowRoot: { querySelector: () => hide }, remove: () => { overlays.delete(instance); } }, hide, finish,
      // Model a mount whose asynchronous work completes after early disposal.
      mount: () => pending.then(() => {
        if (options.mountRejects && instances[0] === instance) {
          if (options.partialMount) overlays.add(instance);
          throw new Error('Mount failed');
        }
        overlays.add(instance);
      }),
      destroy: () => {
        if (options.destroyBeforeRemoval && instances[0] === instance) throw new Error('Cleanup failed before removing host');
        overlays.delete(instance);
        if (options.destroyThrows && instances[0] === instance) throw new Error('Cleanup failed');
      },
      setLanguage() {},
    };
    if (options.destroyMissing && instances.length === 0) instance.destroy = undefined;
    if (options.destroyNonCallable && instances.length === 0) instance.destroy = 'invalid';
    instances.push(instance);
    return instance;
  } };
  const scripts = [];
  const motion = new EventTarget();
  motion.matches = !!options.reducedMotion;
  window.matchMedia = () => motion;
  let logChanged = () => {};
  window.MutationObserver = class {
    constructor(callback) { logChanged = callback; }
    observe() {}
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
  return { toggle, error, retry, document, window, instances, overlays, saves, saved, setEnabled, setHidden, scripts, motion, log, preview, logChanged, factory };
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

test('canceled old mount cleans up before a replacement receives the Hide action', async () => {
  const state = await setup();
  state.setEnabled(false);
  await flush();
  state.setEnabled(true);
  await flush();
  assert.equal(state.instances.length, 1, 'the replacement waits for the old mount');
  const old = state.instances[0];
  old.finish();
  await flush();
  assert.equal(state.instances.length, 2);
  assert.equal(state.overlays.size, 0);
  const active = state.instances[1];
  active.finish();
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
  assert.equal(state.instances.length, 1);
  state.instances[0].finish();
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
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
  assert.equal(state.instances.length, 1);
  state.instances[0].finish();
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
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
  assert.equal(state.instances.length, 1);
  old.finish();
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
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

test('replacement waits for canceled mount cleanup and keeps only its own host', async () => {
  const state = await setup({ updated: true, mountRejects: true, partialMount: true, destroyBeforeRemoval: true });
  state.setHidden(true);
  state.setHidden(false);
  await flush();
  assert.equal(state.instances.length, 1, 'SDK mounts must not overlap');
  state.instances[0].finish();
  await flush();
  assert.equal(state.instances.length, 2);
  assert.equal(state.overlays.size, 0, 'old host must be removed before the replacement starts');
  state.instances[1].finish();
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

test('obsolete queued generations never mount after repeated visibility changes', async () => {
  const state = await setup({ updated: true, destroyBeforeRemoval: true });
  for (let i = 0; i < 3; i++) { state.setHidden(true); state.setHidden(false); }
  await flush();
  assert.equal(state.instances.length, 1);
  state.instances[0].finish();
  await flush();
  assert.equal(state.instances.length, 2);
  state.instances[1].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
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
