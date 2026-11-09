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
  const document = new EventTarget();
  document.hidden = false;
  const overlays = new Set();
  document.querySelector = (selector) => selector === '#page-companion' ? toggle : selector === '#page-companion-error' ? error : [...overlays][0]?.host;
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
      host: { shadowRoot: { querySelector: () => hide } }, hide, finish,
      // Model a mount whose asynchronous work completes after early disposal.
      mount: () => pending.then(() => { overlays.add(instance); }),
      destroy: () => { overlays.delete(instance); },
    };
    instances.push(instance);
    return instance;
  } };
  runInNewContext(source, { window, document, console });
  const setEnabled = (enabled) => { toggle.checked = enabled; toggle.dispatchEvent(new Event('change')); };
  const setHidden = (hidden) => { document.hidden = hidden; document.dispatchEvent(new Event('visibilitychange')); };
  await flush();
  return { toggle, error, document, window, instances, overlays, saves, saved, setEnabled, setHidden };
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

test('late old mount does not leave an orphan or replace the active Hide action', async () => {
  const state = await setup();
  state.setEnabled(false);
  await flush();
  state.setEnabled(true);
  await flush();
  assert.equal(state.instances.length, 2);
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
  assert.equal(state.instances.length, 2);
  state.instances[0].finish();
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
