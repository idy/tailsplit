'use strict';
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = readFileSync(process.argv[2] || join(__dirname, '../assets/app.js'), 'utf8');
const i18nSource = readFileSync(join(__dirname, '../assets/i18n.js'), 'utf8');
const yaml = 'log_level: info\ntun:\n  mtu: 1400\n';
const doc = (text = yaml, revision = 'original') => ({
  yaml: text, revision, valid: true, parsed: true,
  values: { log_level: text.match(/log_level: (\w+)/)?.[1] || 'info', tun: { mtu: 1400 } },
});
const response = (value, ok = true) => ({ ok, json: async () => value });
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
};
const settle = () => new Promise(setImmediate);
const updateState = (changes = {}) => ({
  current_version: '0.6.1', latest_version: '', automatic: true,
  checking: false, downloading: false, installing: false, available: false,
  downloaded_bytes: 0, total_bytes: 0, last_checked: '0001-01-01T00:00:00Z',
  error: '', release_notes: '', ...changes,
});

class Element {
  constructor() {
    this.value = ''; this.type = ''; this.dataset = {}; this.hidden = false; this.disabled = false;
    this.textContent = ''; this.listeners = {}; this.attributes = new Map(); this.classes = new Set();
    this.classList = {
      toggle: (name, enabled) => enabled ? this.classes.add(name) : this.classes.delete(name),
      contains: (name) => this.classes.has(name), remove: (name) => this.classes.delete(name),
    };
  }
  addEventListener(name, callback) { (this.listeners[name] ||= []).push(callback); }
  setAttribute(name, value) { this.attributes.set(name, value); }
  removeAttribute(name) { this.attributes.delete(name); }
  append() {}
  replaceChildren() {}
  focus() {}
  async emit(name, event = {}) {
    if (this.disabled && ['click', 'input', 'change', 'keydown'].includes(name)) return;
    await Promise.all((this.listeners[name] || []).map((callback) => callback({
      target: this, preventDefault() {}, ...event,
    })));
  }
}

async function page(snapshot = {}, native = '', localeOptions = {}) {
  const nodes = new Map();
  const $ = (selector) => {
    if (!nodes.has(selector)) nodes.set(selector, new Element());
    return nodes.get(selector);
  };
  const level = $('#level'); level.dataset.field = 'log_level';
  const mtu = $('#mtu'); mtu.dataset = { field: 'tun.mtu', kind: 'int' };
  const routing = $('#routing-mode'); routing.dataset.field = 'rules.mode';
  const tun = $('#tun-enabled'); tun.type = 'checkbox'; tun.dataset.field = 'tun.enabled';
  const fields = [level, mtu, routing, tun];
  $('#form-tab').setAttribute('aria-selected', 'true');
  $('#yaml-tab').setAttribute('aria-selected', 'false');
  $('#form-tab').tabIndex = 0;
  $('#yaml-tab').tabIndex = -1;
  $('#yaml-editor').hidden = true;
  const navigation = $('[data-section="connection"]'); navigation.dataset.section = 'connection';
  const events = new Element();
  const requests = [];
  const timers = new Map();
  let timer = 0;
  let poll;
  const window = new Element();
  const messages = [];
  const host = { postMessage: (value) => messages.push(value) };
  if (native === 'macos') window.webkit = { messageHandlers: { tailsplitWindow: host } };
  if (native === 'windows') window.chrome = { webview: host };
  const windowButtons = ['minimize', 'zoom', 'close'].map((action) => {
    const button = $(`[data-window-action="${action}"]`);
    button.dataset.windowAction = action;
    return button;
  });
  $('#window-controls').hidden = true;
  const app = { $, requests, messages, windowEvents: window, snapshot: { state: 'stopped', revision: 'original', ...snapshot },
    language: { preference: localeOptions.preference || 'auto', language: localeOptions.language || 'zh' }, override: () => undefined };
  const document = {
    documentElement: new Element(), hidden: false, querySelector: $, createElement: () => new Element(),
    createTextNode: () => new Element(), createDocumentFragment: () => new Element(),
    querySelectorAll: (selector) => selector === '[data-field]' ? fields :
      selector === '[data-window-action]' ? windowButtons :
      selector === '[data-section]' ? [navigation] :
      selector === '.editor-tabs button, [data-section]' ? [$('#form-tab'), $('#yaml-tab'), navigation] : [],
    addEventListener: (...args) => events.addEventListener(...args),
  };
  const sandbox = {
    document, window, URLSearchParams, location: { hash: '#token=fixture' },
    history: { replaceState() {} }, sessionStorage: { setItem() {}, getItem() {} },
    navigator: { languages: localeOptions.languages || ['zh-CN'], clipboard: { writeText: async () => {} } },
    setTimeout: (callback) => { timers.set(++timer, callback); return timer; },
    clearTimeout: (id) => timers.delete(id), setInterval: (callback) => { poll = callback; },
    fetch: async (path, options) => {
      const request = { path, method: options.method, headers: options.headers, data: options.body && JSON.parse(options.body) };
      requests.push(request);
      const overridden = app.override(request);
      if (overridden !== undefined) return overridden;
      if (path === '/api/language') {
        if (request.method === 'POST') app.language = { preference: request.data.preference, language: request.data.preference === 'auto' ? localeOptions.language || 'zh' : request.data.preference };
        return response(app.language);
      }
      if (path === '/api/state') return response(app.snapshot);
      if (path === '/api/config') return response(doc());
      if (path === '/api/preview') {
        let text = request.data.yaml;
        if (request.data.changes?.log_level) text = text.replace(/log_level: \w+/, `log_level: ${request.data.changes.log_level}`);
        if (request.data.changes?.['tun.mtu']) text = text.replace(/mtu: \S+/, `mtu: ${request.data.changes['tun.mtu']}`);
        return response(doc(text));
      }
      return response({ valid: true });
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(i18nSource, sandbox, { filename: 'i18n.js' });
  vm.runInContext(source, sandbox, { filename: 'app.js' });
  app.i18n = window.TailSplitI18n;
  await settle();
  assert.equal($('#level').value, 'info');
  app.poll = () => poll();
  app.input = async (selector, value) => {
    const control = $(selector);
    if (control.disabled) return;
    control.value = value;
    await control.emit('input');
  };
  return app;
}

test('native titlebar gestures exclude controls and keep platform window buttons separate', async () => {
  for (const platform of ['macos', 'windows']) {
    const app = await page({}, platform);
    assert.equal(app.$('#window-controls').hidden, platform !== 'windows');
    const blank = { closest: () => null };
    const control = { closest: () => ({}) };
    await app.$('#titlebar').emit('mousedown', { button: 0, detail: 1, target: blank, clientX: 200, clientY: 20 });
    await app.windowEvents.emit('mousemove', { buttons: 1, clientX: 202, clientY: 20 });
    assert.deepEqual(app.messages, []); // Small motion must still allow double-click.
    await app.windowEvents.emit('mousemove', { buttons: 1, clientX: 205, clientY: 20 });
    await app.windowEvents.emit('mousemove', { buttons: 1, clientX: 210, clientY: 20 });
    assert.deepEqual(app.messages, ['drag']); // Only one native move loop per drag.
    for (const input of [
      { button: 0, detail: 2, target: blank },
      { button: 2, detail: 1, target: blank },
      { button: 0, detail: 1, target: control },
    ]) {
      await app.$('#titlebar').emit('mousedown', { ...input, clientX: 200, clientY: 20 });
      await app.windowEvents.emit('mousemove', { buttons: 1, clientX: 210, clientY: 20 });
    }
    await app.$('#titlebar').emit('mousedown', { button: 0, detail: 1, target: blank, clientX: 200, clientY: 20 });
    await app.windowEvents.emit('mouseup');
    await app.windowEvents.emit('mousemove', { buttons: 0, clientX: 210, clientY: 20 });
    await app.$('#titlebar').emit('dblclick', { button: 0, target: blank });
    await app.$('#titlebar').emit('dblclick', { button: 0, target: control });
    assert.deepEqual(app.messages, ['drag', 'zoom']);
    if (platform === 'windows') {
      for (const action of ['minimize', 'zoom', 'close']) await app.$(`[data-window-action="${action}"]`).emit('click');
      assert.deepEqual(app.messages, ['drag', 'zoom', 'minimize', 'zoom', 'close']);
    }
  }
  const browser = await page();
  assert.equal(browser.$('#window-controls').hidden, true);
  assert.deepEqual(browser.messages, []);
});

test('titlebar updates preserve the current YAML draft and do not switch pages', async () => {
  const app = await page({ updates: updateState() });
  await app.$('#yaml-tab').emit('click');
  await app.input('#raw-yaml', 'unfinished: [');
  app.override = (request) => {
    if (request.path !== '/api/update-action') return undefined;
    if (request.data.action === 'check') app.snapshot.updates = updateState({ available: true, latest_version: '0.6.2' });
    else app.snapshot.updates.installing = true;
    return response({ ok: true });
  };
  await app.$('#update-pill').emit('click');
  assert.equal(app.$('#update-pill-version').textContent, '更新');
  await app.$('#update-pill').emit('click');
  assert.equal(app.$('#update-pill-version').textContent, '安装中…');
  assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
  assert.equal(app.$('#yaml-tab').attributes.get('aria-selected'), 'true');
  assert.deepEqual(app.requests.filter((r) => r.path === '/api/update-action').map((r) => r.data.action), ['check', 'install']);
  assert.equal(app.requests.some((r) => r.path === '/api/config' && r.method === 'POST'), false);
});

test('a rejected form preview keeps edits when changing mode', async () => {
  const app = await page();
  app.override = (request) => request.path === '/api/preview' && request.data.changes?.['tun.mtu'] === 1400.5
    ? response({ error: 'MTU must be an integer' }, false) : undefined;
  await app.input('#mtu', '1400.5');
  await app.$('#yaml-tab').emit('click');
  assert.equal(app.$('#mtu').value, '1400.5');
  assert.equal(app.$('#yaml-tab').attributes.get('aria-selected'), 'false');
  assert.equal(app.$('#save').disabled, true);
  await app.input('#mtu', '1500');
  await app.$('#yaml-tab').emit('click');
  assert.match(app.$('#raw-yaml').value, /mtu: 1500/);
});

test('mode changes lock inputs until their current preview is incorporated', async () => {
  const app = await page();
  const preview = deferred();
  app.override = (request) => request.path === '/api/preview' ? preview.promise : undefined;
  await app.input('#level', 'debug');
  const switching = app.$('#yaml-tab').emit('click');
  assert.equal(app.$('#level').disabled, true);
  await app.input('#level', 'warn');
  preview.resolve(response(doc(yaml.replace('info', 'debug'))));
  await switching;
  assert.match(app.$('#raw-yaml').value, /log_level: debug/);
  assert.equal(app.$('#raw-yaml').disabled, false);
});

test('an external reload response cannot replace a newer browser draft', async () => {
  const app = await page();
  const reload = deferred();
  app.snapshot = { state: 'stopped', revision: 'external' };
  app.override = (request) => request.path === '/api/config' ? reload.promise : undefined;
  const polling = app.poll();
  await settle();
  assert.equal(app.requests.at(-1).path, '/api/config');
  await app.input('#level', 'debug');
  reload.resolve(response(doc(yaml + '# external edit\n', 'external')));
  await polling;
  assert.equal(app.$('#level').value, 'debug');
  assert.equal(app.$('#save-state').classList.contains('dirty'), true);
  assert.equal(app.$('#save').disabled, true);
  await app.$('#yaml-tab').emit('click');
  assert.match(app.$('#raw-yaml').value, /log_level: debug/);
  assert.doesNotMatch(app.$('#raw-yaml').value, /external edit/);
});

test('editor arrow navigation keeps only the selected tab in the Tab sequence', async () => {
  const app = await page();
  assert.equal(app.$('#form-tab').tabIndex, 0);
  assert.equal(app.$('#yaml-tab').tabIndex, -1);
  await app.$('.editor-tabs').emit('keydown', { key: 'ArrowRight' });
  assert.equal(app.$('#yaml-tab').tabIndex, 0);
  assert.equal(app.$('#form-tab').tabIndex, -1);
  assert.equal(app.$('#yaml-editor').hidden, false);
  await app.$('.editor-tabs').emit('keydown', { key: 'Home' });
  assert.equal(app.$('#form-tab').tabIndex, 0);
  assert.equal(app.$('#yaml-tab').tabIndex, -1);
  assert.equal(app.$('#form-editor').hidden, false);
});

test('connection warnings and offline errors clear after recovery', async () => {
  const app = await page({ active: true, mode: 'proxy', state: 'blocked' });
  assert.equal(app.$('#connection-status').classList.contains('attention'), true);
  assert.equal(app.$('#connection-status').title, '出口不可用 · 已阻断');
  app.override = (request) => request.path === '/api/state' ? response({ error: 'Offline' }, false) : undefined;
  await app.poll();
  assert.equal(app.$('#connection-status').classList.contains('error'), true);
  assert.equal(app.$('#connection-status').classList.contains('attention'), false);
  assert.equal(app.$('#connection-status').title, '页面连接中断');
  app.override = () => undefined;
  app.snapshot.state = 'failed';
  await app.poll();
  assert.equal(app.$('#connection-status span').textContent, '连接异常');
  assert.equal(app.$('#connection-status').classList.contains('error'), true);
  app.snapshot.state = 'running';
  await app.poll();
  assert.equal(app.$('#connection-status span').textContent, '已连接');
  assert.equal(app.$('#connection-status').classList.contains('connected'), true);
  assert.equal(app.$('#connection-status').classList.contains('error'), false);
});

test('only a running proxy is presented as connected', async () => {
  const app = await page();
  for (const state of ['blocked', 'checking-exit', 'waiting-exit', 'missing-exit', 'connecting', 'login']) {
    app.snapshot = { active: true, mode: 'tun', state, revision: 'original' };
    await app.poll();
    assert.equal(app.$('#connection-status').classList.contains('connected'), false, state);
    assert.notEqual(app.$('#connection-status span').textContent, '已连接', state);
  }
  app.snapshot.state = 'running';
  await app.poll();
  assert.equal(app.$('#connection-status').classList.contains('connected'), true);
});

test('update controls are independent of desktop connection controls', async () => {
  const app = await page();
  assert.equal(app.$('#app-updates').hidden, true);
  app.snapshot.updates = updateState();
  await app.poll();
  assert.equal(app.$('#app-updates').hidden, false);
  assert.equal(app.$('#desktop-controls').hidden, true);
  assert.equal(app.$('#update-current').textContent, '当前版本 0.6.1');
  assert.equal(app.$('#update-latest').textContent, '最新版本 尚未检查');
  assert.equal(app.$('#update-pill').hidden, false);
  assert.equal(app.$('#update-pill-version').textContent, '检查更新');
  assert.equal(app.$('#update-last-checked').textContent, '上次检查：尚未检查');
  app.snapshot.updates = updateState({ latest_version: '0.6.2', available: true });
  app.snapshot.preview = true;
  await app.poll();
  assert.equal(app.$('#update-pill').hidden, false);
  assert.equal(app.$('#update-pill-version').textContent, '模拟更新');
  assert.equal(app.$('#update-preview').hidden, false);
  app.snapshot.updates = null;
  await app.poll();
  assert.equal(app.$('#app-updates').hidden, true);
});

test('automatic update preferences save without applying or dirtying the editor', async () => {
  const app = await page({ updates: updateState() });
  app.override = (request) => {
    if (request.path === '/api/update-action') {
      app.snapshot.updates.automatic = request.data.action === 'automatic-on';
      app.snapshot.revision = 'preference';
      return response({ ok: true });
    }
    if (request.path === '/api/config' && app.snapshot.revision === 'preference') {
      return response(doc(yaml + 'app:\n  check_for_updates: false\n', 'preference'));
    }
    return undefined;
  };
  app.$('#update-automatic').checked = false;
  await app.$('#update-automatic').emit('change');
  assert.equal(app.$('#update-automatic').checked, false);
  assert.equal(app.$('#save-state').classList.contains('dirty'), false);
  assert.equal(app.$('#notice').hidden, true);
  assert.equal(app.$('#save').disabled, true);
  assert.deepEqual(app.requests.filter((request) => request.method === 'POST').map((request) => [request.path, request.data]), [
    ['/api/update-action', { action: 'automatic-off' }],
  ]);
  app.$('#update-automatic').checked = true;
  await app.$('#update-automatic').emit('change');
  assert.equal(app.$('#update-automatic').checked, true);
  assert.equal(app.requests.at(-2).path, '/api/update-action');
  assert.equal(app.requests.at(-2).data.action, 'automatic-on');
});

test('pending update requests block duplicates while leaving draft controls usable', async () => {
  const app = await page({ updates: updateState({ latest_version: '0.6.2', available: true }) });
  const check = deferred();
  app.override = (request) => request.path === '/api/update-action' ? check.promise : undefined;
  const checking = app.$('#update-check').emit('click');
  for (const selector of ['#update-check', '#update-pill', '#update-automatic']) assert.equal(app.$(selector).disabled, true);
  assert.equal(app.$('#level').disabled, false);
  await app.$('#update-check').emit('click');
  await app.$('#update-pill').emit('click');
  await app.$('#update-automatic').emit('change');
  await app.input('#level', 'debug');
  app.snapshot.updates.checking = true;
  check.resolve(response({ ok: true }));
  await checking;
  assert.equal(app.requests.filter((request) => request.path === '/api/update-action').length, 1);
  assert.equal(app.$('#level').value, 'debug');
  assert.equal(app.$('#save-state').classList.contains('dirty'), true);
  assert.equal(app.$('#update-check').disabled, true);
  app.snapshot.updates.checking = false;
  await app.poll();
  assert.equal(app.$('#update-check').disabled, false);
});

test('backend update phases disable actions and show bounded download progress', async () => {
  const app = await page({ updates: updateState() });
  for (const phase of ['checking', 'downloading', 'installing']) {
    app.snapshot.updates = updateState({
      [phase]: true, available: true, latest_version: '0.6.2', downloaded_bytes: 524288, total_bytes: 1048576,
    });
    await app.poll();
    for (const selector of ['#update-check', '#update-pill', '#update-automatic']) assert.equal(app.$(selector).disabled, true, phase);
    await app.$('#update-check').emit('click');
    assert.equal(app.$('#update-pill').hidden, false);
    assert.equal(app.$('#update-pill').classList.contains('busy'), true);
    if (phase === 'downloading') {
      app.snapshot.updates.installing = true;
      await app.poll();
      assert.equal(app.$('#update-pill-version').textContent, '下载中 50%');
      assert.match(app.$('#update-pill').title, /512.0 KiB \/ 1.0 MiB/);
      assert.match(app.$('#update-pill').attributes.get('aria-label'), /下载中 50%/);
      app.snapshot.updates.downloaded_bytes = 2097152;
      await app.poll();
      assert.equal(app.$('#update-pill-version').textContent, '下载中 100%');
      app.snapshot.updates.total_bytes = 0;
      await app.poll();
      assert.equal(app.$('#update-pill-version').textContent, '下载中…');
      assert.match(app.$('#update-pill').title, /总大小待确定/);
      app.snapshot.updates.total_bytes = 1048576;
      app.snapshot.updates.downloaded_bytes = -1;
      await app.poll();
      assert.equal(app.$('#update-pill-version').textContent, '下载中 0%');
    }
    if (phase === 'installing') assert.equal(app.$('#update-pill-version').textContent, '安装中…');
    if (phase === 'checking') assert.equal(app.$('#update-pill-version').textContent, '检查中…');
  }
  assert.equal(app.requests.some((request) => request.path === '/api/update-action'), false);
});

test('install requests go directly to native confirmation and follow installing state', async () => {
  const app = await page({ updates: updateState({ latest_version: '0.6.2', available: true }) });
  app.override = (request) => {
    if (request.path !== '/api/update-action') return undefined;
    assert.equal(request.data.action, 'install');
    app.snapshot.updates.installing = true;
    return response({ ok: true });
  };
  await app.$('#update-pill').emit('click');
  assert.equal(app.requests.filter((request) => request.path === '/api/update-action').length, 1);
  assert.equal(app.$('#update-pill').disabled, true);
  assert.equal(app.$('#update-pill-version').textContent, '安装中…');
  assert.match(app.$('#update-pill').title, /系统确认或安装向导/);
  assert.equal(app.$('#notice').hidden, true);
});

test('release notes are bounded plain text and background errors stay inline', async () => {
  const notes = '<img src=x onerror="alert(1)">\n' + 'release notes\n'.repeat(2000);
  const app = await page({ updates: updateState({
    latest_version: '0.6.2', available: true, release_notes: notes, error: 'Update check failed', last_checked: '2026-11-03T12:00:00Z',
  }) });
  assert.equal(app.$('#update-release-notes').textContent, notes.slice(0, 12000) + '…');
  assert.equal(app.$('#update-notes').hidden, false);
  assert.equal(app.$('#update-error').textContent, 'Update check failed');
  assert.equal(app.$('#update-error').hidden, false);
  assert.match(app.$('#update-last-checked').textContent, /2026/);
  assert.equal(app.$('#notice').hidden, true);
  app.snapshot.updates.error = '';
  app.snapshot.updates.release_notes = '';
  await app.poll();
  assert.equal(app.$('#update-error').hidden, true);
  assert.equal(app.$('#update-notes').hidden, true);
  app.snapshot.updates = updateState({ error: 'Update check failed', last_checked: '2026-11-03T12:00:00Z' });
  await app.poll();
  assert.equal(app.$('#update-pill-version').textContent, '重试更新');
  assert.equal(app.$('#update-pill').title, 'Update check failed');
  assert.equal(app.$('#update-pill').classList.contains('error'), true);
  assert.equal(app.$('#notice').hidden, true);
});

test('failed automatic preference requests restore the checkbox and report inline', async () => {
  const app = await page({ updates: updateState() });
  app.override = (request) => request.path === '/api/update-action' ? response({ error: 'App busy', code: 'action' }, false) : undefined;
  app.$('#update-automatic').checked = false;
  await app.$('#update-automatic').emit('change');
  assert.equal(app.$('#update-automatic').checked, true);
  assert.equal(app.$('#update-automatic').disabled, false);
  assert.equal(app.$('#update-error').textContent, 'App busy');
  assert.equal(app.$('#notice').hidden, true);
  assert.equal(app.$('#save-state').classList.contains('dirty'), false);
});

test('a poll started before an update action cannot replace its new update state', async () => {
  const app = await page({ updates: updateState() });
  const stale = deferred();
  let delayNextState = true;
  app.override = (request) => {
    if (request.path === '/api/state' && delayNextState) {
      delayNextState = false;
      return stale.promise;
    }
    if (request.path === '/api/update-action') {
      app.snapshot.updates = updateState({ latest_version: '0.6.2', available: true });
      return response({ ok: true });
    }
    return undefined;
  };
  const polling = app.poll();
  await settle();
  await app.$('#update-check').emit('click');
  assert.equal(app.$('#update-pill').hidden, false);
  stale.resolve(response({ state: 'stopped', revision: 'original', updates: updateState() }));
  await polling;
  assert.equal(app.$('#update-pill').hidden, false);
  assert.equal(app.$('#update-latest').textContent, '最新版本 0.6.2');
});

test('a saved update preference preserves an existing draft and its revision protection', async () => {
  const app = await page({ updates: updateState() });
  await app.input('#level', 'debug');
  app.override = (request) => {
    if (request.path !== '/api/update-action') return undefined;
    app.snapshot.updates.automatic = false;
    app.snapshot.revision = 'preference';
    return response({ ok: true });
  };
  app.$('#update-automatic').checked = false;
  await app.$('#update-automatic').emit('change');
  assert.equal(app.$('#level').value, 'debug');
  assert.equal(app.$('#save-state').classList.contains('dirty'), true);
  assert.equal(app.$('#save').disabled, true);
  assert.equal(app.$('#notice-reload').hidden, false);
  await app.$('#yaml-tab').emit('click');
  assert.match(app.$('#raw-yaml').value, /log_level: debug/);
  assert.equal(app.requests.some((request) => request.method === 'POST' && request.path === '/api/config'), false);
  assert.equal(app.requests.some((request) => request.path === '/api/action'), false);
});

test('English preference translates dynamic states and uses English API diagnostics', async () => {
  const app = await page({ state: 'blocked', updates: updateState({ downloading: true, downloaded_bytes: 512, total_bytes: 1024 }) }, '', { preference: 'en', language: 'en', languages: ['en-US'] });
  assert.equal(app.i18n.language, 'en');
  assert.equal(app.$('#ui-language').value, 'en');
  assert.equal(app.$('#save-state span:last-child').textContent, 'All changes saved');
  assert.equal(app.$('#connection-status span').textContent, 'Exit unavailable · Blocked');
  assert.equal(app.$('#update-pill-version').textContent, 'Downloading 50%');
  assert.equal(app.requests.find(r => r.path === '/api/config').headers['Accept-Language'], 'en');
});

test('language changes preserve incomplete YAML drafts without saving or reconnecting', async () => {
  const app = await page({ desktop: true, updates: updateState() });
  await app.$('#yaml-tab').emit('click');
  await app.input('#raw-yaml', 'unfinished: [');
  app.$('#ui-language').value = 'en';
  await app.$('#ui-language').emit('change');
  assert.equal(app.i18n.language, 'en');
  assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
  assert.equal(app.$('#yaml-tab').attributes.get('aria-selected'), 'true');
  assert.equal(app.$('#save-state span:last-child').textContent, 'Checking changes…');
  assert.equal(app.requests.some(r => r.path === '/api/action' || r.method === 'POST' && r.path === '/api/config'), false);
  app.$('#ui-language').value = 'zh';
  await app.$('#ui-language').emit('change');
  assert.equal(app.i18n.language, 'zh');
  assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
});

test('a failed language save restores the previous preference and keeps the form draft', async () => {
  const app = await page();
  await app.input('#level', 'debug');
  app.override = r => r.path === '/api/language' && r.method === 'POST' ? response({ error: 'Read-only settings directory' }, false) : undefined;
  app.$('#ui-language').value = 'en';
  await app.$('#ui-language').emit('change');
  assert.equal(app.i18n.language, 'zh');
  assert.equal(app.$('#ui-language').value, 'auto');
  assert.equal(app.$('#ui-language').disabled, false);
  assert.equal(app.$('#level').value, 'debug');
  assert.equal(app.$('#notice-title').textContent, '无法保存语言偏好');
  assert.equal(app.$('#notice-detail').textContent, 'Read-only settings directory');
});

test('pending language requests do not block edits or send duplicate requests', async () => {
  const app = await page();
  const pending = deferred();
  app.override = r => r.path === '/api/language' && r.method === 'POST' ? pending.promise : undefined;
  app.$('#ui-language').value = 'en';
  const changing = app.$('#ui-language').emit('change');
  await app.$('#ui-language').emit('change');
  await app.input('#level', 'debug');
  assert.equal(app.$('#level').value, 'debug');
  pending.resolve(response({ preference: 'en', language: 'en' }));
  await changing;
  assert.equal(app.i18n.language, 'en');
  assert.equal(app.requests.filter(r => r.path === '/api/language' && r.method === 'POST').length, 1);
  assert.equal(app.$('#level').value, 'debug');
});

test('changing language cannot restore a stale connected indicator after a failed poll', async () => {
  const app = await page({ state: 'running', active: true, mode: 'proxy' });
  assert.equal(app.$('#connection-status').classList.contains('connected'), true);
  app.override = r => r.path === '/api/state' ? Promise.reject(new Error('Offline')) : undefined;
  await app.poll();
  app.$('#ui-language').value = 'en';
  await app.$('#ui-language').emit('change');
  assert.equal(app.$('#connection-status').classList.contains('connected'), false);
  assert.equal(app.$('#connection-status span').textContent, 'Page connection lost');
});
