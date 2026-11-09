const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const { join } = require('node:path');
const { runInNewContext } = require('node:vm');

const source = readFileSync(join(__dirname, '../assets/companion.js'), 'utf8');
const flush = () => new Promise(setImmediate);

function setup() {
  const toggle = new EventTarget();
  toggle.checked = true;
  const document = new EventTarget();
  document.hidden = false;
  const overlays = new Set();
  document.querySelector = (selector) => selector === '#page-companion' ? toggle : [...overlays][0]?.host;
  const storage = new Map();
  const localStorage = { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) };
  const instances = [];
  const window = new EventTarget();
  window.TailSplitI18n = { language: 'en', subscribe() {} };
  window.Peetty = { createPeetty() {
    let finish;
    const pending = new Promise((resolve) => { finish = resolve; });
    const hide = new EventTarget();
    const instance = {
      host: { shadowRoot: { querySelector: () => hide } },
      hide,
      finish,
      // Model a mount whose asynchronous work completes after early disposal.
      mount: () => pending.then(() => { overlays.add(instance); }),
      destroy: () => { overlays.delete(instance); },
    };
    instances.push(instance);
    return instance;
  } };
  runInNewContext(source, { window, document, localStorage, console });
  const setEnabled = (enabled) => { toggle.checked = enabled; toggle.dispatchEvent(new Event('change')); };
  const setHidden = (hidden) => { document.hidden = hidden; document.dispatchEvent(new Event('visibilitychange')); };
  return { toggle, document, window, instances, overlays, storage, setEnabled, setHidden };
}

for (const interruption of ['toggle off', 'document hidden', 'page hidden']) {
  test(`late mount is removed after ${interruption}`, async () => {
    const state = setup();
    if (interruption === 'toggle off') state.setEnabled(false);
    else if (interruption === 'document hidden') state.setHidden(true);
    else state.window.dispatchEvent(new Event('pagehide'));
    state.instances[0].finish();
    await flush();
    assert.equal(state.overlays.size, 0, 'a canceled companion must not reappear after mount completes');
  });
}

test('late old mount does not leave an orphan or replace the active Hide action', async () => {
  const state = setup();
  state.setEnabled(false);
  state.setEnabled(true);
  assert.equal(state.instances.length, 2);
  const [old, active] = state.instances;
  active.finish();
  await flush();
  old.finish();
  await flush();
  assert.deepEqual([...state.overlays], [active]);
  active.hide.dispatchEvent(new Event('click'));
  assert.equal(state.toggle.checked, false);
  assert.equal(state.storage.get('tailsplit.page-companion'), 'off');
  assert.equal(state.overlays.size, 0);
});

test('a page restored before its old mount settles keeps only the restored companion', async () => {
  const state = setup();
  state.window.dispatchEvent(new Event('pagehide'));
  state.window.dispatchEvent(new Event('pageshow'));
  assert.equal(state.instances.length, 2);
  state.instances[0].finish();
  state.instances[1].finish();
  await flush();
  assert.deepEqual([...state.overlays], [state.instances[1]]);
  assert.equal(state.toggle.checked, true);
});
