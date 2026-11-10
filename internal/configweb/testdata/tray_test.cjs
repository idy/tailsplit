'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const { popupView } = require('../assets/tray.js');
const source = readFileSync(join(__dirname, '../assets/tray.js'), 'utf8');
const config = { valid: true, revision: 'saved-revision', values: {
  tailscale: { name: 'Personal', exit_node: '100.64.0.1' },
  exits: [{ id: 'office', name: 'Office', exit_node: '100.64.0.2' }],
  rules: { mode: 'split', default_exit: 'office', global_exit: 'office' },
} };

const officeLive = { exit: '100.64.0.2', exit_nodes: [{ id: '100.64.0.2', name: 'tokyo-node' }] };

test('additional traffic exit stays connected when primary needs login', () => {
  const view = popupView(config, { active: true, mode: 'tun', state: 'running', routing_mode: 'split',
    traffic_exit: 'office', backend_state: 'NeedsLogin', exit_nodes: [],
    exits: { office: { backend_state: 'Running', ...officeLive } } });
  assert.equal(view.status, '已连接');
  assert.equal(view.gateway, 'tokyo-node');
  assert.equal(view.setup, false);
});

test('active helper selection wins over config changed since connection', () => {
  const changed = structuredClone(config);
  changed.values.rules = { mode: 'global', global_exit: 'main' };
  const view = popupView(changed, { active: true, mode: 'tun', state: 'running',
    routing_mode: 'split', traffic_exit: 'office', exits: { office: officeLive } });
  assert.equal(view.mode, 'split');
  assert.equal(view.gateway, 'tokyo-node');
});

test('renaming and retargeting the same saved exit cannot rename the running gateway', () => {
  for (const id of ['main', 'office']) {
    const changed = structuredClone(config);
    const profile = id === 'main' ? changed.values.tailscale : changed.values.exits[0];
    profile.name = 'New saved label'; profile.exit_node = '100.64.0.99';
    const device = { exit: '100.64.0.2', exit_nodes: [
      { id: '100.64.0.2', name: 'old-running-node' }, { id: '100.64.0.99', name: 'new-saved-node' },
    ] };
    const live = { active: true, mode: 'tun', state: 'running', traffic_exit: id,
      ...(id === 'main' ? device : { exits: { office: device } }) };
    assert.equal(popupView(changed, live).gateway, 'old-running-node');
    device.exit_nodes.length = 0;
    assert.equal(popupView(changed, live).gateway, '100.64.0.2');
    if (id === 'main') live.exit = ''; else device.exit = '';
    assert.equal(popupView(changed, live).gateway, '');
    delete live.traffic_exit;
    assert.equal(popupView(changed, live).gateway, '');
  }
});

test('extras-only disconnected configuration shows its configured gateway', () => {
  const extrasOnly = structuredClone(config);
  extrasOnly.values.tailscale = null;
  const view = popupView(extrasOnly, { active: false, state: 'stopped', backend_state: 'NeedsLogin' });
  assert.equal(view.gateway, 'Office');
  assert.equal(view.status, '未连接');
  assert.equal(view.setup, false);
});

test('empty or invalid configuration directs to setup without inventing a gateway', () => {
  for (const doc of [{ valid: false }, { valid: true, values: {
    tailscale: null, exits: [], rules: { default_exit: '-', final: 'proxy' },
  } }]) {
    const view = popupView(doc, { active: false });
    assert.equal(view.setup, true);
    assert.equal(view.gateway, '');
  }
});

test('valid direct-only Split connects without a gateway or any devices', () => {
  for (const defaultExit of [undefined, '', '-', 'main']) {
    const doc = { valid: true, values: { tailscale: null, exits: [],
      rules: { mode: 'split', default_exit: defaultExit, final: 'direct', proxy_domains: [], proxy_ips: [] } } };
    const idle = popupView(doc, { active: false });
    assert.equal(idle.setup, false);
    assert.equal(idle.gateway, '');
    assert.equal(idle.detail, '按已保存的配置连接');
    const running = popupView(doc, { active: true, mode: 'tun', state: 'running', routing_mode: 'split' });
    assert.equal(running.status, '已连接');
    assert.equal(running.gateway, '');
  }
});

test('default-exit policy still requires a gateway for global and implicit proxy rules', () => {
  for (const rules of [
    { mode: 'global' }, { final: 'proxy' }, { source: 'https://example.test/rules' },
    { gfwlist: 'https://example.test/legacy' }, { proxy_domains: ['example.test'] }, { proxy_ips: ['192.0.2.0/24'] },
  ]) {
    assert.equal(popupView({ valid: true, values: { rules } }, { active: false }).setup, true);
  }
  // Rules with an explicit exit need no default gateway; config validation checks that exit.
  assert.equal(popupView({ valid: true, values: { rules: {
    mode: 'split', final: 'direct', default_exit: '-', routes: [{ exit: 'office', domains: ['example.test'] }],
  }, exits: config.values.exits } }, { active: false }).setup, false);
});

test('connection preflight and errors never claim connected', () => {
  for (const live of [
    { active: true, mode: 'tun', state: 'starting' },
    { active: true, mode: 'tun', state: 'blocked' },
    { active: true, mode: 'tun', state: 'waiting-exit' },
    { active: true, mode: 'login', state: 'needs_login' },
    { active: true, mode: 'tun', state: 'running', error: 'exit offline' },
  ]) {
    const view = popupView(config, live);
    assert.notEqual(view.status, '已连接');
    assert.notEqual(view.tone, 'good');
    assert.equal(view.active, true); // Cancel/disconnect remains available.
  }
});

test('traffic helpers awaiting login show sign-in guidance', () => {
  for (const mode of ['tun', 'proxy']) {
    for (const state of ['login', 'needs_login']) {
      const view = popupView(config, { active: true, mode, state });
      assert.equal(view.status, '等待登录');
      assert.equal(view.detail, '请在浏览器中完成授权');
      assert.equal(view.tone, 'warn');
    }
  }
});

test('stopping takes priority over login and discovery helper modes', () => {
  for (const mode of ['login', 'discovery', 'tun', 'proxy']) {
    const view = popupView(config, { active: true, mode, state: 'stopping' });
    assert.equal(view.status, '正在断开');
    assert.equal(view.detail, '正在恢复系统网络');
    assert.equal(view.tone, 'warn');
  }
});

test('discovery helpers show reading exits instead of connecting', () => {
  for (const live of [
    { active: true, mode: 'discovery', state: 'starting' },
    { active: true, mode: 'discovery', state: 'discovering' },
    { active: true, state: 'discovering' },
  ]) {
    const view = popupView(config, live);
    assert.equal(view.status, '正在读取出口');
    assert.equal(view.carrying, false);
    assert.equal(view.tone, 'warn');
  }
});

test('an unnamed idle gateway uses its node name without appending an IP', () => {
  const unnamed = structuredClone(config);
  delete unnamed.values.exits[0].name;
  const view = popupView(unnamed, { exits: { office: officeLive } });
  assert.equal(view.gateway, 'tokyo-node');
});

const settle = () => new Promise(setImmediate);
const response = (value, ok = true) => ({ ok, json: async () => value });
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};

// Exercise the browser entry point and real fetch/event flow without exporting
// mutable UI internals. Timers are controlled so each test owns its reads.
async function page({ native = false, reads = {}, action } = {}) {
  const nodes = new Map();
  const $ = selector => {
    if (!nodes.has(selector)) nodes.set(selector, {
      textContent: '', dataset: {}, hidden: false, disabled: false, attributes: {}, listeners: {},
      setAttribute(name, value) { this.attributes[name] = value; },
      addEventListener(name, callback) { this.listeners[name] = callback; },
      async click() { if (!this.disabled) await this.listeners.click?.(); await settle(); },
    });
    return nodes.get(selector);
  };
  const open = [$('#header-open'), $('#footer-open')];
  const document = { hidden: false, documentElement: { dataset: {} }, querySelector: $,
    querySelectorAll: selector => selector === '[data-open-panel]' ? open : [], addEventListener() {} };
  const requests = [], navigation = [];
  const values = {
    config: structuredClone(config),
    state: { revision: config.revision, active: false, state: 'stopped', desktop: true,
      open_panel_available: true, quit_available: true },
    language: { language: 'en' }, ...reads,
  };
  const locale = { language: 'zh', t: key => key, setLanguage(language) { this.language = language; }, subscribe() {} };
  let closed = 0, poll;
  const window = { TailSplitI18n: locale, addEventListener() {},
    ...(native ? { mygo: { window: { async close() { closed++; } } } } : {}) };
  vm.runInNewContext(source, {
    window, document, URLSearchParams, AbortSignal,
    location: { hash: '#token=popup-token', pathname: '/tray', assign: url => navigation.push(url) },
    history: { replaceState() {} }, sessionStorage: { setItem() {} },
    setTimeout: callback => { poll = callback; return 1; }, clearTimeout() {},
    fetch: async (url, options) => {
      const path = url.slice('/api/'.length);
      const body = options.body && JSON.parse(options.body);
      requests.push({ path, body, headers: options.headers });
      if (path === 'action') return action ? action(body) : response({ ok: true });
      const value = values[path];
      if (value instanceof Error) throw value;
      return response(await value);
    },
  });
  await settle();
  return { $, values, requests, navigation, locale, document, get closed() { return closed; },
    actions: () => requests.filter(request => request.path === 'action').map(request => request.body),
    async poll() { await poll(); await settle(); } };
}

test('config read failure retains state capabilities and language, and retry recovers', async () => {
  const p = await page({ reads: { config: new Error('Configuration unreadable') } });
  assert.equal(p.$('#connect').disabled, true);
  assert.equal(p.$('#quit').disabled, false);
  assert.equal(p.$('#retry').disabled, false);
  assert.equal(p.$('#error-message').textContent, 'Configuration unreadable');
  assert.equal(p.locale.language, 'en');
  await p.$('#footer-open').click();
  await p.$('#quit').click();
  assert.deepEqual(p.actions(), [{ action: 'open-panel' }, { action: 'quit' }]);
  assert.deepEqual(p.navigation, []);
  p.values.config = structuredClone(config);
  await p.$('#retry').click();
  assert.equal(p.$('#error').hidden, true);
  assert.equal(p.$('#connect').disabled, false);
  assert.equal(p.$('#exit-name').textContent, 'Office');
});

test('native popup can open and quit when state or both initial reads fail', async () => {
  for (const configFails of [false, true]) {
    const p = await page({ native: true, reads: {
      state: new Error('State unavailable'), ...(configFails ? { config: new Error('Config unavailable') } : {}),
    } });
    assert.equal(p.$('#connect').disabled, true);
    assert.equal(p.$('#quit').disabled, false);
    if (!configFails) assert.equal(p.$('#exit-name').textContent, 'Office');
    await p.$('#header-open').click();
    await p.$('#quit').click();
    assert.deepEqual(p.actions(), [{ action: 'open-panel' }, { action: 'quit' }]);
    assert.equal(p.closed, 1);
    assert.deepEqual(p.navigation, []);
    p.values.config = structuredClone(config);
    p.values.state = { revision: config.revision, active: false, desktop: true };
    await p.$('#retry').click();
    assert.equal(p.$('#connect').disabled, false);
    assert.equal(p.$('#error').hidden, true);
  }
});

test('native action failures remain visible over read errors and never navigate the flyout', async () => {
  const p = await page({ native: true, reads: { state: new Error('State unavailable') },
    action: body => response({ error: `${body.action} unavailable` }, false) });
  await p.$('#header-open').click();
  assert.equal(p.$('#error-message').textContent, 'open-panel unavailable');
  assert.equal(p.closed, 0);
  await p.$('#quit').click();
  assert.equal(p.$('#error-message').textContent, 'quit unavailable');
  assert.equal(p.$('#quit').disabled, false);
  assert.deepEqual(p.navigation, []);
});

test('only a confirmed standalone browser preview navigates to the full panel', async () => {
  for (const [native, state, fallback] of [
    [false, { preview: true }, true], [true, { preview: true }, false],
    [false, {}, false], [true, {}, false], [false, new Error('State unavailable'), false],
  ]) {
    const p = await page({ native, reads: { state } });
    await p.$('#header-open').click();
    assert.deepEqual(p.navigation, fallback ? ['/#token=popup-token'] : []);
    assert.deepEqual(p.actions(), fallback ? [] : [{ action: 'open-panel' }]);
  }
});

test('failed subsequent reads disable stale connection actions until a matching retry', async () => {
  for (const failedRead of ['config', 'state', 'language']) {
    const p = await page({ native: true });
    const good = p.values[failedRead];
    p.values[failedRead] = new Error(`${failedRead} read failed`);
    await p.poll();
    assert.equal(p.$('#connect').disabled, true);
    assert.equal(p.$('#quit').disabled, false);
    await p.$('#connect').click();
    assert.deepEqual(p.actions(), []);
    p.values[failedRead] = good;
    await p.$('#retry').click();
    assert.equal(p.$('#connect').disabled, false);
    assert.equal(p.$('#error').hidden, true);
  }
});

test('revision mismatch disables connection while leaving native actions usable', async () => {
  const p = await page({ native: true, reads: { state: {
    revision: 'other-revision', desktop: true, active: false,
  } } });
  assert.equal(p.$('#connect').disabled, true);
  assert.equal(p.$('#quit').disabled, false);
  assert.equal(p.$('#error-message').textContent, '配置文件已更新，请重新读取后重试。');
  p.values.state.revision = config.revision;
  await p.$('#retry').click();
  assert.equal(p.$('#connect').disabled, false);
});

test('connect sends the displayed revision and other actions stay revision independent', async () => {
  const p = await page();
  await p.$('#connect').click();
  assert.deepEqual(p.actions(), [{ action: 'connect', revision: config.revision }]);
  assert.equal(p.requests.find(request => request.path === 'action').headers.Authorization, 'Bearer popup-token');
  p.values.config.revision = 'new-revision';
  p.values.state.revision = 'new-revision';
  await p.$('#retry').click();
  await p.$('#connect').click();
  assert.deepEqual(p.actions().at(-1), { action: 'connect', revision: 'new-revision' });
  p.values.state.active = true; p.values.state.state = 'running'; p.values.state.mode = 'tun';
  await p.$('#retry').click();
  await p.$('#connect').click();
  await p.$('#header-open').click();
  await p.$('#quit').click();
  assert.deepEqual(p.actions().slice(-3), [{ action: 'disconnect' }, { action: 'open-panel' }, { action: 'quit' }]);
});

test('direct-only configuration exposes the connection switch without setup', async () => {
  const p = await page({ reads: { config: { valid: true, revision: config.revision,
    values: { rules: { mode: 'split', final: 'direct' }, tailscale: null, exits: [] } } } });
  assert.equal(p.$('#connect').hidden, false);
  assert.equal(p.$('#connect').disabled, false);
  assert.equal(p.$('#setup').hidden, true);
  assert.equal(p.$('#exit-name').textContent, '未设置');
  await p.$('#connect').click();
  assert.deepEqual(p.actions(), [{ action: 'connect', revision: config.revision }]);
});

test('stale connect rejection reports the error without claiming a connection', async () => {
  const p = await page({ action: () => response({ error: 'Configuration changed; retry' }, false) });
  await p.$('#connect').click();
  assert.equal(p.$('#status').textContent, '未连接');
  assert.equal(p.$('#connect').attributes['aria-checked'], 'false');
  assert.equal(p.$('#error-message').textContent, 'Configuration changed; retry');
});

test('an action invalidates all results and errors from an older in-flight poll', async () => {
  for (const failConfig of [false, true]) {
    const p = await page();
    const delayed = deferred();
    p.values.state = delayed.promise;
    p.values.language = { language: 'zh' };
    p.values.config = failConfig ? new Error('Obsolete config failure') : {
      ...structuredClone(config), revision: 'obsolete',
    };
    const polling = p.poll();
    await settle();
    await p.$('#quit').click();
    delayed.resolve({ revision: 'obsolete', active: true, desktop: true, mode: 'tun', state: 'running' });
    await polling;
    assert.equal(p.$('#status').textContent, '未连接');
    assert.equal(p.$('#connect').attributes['aria-checked'], 'false');
    assert.equal(p.$('#error').hidden, true);
    assert.equal(p.locale.language, 'en');
    await p.$('#connect').click();
    assert.deepEqual(p.actions().at(-1), { action: 'connect', revision: config.revision });
  }
});

test('mode and exit details have no interactive markup or handlers', async () => {
  const html = readFileSync(join(__dirname, '../assets/tray.html'), 'utf8');
  const details = html.match(/<div class="popup-details">([\s\S]*?)<div class="popup-error"/)[1];
  assert.doesNotMatch(details, /<(?:button|a|input|select)\b|tabindex|role="(?:button|switch)"|data-open-panel/);
  const p = await page();
  assert.deepEqual(p.$('#routing-mode').listeners, {});
  assert.deepEqual(p.$('#exit-name').listeners, {});
});
