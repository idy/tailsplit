'use strict';
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = readFileSync(process.argv[2] || join(__dirname, '../assets/app.js'), 'utf8');
const i18nSource = readFileSync(join(__dirname, '../assets/i18n.js'), 'utf8');
const yaml = 'log_level: info\ntun:\n  mtu: 1400\n';
const firstAddedID = `exit-${'01'.repeat(12)}`;
const secondAddedID = `exit-${'02'.repeat(12)}`;
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
    this.textContent = ''; this.listeners = {}; this.attributes = new Map(); this.classes = new Set(); this.children = [];
    this.scrollHeight = 0; this.clientHeight = 0; this.scrollOffset = 0;
    this.classList = {
      toggle: (name, enabled) => enabled ? this.classes.add(name) : this.classes.delete(name),
      contains: (name) => this.classes.has(name), remove: (name) => this.classes.delete(name),
    };
  }
  get scrollTop() { return this.scrollOffset; }
  set scrollTop(value) { this.scrollOffset = Math.min(Math.max(0, value), Math.max(0, this.scrollHeight - this.clientHeight)); }
  addEventListener(name, callback) { (this.listeners[name] ||= []).push(callback); }
  setAttribute(name, value) { this.attributes.set(name, value); }
  removeAttribute(name) { this.attributes.delete(name); }
  append(...children) { this.children.push(...children.flatMap((child) => child.fragment ? child.children : [child])); }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  querySelectorAll(selector) {
    const descendants = this.children.flatMap(child => [child, ...child.querySelectorAll(selector)]);
    return selector === '[data-exit-choice]' ? descendants.filter(child => child.dataset.exitChoice) : [];
  }
  showModal() { this.open = true; }
  close() { this.open = false; this.emit('close'); }
  focus() {}
  async emit(name, event = {}) {
    if (this.disabled && ['click', 'input', 'change', 'keydown'].includes(name)) return;
    await Promise.all((this.listeners[name] || []).map((callback) => callback({
      target: this, preventDefault() {}, ...event,
    })));
  }
}

async function page(snapshot = {}, native = '', localeOptions = {}, initialDocument = doc()) {
  const nodes = new Map();
  const $ = (selector) => {
    if (!nodes.has(selector)) nodes.set(selector, new Element());
    return nodes.get(selector);
  };
  const level = $('#log-level'); nodes.set('#level', level); level.dataset.field = 'log_level';
  const mtu = $('#mtu'); mtu.dataset = { field: 'tun.mtu', kind: 'int' };
  const tun = $('#tun-enabled'); tun.type = 'checkbox'; tun.dataset.field = 'tun.enabled';
  const ruleSource = $('#rule-source'); ruleSource.dataset.field = 'rules.source';
  const routingMode = $('#routing-mode'); routingMode.dataset.field = 'rules.mode';
  const globalExit = $('#global-exit'); globalExit.dataset.field = 'rules.global_exit';
  const defaultExit = $('#default-exit'); defaultExit.dataset.field = 'rules.default_exit';
  const finalRoute = $('#final-route'); finalRoute.dataset.field = 'rules.final';
  const fields = [level, mtu, tun, ruleSource, routingMode, globalExit, defaultExit, finalRoute];
  const pages = ['home', 'connection', 'logs', 'settings'].map((name) => {
    const nav = $(`[data-page="${name}"]`); nav.dataset.page = name; return nav;
  });
  const sections = ['general', 'network', 'dns', 'routing', 'advanced', 'about'].map((name) => {
    const nav = $(`[data-section="${name}"]`); nav.dataset.section = name; return nav;
  });
  const modes = ['split', 'global'].map((mode) => {
    const button = $(`[data-routing-mode="${mode}"]`); button.dataset.routingMode = mode; return button;
  });
  const desktopActions = ['connect', 'disconnect', 'login', 'refresh-exits'].map((action) => {
    const selector = action === 'refresh-exits' ? '#refresh-exits' : `#desktop-${action}`;
    const button = $(selector); button.dataset.desktopAction = action; return button;
  });
  const appPages = ['home', 'connection', 'logs', 'settings'].map((name) => { const element = $(`#page-${name}`); element.id = `page-${name}`; return element; });
  const settingsPages = sections.map((nav) => { const element = $(`#section-${nav.dataset.section}`); element.id = `section-${nav.dataset.section}`; return element; });
  $('#form-tab').setAttribute('aria-selected', 'true');
  $('#yaml-tab').setAttribute('aria-selected', 'false');
  $('#form-tab').tabIndex = 0;
  $('#yaml-tab').tabIndex = -1;
  $('#yaml-editor').hidden = true;
  const navigation = sections[0];
  const events = new Element();
  const requests = [];
  const timers = new Map();
  let timer = 0;
  let poll;
  const window = new Element();
  let nonce = 0;
  let operationNonce = 0;
  window.crypto = { getRandomValues: (bytes) => { bytes.fill(++nonce); return bytes; }, randomUUID: () => `operation-${++operationNonce}` };
  const messages = [];
  if (native.startsWith('mygo-')) {
    window.mygo = { platform: native === 'mygo-macos' ? 'darwin' : 'win32', window: {
      minimize: async () => messages.push('minimize'),
      toggleMaximize: async () => messages.push('zoom'),
      close: async () => messages.push('close'),
    } };
    // WebView2 exists in MyGo too; it must not receive the legacy string bridge.
    if (native === 'mygo-windows') window.chrome = { webview: { postMessage: () => assert.fail('legacy bridge used in MyGo') } };
  }
  const windowButtons = ['minimize', 'zoom', 'close'].map((action) => {
    const button = $(`[data-window-action="${action}"]`);
    button.dataset.windowAction = action;
    return button;
  });
  $('#window-controls').hidden = true;
  const app = { $, requests, messages, canceledOperations: new Set(), windowEvents: window, config: initialDocument, snapshot: { state: 'stopped', revision: 'original', operation_id: snapshot.active ? 'existing-operation' : '', active_revision: snapshot.active && ['proxy', 'tun'].includes(snapshot.mode) ? snapshot.revision || 'original' : '', ...snapshot },
    language: { preference: localeOptions.preference || 'auto', language: localeOptions.language || 'zh' }, override: localeOptions.override || (() => undefined) };
  const document = {
    documentElement: new Element(), hidden: false, querySelector: $, createElement: (tag) => { const element = new Element(); element.tagName = tag; return element; },
    createElementNS: (_namespace, tag) => { const element = new Element(); element.tagName = tag; return element; },
    createTextNode: () => new Element(), createDocumentFragment: () => { const fragment = new Element(); fragment.fragment = true; return fragment; },
    querySelectorAll: (selector) => selector === '[data-field]' ? fields :
      selector === '[data-window-action]' ? windowButtons :
      selector === '[data-section]' ? sections :
      selector === '[data-page]' ? pages : selector === '.app-page' ? appPages : selector === '.settings-page' ? settingsPages :
      selector === '[data-routing-mode]' ? modes : selector === '[data-desktop-action]' ? desktopActions :
      selector === '.editor-tabs button, [data-section]' ? [$('#form-tab'), $('#yaml-tab'), ...sections] : [],
    addEventListener: (...args) => events.addEventListener(...args),
  };
  app.document = document;
  const storage = localeOptions.storage || new Map();
  const sharedStorage = localeOptions.localStorage || new Map();
  app.storage = storage;
  app.localStorage = sharedStorage;
  const storageAPI = data => ({ setItem(key, value) { data.set(key, value); }, getItem(key) { return data.get(key); }, removeItem(key) { data.delete(key); }, key(index) { return [...data.keys()][index] ?? null; }, get length() { return data.size; } });
  const sandbox = {
    document, window, URL, URLSearchParams, location: { hash: localeOptions.hash || '#token=fixture', pathname: '/' },
    history: { replaceState() {} }, sessionStorage: storageAPI(storage), localStorage: storageAPI(sharedStorage),
    navigator: { languages: localeOptions.languages || ['zh-CN'], clipboard: { writeText: async () => {} } },
    setTimeout: (callback) => { timers.set(++timer, callback); return timer; },
    clearTimeout: (id) => timers.delete(id), setInterval: (callback) => { poll = callback; },
    fetch: async (path, options) => {
      const request = { path, method: options.method, headers: options.headers, data: options.body && JSON.parse(options.body) };
      requests.push(request);
      if (path === '/api/action' && request.data.operation_id) {
        if (app.canceledOperations.has(request.data.operation_id)) return response({ error: 'operation canceled' }, false);
        app.snapshot.operation_id = request.data.operation_id;
      }
      const overridden = app.override(request);
      if (overridden !== undefined) return overridden;
      if (path === '/api/language') {
        if (request.method === 'POST') app.language = { preference: request.data.preference, language: request.data.preference === 'auto' ? localeOptions.language || 'zh' : request.data.preference };
        return response(app.language);
      }
      if (path === '/api/state') return response(app.snapshot);
      if (path === '/api/connect-request') {
        const claimed = Boolean(request.data.request) && !app.canceledOperations.has(request.data.operation_id || request.data.request) && request.data.request === app.snapshot.connect_request;
        if (claimed) app.snapshot.connect_request = '';
        return response({ claimed });
      }
      if (path === '/api/cancel') {
        const { request: id, operation_id: operation } = request.data;
        if (id && id === app.snapshot.connect_request) app.snapshot.connect_request = '';
        if (operation) app.canceledOperations.add(operation);
        if (operation && operation === app.snapshot.operation_id) Object.assign(app.snapshot, { active: false, state: 'stopped', mode: '' });
        return response({ ok: true });
      }
      if (path === '/api/config') return response(app.config);
      if (path === '/api/preview') {
        let text = request.data.yaml;
        if (request.data.changes?.log_level) text = text.replace(/log_level: \w+/, `log_level: ${request.data.changes.log_level}`);
        if (request.data.changes?.['tun.mtu']) text = text.replace(/mtu: \S+/, `mtu: ${request.data.changes['tun.mtu']}`);
        return response(doc(text));
      }
      return response({ valid: true });
    },
  };
  app.location = sandbox.location;
  vm.createContext(sandbox);
  vm.runInContext(i18nSource, sandbox, { filename: 'i18n.js' });
  vm.runInContext(source, sandbox, { filename: 'app.js' });
  app.i18n = window.TailSplitI18n;
  await settle();
  assert.equal($('#level').value, 'info');
  // Most cases exercise Exit Nodes; home cases opt in to the default destination.
  if (localeOptions.page !== 'home') { await $('[data-page="connection"]').emit('click'); await settle(); }
  app.poll = () => poll();
  app.flushTimers = async () => {
    const callbacks = [...timers.values()]; timers.clear();
    for (const callback of callbacks) callback();
    await settle();
  };
  app.input = async (selector, value) => {
    const control = $(selector);
    if (control.disabled) return;
    control.value = value;
    await control.emit('input');
  };
  return app;
}

function ruleDocument(source, key = 'source') {
  const text = `log_level: info\ntailscale: {}\nrules:\n  ${key}: ${JSON.stringify(source)}\n`;
  return { ...doc(text), values: { log_level: 'info', rules: { [key]: source } } };
}

test('rule source form reads new and legacy source selections without changing YAML', async () => {
  for (const key of ['source', 'gfwlist']) {
    for (const [source, mode] of [['builtin', 'builtin'], ['https://rules.example/list.txt', 'custom'], ['./rules.txt', 'custom'], ['', '']]) {
      const initial = ruleDocument(source, key);
      const app = await page({}, '', {}, initial);
      assert.equal(app.$('#rule-source-type').value, mode);
      assert.equal(app.$('#rule-source').value, source);
      assert.equal(app.$('#custom-rule-source').hidden, mode !== 'custom');
      assert.equal(app.$('#builtin-rule-hint').hidden, mode !== 'builtin');
      assert.equal(app.requests.some((request) => request.path === '/api/preview'), false);
      assert.equal(app.$('#save').disabled, true);
    }
  }
});

test('null rule sources use the effective legacy source in the form', async () => {
  for (const [source, mode] of [['builtin', 'builtin'], ['https://rules.example/list.txt', 'custom'], ['./rules.txt', 'custom'], ['', '']]) {
    for (const scalar of ['null', '']) {
      const initial = ruleDocument(source, 'gfwlist');
      initial.yaml += `  source: ${scalar}\n`;
      initial.values.rules.source = null;
      const app = await page({}, '', {}, initial);
      assert.equal(app.$('#rule-source-type').value, mode);
      assert.equal(app.$('#rule-source').value, source);
      assert.equal(app.$('#custom-rule-source').hidden, mode !== 'custom');
      assert.equal(app.$('#save').disabled, true);
      assert.equal(app.requests.some((request) => request.path === '/api/preview'), false);
    }
  }
});

test('switching rule sources retains a custom URL and writes the chosen source', async () => {
  const app = await page({}, '', {}, ruleDocument('builtin'));
  app.override = (request) => request.path === '/api/preview'
    ? response(ruleDocument(request.data.changes['rules.source'])) : undefined;
  const selector = app.$('#rule-source-type');
  selector.value = 'custom'; await selector.emit('change');
  assert.equal(app.$('#custom-rule-source').hidden, false);
  assert.equal(app.$('#rule-source-error').hidden, false);
  assert.equal(app.$('#validate').disabled, true);
  assert.equal(app.$('#save').disabled, true);
  await app.input('#rule-source', 'https://rules.example/custom.txt');
  assert.equal(app.$('#rule-source-error').hidden, true);
  selector.value = 'builtin'; await selector.emit('change');
  assert.equal(app.$('#rule-source').value, 'builtin');
  assert.equal(app.$('#custom-rule-source').hidden, true);
  selector.value = 'custom'; await selector.emit('change');
  assert.equal(app.$('#rule-source').value, 'https://rules.example/custom.txt');
  await app.$('#yaml-tab').emit('click');
  assert.match(app.$('#raw-yaml').value, /source: "https:\/\/rules.example\/custom.txt"/);
  await app.$('#form-tab').emit('click');
  selector.value = ''; await selector.emit('change');
  await app.$('#yaml-tab').emit('click');
  assert.match(app.$('#raw-yaml').value, /source: ""/);
});

test('an empty custom source can be repaired and saved in the YAML editor', async () => {
  const app = await page({}, '', {}, ruleDocument('builtin'));
  const selector = app.$('#rule-source-type');
  app.override = (request) => {
    if (request.path !== '/api/preview') return undefined;
    return response(ruleDocument(request.data.changes?.['rules.source'] ?? 'https://rules.example/repaired.txt'));
  };
  selector.value = 'custom'; await selector.emit('change');
  assert.equal(app.$('#save').disabled, true);
  await app.$('#yaml-tab').emit('click');
  assert.equal(app.$('#validate').disabled, false, 'a hidden empty form must not block valid YAML');
  const repaired = ruleDocument('https://rules.example/repaired.txt');
  await app.input('#raw-yaml', repaired.yaml);
  await app.$('#form-tab').emit('click');
  await app.$('#yaml-tab').emit('click');
  await app.$('#validate').emit('click');
  assert.equal(app.requests.some((request) => request.path === '/api/validate' && request.data.yaml === repaired.yaml), true);
  assert.equal(app.$('#save').disabled, false);
  await app.$('#form-tab').emit('click');
  assert.equal(app.$('#rule-source').value, 'https://rules.example/repaired.txt');
  assert.equal(app.$('#rule-source-error').hidden, true);
});

test('rule source selection follows raw YAML edits when returning to the form', async () => {
  const app = await page({}, '', {}, ruleDocument('builtin'));
  await app.$('#yaml-tab').emit('click');
  const custom = ruleDocument('https://rules.example/from-yaml.txt');
  app.override = (request) => request.path === '/api/preview' ? response(custom) : undefined;
  await app.input('#raw-yaml', custom.yaml);
  await app.$('#form-tab').emit('click');
  assert.equal(app.$('#rule-source-type').value, 'custom');
  assert.equal(app.$('#rule-source').value, 'https://rules.example/from-yaml.txt');
  assert.equal(app.$('#custom-rule-source').hidden, false);
});

test('desktop logs render sing-box foreground colors without exposing ANSI sequences', async () => {
  const app = await page({ logs_available: true });
  const text = '\x1b[36mINFO\x1b[0m[9403] [\x1b[38;5;168m3717191320\x1b[0m 0ms] dns: lookup succeeded for example.com\n';
  app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
  await app.$('#desktop-log-refresh').emit('click');
  const spans = app.$('#desktop-log-text').children;
  assert.equal(spans.map((span) => span.textContent).join(''), 'INFO[9403] [3717191320 0ms] dns: lookup succeeded for example.com\n');
  assert.equal(spans.find((span) => span.textContent === 'INFO').className, 'log-cyan');
  assert.equal(spans.find((span) => span.textContent === '3717191320').className, 'log-magenta');
  assert.equal(spans.at(-1).className, '');
  assert.equal(app.$('#desktop-log-refresh').disabled, false);
});

test('desktop log palettes handle basic, bright, indexed and RGB colors and SGR resets', async () => {
  const app = await page({ logs_available: true });
  for (const [codes, expected] of [
    ['31', 'log-red'], ['93', 'log-yellow'], ['38;5;191', 'log-yellow'],
    ['38;5;4', 'log-blue'], ['38;5;244', 'log-muted'], ['38;2;0;180;0', 'log-green'],
    ['1;35', 'log-magenta log-bold'], ['31;48;5;4', 'log-red'], ['31;58;2;1;2;3', 'log-red'],
    ['38;5;300', ''], ['38;2;999;0;0', ''], ['38;2;255', ''],
  ]) {
    app.override = (request) => request.path === '/api/logs' ? response({ text: `\x1b[${codes}mcolored\x1b[m normal` }) : undefined;
    await app.$('#desktop-log-refresh').emit('click');
    const spans = app.$('#desktop-log-text').children;
    assert.equal(spans[0].textContent, 'colored', codes);
    assert.equal(spans[0].className, expected, codes);
    assert.equal(spans[1].className, '', codes);
  }
  app.override = (request) => request.path === '/api/logs'
    ? response({ text: '\x1b[1;31mfirst\x1b[39msecond\x1b[22mthird' }) : undefined;
  await app.$('#desktop-log-refresh').emit('click');
  assert.deepEqual(app.$('#desktop-log-text').children.map((span) => span.className), ['log-red log-bold', 'log-bold', '']);
});

test('desktop logs discard terminal commands and render HTML and terminal hyperlinks as text', async () => {
  const app = await page({ logs_available: true });
  const text = '\x1b[2J\x1b[H\x1b]0;terminal title\x07\x1b]8;;https://example.com\x1b\\example.com\x1b]8;;\x1b\\\n' +
    '<img src=x onerror=alert(1)> & <script>alert(1)</script>\x00\x07\x1b[38;5;';
  app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
  await app.$('#desktop-log-refresh').emit('click');
  const spans = app.$('#desktop-log-text').children;
  assert.equal(spans.map((span) => span.textContent).join(''), 'example.com\n<img src=x onerror=alert(1)> & <script>alert(1)</script>');
  assert.ok(spans.every((span) => span.tagName === 'span' && span.children.length === 0));
});

test('desktop log refresh replaces old styling and handles empty and failed reads', async () => {
  const app = await page({ logs_available: true });
  for (const text of ['\x1b[31mred', 'plain text\n']) {
    app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
    await app.$('#desktop-log-refresh').emit('click');
  }
  assert.equal(app.$('#desktop-log-text').children.length, 1);
  assert.equal(app.$('#desktop-log-text').children[0].className, '');
  assert.equal(app.$('#desktop-log-text').children[0].textContent, 'plain text\n');
  app.override = (request) => request.path === '/api/logs' ? response({ text: '\x1b[0m' }) : undefined;
  await app.$('#desktop-log-refresh').emit('click');
  assert.equal(app.$('#desktop-log-text').textContent, '暂无日志。');
  app.i18n.setLanguage('en');
  assert.equal(app.$('#desktop-log-text').textContent, 'No logs yet.');
  app.override = (request) => request.path === '/api/logs' ? response({ error: 'Could not read logs.' }, false) : undefined;
  await app.$('#desktop-log-refresh').emit('click');
  assert.equal(app.$('#desktop-log-error').textContent, 'Could not read logs.');
  assert.equal(app.$('#desktop-log-error').hidden, false);
  assert.equal(app.$('#desktop-log-text').textContent, 'No logs yet.');
  assert.equal(app.$('#desktop-log-refresh').disabled, false);
});

test('desktop logs poll only while visible and initially show the latest entry', async () => {
  const app = await page({ logs_available: true });
  const output = app.$('#desktop-log-text');
  output.clientHeight = 200; output.scrollHeight = 1000;
  app.override = (request) => request.path === '/api/logs' ? response({ text: 'latest entry\n' }) : undefined;
  await app.poll();
  assert.equal(app.requests.some((request) => request.path === '/api/logs'), false);
  await app.$('[data-page="logs"]').emit('click');
  await settle();
  assert.equal(output.scrollTop, 800);
  const firstSpan = output.children[0];
  app.requests.length = 0;
  await app.poll();
  assert.equal(app.requests.filter((request) => request.path === '/api/logs').length, 1);
  assert.equal(output.children[0], firstSpan, 'unchanged logs should preserve text nodes and selection');
  app.document.hidden = true;
  app.requests.length = 0;
  await app.poll();
  assert.equal(app.requests.length, 0);
  app.document.hidden = false;
  await app.$('[data-page="settings"]').emit('click');
  app.requests.length = 0;
  await app.poll();
  assert.equal(app.requests.some((request) => request.path === '/api/logs'), false);
});

test('desktop logs preserve a reader position and resume following at the bottom', async () => {
  const app = await page({ logs_available: true });
  const output = app.$('#desktop-log-text');
  output.clientHeight = 200; output.scrollHeight = 1000;
  let text = 'first entry\n';
  app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
  await app.$('[data-page="logs"]').emit('click');
  await settle();
  assert.equal(output.scrollTop, 800);
  output.scrollTop = 120;
  await output.emit('scroll');
  const pausedSpan = output.children[0];
  text += 'second entry\n';
  await app.poll();
  assert.equal(output.scrollTop, 120, 'new entries must not pull a reader back down');
  assert.equal(output.children[0], pausedSpan, 'paused snapshots must keep their text and selection');
  assert.doesNotMatch(output.children.map((span) => span.textContent).join(''), /second entry/);
  output.scrollTop = output.scrollHeight;
  await output.emit('scroll');
  assert.match(output.children.map((span) => span.textContent).join(''), /second entry/);
  text += 'third entry\n'; output.scrollHeight = 1400;
  await app.poll();
  assert.equal(output.scrollTop, 1200);
});

test('desktop logs respect scrolling that happens while a refresh is in flight', async () => {
  const app = await page({ logs_available: true });
  const output = app.$('#desktop-log-text');
  output.clientHeight = 200; output.scrollHeight = 1000;
  app.override = (request) => request.path === '/api/logs' ? response({ text: 'first entry\n' }) : undefined;
  await app.$('[data-page="logs"]').emit('click');
  await settle();
  const pending = deferred();
  app.override = (request) => request.path === '/api/logs' ? pending.promise : undefined;
  const polling = app.poll();
  await settle();
  output.scrollTop = 90;
  await output.emit('scroll');
  output.scrollHeight = 1200;
  pending.resolve(response({ text: 'first entry\nsecond entry\n' }));
  await polling;
  assert.equal(output.scrollTop, 90);
});

test('paused log snapshots survive a bounded tail that drops leading entries', async () => {
  const app = await page({ logs_available: true });
  const output = app.$('#desktop-log-text');
  output.clientHeight = 200; output.scrollHeight = 1000;
  const original = 'oldest entry\nretained entry\nlatest entry\n';
  let text = original;
  app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
  await app.$('[data-page="logs"]').emit('click');
  await settle();
  output.scrollTop = 120; await output.emit('scroll');
  const pausedSpan = output.children[0];
  text = 'retained entry\nlatest entry\nnew entry\n';
  await app.poll();
  assert.equal(output.children[0], pausedSpan);
  assert.equal(output.children.map((span) => span.textContent).join(''), original);
  assert.equal(output.scrollTop, 120);
  const reads = app.requests.filter((request) => request.path === '/api/logs').length;
  output.scrollTop = output.scrollHeight; await output.emit('scroll');
  assert.equal(output.children.map((span) => span.textContent).join(''), text);
  assert.equal(app.requests.filter((request) => request.path === '/api/logs').length, reads);
});

test('failed log refreshes preserve the last output and paused following state', async () => {
  const app = await page({ logs_available: true });
  const output = app.$('#desktop-log-text');
  output.clientHeight = 200; output.scrollHeight = 1000;
  app.override = (request) => request.path === '/api/logs' ? response({ text: 'original entry\n' }) : undefined;
  await app.$('[data-page="logs"]').emit('click');
  await settle();
  output.scrollTop = 120; await output.emit('scroll');
  const pausedSpan = output.children[0];
  app.override = (request) => request.path === '/api/logs' ? response({ error: 'Could not read logs.' }, false) : undefined;
  await app.poll();
  assert.equal(app.$('#desktop-log-error').hidden, false);
  assert.equal(app.$('#desktop-log-error').textContent, 'Could not read logs.');
  assert.equal(output.children[0], pausedSpan);
  assert.equal(output.scrollTop, 120);
  app.i18n.setLanguage('en');
  assert.equal(output.children[0], pausedSpan, 'error translation must not replace the paused snapshot');
  app.override = (request) => request.path === '/api/logs' ? response({ text: 'original entry\nrecovered entry\n' }) : undefined;
  await app.poll();
  assert.equal(app.$('#desktop-log-error').hidden, true);
  assert.equal(output.children[0], pausedSpan);
  assert.equal(output.scrollTop, 120);
  output.scrollTop = output.scrollHeight; await output.emit('scroll');
  assert.match(output.children.map((span) => span.textContent).join(''), /recovered entry/);
});

test('reopening Logs resets paused following and displays the latest file contents', async () => {
  const app = await page({ logs_available: true });
  const output = app.$('#desktop-log-text');
  output.clientHeight = 200; output.scrollHeight = 1000;
  let text = 'first entry\n';
  app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
  await app.$('[data-page="logs"]').emit('click'); await settle();
  output.scrollTop = 120; await output.emit('scroll');
  await app.$('[data-page="settings"]').emit('click');
  output.scrollTop = 0; // Hidden web views can drop their scroll offset.
  text += 'second entry\n';
  await app.$('[data-page="logs"]').emit('click'); await settle();
  assert.match(output.children.map((span) => span.textContent).join(''), /second entry/);
  assert.equal(output.scrollTop, 800);
  text += 'third entry\n'; output.scrollHeight = 1200;
  await app.poll();
  assert.equal(output.scrollTop, 1000);
});

test('unsupported terminal commands and control strings leave no visible fragments', async () => {
  const app = await page({ logs_available: true });
  for (const text of [
    '\x1b7INFO\x1b8', '\x1b(BINFO\x1b#8',
    '\x1bP1;2|hidden payload\x1b\\INFO', '\x1b_hidden payload\x1b\\INFO',
    '\x901;2|hidden payload\x9cINFO', '\x9b36mINFO\x9b0m',
    'INFO\x1bPunfinished payload',
  ]) {
    app.override = (request) => request.path === '/api/logs' ? response({ text }) : undefined;
    await app.$('#desktop-log-refresh').emit('click');
    assert.equal(app.$('#desktop-log-text').children.map((span) => span.textContent).join(''), 'INFO');
  }
});

test('every page exposes its own refresh in the shared titlebar slot without a separate exit label', async () => {
  const app = await page({ desktop: true, logs_available: true });
  for (const [name, refresh] of [['connection', '#refresh-exits'], ['settings', '#reload'], ['logs', '#desktop-log-refresh']]) {
    await app.$(`[data-page="${name}"]`).emit('click');
    for (const id of ['#refresh-exits', '#reload', '#desktop-log-refresh']) assert.equal(app.$(id).hidden, id !== refresh);
    assert.equal(app.$('#settings-tools').hidden, name !== 'settings');
    if (name === 'settings') {
      const reads = app.requests.filter(request => request.path === '/api/config').length;
      await app.$(refresh).emit('click');
      assert.equal(app.requests.filter(request => request.path === '/api/config').length, reads + 1);
    }
  }
});

test('MyGo uses the existing platform chrome and its own window controls without duplicate drag handlers', async () => {
  for (const platform of ['macos', 'windows']) {
    const app = await page({}, `mygo-${platform}`);
    assert.equal(app.document.documentElement.dataset.windowChrome, platform);
    assert.equal(app.document.documentElement.dataset.windowEngine, 'mygo');
    assert.equal(app.$('#window-controls').hidden, platform !== 'windows');
    const blank = { closest: () => null };
    await app.$('#titlebar').emit('mousedown', { button: 0, detail: 1, target: blank, clientX: 200, clientY: 20 });
    await app.windowEvents.emit('mousemove', { buttons: 1, clientX: 220, clientY: 20 });
    await app.$('#titlebar').emit('dblclick', { button: 0, target: blank });
    assert.deepEqual(app.messages, [], 'CSS/native drag regions must own titlebar gestures');
    if (platform === 'windows') {
      for (const action of ['minimize', 'zoom', 'close']) await app.$(`[data-window-action="${action}"]`).emit('click');
      assert.deepEqual(app.messages, ['minimize', 'zoom', 'close']);
    }
  }
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

test('current network follows authentication and keeps user names literal across languages', async () => {
  const app = await page({ desktop: true, network: '<Team>.ts.net', backend_state: 'Running' });
  assert.equal(app.$('#desktop-network-name').textContent, '<Team>.ts.net');
  app.i18n.setLanguage('en');
  assert.equal(app.$('#desktop-network-name').textContent, '<Team>.ts.net');
  app.snapshot.network = '';
  app.snapshot.backend_state = 'NeedsLogin';
  await app.poll();
  assert.equal(app.$('#desktop-network-name').textContent, 'Signed out');
  app.snapshot.backend_state = '';
  await app.poll();
  assert.equal(app.$('#desktop-network-name').textContent, 'Network not checked yet');
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
  for (const phase of ['checking', 'confirming', 'downloading', 'installing', 'busy']) {
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
    if (phase === 'confirming') assert.equal(app.$('#update-pill-version').textContent, '等待确认…');
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

test('server confirmation keeps update controls locked after POST has returned', async () => {
  const app = await page({ updates: updateState({ available: true, latest_version: '0.8.0' }) });
  app.override = request => {
    if (request.path !== '/api/update-action') return undefined;
    app.snapshot.updates = { ...app.snapshot.updates, busy: true, confirming: true };
    return response({ ok: true });
  };
  await app.$('#update-pill').emit('click');
  await app.poll();
  for (const selector of ['#update-check', '#update-pill', '#update-automatic']) assert.equal(app.$(selector).disabled, true);
  assert.equal(app.$('#update-pill-version').textContent, '等待确认…');
  await app.$('#update-pill').emit('click');
  await app.$('#update-automatic').emit('change');
  assert.equal(app.requests.filter(request => request.path === '/api/update-action').length, 1);
  app.snapshot.updates = { ...app.snapshot.updates, busy: false, confirming: false };
  await app.poll();
  assert.equal(app.$('#update-pill').disabled, false);
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

test('visible backend errors and location labels switch languages without retrying a save', async () => {
  const app = await page({}, '', { preference: 'en', language: 'en' });
  await app.input('#level', 'debug');
  await app.$('#yaml-tab').emit('click');
  const diagnostic = 'disk full: user/config.yaml';
  app.override = r => r.path === '/api/config' && r.method === 'POST' ? response({
    code: 'apply', error: 'Could not apply the configuration: ' + diagnostic,
    error_i18n: { en: 'Could not apply the configuration: ' + diagnostic, zh: '无法应用配置：' + diagnostic },
  }, false) : undefined;
  await app.$('#save').emit('click');
  assert.equal(app.$('#notice-detail').textContent, 'Could not apply the configuration: ' + diagnostic);
  for (const language of ['zh', 'en']) {
    app.$('#ui-language').value = language;
    await app.$('#ui-language').emit('change');
    assert.equal(app.$('#notice-detail').textContent, (language === 'zh' ? '无法应用配置：' : 'Could not apply the configuration: ') + diagnostic);
    assert.equal(app.$('#file-location').textContent, language === 'zh' ? '当前设备' : 'This device');
    assert.match(app.$('#raw-yaml').value, /log_level: debug/);
  }
  assert.equal(app.requests.filter(r => r.path === '/api/config' && r.method === 'POST').length, 1);
});

test('active sessions needing authentication can request sign-in without disconnecting', async () => {
  for (const mode of ['proxy', 'tun', 'login']) {
    for (const [state, backend_state] of [['login', 'NeedsLogin'], ['blocked', 'NeedsLogin'], ['login', 'Running']]) {
      const app = await page({ desktop: true, active: true, mode, state, backend_state }, '', {}, exitDocument());
      assert.equal(app.$('#desktop-login').hidden, false, `${mode}/${state}`);
      assert.equal(app.$('#desktop-login').disabled, false, `${mode}/${state}`);
      await app.$('#desktop-login').emit('click');
      assert.deepEqual(app.requests.filter(request => request.path === '/api/action').map(request => request.data.action), ['login']);
      assert.equal(app.snapshot.active, true);
      assert.equal(app.snapshot.mode, mode);
    }
  }
  const starting = await page({ desktop: true, active: true, mode: 'tun', state: 'starting', backend_state: '' });
  assert.equal(starting.$('#desktop-login').disabled, true);
});

test('log-level changes in YAML mode update the source and saved payload without losing other edits', async () => {
  const edited = '# Keep this comment\n' + yaml.replace('1400', '1500');
  const app = await page({}, '', {}, doc(edited));
  await app.$('[data-page="settings"]').emit('click');
  await app.$('#yaml-tab').emit('click');
  await app.$('[data-page="logs"]').emit('click');
  await app.input('#level', 'debug');
  assert.equal(app.$('#yaml-tab').attributes.get('aria-selected'), 'true');
  assert.equal(app.$('#raw-yaml').value, edited.replace('info', 'debug'));
  app.override = (request) => request.path === '/api/config' && request.method === 'POST'
    ? response(doc(request.data.yaml, 'updated')) : undefined;
  await app.$('#save').emit('click');
  const save = app.requests.find((request) => request.path === '/api/config' && request.method === 'POST');
  assert.equal(save.data.yaml, edited.replace('info', 'debug'));
});

test('an unparseable YAML draft disables log-level edits until the source is repaired', async () => {
  const app = await page();
  await app.$('#yaml-tab').emit('click');
  app.override = (request) => request.path === '/api/preview' && request.data.yaml === 'unfinished: ['
    ? response({ ...doc('unfinished: ['), valid: false, parsed: false, values: {}, error: 'Invalid YAML' }) : undefined;
  await app.input('#raw-yaml', 'unfinished: [');
  await app.$('#form-tab').emit('click');
  await app.$('[data-page="logs"]').emit('click');
  assert.equal(app.$('#level').disabled, true);
  await app.input('#level', 'debug');
  assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
  assert.equal(app.requests.some((request) => request.path === '/api/config' && request.method === 'POST'), false);
  await app.input('#raw-yaml', yaml);
  await app.$('#form-tab').emit('click');
  await app.$('#yaml-tab').emit('click');
  assert.equal(app.$('#level').disabled, false);
});

test('a pending YAML log-level patch protects raw input and a failed request restores the control', async () => {
  const app = await page();
  await app.$('#yaml-tab').emit('click');
  const pending = deferred();
  app.override = (request) => request.path === '/api/preview' && request.data.changes?.log_level === 'debug'
    ? pending.promise : undefined;
  const change = app.input('#level', 'debug');
  assert.equal(app.$('#raw-yaml').disabled, true);
  await app.input('#raw-yaml', '# newer edit\n' + yaml);
  pending.resolve(response({ error: 'Preview unavailable' }, false));
  await change;
  assert.equal(app.$('#raw-yaml').value, yaml);
  assert.equal(app.$('#level').value, 'info');
  assert.equal(app.$('#raw-yaml').disabled, false);
  await app.input('#level', 'warn');
  assert.match(app.$('#raw-yaml').value, /log_level: warn/);
});

const exitDocument = (exit = '100.64.0.10', mode = 'split', revision = 'original') => ({
  ...doc(`tailscale:\n  exit_node: ${exit}\nrules:\n  mode: ${mode}\ntun:\n  enabled: false\n`, revision),
  values: { log_level: 'info', tailscale: { exit_node: exit, tailnet: 'example.ts.net' }, rules: { mode }, tun: { enabled: false, mtu: 1400 } },
});
const exitsSnapshot = (changes = {}) => ({
  desktop: true, ping_available: true, backend_state: 'Running', hostname: 'my-device', network: 'example.ts.net',
  exit_nodes: [
    { id: '100.64.0.10', name: 'home', online: true, selected: true },
    { id: '100.64.0.20', name: 'travel', online: true },
    { id: '100.64.0.30', name: 'backup', online: false },
  ], ...changes,
});
const exitChoice = (app, id) => app.$('#exit-list').children.flatMap(row => row.children).find(child => child.dataset.exitChoice === id);

test('page navigation preserves incomplete YAML and keeps settings at a separate destination', async () => {
  const app = await page(exitsSnapshot());
  await app.$('[data-page="settings"]').emit('click');
  await app.$('#yaml-tab').emit('click');
  await app.input('#raw-yaml', 'unfinished: [');
  for (const name of ['logs', 'connection', 'settings']) {
    await app.$(`[data-page="${name}"]`).emit('click');
    assert.equal(app.$(`#page-${name}`).hidden, false);
    assert.equal(app.$('[data-page="settings"]').classes.has('active'), name === 'settings');
    assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
  }
  assert.equal(app.$('#yaml-tab').attributes.get('aria-selected'), 'true');
  assert.equal(app.requests.some((r) => r.method === 'POST' && r.path === '/api/config'), false);
});

test('an exit selection requires confirmation and saves only its field while disconnected', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await exitChoice(app, '100.64.0.20').emit('click');
  assert.equal(app.requests.some((r) => r.method === 'POST'), false);
  assert.equal(app.$('#connection-apply').hidden, false);
  assert.equal(app.$('#apply-connection-change').textContent, '保存选择');
  app.override = (request) => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { 'tailscale.exit_node': '100.64.0.20' });
    assert.equal(request.data.revision, 'original');
    app.config = exitDocument('100.64.0.20', 'split', 'new'); app.snapshot.revision = 'new';
    app.snapshot.exit_nodes.forEach((node) => { node.selected = node.id === '100.64.0.20'; });
    return response(app.config);
  };
  await app.$('#apply-connection-change').emit('click');
  assert.equal(app.$('#connection-apply').hidden, true);
  assert.equal(app.requests.filter((r) => r.path === '/api/action').length, 0);
});

test('quick-apply conflicts survive automatic document reloads and clear after a successful retry', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  const failure = { zh: '配置在其他位置更新，请重试。', en: 'Configuration changed elsewhere. Try again.' };
  let first = true;
  app.override = (request) => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    if (first) {
      first = false;
      app.config = exitDocument('100.64.0.10', 'split', 'external');
      app.config.yaml += '# External edit\n';
      app.snapshot.revision = 'external';
      return response({ code: 'conflict', error: failure.en, error_i18n: failure }, false);
    }
    assert.equal(request.data.revision, 'external');
    assert.match(request.data.yaml, /# External edit/);
    app.config = { ...exitDocument('100.64.0.20', 'split', 'recovered'), yaml: request.data.yaml.replace('exit_node: 100.64.0.10', 'exit_node: 100.64.0.20') };
    app.snapshot.revision = 'recovered';
    app.snapshot.exit_nodes.forEach(node => { node.selected = node.id === '100.64.0.20'; });
    return response(app.config);
  };
  await exitChoice(app, '100.64.0.20').emit('click');
  await app.$('#apply-connection-change').emit('click');
  assert.match(app.$('#raw-yaml').value, /# External edit/);
  assert.equal(app.$('#notice').hidden, false);
  assert.equal(app.$('#notice-title').textContent, '配置文件已更新');
  assert.equal(app.$('#notice-detail').textContent, failure.zh);
  assert.equal(app.$('#notice-reload').hidden, false);
  await app.poll();
  app.i18n.setLanguage('en');
  assert.equal(app.$('#notice').hidden, false);
  assert.equal(app.$('#notice-detail').textContent, failure.en);
  assert.equal(app.$('#apply-connection-change').disabled, false);
  await app.$('#apply-connection-change').emit('click');
  assert.equal(app.$('#notice').hidden, true);
  assert.equal(app.requests.filter(request => request.path === '/api/config' && request.method === 'POST').length, 2);
});

test('a reconnect failure remains visible after its saved configuration is automatically reloaded', async () => {
  const app = await page(exitsSnapshot({ active: true, mode: 'proxy', state: 'running' }), '', {}, exitDocument());
  const failure = 'Configuration saved, but reconnect failed';
  app.override = (request) => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    app.config = exitDocument('100.64.0.20', 'split', 'saved-before-failure');
    app.config.values.tun.enabled = true;
    app.config.yaml = app.config.yaml.replace('enabled: false', 'enabled: true');
    Object.assign(app.snapshot, { active: false, mode: '', state: 'failed', error: failure, revision: 'saved-before-failure' });
    return response({ code: 'apply', error: failure }, false);
  };
  await exitChoice(app, '100.64.0.20').emit('click');
  await app.$('#apply-connection-change').emit('click');
  assert.match(app.$('#raw-yaml').value, /exit_node: 100.64.0.20/);
  assert.equal(app.$('#connection-apply').hidden, true);
  assert.equal(app.$('#notice').hidden, false);
  assert.equal(app.$('#notice-title').textContent, '无法应用连接更改');
  assert.equal(app.$('#notice-detail').textContent, failure);
  await app.poll();
  assert.equal(app.$('#notice').hidden, false);
  await app.$('[data-page="settings"]').emit('click');
  await app.$('#reload').emit('click');
  assert.equal(app.$('#notice').hidden, true);
  assert.equal(app.$('#connection-status span').textContent, '连接异常');
});

test('routing mode is a settings draft, hides split controls, and saves with the selected global exit', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', routes: [{ exit: 'office', domains: ['corp.example'] }] };
  const app = await page(officeSnapshot(), '', {}, initial);
  await app.$('[data-page="settings"]').emit('click');
  await app.$('[data-section="routing"]').emit('click');
  await app.$('[data-routing-mode="global"]').emit('click');
  await app.input('#global-exit', 'office');
  assert.equal(app.$('#split-routing-fields').hidden, true);
  assert.equal(app.$('#split-routing-fields').disabled, true);
  assert.equal(app.$('#global-exit-row').hidden, false);
  assert.equal(app.$('#add-route').disabled, true);
  assert.equal(app.$('#tun-enabled').checked, true);
  assert.equal(app.$('#connection-apply').hidden, true);
  assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
  const updated = { ...initial, yaml: initial.yaml + '\nrules:\n  mode: global\n  global_exit: office\n',
    values: { ...initial.values, rules: { ...initial.values.rules, mode: 'global', global_exit: 'office' }, tun: { ...initial.values.tun, enabled: true } } };
  app.override = request => {
    if (request.path === '/api/preview') {
      assert.deepEqual(request.data.changes, { 'rules.mode': 'global', 'tun.enabled': true, 'rules.global_exit': 'office' });
      return response(updated);
    }
    if (request.path === '/api/config' && request.method === 'POST') {
      assert.match(request.data.yaml, /global_exit: office/);
      app.config = { ...updated, revision: 'global-saved' }; app.snapshot.revision = 'global-saved';
      return response(app.config);
    }
    return undefined;
  };
  // Let the debounced preview finish; only Save applies it to the actual config.
  await app.flushTimers();
  await app.$('#validate').emit('click');
  await app.$('#save').emit('click');
  assert.equal(app.requests.filter(r => r.path === '/api/config' && r.method === 'POST').length, 1);
  assert.equal(app.$('#global-exit').value, 'office');
  await app.$('[data-routing-mode="split"]').emit('click');
  assert.equal(app.$('#split-routing-fields').hidden, false);
  assert.equal(app.$('#global-exit-row').hidden, true);
  assert.equal(app.$('#route-groups').children.length, 1, 'switching modes preserves saved rule groups');
  assert.equal(app.$('#add-route').disabled, false);
});

test('settings drafts block quick apply without being saved or replaced', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await exitChoice(app, '100.64.0.20').emit('click');
  await app.input('#level', 'debug');
  assert.equal(app.$('#apply-connection-change').disabled, true);
  assert.equal(app.$('#connection-draft-warning').hidden, false);
  await app.$('#apply-connection-change').emit('click');
  assert.equal(app.requests.some((r) => r.method === 'POST' && r.path === '/api/config'), false);
  assert.equal(app.$('#level').value, 'debug');
  await app.$('[data-page="logs"]').emit('click');
  await app.$('[data-page="connection"]').emit('click');
  assert.equal(app.$('#level').value, 'debug');
  assert.match(app.$('#connection-selection').textContent, /travel/);
});

test('missing configured exits remain visible and offline exits cannot be selected', async () => {
  const snapshot = exitsSnapshot(); snapshot.exit_nodes.forEach((node) => { node.selected = false; });
  const app = await page(snapshot, '', {}, exitDocument('100.64.0.99'));
  const missing = exitChoice(app, '100.64.0.99');
  assert.equal(missing.attributes.get('aria-checked'), 'true');
  assert.equal(missing.disabled, true);
  assert.equal(exitChoice(app, '100.64.0.30').disabled, true);
  // An existing YAML selection is preserved, but it cannot be selected anew
  // through the picker unless discovery reports it online.
  assert.equal(app.$('#desktop-connect').disabled, false);
  assert.deepEqual(app.$('#exit-list').children.map(row => row.children.find(child => child.dataset.exitChoice).dataset.exitChoice), ['100.64.0.99', '100.64.0.10', '100.64.0.20', '100.64.0.30']);
  assert.equal(app.$('#exit-empty').hidden, true);
  assert.equal(app.requests.some((r) => r.method === 'POST'), false);
});

test('a stale selected flag cannot hide a YAML-configured exit or block restoring a discovered exit', async () => {
  const snapshot = exitsSnapshot(); snapshot.exit_nodes[0].selected_for = '100.64.0.10';
  const app = await page(snapshot, '', {}, exitDocument());
  app.override = (request) => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    const chosen = request.data.changes['tailscale.exit_node'];
    app.config = exitDocument(chosen, 'split', chosen); app.snapshot.revision = chosen;
    // Idle reconfiguration deliberately retains the old discovery snapshot.
    return response(app.config);
  };
  // Simulate a configuration edited outside the picker.
  app.config = exitDocument('100.64.0.99', 'split', 'external');
  app.snapshot.revision = 'external';
  await app.$('#reload').emit('click');
  assert.equal(exitChoice(app, '100.64.0.99').attributes.get('aria-checked') === 'true', true);
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked') === 'true', false);
  await exitChoice(app, '100.64.0.10').emit('click');
  assert.equal(app.$('#connection-apply').hidden, false);
  assert.equal(app.$('#apply-connection-change').disabled, false);
  await app.$('#apply-connection-change').emit('click');
  assert.deepEqual(app.requests.filter(r => r.path === '/api/config' && r.method === 'POST').map(r => r.data.changes['tailscale.exit_node']), ['100.64.0.10']);
});

test('selection metadata bound to the saved DNS alias identifies its discovered node', async () => {
  const alias = 'home.example.ts.net';
  const snapshot = exitsSnapshot(); snapshot.exit_nodes[0].selected_for = alias;
  const app = await page(snapshot, '', {}, exitDocument(alias));
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked') === 'true', true);
  assert.equal(exitChoice(app, alias), undefined);
});

test('a stale poll cannot restore old device state after a desktop action', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  const old = deferred(); let delay = true;
  app.override = (request) => {
    if (request.path === '/api/state' && delay) { delay = false; return old.promise; }
    if (request.path === '/api/cancel') {
      app.snapshot = { ...app.snapshot, active: false, state: 'stopped', backend_state: 'NeedsLogin', hostname: '', network: '', exit_nodes: [] };
      return response({ ok: true });
    }
    return undefined;
  };
  const polling = app.poll(); await settle();
  // The controller always publishes an identity for an active helper.
  app.snapshot.active = true;
  app.snapshot.operation_id = 'stale-poll-operation';
  await app.poll();
  await app.$('#desktop-disconnect').emit('click');
  old.resolve(response(exitsSnapshot({ revision: 'original' }))); await polling;
  assert.equal(app.$('#tailnet-identity').textContent, '未登录 Tailscale');
});

// The picker list is the only node surface; it renders while the dialog is closed.
const nodeRow = (app, id) => app.$('#exit-list').children.find(row => row.children.some(child => child.dataset.nodePing === id));
const pingButton = (app, id) => nodeRow(app, id)?.children.find(child => child.dataset.nodePing === id);
const nodeLatency = (app, id) => nodeRow(app, id)?.children.find(child => child.className?.startsWith('node-latency'));

test('disappeared discovered selection cannot be applied until an online list entry is chosen', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  await exitChoice(app, '100.64.0.20').emit('click');
  app.snapshot.exit_nodes = app.snapshot.exit_nodes.filter(node => node.id !== '100.64.0.20');
  await app.poll();
  assert.equal(app.$('#apply-exit-picker').disabled, true);
  app.snapshot.exit_nodes.push({ id: '100.64.0.99', name: 'replacement', online: true });
  await app.poll();
  await exitChoice(app, '100.64.0.99').emit('click');
  assert.equal(app.$('#apply-exit-picker').disabled, false);
});

test('active exit remains visible when absent from discovered nodes', async () => {
  const app = await page(exitsSnapshot({ active: true, mode: 'proxy', state: 'running', exit: 'old-exit' }), '', {}, exitDocument());
  assert.equal(app.$('#current-exit').textContent, 'old-exit');
});

test('picker radio choices support arrow navigation and skip offline nodes', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  const home = exitChoice(app, '100.64.0.10');
  assert.equal(home.attributes.get('role'), 'radio');
  assert.equal(home.tabIndex, 0);
  assert.equal(exitChoice(app, '100.64.0.20').tabIndex, -1);
  let prevented = false;
  await home.emit('keydown', { key: 'ArrowDown', preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(exitChoice(app, '100.64.0.20').attributes.get('aria-checked'), 'true');
  assert.equal(exitChoice(app, '100.64.0.20').tabIndex, 0);
  await exitChoice(app, '100.64.0.20').emit('keydown', { key: 'ArrowDown' });
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked'), 'true');
});

test('exit picker cancel discards its selection and preserves a pending mode', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('[data-routing-mode="global"]').emit('click');
  await app.$('#choose-exit').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  await exitChoice(app, '100.64.0.20').emit('click');
  assert.equal(app.$('#connection-apply').hidden, true);
  assert.equal(app.$('#current-exit').textContent, 'home');
  assert.match(app.$('#exit-dialog-selection').textContent, /travel/);
  await app.$('#cancel-exit-picker').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, false);
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked') === 'true', true);
  assert.equal(app.$('#connection-apply').hidden, true);
  assert.equal(app.$('#routing-mode').value, 'global', 'canceling an exit picker must preserve the settings draft');
  assert.equal(app.$('#global-exit-row').hidden, false);
  assert.equal(app.$('#connection-draft-warning').hidden, false);
  assert.equal(app.requests.some(request => request.method === 'POST'), false);
  await app.$('#choose-exit').emit('click');
  await exitChoice(app, '100.64.0.20').emit('click');
  // Escape's native default closes the dialog and fires the same close event.
  await app.$('#exit-picker-dialog').emit('cancel');
  app.$('#exit-picker-dialog').close();
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked') === 'true', true);
});

const multiExitDocument = (office = '', revision = 'original') => ({
  ...doc(`tailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    name: Office\n    exit_node: "${office}"\n`, revision),
  values: { log_level: 'info', tailscale: { exit_node: '100.64.0.10' }, exits: [{ id: 'office', name: 'Office', exit_node: office, tailnet: 'office.ts.net' }], tun: { mtu: 1400 } },
});
const officeSnapshot = (changes = {}) => exitsSnapshot({ exits: { office: {
  backend_state: 'Running', network: 'office.ts.net', hostname: 'my-device-office', ping_available: true,
  exit_nodes: [{ id: '100.101.0.11', name: 'hk-gateway', online: true }, { id: '100.101.0.12', name: 'us-gateway', online: true }],
  ...changes } } });
const extraRow = (app, id) => app.$('#extra-exits').children.flatMap(item => item.children).find(child => child.dataset.exitRow === id);

test('backend-normalized scalar exit IDs remain selectable and survive Add', async () => {
  for (const id of ['0', '001', 'false', '12345678901234567890123456789012']) {
    const initial = multiExitDocument('100.101.0.11');
    initial.yaml = initial.yaml.replace('id: office', `id: ${id}`);
    initial.values.exits[0].id = id;
    initial.values.rules = { default_exit: id, global_exit: id, final: id,
      routes: [{ exit: id, domains: ['private.example'] }] };
    const original = { ...initial.values.exits[0] };
    const snapshot = officeSnapshot();
    snapshot.exits = { [id]: snapshot.exits.office };
    const app = await page(snapshot, '', {}, initial);
    assert.ok(extraRow(app, id), 'the existing scalar-ID device must stay visible');
    assert.equal(app.$('#connection-exit').value, id);
    assert.equal(app.$('#default-exit').value, id);
    assert.equal(app.$('#global-exit').value, id);
    assert.equal(app.$('#final-route').value, id);
    assert.equal(app.$('#route-groups').children[0].children[0].children[1].value, id);
    await extraRow(app, id).emit('click');
    assert.equal(app.$('#exit-picker-dialog').open, true);
    assert.ok(exitChoice(app, '100.101.0.11'), 'picker must use this device rather than the primary');
    await app.$('#cancel-exit-picker').emit('click');
    let started = false;
    app.override = request => {
      if (request.path === '/api/action') {
        assert.deepEqual(actionFields(request), { action: 'add-exit', exit: firstAddedID, revision: app.config.revision });
        started = true;
        app.snapshot = { ...app.snapshot, active: true, mode: 'login', state: 'login', focus: firstAddedID };
        return response({ ok: true });
      }
      return undefined;
    };
    await app.$('#add-exit').emit('click');
    assert.equal(started, true);
    assert.deepEqual(app.config.values.exits, [original]);
    assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
    assert.ok(extraRow(app, id), 'Add must preserve the saved identity');
    assert.equal(app.$('#connection-exit').value, id);
  }
});

test('an additional exit opens its own picker and saves only its node', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument());
  assert.equal(app.$('#extra-exits').children.length, 1);
  assert.ok(extraRow(app, 'office'), 'the saved extra exit remains in the card list');
  await extraRow(app, 'office').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  assert.equal(app.$('#heading-exits').textContent, 'Office');
  assert.equal(app.$('#picker-login').hidden, true);
  assert.equal(app.$('#exit-danger-zone').hidden, false);
  assert.equal(exitChoice(app, '100.64.0.10'), undefined, 'the primary Tailnet nodes must not appear');
  await exitChoice(app, '100.101.0.12').emit('click');
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { exits: [{ id: 'office', name: 'Office', exit_node: '100.101.0.12', tailnet: 'office.ts.net' }] });
    app.config = multiExitDocument('100.101.0.12', 'changed'); app.snapshot.revision = 'changed';
    return response(app.config);
  };
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, false);
  assert.equal(app.requests.some(request => request.path === '/api/action'), false);
  // Closing the extra picker returns the shared list to the primary exit.
  assert.ok(exitChoice(app, '100.64.0.10'));
});

test('logging in another device never shows a sign-in prompt for an authenticated primary', async () => {
  const app = await page(officeSnapshot({ backend_state: 'NeedsLogin' }), '', {}, multiExitDocument());
  app.snapshot = { ...app.snapshot, state: 'login', mode: 'login', active: true, focus: 'office' };
  await app.poll();
  assert.equal(app.$('#desktop-login').hidden, true, 'focused login must not override primary authentication');
  assert.equal(app.$('#picker-login').hidden, true);
  app.snapshot = { ...app.snapshot, state: 'authenticated', active: false };
  app.snapshot.exits.office.backend_state = 'Running';
  await app.poll();
  assert.equal(app.$('#desktop-login').hidden, true);
  app.snapshot = { ...app.snapshot, state: 'stopped', mode: '', focus: '' };
  await app.poll();
  assert.equal(app.$('#desktop-login').hidden, true, 'the completed login must leave primary signed in');
});

test('additional exit nodes ping individually through their own device', async () => {
  const app = await page(officeSnapshot(), '', { language: 'en' }, multiExitDocument('100.101.0.11'));
  await extraRow(app, 'office').emit('click');
  const bodies = [];
  app.override = request => {
    if (request.path !== '/api/exit-ping') return undefined;
    bodies.push(request.data);
    return response({ id: request.data.id, latency_ms: 30, direct: true });
  };
  await pickerPingButton(app, '100.101.0.11').emit('click');
  await pickerPingButton(app, '100.101.0.12').emit('click');
  assert.deepEqual(bodies, [{ id: '100.101.0.11', exit: 'office' }, { id: '100.101.0.12', exit: 'office' }]);
  assert.equal(nodeLatency(app, '100.101.0.12').children[0].textContent, '30 ms');
  await app.$('#cancel-exit-picker').emit('click');
  // Results are scoped per device: the primary picker shows none of them.
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '—');
});

test('Add signs in and names the first exit without assigning a traffic default', async () => {
  const initial = exitDocument();
  initial.values.tailscale = null;
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: '-', global_exit: '-' };
  const app = await page(exitsSnapshot(), '', {}, initial);
  let saves = 0;
  app.override = request => {
    if (request.path === '/api/config' && request.method === 'POST') {
      saves++;
      assert.deepEqual(request.data.changes, { exits: [{ id: firstAddedID, auto_name: true, exit_node: '100.101.0.11', tailnet: 'example.com', magic_dns_suffix: 'tail123.ts.net', name: 'hk-gateway@example.com' }] });
      app.config = { ...app.config, revision: `saved-${saves}`,
        values: { ...app.config.values, exits: request.data.changes.exits } };
      app.snapshot.revision = app.config.revision;
      return response(app.config);
    }
    if (request.path === '/api/action') {
      assert.deepEqual(actionFields(request), { action: 'add-exit', exit: firstAddedID, revision: app.config.revision });
      assert.equal(extraRow(app, firstAddedID), undefined, 'no row exists before successful authorization');
      app.config = { ...initial, revision: 'added', values: { ...initial.values, exits: [{ id: firstAddedID, auto_name: true, exit_node: '' }] } };
      app.snapshot = exitsSnapshot({ state: 'authenticated', mode: 'login', revision: 'added', exits: { [firstAddedID]: officeSnapshot({ network: 'example.com', magic_dns_suffix: 'tail123.ts.net' }).exits.office } });
      return response({ ok: true });
    }
    return undefined;
  };
  await app.$('#add-exit').emit('click');
  assert.equal(app.$('#add-exit-dialog').open, undefined, 'no naming dialog precedes login');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  assert.equal(app.$('#heading-exits').textContent, 'example.com');
  await exitChoice(app, '100.101.0.11').emit('click');
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(saves, 1);
  assert.equal(app.$('#exit-picker-dialog').open, false);
  await extraRow(app, firstAddedID).emit('click');
  assert.equal(app.$('#heading-exits').textContent, 'hk-gateway@example.com');
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(app.config.values.rules.global_exit, '-');
});

test('Add remains pending until real authenticated device status arrives and can recover from failed authorization', async () => {
  const initial = exitDocument(); initial.values.tailscale = null;
  const app = await page(exitsSnapshot(), '', {}, initial);
  let authenticated = false;
  app.override = request => {
    if (request.path === '/api/action') {
      const id = authenticated ? secondAddedID : firstAddedID;
      assert.deepEqual(actionFields(request), { action: 'add-exit', exit: id, revision: app.config.revision });
      if (authenticated) app.config = { ...initial, revision: 'added', values: { ...initial.values, exits: [{ id, auto_name: true, exit_node: '' }] } };
      app.snapshot = exitsSnapshot({ revision: app.config.revision, state: authenticated ? 'authenticated' : 'login', active: !authenticated,
        mode: 'login', focus: authenticated ? '' : id, exits: { [id]: authenticated ? officeSnapshot().exits.office : { backend_state: 'NeedsLogin' } } });
      return response({ ok: true });
    }
    return undefined;
  };
  await app.$('#add-exit').emit('click');
  assert.equal(app.$('#toast').textContent, '等待 Tailscale 登录');
  assert.ok(!app.$('#exit-picker-dialog').open, 'opening authorization is not a successful Add');
  assert.equal(extraRow(app, firstAddedID), undefined, 'pending devices must not appear in the list');
  assert.equal(app.config, initial, 'pending authentication must not save a provisional profile');
  await app.poll();
  assert.notEqual(app.$('#toast').textContent, '出口已添加', 'waiting or closing the browser cannot confirm success');
  app.snapshot = { ...app.snapshot, state: 'failed', active: false, mode: '', focus: '', error: 'authorization failed' };
  await app.poll();
  assert.notEqual(app.$('#toast').textContent, '出口已添加');
  assert.ok(!app.$('#exit-picker-dialog').open);
  authenticated = true;
  assert.equal(extraRow(app, firstAddedID), undefined, 'failed authorization must not leave a list item');
  const retry = app.$('#add-exit');
  assert.equal(retry.disabled, false, 'failed authorization must remain retryable');
  await retry.emit('click');
  assert.deepEqual(app.requests.filter(request => request.path === '/api/action' && request.data.action === 'add-exit').map(request => request.data.exit), [firstAddedID, secondAddedID]);
  assert.equal(app.$('#toast').textContent, '出口已添加');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  app.$('#toast').textContent = 'unchanged'; await app.poll();
  assert.equal(app.$('#toast').textContent, 'unchanged', 'authenticated polling must not announce Add repeatedly');
});

test('a polled Add completion opens its picker when the committed row first appears', async () => {
  const initial = exitDocument(); initial.values.tailscale = null;
  const app = await page(exitsSnapshot(), '', {}, initial);
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    app.snapshot = exitsSnapshot({ active: true, state: 'login', mode: 'login', focus: firstAddedID,
      exits: { [firstAddedID]: { backend_state: 'NeedsLogin' } } });
    return response({ ok: true });
  };
  await app.$('#add-exit').emit('click');
  assert.equal(extraRow(app, firstAddedID), undefined);
  assert.ok(!app.$('#exit-picker-dialog').open);
  app.config = { ...initial, revision: 'committed', values: { ...initial.values,
    exits: [{ id: firstAddedID, auto_name: true, exit_node: '' }] } };
  app.snapshot = exitsSnapshot({ active: false, state: 'authenticated', mode: 'login', revision: 'committed',
    exits: { [firstAddedID]: officeSnapshot().exits.office } });
  await app.poll();
  assert.ok(extraRow(app, firstAddedID), 'the committed profile must be visible');
  assert.equal(app.$('#toast').textContent, '出口已添加', 'success must not wait for another poll');
  assert.equal(app.$('#exit-picker-dialog').open, true, 'the first committed render must open the picker');
  await app.$('#cancel-exit-picker').emit('click');
  app.$('#toast').textContent = 'no-repeat';
  await app.poll();
  assert.equal(app.$('#toast').textContent, 'no-repeat');
  assert.equal(app.$('#exit-picker-dialog').open, false);
});

test('a config reload from a superseded state poll cannot overwrite the latest view', async () => {
  const app = await page();
  const pending = deferred();
  app.snapshot = { state: 'stopped', revision: 'external' };
  app.override = request => request.path === '/api/config' ? pending.promise : undefined;
  const oldPoll = app.poll(); await settle();
  app.snapshot = { state: 'failed', revision: 'original', error: 'latest failure' };
  await app.poll();
  pending.resolve(response(doc('log_level: debug\n', 'external')));
  await oldPoll;
  assert.equal(app.$('#level').value, 'info');
  assert.equal(app.$('#connection-status').classList.contains('error'), true);
});

test('failed Add login request never announces a successful Add', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  app.override = request => {
    if (request.path === '/api/config' && request.method === 'POST') {
      app.config = { ...app.config, revision: 'pending', values: { ...app.config.values, exits: request.data.changes.exits } };
      app.snapshot.revision = 'pending'; return response(app.config);
    }
    if (request.path === '/api/action') return response({ error: 'login could not start' }, false);
    return undefined;
  };
  await app.$('#add-exit').emit('click');
  assert.notEqual(app.$('#toast').textContent, '出口已添加');
  assert.equal(app.$('#notice').hidden, false);
  assert.ok(!app.$('#exit-picker-dialog').open);
});

const activeGlobalDocument = () => {
  const initial = exitDocument('100.64.0.10', 'global');
  initial.values.tun.enabled = true;
  initial.yaml = initial.yaml.replace('enabled: false', 'enabled: true');
  return initial;
};
const activeGlobalSnapshot = () => exitsSnapshot({ active: true, mode: 'tun', state: 'running' });
const mutations = (app) => app.requests.filter(request => request.method === 'POST' && ['/api/config', '/api/config-rebuild', '/api/config-repair', '/api/action', '/api/cancel'].includes(request.path));
const mutationAction = request => request.path === '/api/cancel' ? 'cancel' : request.data.action;
const actionFields = request => { const { operation_id, ...fields } = request.data; if (operation_id !== undefined) assert.equal(typeof operation_id, 'string'); return fields; };
// Call the listener directly to verify its guards independently of disabled UI.
const invokeAdd = (app) => app.$('#add-exit').listeners.click[0]();
const answerAddConsent = (app, answer) => {
  const dialog = app.$('#add-exit-consent-dialog');
  dialog.returnValue = answer; dialog.close();
};

test('Global Add cancellation and Escape preserve protection and configuration without any mutation', async () => {
  for (const dismissal of ['cancel', 'escape']) {
    const initial = activeGlobalDocument();
    const app = await page(activeGlobalSnapshot(), '', {}, initial);
    const adding = invokeAdd(app);
    await settle();
    assert.equal(app.$('#add-exit-consent-dialog').open, true);
    assert.equal(app.$('#add-exit').disabled, true);
    assert.deepEqual(mutations(app), []);
    await invokeAdd(app);
    if (dismissal === 'escape') {
      await app.$('#add-exit-consent-dialog').emit('cancel');
      app.$('#add-exit-consent-dialog').close();
    } else answerAddConsent(app, 'cancel');
    await adding;
    assert.deepEqual(mutations(app), []);
    assert.equal(app.snapshot.active, true);
    assert.equal(app.config, initial);
    assert.equal(app.$('#raw-yaml').value, initial.yaml);
    assert.equal(app.$('#add-exit').disabled, false);
  }
});

test('confirmed Global Add disconnects before staged authorization and never saves a provisional profile', async () => {
  for (const failure of ['', 'disconnect', 'add-exit']) {
    const initial = activeGlobalDocument();
    const app = await page(activeGlobalSnapshot(), '', {}, initial);
    app.override = request => {
      if (request.path === '/api/action') {
        if (request.data.action === failure) return response({ error: `${failure} failed` }, false);
        if (request.data.action === 'disconnect') Object.assign(app.snapshot, { active: false, mode: '', state: 'stopped' });
        return response({ ok: true });
      }
      if (request.path === '/api/config' && request.method === 'POST') {
        assert.equal(app.snapshot.active, false);
        assert.deepEqual(request.data.changes, { exits: [{ id: firstAddedID, auto_name: true, exit_node: '' }] });
        if (failure === 'save') return response({ error: 'save failed' }, false);
        app.config = { ...initial, revision: 'added', values: { ...initial.values, exits: request.data.changes.exits } };
        app.snapshot.revision = 'added';
        return response(app.config);
      }
      return undefined;
    };
    const adding = invokeAdd(app); await settle();
    assert.equal(app.$('#add-exit-consent-dialog').open, true);
    assert.deepEqual(mutations(app), []);
    answerAddConsent(app, 'add'); await adding;
    const steps = mutations(app).map(request => request.path === '/api/config' ? 'save' : request.data.action);
    assert.deepEqual(steps, failure === 'disconnect' ? ['disconnect'] : ['disconnect', 'add-exit']);
    assert.equal(app.snapshot.active, failure === 'disconnect');
    assert.equal(app.config, initial, 'the browser must never save a provisional profile');
    assert.equal(app.$('#notice').hidden, !failure);
    assert.equal(app.$('#add-exit').disabled, false);
  }
});

test('Add is disabled and its listener cannot save or act in unsupported or transitioning modes', async () => {
  for (const change of [
    { desktop: false },
    ...['starting', 'stopping', 'authorizing', 'logging-out'].map(state => ({ state })),
    { active: true, mode: 'discovery', state: 'checking' },
    { active: true, mode: 'login', state: 'login' },
  ]) {
    const app = await page(exitsSnapshot(change), '', {}, exitDocument());
    assert.equal(app.$('#add-exit').disabled, true, JSON.stringify(change));
    await invokeAdd(app);
    assert.deepEqual(mutations(app), []);
    assert.ok(!app.$('#add-exit-consent-dialog').open);
  }
});

test('invalid saved configurations disable Add and cannot be rewritten by its handler', async () => {
  for (const active of [false, true]) {
    const initial = { ...activeGlobalDocument(), valid: false, error: 'Invalid saved configuration' };
    const app = await page(active ? activeGlobalSnapshot() : exitsSnapshot(), '', {}, initial);
    assert.equal(app.$('#add-exit').disabled, true);
    await invokeAdd(app);
    assert.deepEqual(mutations(app), []);
    assert.equal(app.config, initial);
    assert.equal(app.$('#raw-yaml').value, initial.yaml);
    assert.ok(!app.$('#add-exit-consent-dialog').open);
  }
});

test('valid dormant references still permit adding a replacement without changing the retained rules', async () => {
  const initial = exitDocument();
  initial.values.rules = { mode: 'split', final: 'removed', routes: [{ exit: 'removed', domains: ['corp.example'] }] };
  initial.yaml = initial.yaml.replace('mode: split\n', 'mode: split\n  final: removed\n  routes:\n    - exit: removed\n      domains: [corp.example]\n');
  const app = await page(exitsSnapshot(), '', {}, initial);
  assert.equal(app.$('#add-exit').disabled, false);
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { exits: [{ id: firstAddedID, auto_name: true, exit_node: '' }] });
    app.config = { ...initial, revision: 'added', values: { ...initial.values, exits: request.data.changes.exits } };
    app.snapshot.revision = 'added';
    return response(app.config);
  };
  await invokeAdd(app);
  assert.deepEqual(mutations(app).map(request => request.path === '/api/config' ? 'save' : request.data.action), ['add-exit']);
  assert.deepEqual(app.config.values.rules, initial.values.rules);
});

test('Add handler blocks drafts, stale documents, lost connections, pending actions and full lists', async () => {
  for (const condition of ['draft', 'external', 'stale', 'lost', 'busy', 'full']) {
    const initial = exitDocument();
    if (condition === 'full') initial.values.exits = Array.from({ length: 8 }, (_, index) => ({ id: `saved-${index}`, exit_node: '' }));
    const app = await page(exitsSnapshot(), '', {}, initial);
    const pending = deferred();
    let working;
    if (['draft', 'external'].includes(condition)) await app.input('#level', 'debug');
    if (['external', 'stale'].includes(condition)) {
      app.snapshot = { ...app.snapshot, revision: 'external' };
      if (condition === 'stale') {
        app.override = request => request.path === '/api/config' ? pending.promise : undefined;
        working = app.poll(); await settle();
      } else await app.poll();
    }
    if (condition === 'lost') {
      app.override = request => request.path === '/api/state' ? response({ error: 'Unavailable' }, false) : undefined;
      await app.poll();
    }
    if (condition === 'busy') {
      app.override = request => request.path === '/api/action' ? pending.promise : undefined;
      working = app.$('#refresh-exits').emit('click'); await settle();
    }
    const before = mutations(app).length;
    assert.equal(app.$('#add-exit').disabled, true, condition);
    await invokeAdd(app);
    assert.equal(mutations(app).length, before, condition);
    if (working) {
      pending.resolve(response(condition === 'stale' ? { ...initial, revision: 'external' } : { ok: true }));
      await working;
    }
  }
});

test('Global Add rechecks capability, revision and busy state after consent before stopping anything', async () => {
  for (const condition of ['revision', 'unsupported', 'transition', 'lost', 'draft']) {
    const app = await page(activeGlobalSnapshot(), '', {}, activeGlobalDocument());
    const adding = invokeAdd(app); await settle();
    assert.equal(app.$('#add-exit-consent-dialog').open, true);
    if (condition === 'revision') {
      app.snapshot = { ...app.snapshot, revision: 'newer' };
      app.config = { ...app.config, revision: 'newer' };
    } else if (condition === 'unsupported') app.snapshot = { ...app.snapshot, desktop: false };
    else if (condition === 'transition') app.snapshot = { ...app.snapshot, state: 'stopping' };
    else if (condition === 'lost') app.override = request => request.path === '/api/state' ? response({ error: 'Unavailable' }, false) : undefined;
    else await app.input('#level', 'debug');
    await app.poll();
    answerAddConsent(app, 'add'); await adding;
    assert.deepEqual(mutations(app), [], condition);
    assert.equal(app.snapshot.active, true);
  }
});

test('Add cannot save after disconnect when state is stale, unavailable or still active', async () => {
  for (const condition of ['revision', 'unsupported', 'lost', 'active']) {
    const app = await page(activeGlobalSnapshot(), '', {}, activeGlobalDocument());
    app.override = request => {
      if (request.path === '/api/action' && request.data.action === 'disconnect') {
        app.snapshot = { ...app.snapshot, active: condition === 'active', mode: condition === 'active' ? 'tun' : '', state: condition === 'active' ? 'running' : 'stopped' };
        if (condition === 'revision') {
          app.config = { ...app.config, revision: 'newer' }; app.snapshot.revision = 'newer';
        } else if (condition === 'unsupported') app.snapshot.desktop = false;
        return response({ ok: true });
      }
      if (condition === 'lost' && request.path === '/api/state') return response({ error: 'Unavailable' }, false);
      return undefined;
    };
    const adding = invokeAdd(app); await settle();
    answerAddConsent(app, 'add'); await adding;
    assert.deepEqual(mutations(app).map(request => request.data.action), ['disconnect'], condition);
  }
});

test('a rejected or superseded staged Add never leaves a browser-saved profile', async () => {
  for (const condition of ['revision', 'unsupported']) {
    const initial = exitDocument();
    const app = await page(exitsSnapshot(), '', {}, initial);
    app.override = request => {
      if (request.path !== '/api/action') return undefined;
      assert.deepEqual(actionFields(request), { action: 'add-exit', exit: firstAddedID, revision: app.config.revision });
      if (condition === 'revision') {
        app.config = { ...initial, revision: 'newer' };
        app.snapshot = { ...app.snapshot, revision: 'newer', state: 'failed', error: 'configuration changed' };
      } else app.snapshot.desktop = false;
      return response({ error: 'staged Add rejected' }, false);
    };
    await invokeAdd(app);
    assert.deepEqual(mutations(app).map(request => request.path), ['/api/action']);
    assert.equal(extraRow(app, firstAddedID), undefined);
    assert.deepEqual(app.config.values.exits, initial.values.exits);
  }
});

test('automatic names follow later node changes without changing the profile ID', async () => {
  const document = multiExitDocument('100.101.0.11');
  Object.assign(document.values.exits[0], { name: 'hk-gateway@office.ts.net', auto_name: true });
  const app = await page(officeSnapshot(), '', {}, document);
  await extraRow(app, 'office').emit('click');
  await exitChoice(app, '100.101.0.12').emit('click');
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { exits: [{ id: 'office', auto_name: true,
      name: 'us-gateway@office.ts.net', exit_node: '100.101.0.12', tailnet: 'office.ts.net' }] });
    app.config = { ...document, revision: 'changed', values: { ...document.values, exits: request.data.changes.exits } };
    app.snapshot.revision = 'changed';
    return response(app.config);
  };
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, false);
  assert.equal(app.config.values.exits[0].name, 'us-gateway@office.ts.net');
});

test('node changes preserve custom names and guards while saving only actual DNS metadata', async () => {
  for (const [savedSuffix, liveSuffix, expectedSuffix] of [[undefined, 'tail123.ts.net', 'tail123.ts.net'], [undefined, undefined, undefined], ['tail-old.ts.net', undefined, 'tail-old.ts.net']]) {
    const document = multiExitDocument('100.101.0.11');
    Object.assign(document.values.exits[0], { tailnet: 'example.com', ...(savedSuffix ? { magic_dns_suffix: savedSuffix } : {}) });
    const app = await page(officeSnapshot({ network: 'example.com', ...(liveSuffix ? { magic_dns_suffix: liveSuffix } : {}) }), '', {}, document);
    await extraRow(app, 'office').emit('click');
    await exitChoice(app, '100.101.0.12').emit('click');
    let saves = 0;
    app.override = request => {
      if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
      saves++;
      assert.deepEqual(request.data.changes, { exits: [{ id: 'office', name: 'Office', exit_node: '100.101.0.12', tailnet: 'example.com',
        ...(expectedSuffix ? { magic_dns_suffix: expectedSuffix } : {}) }] });
      app.config = { ...document, revision: 'changed', values: { ...document.values, exits: request.data.changes.exits } };
      app.snapshot.revision = 'changed';
      return response(app.config);
    };
    await app.$('#apply-exit-picker').emit('click');
    assert.equal(saves, 1);
    assert.equal(app.$('#exit-picker-dialog').open, false);
  }
});

test('picker save failures remain inside the dialog with the pending exit and retry', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  await exitChoice(app, '100.64.0.20').emit('click');
  app.override = request => request.path === '/api/config' && request.method === 'POST'
    ? response({ code: 'apply', error: 'Save failed' }, false) : undefined;
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  assert.equal(app.$('#exit-dialog-error').hidden, false);
  assert.equal(app.$('#exit-dialog-error').textContent, 'Save failed');
  assert.equal(exitChoice(app, '100.64.0.20').attributes.get('aria-checked') === 'true', true);
  assert.equal(app.$('#apply-exit-picker').disabled, false);
});

test('picker protects settings drafts and cannot dismiss an in-flight save', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  await exitChoice(app, '100.64.0.20').emit('click');
  await app.input('#level', 'debug');
  assert.equal(app.$('#exit-dialog-draft-warning').hidden, false);
  assert.equal(app.$('#apply-exit-picker').disabled, true);
  await app.$('#picker-view-settings').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, false);
  assert.equal(app.$('#page-settings').hidden, false);
  assert.equal(app.$('#level').value, 'debug');
  assert.equal(app.requests.some(request => request.path === '/api/config' && request.method === 'POST'), false);

  const clean = await page(exitsSnapshot(), '', {}, exitDocument());
  await clean.$('#choose-exit').emit('click');
  await exitChoice(clean, '100.64.0.20').emit('click');
  const save = deferred();
  clean.override = request => request.path === '/api/config' && request.method === 'POST' ? save.promise : undefined;
  const applying = clean.$('#apply-exit-picker').emit('click');
  await settle();
  let canceled = false;
  await clean.$('#exit-picker-dialog').emit('cancel', { preventDefault() { canceled = true; } });
  await clean.$('#cancel-exit-picker').emit('click');
  const busyDialog = clean.$('#exit-picker-dialog');
  busyDialog.getBoundingClientRect = () => ({ left: 100, top: 80, right: 600, bottom: 500 });
  await busyDialog.emit('pointerdown', { clientX: 20, clientY: 20 });
  await busyDialog.emit('click', { clientX: 20, clientY: 20 });
  assert.equal(canceled, true);
  assert.equal(clean.$('#exit-picker-dialog').open, true);
  save.resolve(response({ code: 'apply', error: 'Retry later' }, false));
  await applying;
  assert.equal(clean.$('#cancel-exit-picker').disabled, false);
});

test('node pings serialize requests and display real returned latency without changing selection', async () => {
  const app = await page(exitsSnapshot(), '', { language: 'en' }, exitDocument());
  const pending = deferred();
  app.override = request => request.path === '/api/exit-ping' ? pending.promise : undefined;
  const pinging = pingButton(app, '100.64.0.20').emit('click');
  await settle();
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, 'Testing…');
  await pingButton(app, '100.64.0.20').emit('click');
  await pingButton(app, '100.64.0.10').emit('click');
  assert.equal(app.requests.filter(request => request.path === '/api/exit-ping').length, 1);
  pending.resolve(response({ id: '100.64.0.20', latency_ms: 24.7, direct: false, relay: 'sin' }));
  await pinging;
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '24.7 ms');
  assert.equal(nodeLatency(app, '100.64.0.20').title, '24.7 ms · Relay · sin');
  assert.equal(pingButton(app, '100.64.0.20').disabled, false);
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked') === 'true', true);
  assert.equal(app.requests.some(request => request.method === 'POST' && request.path !== '/api/exit-ping'), false);
  assert.equal(app.$('#exit-list').children.length, 3);
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '24.7 ms');
});

test('ping errors translate inline and a retry replaces them with its result', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  app.override = request => request.path === '/api/exit-ping'
    ? response({ code: 'ping_timeout', error: 'Timed out', error_i18n: { zh: '请求超时，请重试。', en: 'Timed out. Retry.' } }, false) : undefined;
  await pingButton(app, '100.64.0.20').emit('click');
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '测试失败');
  assert.equal(nodeLatency(app, '100.64.0.20').title, '请求超时，请重试。');
  app.i18n.setLanguage('en');
  assert.equal(nodeLatency(app, '100.64.0.20').title, 'Timed out. Retry.');
  app.override = request => request.path === '/api/exit-ping' ? response({ id: '100.64.0.20', latency_ms: 0.4, direct: true }) : undefined;
  await pingButton(app, '100.64.0.20').emit('click');
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '0.4 ms');
  assert.equal(pingButton(app, '100.64.0.20').attributes.get('aria-description'), '0.4 ms · Direct');
});

test('old Tailnet ping results cannot appear after network switching or sign-out', async () => {
  for (const change of [{ network: 'other.ts.net' }, { backend_state: 'NeedsLogin' }]) {
    const app = await page(exitsSnapshot(), '', {}, exitDocument());
    const pending = deferred();
    app.override = request => request.path === '/api/exit-ping' ? pending.promise : undefined;
    const pinging = pingButton(app, '100.64.0.20').emit('click'); await settle();
    app.snapshot = { ...app.snapshot, ...change }; await app.poll();
    pending.resolve(response({ id: '100.64.0.20', latency_ms: 24, direct: true })); await pinging;
    assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '—');
    assert.equal(app.$('#ping-announcement').textContent, '');
  }
});

test('additional device identity changes discard in-flight ping successes and failures, even after identity returns', async () => {
  for (const change of [{ network: 'other.ts.net' }, { hostname: 'replacement' }, { backend_state: 'NeedsLogin' }, null]) {
    for (const failed of [false, true]) {
      const app = await page(officeSnapshot(), '', { language: 'en' }, multiExitDocument('100.101.0.11'));
      await extraRow(app, 'office').emit('click');
      const pending = deferred();
      app.override = request => request.path === '/api/exit-ping' ? pending.promise : undefined;
      const pinging = pickerPingButton(app, '100.101.0.12').emit('click'); await settle();
      const original = app.snapshot.exits.office;
      app.snapshot = { ...app.snapshot, exits: change ? { office: { ...original, ...change } } : {} };
      await app.poll();
      app.snapshot = { ...app.snapshot, exits: { office: original } }; await app.poll();
      pending.resolve(failed ? response({ error: 'Old failure' }, false) : response({ id: '100.101.0.12', latency_ms: 24, direct: true }));
      await pinging;
      assert.equal(pickerLatency(app, '100.101.0.12').children[0].textContent, '—');
      assert.equal(app.$('#picker-ping-announcement').textContent, '');
      assert.equal(app.$('#ping-announcement').textContent, '');
      assert.equal(pickerPingButton(app, '100.101.0.12').disabled, false);
    }
  }
});

test('device-specific ping invalidation preserves other cached results and in-flight requests', async () => {
  const app = await page(officeSnapshot(), '', { language: 'en' }, multiExitDocument('100.101.0.11'));
  app.override = request => request.path === '/api/exit-ping' ? response({ id: request.data.id, latency_ms: 12, direct: true }) : undefined;
  await pingButton(app, '100.64.0.20').emit('click');
  await extraRow(app, 'office').emit('click');
  await pickerPingButton(app, '100.101.0.11').emit('click');
  app.snapshot = { ...app.snapshot, exits: { office: { ...app.snapshot.exits.office, hostname: 'replacement' } } }; await app.poll();
  assert.equal(pickerLatency(app, '100.101.0.11').children[0].textContent, '—');
  await app.$('#cancel-exit-picker').emit('click');
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '12 ms');
  await extraRow(app, 'office').emit('click');
  const pending = deferred();
  app.override = request => request.path === '/api/exit-ping' ? pending.promise : undefined;
  const pinging = pickerPingButton(app, '100.101.0.12').emit('click'); await settle();
  app.snapshot = { ...app.snapshot, network: 'other-primary.ts.net' }; await app.poll();
  assert.equal(app.$('#picker-ping-announcement').textContent, 'Pinging us-gateway');
  pending.resolve(response({ id: '100.101.0.12', latency_ms: 30, direct: true })); await pinging;
  assert.equal(pickerLatency(app, '100.101.0.12').children[0].textContent, '30 ms');
  await app.$('#cancel-exit-picker').emit('click');
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '—');
});

test('an invalidated ping cannot overwrite or finish a newer request for the same device and node', async () => {
  const app = await page(officeSnapshot(), '', { language: 'en' }, multiExitDocument('100.101.0.11'));
  await extraRow(app, 'office').emit('click');
  const old = deferred(), fresh = deferred();
  app.override = request => request.path === '/api/exit-ping' ? old.promise : undefined;
  const oldPing = pickerPingButton(app, '100.101.0.12').emit('click'); await settle();
  app.snapshot = { ...app.snapshot, exits: { office: { ...app.snapshot.exits.office, network: 'replacement.ts.net' } } }; await app.poll();
  assert.equal(pickerPingButton(app, '100.101.0.12').disabled, false);
  app.override = request => request.path === '/api/exit-ping' ? fresh.promise : undefined;
  const freshPing = pickerPingButton(app, '100.101.0.12').emit('click'); await settle();
  assert.equal(app.requests.filter(request => request.path === '/api/exit-ping').length, 2);
  old.resolve(response({ id: '100.101.0.12', latency_ms: 90, direct: true })); await oldPing;
  assert.equal(pickerLatency(app, '100.101.0.12').children[0].textContent, 'Testing…');
  assert.equal(app.$('#picker-ping-announcement').textContent, 'Pinging us-gateway');
  fresh.resolve(response({ id: '100.101.0.12', latency_ms: 20, direct: true })); await freshPing;
  assert.equal(pickerLatency(app, '100.101.0.12').children[0].textContent, '20 ms');
});

test('offline nodes and unavailable backends never issue ping requests', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await pingButton(app, '100.64.0.30').emit('click');
  assert.equal(pingButton(app, '100.64.0.30').disabled, true);
  for (const change of [{ ping_available: false }, { ping_available: true, backend_state: 'NeedsLogin' }]) {
    Object.assign(app.snapshot, change); await app.poll();
    assert.equal(pingButton(app, '100.64.0.20').disabled, true);
    assert.equal(pickerPingButton(app, '100.64.0.20').disabled, true);
    await pingButton(app, '100.64.0.20').emit('click');
  }
  assert.equal(app.requests.some(request => request.path === '/api/exit-ping'), false);
});

test('invalid ping payloads cannot display fabricated latency', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  app.override = request => request.path === '/api/exit-ping' ? response({ id: 'other-node', latency_ms: 12, direct: true }) : undefined;
  await pingButton(app, '100.64.0.20').emit('click');
  assert.equal(nodeLatency(app, '100.64.0.20').children[0].textContent, '测试失败');
  assert.equal(nodeLatency(app, '100.64.0.20').title, '未收到有效的 Ping 结果，请重试。');
});

const pickerPingButton = (app, id) => app.$('#exit-list').children.flatMap(row => row.children).find(child => child.dataset.nodePing === id);
const pickerLatency = (app, id) => app.$('#exit-list').children.find(row => row.children.some(child => child.dataset.exitChoice === id))?.children.find(child => child.className?.startsWith('node-latency'));

test('Ping live regions are available inside and outside the picker dialog', () => {
  const html = readFileSync(join(__dirname, '../assets/index.html'), 'utf8');
  const dialog = html.match(/<dialog class="exit-picker-dialog"[\s\S]*?<\/dialog>/)?.[0];
  assert.ok(dialog);
  assert.doesNotMatch(html, /data-page="nodes"/);
  assert.match(dialog, /id="picker-ping-announcement" role="status" aria-atomic="true"/);
  assert.match(html.replace(dialog, ''), /id="ping-announcement" role="status" aria-atomic="true"/);
});

test('picker announces Ping progress, success and failure within the dialog', async () => {
  const app = await page(exitsSnapshot(), '', { language: 'en' }, exitDocument());
  await app.$('#choose-exit').emit('click');
  const pending = deferred();
  app.override = request => request.path === '/api/exit-ping' ? pending.promise : undefined;
  const pinging = pickerPingButton(app, '100.64.0.20').emit('click');
  await settle();
  assert.equal(app.$('#picker-ping-announcement').textContent, 'Pinging travel');
  assert.equal(app.$('#ping-announcement').textContent, '');
  pending.resolve(response({ id: '100.64.0.20', latency_ms: 24.7, direct: true }));
  await pinging;
  assert.equal(app.$('#picker-ping-announcement').textContent, 'travel: 24.7 ms');
  app.override = request => request.path === '/api/exit-ping' ? response({ error: 'Timed out' }, false) : undefined;
  await pickerPingButton(app, '100.64.0.20').emit('click');
  assert.match(app.$('#picker-ping-announcement').textContent, /travel: ping failed/);
  assert.equal(app.$('#ping-announcement').textContent, '');
});

test('in-flight Ping announcements follow the active dialog', async () => {
  const app = await page(exitsSnapshot(), '', { language: 'en' }, exitDocument());
  const pending = deferred();
  app.override = request => request.path === '/api/exit-ping' ? pending.promise : undefined;
  const pinging = pingButton(app, '100.64.0.20').emit('click');
  await settle();
  assert.equal(app.$('#ping-announcement').textContent, 'Pinging travel');
  await app.$('#choose-exit').emit('click');
  assert.equal(app.$('#ping-announcement').textContent, '');
  assert.equal(app.$('#picker-ping-announcement').textContent, 'Pinging travel');
  await app.$('#cancel-exit-picker').emit('click');
  assert.equal(app.$('#picker-ping-announcement').textContent, '');
  assert.equal(app.$('#ping-announcement').textContent, 'Pinging travel');
  pending.resolve(response({ id: '100.64.0.20', latency_ms: 24.7, direct: true }));
  await pinging;
  assert.equal(app.$('#ping-announcement').textContent, 'travel: 24.7 ms');
});

test('latency thresholds include 500 ms in green and 1000 ms in amber on both surfaces', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  for (const [ms, band] of [[0, 'fast'], [499.9, 'fast'], [500, 'fast'], [500.1, 'medium'], [999.9, 'medium'], [1000, 'medium'], [1000.1, 'slow'], [1500, 'slow']]) {
    app.override = request => request.path === '/api/exit-ping' ? response({ id: '100.64.0.20', latency_ms: ms, direct: true }) : undefined;
    await pingButton(app, '100.64.0.20').emit('click');
    for (const latency of [pickerLatency(app, '100.64.0.20')]) {
      assert.equal(latency.className, `node-latency ${band}`, `${ms} ms`);
      assert.match(latency.children[0].textContent, /ms$/);
    }
  }
});

test('Ping preserves radio selection while the picker uses plain check icons', async () => {
  const iconFor = choice => choice.children[0].children[0].children[0].attributes.get('href');
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  app.override = request => request.path === '/api/exit-ping' ? response({ id: '100.64.0.20', latency_ms: 740, direct: false }) : undefined;
  assert.equal(pickerPingButton(app, '100.64.0.20').textContent, 'Ping');
  await pickerPingButton(app, '100.64.0.20').emit('click');
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked'), 'true');
  assert.equal(exitChoice(app, '100.64.0.20').attributes.get('aria-checked'), 'false');
  for (const id of ['100.64.0.10', '100.64.0.20']) {
    assert.equal(exitChoice(app, id).children[0].className, 'node-check');
    assert.equal(exitChoice(app, id).children[0].hidden, false);
  }
  assert.equal(iconFor(exitChoice(app, '100.64.0.10')), '#i-check');
  assert.equal(iconFor(exitChoice(app, '100.64.0.20')), '#i-check');
  assert.equal(app.$('#apply-exit-picker').disabled, true);
  assert.equal(nodeLatency(app, '100.64.0.20').className, 'node-latency medium');
  await exitChoice(app, '100.64.0.20').emit('click');
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked'), 'false');
  assert.equal(exitChoice(app, '100.64.0.20').attributes.get('aria-checked'), 'true');
  assert.equal(iconFor(exitChoice(app, '100.64.0.10')), '#i-check');
  assert.equal(iconFor(exitChoice(app, '100.64.0.20')), '#i-check');
  assert.equal(app.$('#apply-exit-picker').disabled, false);
  assert.equal(exitChoice(app, '100.64.0.20').children[1].textContent, 'travel');
  const descendants = element => [element, ...element.children.flatMap(descendants)];
  for (const list of [app.$('#exit-list')]) {
    assert.equal(descendants(list).some(element => element.type === 'radio'), false);
    assert.equal(descendants(list).some(element => element.textContent.includes('100.64.')), false);
  }
  await app.$('#cancel-exit-picker').emit('click');
  assert.equal(iconFor(exitChoice(app, '100.64.0.10')), '#i-check');
  assert.equal(iconFor(exitChoice(app, '100.64.0.20')), '#i-check');
  assert.equal(app.requests.some(request => request.path === '/api/config' && request.method === 'POST'), false);
});


test('backdrop dismissal cancels a staged exit and ignores interior clicks or drags', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  const dialog = app.$('#exit-picker-dialog');
  dialog.getBoundingClientRect = () => ({ left: 100, top: 80, right: 600, bottom: 500 });
  await exitChoice(app, '100.64.0.20').emit('click');
  await dialog.emit('pointerdown', { clientX: 130, clientY: 110 });
  await dialog.emit('click', { clientX: 130, clientY: 110 });
  assert.equal(dialog.open, true, 'clicking dialog padding must not close it');
  await dialog.emit('pointerdown', { target: exitChoice(app, '100.64.0.20'), clientX: 160, clientY: 150 });
  await dialog.emit('click', { clientX: 20, clientY: 20 });
  assert.equal(dialog.open, true, 'a drag starting inside must not dismiss');
  await dialog.emit('pointerdown', { clientX: 20, clientY: 20 });
  await dialog.emit('click', { clientX: 20, clientY: 20 });
  assert.equal(dialog.open, false);
  assert.equal(exitChoice(app, '100.64.0.10').attributes.get('aria-checked'), 'true');
  assert.equal(app.requests.some(request => request.path === '/api/config' && request.method === 'POST'), false);
});


test('Global connection uses its selected extra while unrelated primary and exits stay signed out', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.tailscale.exit_node = '';
  initial.values.rules = { mode: 'global', global_exit: 'office' };
  initial.values.exits.push({ id: 'unused', name: 'Unused', exit_node: '100.102.0.11' });
  const snapshot = officeSnapshot({ exit: 'hk-gateway' });
  snapshot.backend_state = 'NeedsLogin';
  snapshot.exits.unused = { backend_state: 'NeedsLogin' };
  const app = await page(snapshot, '', {}, initial);
  assert.equal(app.$('#desktop-connect').disabled, false);
  assert.equal(app.$('#desktop-login').hidden, false, 'primary sign-in still belongs to primary row');
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    assert.deepEqual(actionFields(request), { action: 'connect', revision: app.config.revision });
    Object.assign(app.snapshot, { active: true, state: 'running', mode: 'tun', routing_mode: 'global' });
    return response({ ok: true });
  };
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#desktop-connect').hidden, true);
  assert.equal(app.$('#desktop-disconnect').hidden, false);
  assert.equal(app.$('#desktop-disconnect').disabled, false);
  assert.equal(app.$('#connection-status span').textContent, '已连接');
});


test('header switch distinguishes device-only work from a blocked traffic session', async () => {
  for (const [mode, state, checked] of [['discovery','discovering','false'], ['login','authenticating','false'], ['tun','blocked','true']]) {
    const app = await page(exitsSnapshot({ active: true, mode, state }), '', {}, exitDocument());
    assert.equal(app.$('#desktop-disconnect').hidden, false);
    assert.equal(app.$('#desktop-disconnect').attributes.get('aria-checked'), checked);
  }
});


async function confirmExitRemoval(app) {
  const removing = app.$('#exit-remove').emit('click');
  await settle();
  assert.equal(app.$('#remove-exit-dialog').open, true);
  app.$('#remove-exit-dialog').returnValue = 'remove';
  app.$('#remove-exit-dialog').close();
  await removing;
}

test('the primary exit can be removed, leaving an addable empty list and dormant policy', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  assert.equal(app.$('#exit-danger-zone').hidden, false);
  app.override = request => {
    if (request.path !== '/api/exit-remove') return undefined;
    assert.deepEqual(request.data, { exit: 'main', revision: 'original' });
    app.config = { ...doc('tailscale: null\nexits: []\nrules:\n  source: builtin\n', 'removed'), values: { rules: { source: 'builtin', default_exit: 'main', global_exit: 'main' }, exits: [] } };
    Object.assign(app.snapshot, { revision: 'removed', active: false, state: 'stopped', backend_state: '', exit_nodes: [] });
    return response(app.config);
  };
  await confirmExitRemoval(app);
  assert.equal(app.$('#exit-picker-dialog').open, false);
  assert.equal(app.$('#main-exit-item').hidden, true);
  assert.equal(app.$('#exits-card').hidden, true);
  assert.equal(app.$('#exits-empty').hidden, false);
  assert.equal(app.$('#connection-exit').value, '');
  assert.equal(app.$('#add-exit').disabled, false);
  assert.equal(app.requests.some(r => r.path === '/api/action'), false, 'the atomic removal API owns logout');
  await app.$('#desktop-connect').emit('click');
  assert.deepEqual(app.requests.filter(r => r.path === '/api/action').map(r => r.data.action), ['add-exit']);
  assert.equal(app.requests.some(r => r.data?.action === 'connect'), false, 'empty exits never start as a direct fallback');
});

test('exit removal protects drafts, cancellation, and config changes during confirmation', async () => {
  const draft = await page(exitsSnapshot(), '', {}, exitDocument());
  await draft.$('#choose-exit').emit('click');
  await draft.input('#level', 'debug');
  assert.equal(draft.$('#exit-remove').disabled, true);
  await draft.$('#exit-remove').emit('click');
  assert.equal(draft.$('#remove-exit-dialog').open, undefined);
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await app.$('#choose-exit').emit('click');
  let removing = app.$('#exit-remove').emit('click'); await settle();
  app.$('#remove-exit-dialog').returnValue = 'cancel'; app.$('#remove-exit-dialog').close(); await removing;
  assert.equal(app.$('#exit-picker-dialog').open, true);
  removing = app.$('#exit-remove').emit('click'); await settle();
  app.config = exitDocument('100.64.0.20', 'split', 'external'); app.snapshot.revision = 'external';
  await app.poll();
  app.$('#remove-exit-dialog').returnValue = 'remove'; app.$('#remove-exit-dialog').close(); await removing;
  assert.equal(app.requests.some(r => r.path === '/api/exit-remove'), false);
  assert.equal(app.$('#notice').hidden, false);
});

test('failed removal keeps the saved exit visible for retry', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  await extraRow(app, 'office').emit('click');
  app.override = request => request.path === '/api/exit-remove' ? response({ code: 'remove', error: 'Could not sign out' }, false) : undefined;
  await confirmExitRemoval(app);
  assert.equal(app.$('#exit-picker-dialog').open, true);
  assert.ok(extraRow(app, 'office'), 'the saved extra exit remains in the card list');
  assert.equal(app.$('#exit-remove').disabled, false);
  assert.equal(app.$('#exit-dialog-error').textContent, 'Could not sign out');
});

test('Split connection and rule groups can use an extra without a primary device', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.tailscale = null;
  initial.values.rules = { mode: 'split', default_exit: 'office', source: 'builtin' };
  const snapshot = officeSnapshot(); snapshot.backend_state = '';
  const app = await page(snapshot, '', {}, initial);
  assert.equal(app.$('#main-exit-item').hidden, true);
  assert.equal(app.$('#connection-exit').value, 'office');
  assert.equal(app.$('#desktop-connect').disabled, false);
  assert.equal(app.$('#default-exit').value, 'office');
  assert.equal(app.$('#add-route').disabled, false);
});


for (const mode of ['split', 'global']) test(`Exit Nodes displays the saved ${mode} exit without editing it`, async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode, default_exit: mode === 'split' ? 'office' : 'main', global_exit: 'office', final: 'direct', source: 'builtin',
    routes: [{ exit: 'main', domains: ['keep.example'] }] };
  const app = await page(officeSnapshot(), '', {}, initial);
  assert.equal(app.$('#connection-exit').value, 'office');
  assert.equal(app.$('#connection-exit-control').hidden, false);
  assert.equal(app.$('#main-exit-badge').hidden, true);
  assert.equal(app.config.values.rules.final, 'direct');
  assert.equal(app.requests.some(r => r.method === 'POST'), false);
});

for (const mode of ['split', 'global']) test(`the ${mode} gateway dropdown saves directly without connecting or showing a dialog`, async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode, default_exit: 'main', global_exit: 'main', final: 'direct',
    routes: [{ exit: 'main', domains: ['keep.example'] }] };
  const app = await page(officeSnapshot({ backend_state: 'NeedsLogin' }), '', {}, initial);
  const field = mode === 'global' ? 'global_exit' : 'default_exit';
  const select = app.$('#connection-exit');
  assert.equal(select.value, 'main');
  assert.equal(select.children.find(x => x.value === 'office').textContent, 'Office', 'only display names, never appended IPs');
  assert.equal(select.children.find(x => x.value === 'office').disabled, false, 'idle preferences may name offline devices');
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { [`rules.${field}`]: 'office' });
    app.config = { ...initial, revision: 'saved', values: { ...initial.values,
      rules: { ...initial.values.rules, [field]: 'office' } } };
    app.snapshot.revision = 'saved';
    return response(app.config);
  };
  select.value = 'office';
  await select.emit('change');
  assert.equal(select.value, 'office');
  assert.ok(!app.$('#default-exit-dialog').open);
  assert.equal(app.requests.some(r => r.path === '/api/action'), false);
  assert.deepEqual(app.config.values.rules.routes, initial.values.rules.routes);
});

test('connected gateway dropdown changes use the guarded reconnecting save only', async () => {
  const app = await page({ ...officeSnapshot(), active: true, mode: 'proxy', state: 'running' }, '', {}, multiExitDocument('100.101.0.11'));
  const select = app.$('#connection-exit');
  assert.match(select.title, /重新连接/);
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { 'rules.default_exit': 'office' });
    app.config = { ...app.config, revision: 'saved', values: { ...app.config.values, rules: { default_exit: 'office' } } };
    app.snapshot.revision = 'saved';
    return response(app.config);
  };
  select.value = 'office';
  await select.emit('change');
  assert.ok(!app.$('#default-exit-dialog').open);
  assert.equal(app.requests.some(r => r.path === '/api/action'), false, 'the config callback owns reconnection');
});

function activeGlobalGatewayDocument() {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'global', global_exit: 'office', default_exit: 'main', final: 'direct' };
  initial.values.exits.push({ id: 'spare', name: 'Spare', exit_node: '100.102.0.11' });
  return initial;
}
function activeGlobalGatewaySnapshot() {
  return { ...officeSnapshot(), active: true, mode: 'tun', state: 'running', routing_mode: 'global',
    backend_state: '', network: '', hostname: '', exit: '', exit_nodes: [] };
}

for (const target of ['main', 'spare']) test(`active Global requires consent and stays stopped when saving uncached ${target}`, async () => {
  for (const failure of ['', 'disconnect', 'save']) {
    const initial = activeGlobalGatewayDocument();
    const app = await page(activeGlobalGatewaySnapshot(), '', {}, initial);
    const select = app.$('#connection-exit');
    assert.equal(select.value, 'office');
    assert.equal(select.children.find(option => option.value === target).disabled, false);
    assert.match(select.title, /保存后保持未连接/);
    app.override = request => {
      if (request.path === '/api/action') {
        assert.equal(request.data.action, 'disconnect');
        if (failure === 'disconnect') return response({ error: 'disconnect failed' }, false);
        Object.assign(app.snapshot, { active: false, mode: '', state: 'stopped' });
        return response({ ok: true });
      }
      if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
      assert.equal(app.snapshot.active, false);
      assert.deepEqual(request.data.changes, { 'rules.global_exit': target });
      assert.equal(request.data.revision, 'original');
      if (failure === 'save') return response({ error: 'save failed' }, false);
      app.config = { ...initial, revision: 'saved', values: { ...initial.values, rules: { ...initial.values.rules, global_exit: target } } };
      app.snapshot.revision = 'saved';
      return response(app.config);
    };
    select.value = target;
    const changing = select.emit('change'); await settle();
    assert.equal(app.$('#add-exit-consent-dialog').open, true);
    assert.deepEqual(mutations(app), []);
    answerAddConsent(app, 'gateway'); await changing;
    assert.deepEqual(mutations(app).map(request => request.path === '/api/config' ? 'save' : request.data.action), failure === 'disconnect' ? ['disconnect'] : ['disconnect', 'save']);
    assert.equal(select.value, failure ? 'office' : target);
    assert.equal(app.snapshot.active, failure === 'disconnect');
    assert.equal(app.snapshot.backend_state, '', 'selection must not invent primary authentication');
    assert.equal(app.snapshot.exits.spare, undefined, 'selection must not invent extra authentication');
    assert.ok(!app.$('#default-exit-dialog').open, 'no extra gateway-selection dialog');
  }
});

test('active Global keeps known invalid or offline gateway states guarded', async () => {
  for (const target of ['main', 'spare']) {
    for (const status of [
      { backend_state: 'NeedsLogin' },
      { backend_state: 'Stopped' },
      { backend_state: 'Running', exit_nodes: [{ id: target === 'main' ? '100.64.0.10' : '100.102.0.11', online: false }] },
      { backend_state: '', exit_online: false },
    ]) {
      const snapshot = activeGlobalGatewaySnapshot();
      if (target === 'main') Object.assign(snapshot, status);
      else snapshot.exits[target] = status;
      const app = await page(snapshot, '', {}, activeGlobalGatewayDocument());
      const select = app.$('#connection-exit');
      assert.equal(select.children.find(option => option.value === target).disabled, true);
      select.value = target;
      await select.emit('change');
      assert.equal(app.requests.some(request => request.method === 'POST'), false);
      assert.equal(select.value, 'office');
    }
  }
});

test('active Global same-value selection never writes or reconnects', async () => {
  const app = await page(activeGlobalGatewaySnapshot(), '', {}, activeGlobalGatewayDocument());
  await app.$('#connection-exit').emit('change');
  assert.equal(app.requests.some(request => request.method === 'POST'), false);
  assert.equal(app.$('#connection-exit').value, 'office');
});

test('active Global uncached gateway changes retain draft and revision protections', async () => {
  for (const external of [false, true]) {
    const app = await page(activeGlobalGatewaySnapshot(), '', {}, activeGlobalGatewayDocument());
    await app.input('#level', 'debug');
    if (external) { app.snapshot.revision = 'external'; await app.poll(); }
    assert.equal(app.$('#connection-exit').disabled, true);
    app.$('#connection-exit').value = 'spare';
    await app.$('#connection-exit').emit('change');
    assert.equal(app.requests.some(request => request.path === '/api/config' && request.method === 'POST'), false);
    assert.equal(app.$('#level').value, 'debug');
  }
  const app = await page(activeGlobalGatewaySnapshot(), '', {}, activeGlobalGatewayDocument());
  let saves = 0;
  app.override = request => {
    if (request.path === '/api/action') {
      assert.equal(request.data.action, 'disconnect');
      Object.assign(app.snapshot, { active: false, mode: '', state: 'stopped' });
      return response({ ok: true });
    }
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    saves++;
    assert.equal(request.data.revision, 'original');
    return response({ code: 'conflict', error: 'Config changed' }, false);
  };
  app.$('#connection-exit').value = 'spare';
  const changing = app.$('#connection-exit').emit('change'); await settle();
  answerAddConsent(app, 'gateway'); await changing;
  assert.equal(saves, 1);
  assert.equal(app.$('#connection-exit').value, 'office');
  assert.equal(app.$('#connection-exit').disabled, true);
  assert.equal(app.snapshot.active, false);
  assert.deepEqual(app.requests.filter(request => request.path === '/api/action').map(request => request.data.action), ['disconnect']);
});

test('keeping the current dropdown gateway never writes or reconnects', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  await app.$('#connection-exit').emit('change');
  assert.equal(app.requests.some(r => r.method === 'POST'), false);
  assert.ok(!app.$('#default-exit-dialog').open);
});

test('a failed dropdown save restores the saved gateway', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  app.override = request => request.path === '/api/config' && request.method === 'POST'
    ? response({ code: 'apply', error: 'Save failed' }, false) : undefined;
  app.$('#connection-exit').value = 'office';
  await app.$('#connection-exit').emit('change');
  assert.equal(app.$('#connection-exit').value, 'main');
  assert.equal(app.requests.some(r => r.path === '/api/action'), false);
});

test('drafts and external configuration changes block dropdown writes', async () => {
  for (const dirty of [true, false]) {
    const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
    await app.input('#level', 'debug');
    if (!dirty) { app.snapshot.revision = 'external'; await app.poll(); }
    assert.equal(app.$('#connection-exit').disabled, true);
    app.$('#connection-exit').value = 'office';
    await app.$('#connection-exit').emit('change');
    assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
  }
});

test('transient exit discovery reports checking without requesting another choice or connection', async () => {
  const app = await page({ ...officeSnapshot(), active: true, mode: 'proxy', state: 'missing-exit' }, '', { language: 'en' }, multiExitDocument('100.101.0.11'));
  assert.equal(app.$('#connection-status span').textContent, 'Checking exit availability');
  app.snapshot.state = 'running';
  await app.poll();
  assert.equal(app.$('#connection-status span').textContent, 'Connected');
  assert.equal(app.requests.some(request => request.method === 'POST'), false);
});

for (const active of [false, true]) test(`Kill Switch saves Global/Split policy while ${active ? 'connected' : 'idle'}`, async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', default_exit: 'main', global_exit: 'office', source: 'builtin',
    final: 'direct', routes: [{ exit: 'office', domains: ['keep.example'] }] };
  const app = await page({ ...officeSnapshot(), active, state: active ? 'running' : 'stopped', mode: active ? 'proxy' : '' }, '', {}, initial);
  let calls = 0;
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    calls++;
    assert.deepEqual(request.data.changes, calls === 1 ? { 'rules.mode': 'global', 'tun.enabled': true } : { 'rules.mode': 'split' });
    app.config = { ...app.config, revision: `saved-${calls}`, values: { ...app.config.values,
      rules: { ...app.config.values.rules, mode: calls === 1 ? 'global' : 'split' }, tun: { ...app.config.values.tun, enabled: true } } };
    app.snapshot.revision = app.config.revision;
    return response(app.config);
  };
  assert.equal(app.$('#kill-switch').value, 'split');
  app.$('#kill-switch').value = 'global';
  await app.$('#kill-switch').emit('change');
  assert.equal(app.$('#kill-switch').value, 'global');
  assert.equal(app.$('#connection-exit').value, 'office');
  app.$('#kill-switch').value = 'split';
  await app.$('#kill-switch').emit('change');
  assert.equal(app.$('#kill-switch').value, 'split');
  assert.equal(app.$('#connection-exit').value, 'main');
  await app.$('#kill-switch').emit('change');
  assert.equal(calls, 2, 'keeping the saved mode must not reconnect');
  assert.deepEqual(app.config.values.rules.routes, initial.values.rules.routes);
  assert.equal(app.config.values.rules.source, 'builtin');
  assert.equal(app.requests.some(r => r.path === '/api/action'), false, 'config reconfiguration owns the running connection');
});

test('Kill Switch preserves its saved value after failure and blocks duplicate clicks', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  const save = deferred();
  app.override = request => request.path === '/api/config' && request.method === 'POST' ? save.promise : undefined;
  app.$('#kill-switch').value = 'global';
  const pending = app.$('#kill-switch').emit('change');
  await settle();
  assert.equal(app.$('#kill-switch').disabled, true);
  app.$('#kill-switch').value = 'global';
  await app.$('#kill-switch').emit('change');
  save.resolve(response({ code: 'apply', error: 'Save failed' }, false));
  await pending;
  assert.equal(app.$('#kill-switch').value, 'split');
  assert.equal(app.requests.filter(r => r.path === '/api/config' && r.method === 'POST').length, 1);
});

test('Kill Switch cannot reconnect into an unconfigured gateway or overwrite a draft', async () => {
  const initial = multiExitDocument('100.101.0.11'); initial.values.rules = { mode: 'split', global_exit: '-' };
  const app = await page({ ...officeSnapshot(), active: true, mode: 'proxy', state: 'running' }, '', {}, initial);
  assert.equal(app.$('#kill-switch').disabled, true);
  app.$('#kill-switch').value = 'global';
  await app.$('#kill-switch').emit('change');
  const draft = await page(officeSnapshot(), '', {}, initial);
  await draft.input('#level', 'debug');
  assert.equal(draft.$('#kill-switch').disabled, true);
  draft.$('#kill-switch').value = 'global';
  await draft.$('#kill-switch').emit('change');
  for (const instance of [app, draft]) assert.equal(instance.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
});

const withoutDefaultDocument = (mode = 'split') => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode, default_exit: '-', global_exit: '-', source: 'builtin' };
  return initial;
};

for (const mode of ['split', 'global']) test(`${mode} connect requires explicit default selection and confirmation`, async () => {
  const initial = withoutDefaultDocument(mode);
  const field = mode === 'global' ? 'global_exit' : 'default_exit';
  const app = await page(officeSnapshot(), '', {}, initial);
  const calls = [];
  assert.equal(app.$('#connection-exit').value, '');
  assert.equal(app.$('#main-exit-badge').hidden, true);
  assert.equal(app.$('#desktop-connect').disabled, false);
  app.override = request => {
    if (request.path === '/api/config' && request.method === 'POST') {
      calls.push('save');
      assert.deepEqual(request.data.changes, { [`rules.${field}`]: 'office' });
      app.config = { ...initial, revision: 'changed', values: { ...initial.values,
        rules: { ...initial.values.rules, [field]: 'office' } } };
      app.snapshot.revision = 'changed';
      return response(app.config);
    }
    if (request.path === '/api/action') {
      calls.push('connect');
      assert.deepEqual(actionFields(request), { action: 'connect', revision: app.config.revision });
      assert.equal(app.config.values.rules[field], 'office');
      return response({ ok: true });
    }
    return undefined;
  };
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#default-exit-dialog').open, true);
  assert.equal(app.$('#default-exit-choice').value, '', 'even one available choice is never preselected');
  assert.equal(app.$('#confirm-default-exit').disabled, true);
  assert.deepEqual(calls, []);
  app.$('#default-exit-choice').value = 'office';
  await app.$('#default-exit-choice').emit('change');
  assert.deepEqual(calls, [], 'choosing alone must not connect');
  await app.$('#confirm-default-exit').emit('click');
  assert.deepEqual(calls, ['save', 'connect']);
  assert.equal(app.$('#default-exit-dialog').open, false);
  assert.equal(app.$('#connection-exit').value, 'office');
});

test('a single saved device without ready login is guided directly to sign-in before choosing a default', async () => {
  const initial = withoutDefaultDocument(); initial.values.tailscale = null;
  const app = await page(officeSnapshot({ backend_state: '' }), '', {}, initial);
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    assert.deepEqual(actionFields(request), { action: 'login-exit', exit: 'office' });
    Object.assign(app.snapshot, { state: 'login', mode: 'login', focus: 'office', active: true });
    return response({ ok: true });
  };
  await app.$('#desktop-connect').emit('click');
  assert.ok(!app.$('#default-exit-dialog').open);
  assert.equal(app.$('#connection-setup').hidden, false);
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
  assert.deepEqual(app.requests.filter(r => r.path === '/api/action').map(r => r.data.action), ['login-exit']);
});

test('canceling the default dialog leaves configuration and connection untouched', async () => {
  const app = await page(officeSnapshot(), '', {}, withoutDefaultDocument());
  for (const escape of [false, true]) {
    await app.$('#desktop-connect').emit('click');
    app.$('#default-exit-choice').value = 'office';
    await app.$('#default-exit-choice').emit('change');
    if (escape) {
      await app.$('#default-exit-dialog').emit('cancel');
      app.$('#default-exit-dialog').close();
    } else await app.$('#cancel-default-exit').emit('click');
    assert.equal(app.$('#default-exit-dialog').open, false);
    assert.equal(app.config.values.rules.default_exit, '-');
    assert.equal(app.requests.some(r => r.method === 'POST'), false);
  }
});

test('a failed default save stays retryable and never starts traffic', async () => {
  const app = await page(officeSnapshot(), '', {}, withoutDefaultDocument());
  app.override = request => request.path === '/api/config' && request.method === 'POST'
    ? response({ code: 'apply', error: 'Save failed' }, false) : undefined;
  await app.$('#desktop-connect').emit('click');
  app.$('#default-exit-choice').value = 'office';
  await app.$('#default-exit-choice').emit('change');
  await app.$('#confirm-default-exit').emit('click');
  assert.equal(app.requests.some(r => r.path === '/api/action'), false);
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(app.$('#default-exit-dialog').open, true);
  assert.equal(app.$('#default-exit-error').textContent, 'Save failed');
  assert.equal(app.$('#confirm-default-exit').disabled, false);
});

test('default confirmation rejects changed configuration and guides a selected signed-out device to login', async () => {
  for (const stale of [false, true]) {
    const app = await page(officeSnapshot(), '', {}, withoutDefaultDocument());
    await app.$('#desktop-connect').emit('click');
    app.$('#default-exit-choice').value = 'office';
    await app.$('#default-exit-choice').emit('change');
    if (stale) {
      app.snapshot.revision = 'external-edit';
      app.config = { ...app.config, revision: 'external-edit', yaml: app.config.yaml + '\n# external edit\n' };
    } else app.snapshot.exits.office.backend_state = 'NeedsLogin';
    await app.poll();
    assert.equal(app.$('#confirm-default-exit').disabled, stale);
    await app.$('#confirm-default-exit').emit('click');
    assert.deepEqual(app.requests.filter(r => r.method === 'POST').map(r => r.data.action), stale ? [] : ['login-exit']);
    assert.equal(app.requests.some(r => r.data?.action === 'connect'), false);
  }
});

test('default selection cannot be canceled during its save', async () => {
  const app = await page(officeSnapshot(), '', {}, withoutDefaultDocument());
  const save = deferred();
  app.override = request => request.path === '/api/config' && request.method === 'POST' ? save.promise : undefined;
  await app.$('#desktop-connect').emit('click');
  app.$('#default-exit-choice').value = 'office';
  await app.$('#default-exit-choice').emit('change');
  const confirming = app.$('#confirm-default-exit').emit('click');
  await settle();
  let prevented = false;
  await app.$('#default-exit-dialog').emit('cancel', { preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(app.$('#cancel-default-exit').disabled, true);
  save.resolve(response({ code: 'apply', error: 'Retry later' }, false));
  await confirming;
  assert.equal(app.$('#default-exit-dialog').open, true);
  assert.equal(app.$('#cancel-default-exit').disabled, false);
});

test('an offline configured default never silently switches to another exit', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', default_exit: 'main', source: 'builtin' };
  const snapshot = officeSnapshot();
  snapshot.exit_nodes.forEach(node => { node.online = false; });
  const app = await page(snapshot, '', {}, initial);
  assert.equal(app.$('#connection-exit').value, 'main');
  assert.equal(app.$('#desktop-connect').disabled, false);
  await app.$('#desktop-connect').emit('click');
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.equal(app.$('#connection-exit').value, 'main');
  assert.equal(app.requests.some(r => r.method === 'POST'), false);
  await app.$('#cancel-exit-picker').emit('click');
  assert.equal(app.$('#connection-setup').hidden, true);
});

test('unused signed-out or offline exits never disable Split connection', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', default_exit: 'main', final: 'direct', routes: [] };
  for (const state of ['NeedsLogin', 'Running']) {
    const snapshot = officeSnapshot({ backend_state: state, exit_nodes: [{ id: '100.101.0.11', name: 'office', online: false }] });
    const app = await page(snapshot, '', {}, initial);
    assert.equal(app.$('#desktop-connect').disabled, false, `unused ${state} device blocked the default`);
  }
  initial.values.rules.routes = [{ exit: 'office', domains: ['corp.example'] }];
  const required = await page(officeSnapshot({ backend_state: 'NeedsLogin' }), '', {}, initial);
  assert.equal(required.$('#desktop-connect').disabled, true, 'a rule group still requires its own login');
});

test('the explicit no-device direct-only Windows fixture connects without a gateway dialog', async () => {
  const initial = { ...doc('tailscale: null\nexits: []\nrules:\n  source: ""\n  final: direct\n'),
    values: { log_level: 'info', tailscale: null, exits: [], rules: { source: '', final: 'direct' }, tun: { enabled: false, mtu: 1400 } } };
  const app = await page({ desktop: true, active: false, state: 'stopped', backend_state: 'Direct' }, '', {}, initial);
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    assert.deepEqual(actionFields(request), { action: 'connect', revision: app.config.revision });
    app.snapshot = { ...app.snapshot, active: true, state: 'running', mode: 'proxy', backend_state: 'Direct' };
    return response({ ok: true });
  };
  assert.equal(app.$('#desktop-connect').disabled, false);
  await app.$('#desktop-connect').emit('click');
  assert.ok(!app.$('#default-exit-dialog').open);
  assert.equal(app.$('#connection-status span').textContent, '已连接');
  assert.equal(app.requests.filter(request => request.path === '/api/action').length, 1);
});

test('settings drafts block connection and retain the saved default display', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  await app.input('#level', 'debug');
  assert.equal(app.$('#desktop-connect').disabled, true);
  assert.equal(app.$('#connection-exit').value, 'main');
  assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
});

test('retained final, route groups and gateway selectors reserve new device IDs', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.exits.push({ id: 'exit-1', exit_node: '' });
  initial.values.rules = { mode: 'global', final: 'exit-2', default_exit: 'exit-3', global_exit: 'exit-4',
    routes: [{ exit: 'exit-5', domains: ['private.example'] }, { exit: 'exit-6' }] };
  const app = await page(exitsSnapshot(), '', {}, initial);
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    assert.deepEqual(actionFields(request), { action: 'add-exit', exit: firstAddedID, revision: app.config.revision });
    return response({ ok: true });
  };
  await invokeAdd(app);
  assert.deepEqual(mutations(app).map(request => request.path === '/api/config' ? 'save' : request.data.action), ['add-exit']);
  assert.deepEqual(app.config.values.rules, initial.values.rules);
});

test('empty route groups allow manual direct-only Connect without choosing an exit', async () => {
  const initial = { ...doc(), values: { tailscale: null, exits: [], rules: { final: 'direct',
    routes: [{ exit: 'removed' }, { exit: 'another', domains: [], ips: [] }] } } };
  const app = await page({ desktop: true, active: false, state: 'stopped', backend_state: 'Direct' }, '', {}, initial);
  assert.equal(app.$('#desktop-connect').disabled, false);
  await app.$('#desktop-connect').emit('click');
  assert.ok(!app.$('#default-exit-dialog').open);
  assert.deepEqual(mutations(app).map(request => request.data.action), ['connect']);
  assert.equal(app.requests.some(request => request.path === '/api/config' && request.method === 'POST'), false);
});

test('the primary is an explicit final-route choice independent of an additional default', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', final: 'direct', default_exit: 'office' };
  const app = await page(officeSnapshot(), '', {}, initial);
  const options = app.$('#final-route').children;
  assert.deepEqual(options.map(option => option.value), ['direct', 'proxy', 'main', 'office']);
  assert.equal(options.find(option => option.value === 'main').textContent, '主出口');
  assert.match(options.find(option => option.value === 'proxy').textContent, /Office/);
  app.override = request => request.path === '/api/preview'
    ? response({ ...initial, yaml: initial.yaml + '# final main\n', values: { ...initial.values, rules: { ...initial.values.rules, final: 'main' } } }) : undefined;
  await app.input('#final-route', 'main'); await app.flushTimers();
  assert.equal(app.$('#final-route').value, 'main');
  assert.equal(app.$('#default-exit').value, 'office');
  assert.deepEqual(app.requests.find(request => request.path === '/api/preview').data.changes, { 'rules.final': 'main' });
});

const savedExitLogin = (app, id) => app.$('#extra-exits').children.flatMap(item => item.children).find(child => child.dataset.exitLogin === id);
const globalLoginDocument = () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'global', global_exit: 'main' };
  return initial;
};
const globalLoginSnapshot = () => ({ ...officeSnapshot({ backend_state: 'NeedsLogin' }), active: true, mode: 'tun', state: 'running', routing_mode: 'global' });

test('Global existing-exit login cancel and Escape preserve the active device and configuration', async () => {
  for (const origin of ['row', 'picker']) for (const dismissal of ['cancel', 'escape']) {
    const initial = globalLoginDocument();
    const app = await page(globalLoginSnapshot(), '', {}, initial);
    if (origin === 'picker') await extraRow(app, 'office').emit('click');
    const signingIn = (origin === 'row' ? savedExitLogin(app, 'office') : app.$('#picker-login')).emit('click'); await settle();
    const dialog = app.$('#add-exit-consent-dialog');
    assert.equal(dialog.open, true);
    assert.equal(app.$('#global-loss-confirm').value, 'login');
    assert.deepEqual(mutations(app), []);
    if (dismissal === 'escape') { await dialog.emit('cancel'); dialog.close(); }
    else answerAddConsent(app, 'cancel');
    await signingIn;
    assert.deepEqual(mutations(app), []);
    assert.equal(app.snapshot.active, true);
    assert.equal(app.config, initial);
  }
});

test('Global existing-exit login explicitly disconnects and never reconnects after success or failure', async () => {
  for (const failure of ['', 'disconnect', 'login-exit']) {
    const app = await page(globalLoginSnapshot(), '', {}, globalLoginDocument());
    app.override = request => {
      if (request.path !== '/api/action') return undefined;
      if (request.data.action === failure) return response({ error: `${failure} failed` }, false);
      if (request.data.action === 'disconnect') Object.assign(app.snapshot, { active: false, mode: '', state: 'stopped' });
      return response({ ok: true });
    };
    const signingIn = savedExitLogin(app, 'office').emit('click'); await settle();
    assert.deepEqual(mutations(app), []);
    answerAddConsent(app, 'login'); await signingIn;
    assert.deepEqual(mutations(app).map(request => request.data.action), failure === 'disconnect' ? ['disconnect'] : ['disconnect', 'login-exit']);
    assert.equal(app.snapshot.active, failure === 'disconnect');
    assert.equal(app.$('#notice').hidden, !failure);
  }
});

test('Global current-device sign-in recovers the pending authorization without consent or disconnect', async () => {
  const snapshot = activeGlobalGatewaySnapshot();
  snapshot.state = 'blocked'; snapshot.exits.office.backend_state = 'NeedsLogin';
  const app = await page(snapshot, '', {}, activeGlobalGatewayDocument());
  await savedExitLogin(app, 'office').emit('click');
  assert.deepEqual(mutations(app).map(actionFields), [{ action: 'login-exit', exit: 'office' }]);
  assert.ok(!app.$('#add-exit-consent-dialog').open);
  assert.equal(app.snapshot.active, true);
});

test('Global gateway cancel and Escape keep the original direct text selector and active protection', async () => {
  for (const dismissal of ['cancel', 'escape']) {
    const initial = activeGlobalGatewayDocument();
    const app = await page(activeGlobalGatewaySnapshot(), '', {}, initial);
    app.$('#connection-exit').value = 'spare';
    const changing = app.$('#connection-exit').emit('change'); await settle();
    const dialog = app.$('#add-exit-consent-dialog');
    assert.equal(dialog.open, true);
    if (dismissal === 'escape') { await dialog.emit('cancel'); dialog.close(); }
    else answerAddConsent(app, 'cancel');
    await changing;
    assert.deepEqual(mutations(app), []);
    assert.equal(app.snapshot.active, true);
    assert.equal(app.config, initial);
    assert.equal(app.$('#connection-exit').value, 'office');
    assert.ok(!app.$('#default-exit-dialog').open);
  }
});

test('Global login and gateway consent recheck revisions, capability, busy state and drafts before disconnect', async () => {
  for (const action of ['login', 'gateway']) for (const condition of ['revision', 'unsupported', 'transition', 'lost', 'draft']) {
    const app = await page(action === 'login' ? globalLoginSnapshot() : activeGlobalGatewaySnapshot(), '', {},
      action === 'login' ? globalLoginDocument() : activeGlobalGatewayDocument());
    if (action === 'gateway') app.$('#connection-exit').value = 'spare';
    const working = action === 'login' ? savedExitLogin(app, 'office').emit('click') : app.$('#connection-exit').emit('change');
    await settle();
    assert.equal(app.$('#add-exit-consent-dialog').open, true);
    if (condition === 'revision') { app.snapshot.revision = 'newer'; app.config = { ...app.config, revision: 'newer' }; }
    else if (condition === 'unsupported') app.snapshot.desktop = false;
    else if (condition === 'transition') app.snapshot.state = 'stopping';
    else if (condition === 'lost') app.override = request => request.path === '/api/state' ? response({ error: 'Unavailable' }, false) : undefined;
    else await app.input('#level', 'debug');
    await app.poll();
    answerAddConsent(app, action); await working;
    assert.deepEqual(mutations(app), [], `${action}/${condition}`);
    assert.equal(app.snapshot.active, true);
  }
});

test('same-node DNS metadata recovery requires explicit Apply and retains the saved account guard', async () => {
  for (const dismissal of ['apply', 'cancel', 'escape']) {
    const initial = multiExitDocument('100.101.0.11'); initial.values.exits[0].magic_dns_suffix = 'old.ts.net';
    const app = await page(officeSnapshot({ network: 'office.ts.net', magic_dns_suffix: 'new.ts.net' }), '', {}, initial);
    await extraRow(app, 'office').emit('click');
    assert.equal(app.$('#apply-exit-picker').disabled, false);
    assert.equal(app.$('#apply-exit-picker').textContent, '保存 DNS 信息');
    assert.match(app.$('#exit-dialog-selection').textContent, /new.ts.net/);
    assert.deepEqual(mutations(app), []);
    if (dismissal === 'apply') {
      app.override = request => {
        if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
        assert.deepEqual(request.data.changes, { exits: [{ ...initial.values.exits[0], magic_dns_suffix: 'new.ts.net' }] });
        app.config = { ...initial, revision: 'saved', values: { ...initial.values, exits: request.data.changes.exits } }; app.snapshot.revision = 'saved';
        return response(app.config);
      };
      await app.$('#apply-exit-picker').emit('click');
      assert.equal(app.config.values.exits[0].magic_dns_suffix, 'new.ts.net');
      assert.equal(app.config.values.exits[0].exit_node, '100.101.0.11');
      assert.equal(app.config.values.exits[0].tailnet, 'office.ts.net');
    } else {
      if (dismissal === 'escape') { await app.$('#exit-picker-dialog').emit('cancel'); app.$('#exit-picker-dialog').close(); }
      else await app.$('#cancel-exit-picker').emit('click');
      assert.deepEqual(mutations(app), []);
      assert.equal(app.config, initial);
    }
  }
});

test('same-node DNS metadata comparison uses canonical and legacy effective suffixes', async () => {
  for (const suffix of ['OLD.TS.NET.', undefined]) {
    const initial = multiExitDocument('100.101.0.11');
    initial.values.exits[0].tailnet = 'old.ts.net';
    if (suffix) initial.values.exits[0].magic_dns_suffix = suffix;
    const app = await page(officeSnapshot({ network: 'old.ts.net', magic_dns_suffix: 'old.ts.net' }), '', {}, initial);
    await extraRow(app, 'office').emit('click');
    assert.equal(app.$('#apply-exit-picker').disabled, true);
    assert.deepEqual(mutations(app), []);
  }
});

test('DNS metadata recovery cannot rewrite an unauthenticated or different Tailnet identity', async () => {
  for (const status of [{ backend_state: 'NeedsLogin' }, { network: 'other.example' }]) {
    const initial = multiExitDocument('100.101.0.11'); initial.values.exits[0].magic_dns_suffix = 'old.ts.net';
    const app = await page(officeSnapshot({ magic_dns_suffix: 'new.ts.net', ...status }), '', {}, initial);
    await extraRow(app, 'office').emit('click');
    assert.equal(app.$('#apply-exit-picker').disabled, true);
    assert.deepEqual(mutations(app), []);
  }
});

test('retained unused devices do not force a default for direct or explicitly owned Split policy', async () => {
  for (const defaultExit of ['-', 'main', 'pending']) {
    for (const rules of [{ final: 'direct' }, { final: 'office' }, { final: 'direct', routes: [{ exit: 'office', domains: ['corp.example'] }] }]) {
      const initial = multiExitDocument('100.101.0.11');
      initial.values.tailscale = { exit_node: '' };
      initial.values.exits.push({ id: 'pending', exit_node: '' });
      initial.values.rules = { ...rules, mode: 'split', default_exit: defaultExit, source: '' };
      const app = await page(officeSnapshot({ backend_state: 'Running' }), '', {}, initial);
      app.snapshot.backend_state = 'NeedsLogin'; await app.poll();
      assert.equal(app.$('#desktop-connect').disabled, false, `${defaultExit}/${rules.final}`);
      assert.equal(app.$('#desktop-hint').hidden, true);
      await app.$('#desktop-connect').emit('click');
      assert.ok(!app.$('#default-exit-dialog').open);
      assert.deepEqual(mutations(app).map(request => request.data.action), ['connect']);
    }
  }
});

test('a configured default with a node retains its bare-Tailnet readiness requirement', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', default_exit: 'office', final: 'direct', source: '' };
  const app = await page(officeSnapshot({ backend_state: 'NeedsLogin' }), '', {}, initial);
  assert.equal(app.$('#desktop-connect').disabled, false);
  await app.$('#desktop-connect').emit('click');
  assert.deepEqual(mutations(app).map(request => request.data.action), ['login-exit']);
  assert.ok(!mutations(app).some(request => request.data.action === 'connect'));
  assert.ok(!app.$('#default-exit-dialog').open);
});

function guidedDocument() {
  return { ...doc('log_level: info\ntailscale: null\nexits: []\nrules: {source: builtin, default_exit: "-"}\n'), values: { log_level: 'info', tailscale: null, exits: [], rules: { mode: 'split', source: 'builtin', default_exit: '-', global_exit: '-' } } };
}
function guidedBackend(app, options = {}) {
  const backend = { deferAdd: false, failAdd: false, failSave: false, lastAdded: '', ...options };
  const nodes = [{ id: '100.101.0.11', name: 'chosen-gateway', online: true }];
  backend.finishAdd = () => {
    const id = backend.lastAdded;
    const revision = `added-${id}`;
    app.config = { ...app.config, revision, yaml: app.config.yaml + `# added ${id}\n`, values: { ...app.config.values, exits: [...app.config.values.exits, { id, auto_name: true, exit_node: '' }] } };
    Object.assign(app.snapshot, { state: 'authenticated', mode: 'login', active: false, focus: '', error: '', error_code: '', revision, added_exit: id, added_revision: revision,
      exits: { ...app.snapshot.exits, [id]: { backend_state: 'Running', network: 'work.example', exit_nodes: nodes } } });
  };
  app.override = request => {
    if (request.path === '/api/action') {
      const { action, exit } = request.data;
      if (action === 'add-exit') {
        backend.lastAdded = exit;
        if (backend.failAdd) {
          Object.assign(app.snapshot, { state: 'failed', active: false, error: 'service installation failed', error_code: 'service-installation' });
          return response({ code: 'action', error: 'service installation failed' }, false);
        }
        Object.assign(app.snapshot, { state: 'authorizing', mode: 'login', active: true, focus: exit, error: '', error_code: '' });
        if (!backend.deferAdd) backend.finishAdd();
      } else if (action === 'connect') {
        const id = app.config.values.rules.default_exit;
        assert.ok(app.config.values.exits.find(exit => exit.id === id)?.exit_node, 'connect requires the confirmed node and default');
        Object.assign(app.snapshot, { state: 'running', mode: 'proxy', active: true, focus: '', error: '', error_code: '' });
      } else if (action === 'disconnect') Object.assign(app.snapshot, { state: 'stopped', mode: '', active: false, focus: '' });
      else if (action === 'login-exit') Object.assign(app.snapshot, { state: 'authenticated', mode: 'login', active: false, exits: { ...app.snapshot.exits, [exit]: { backend_state: 'Running', network: 'work.example', exit_nodes: nodes } } });
      else if (action === 'refresh-exits') app.snapshot.exits[app.snapshot.added_exit].exit_nodes = nodes;
      return response({ ok: true });
    }
    if (request.path === '/api/config' && request.method === 'POST') {
      if (backend.failSave) return response({ code: 'apply', error: 'save failed' }, false);
      const values = JSON.parse(JSON.stringify(app.config.values));
      for (const [path, value] of Object.entries(request.data.changes)) {
        const [section, key] = path.split('.');
        if (key) values[section][key] = value; else values[section] = value;
      }
      const revision = `saved-${app.requests.length}`;
      app.config = { ...app.config, yaml: app.config.yaml + '# saved\n', values, revision };
      app.snapshot.revision = revision;
      return response(app.config);
    }
  };
  return backend;
}

test('Connect without exits adds a device, confirms its node and default, then connects exactly once', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  const backend = guidedBackend(app);
  await app.$('#desktop-connect').emit('click');
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.equal(app.$('#connection-setup-title').textContent, '选择节点后即可连接');
  assert.deepEqual(mutations(app).map(r => r.data.action), ['add-exit']);
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  assert.equal(app.$('#apply-exit-picker').textContent, '保存并连接');
  await app.$('#apply-exit-picker').emit('click');
  const save = mutations(app).find(r => r.path === '/api/config');
  assert.equal(save.data.changes['rules.default_exit'], backend.lastAdded);
  assert.equal(save.data.changes.exits[0].exit_node, '100.101.0.11');
  assert.deepEqual(mutations(app).map(r => r.data.action || 'save'), ['add-exit', 'save', 'connect']);
  assert.ok(app.snapshot.active);
  assert.equal(app.$('#connection-setup').hidden, true);
  await app.poll();
  assert.equal(mutations(app).filter(r => r.data.action === 'connect').length, 1);
});

test('native Connect enters the same guided flow once and cannot replay after cancellation or reload', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', {}, guidedDocument());
  guidedBackend(app);
  app.snapshot.connect_request = 'native-connect-1';
  await app.poll();
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.equal(mutations(app).filter(r => r.data.action === 'add-exit').length, 1);
  await app.$('#cancel-exit-picker').emit('click');
  await app.poll();
  assert.equal(mutations(app).length, 1);
  const reloaded = await page(app.snapshot, 'mygo-windows', { storage: app.storage }, app.config);
  assert.equal(mutations(reloaded).length, 0);
});

test('native Connect with a settings draft explains the block and cannot start after the draft is discarded', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', {}, guidedDocument());
  guidedBackend(app);
  await app.input('#level', 'debug');
  app.snapshot.connect_request = 'native-draft';
  await app.poll();
  assert.match(app.$('#notice-detail').textContent, /先保存配置/);
  assert.equal(mutations(app).length, 0);
  const reload = app.$('#discard').emit('click');
  app.$('#discard-dialog').returnValue = 'discard'; app.$('#discard-dialog').close();
  await reload;
  await app.poll();
  assert.equal(mutations(app).length, 0);
});

test('an existing service mismatch displays an install action without requiring a log visit', async () => {
  const app = await page({ desktop: true, state: 'failed', error: 'old service', error_code: 'service-installation' }, '', {}, guidedDocument());
  guidedBackend(app, { deferAdd: true });
  assert.equal(app.$('#connection-setup').hidden, false);
  assert.equal(app.$('#connection-setup-title').textContent, '后台服务需要安装或更新');
  assert.equal(app.$('#connection-setup-retry').textContent, '安装服务并继续连接');
  await app.$('#connection-setup-retry').emit('click');
  assert.equal(app.$('#connection-setup-title').textContent, '需要安装或更新后台服务');
  assert.deepEqual(mutations(app).map(r => r.data.action), ['add-exit']);
});

test('manual Add still saves a node without choosing a default or connecting', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  guidedBackend(app);
  await app.$('#add-exit').emit('click');
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(mutations(app).some(r => r.data.action === 'connect'), false);
});

test('guided installation is visible, rejects duplicate Connect, and cancellation prevents late connection', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  const backend = guidedBackend(app, { deferAdd: true });
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#connection-setup-title').textContent, '需要安装或更新后台服务');
  assert.equal(app.$('#exits-card').hidden, true);
  await app.$('#desktop-connect').emit('click');
  assert.equal(mutations(app).filter(r => r.data.action === 'add-exit').length, 1);
  await app.$('#connection-setup-cancel').emit('click');
  assert.deepEqual(mutations(app).map(mutationAction), ['add-exit', 'cancel']);
  backend.finishAdd(); // Even a late result cannot restore the canceled intent.
  await app.poll();
  assert.ok(!app.$('#exit-picker-dialog').open);
  assert.ok(!mutations(app).some(r => r.data.action === 'connect'));
});

test('canceling the guided node picker leaves the saved device and never connects later', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  guidedBackend(app);
  await app.$('#desktop-connect').emit('click');
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  await app.$('#cancel-exit-picker').emit('click');
  await app.poll();
  assert.equal(app.config.values.exits.length, 1);
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(mutations(app).length, 1);
  assert.equal(app.$('#connection-setup').hidden, true);
});

test('failed service installation has an explicit retry and never publishes or connects before success', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  const backend = guidedBackend(app, { failAdd: true });
  await app.$('#desktop-connect').emit('click');
  const failedID = backend.lastAdded;
  assert.equal(app.$('#connection-setup-retry').hidden, false);
  assert.equal(app.$('#connection-setup-retry').textContent, '安装服务并继续连接');
  assert.equal(app.config.values.exits.length, 0);
  backend.failAdd = false;
  await app.$('#connection-setup-retry').emit('click');
  assert.notEqual(backend.lastAdded, failedID);
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.ok(!mutations(app).some(r => r.data.action === 'connect'));
});

test('external edits during the Installer wait visibly pause automatic continuation', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  guidedBackend(app, { deferAdd: true });
  await app.$('#desktop-connect').emit('click');
  app.config = { ...app.config, revision: 'external-during-install', yaml: app.config.yaml + '# edit\n' };
  app.snapshot.revision = app.config.revision;
  await app.poll();
  assert.equal(app.$('#connection-setup-title').textContent, '连接设置未完成');
  assert.match(app.$('#connection-setup-detail').textContent, /自动连接已暂停/);
  assert.ok(!mutations(app).some(r => r.data.action === 'connect'));
});

test('external edits after Add pause the connection intent even when the new device is authenticated', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  const backend = guidedBackend(app, { deferAdd: true });
  await app.$('#desktop-connect').emit('click');
  backend.finishAdd();
  app.config = { ...app.config, revision: 'external', yaml: app.config.yaml + '# external policy\n' };
  app.snapshot.revision = 'external';
  await app.poll();
  assert.equal(app.$('#connection-setup-title').textContent, '连接设置未完成');
  assert.match(app.$('#connection-setup-detail').textContent, /配置已更改/);
  assert.ok(!app.$('#exit-picker-dialog').open);
  assert.equal(mutations(app).filter(r => r.data.action === 'connect').length, 0);
});

test('guided node discovery has an explicit retry before node confirmation', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  const backend = guidedBackend(app, { deferAdd: true });
  await app.$('#desktop-connect').emit('click');
  backend.finishAdd();
  app.snapshot.exits[backend.lastAdded].exit_nodes = [];
  await app.poll();
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.equal(app.$('#exit-empty').hidden, false);
  assert.equal(app.$('#picker-go-connection').hidden, false);
  assert.equal(app.$('#picker-go-connection').textContent, '重新读取节点');
  await app.$('#picker-go-connection').emit('click');
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.ok(!mutations(app).some(r => r.data.action === 'connect'));
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(mutations(app).filter(r => r.data.action === 'connect').length, 1);
});

test('a failed guided node save stays retryable and cannot connect before the save succeeds', async () => {
  const app = await page({ desktop: true }, '', {}, guidedDocument());
  const backend = guidedBackend(app, { failSave: true });
  await app.$('#desktop-connect').emit('click');
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  await app.$('#apply-exit-picker').emit('click');
  assert.ok(app.$('#exit-picker-dialog').open);
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(mutations(app).some(r => r.data.action === 'connect'), false);
  backend.failSave = false;
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(mutations(app).filter(r => r.data.action === 'connect').length, 1);
});

test('service installation while signing in explains the system Installer and resumes the login state', async () => {
  for (const language of ['zh', 'en']) {
    const app = await page({ desktop: true, state: 'authorizing', active: true, mode: 'login' }, '', { language, preference: language });
    assert.equal(app.$('#connection-status span').textContent, language === 'zh' ? '等待安装后台服务' : 'Waiting for network service installation');
    assert.match(app.$('#desktop-hint').textContent, language === 'zh' ? /系统安装器.*安装或更新/ : /system Installer/);
    assert.ok(app.$('#desktop-login').disabled);
    assert.equal(mutations(app).length, 0);
    app.snapshot = { ...app.snapshot, state: 'login', backend_state: 'NeedsLogin' };
    await app.poll();
    assert.equal(app.$('#connection-status span').textContent, language === 'zh' ? '等待 Tailscale 登录' : 'Awaiting Tailscale sign-in');
    assert.equal(mutations(app).length, 0);
  }
});

test('guided Connect waits for primary or additional device approval without relaunching login', async () => {
  for (const id of ['main', 'office']) {
    const initial = multiExitDocument('100.101.0.11');
    initial.values.rules = { mode: 'split', source: 'builtin', final: 'direct', default_exit: id };
    const snapshot = officeSnapshot();
    (id === 'main' ? snapshot : snapshot.exits.office).backend_state = 'NeedsMachineAuth';
    const app = await page(snapshot, '', { page: 'home' }, initial);
    app.override = request => {
      if (request.path !== '/api/action') return undefined;
      assert.equal(request.data.action, 'connect', 'approval must never launch another browser login');
      Object.assign(app.snapshot, { active: true, mode: 'proxy', state: 'running', traffic_exit: id });
      return response({ ok: true });
    };
    await app.$('#home-connect').emit('click');
    assert.equal(app.$('#connection-setup-title').textContent, '等待管理员批准设备');
    await app.poll(); await app.poll();
    assert.equal(mutations(app).length, 0);
    (id === 'main' ? app.snapshot : app.snapshot.exits.office).backend_state = 'Running';
    await app.poll();
    assert.deepEqual(mutations(app).map(r => r.data.action), ['connect']);
  }
});

test('approval status follows the login helper owner rather than a different saved gateway', async () => {
  for (const mode of ['login', 'discovery']) {
    const initial = multiExitDocument('100.101.0.11');
    initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'office' };
    const snapshot = officeSnapshot();
    Object.assign(snapshot, { active: true, mode, state: 'authenticating', backend_state: 'NeedsMachineAuth', traffic_exit: 'office' });
    const app = await page(snapshot, '', { page: 'home' }, initial);
    assert.equal(app.$('#home-status').textContent, '等待管理员批准设备');
    assert.equal(app.$('#connection-status span').textContent, '等待管理员批准设备');
    app.snapshot.backend_state = 'Running';
    app.snapshot.exits.office.backend_state = 'NeedsMachineAuth';
    await app.poll();
    assert.notEqual(app.$('#connection-status span').textContent, '等待管理员批准设备', 'approval on another device must not be attributed to this helper');
  }
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main' };
  const app = await page({ ...officeSnapshot({ backend_state: 'NeedsMachineAuth' }), active: true, mode: 'proxy', state: 'authenticating', traffic_exit: 'office' }, '', {}, initial);
  assert.equal(app.$('#connection-status span').textContent, '等待管理员批准设备', 'traffic approval follows the running helper, not newly saved routing');
});

test('missing default can be selected even when another referenced device needs login', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: '-', final: 'direct', routes: [{ exit: 'office', domains: ['work.example'] }] };
  const app = await page(officeSnapshot({ backend_state: 'NeedsLogin' }), '', {}, initial);
  assert.equal(app.$('#desktop-connect').disabled, false);
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#default-exit-dialog').open, true);
  assert.equal(mutations(app).length, 0);
});

test('native window show wakes a visible page immediately without waiting for its polling timer', async () => {
  const app = await page({ desktop: true }, 'mygo-macos', { page: 'home' }, guidedDocument());
  guidedBackend(app);
  app.snapshot.connect_request = 'native-show-immediate';
  await app.windowEvents.emit('tailsplit-window-shown');
  assert.equal(mutations(app).filter(r => r.data.action === 'add-exit').length, 1);
  assert.equal(app.$('#exit-picker-dialog').open, true);
  await app.windowEvents.emit('tailsplit-window-shown');
  assert.equal(mutations(app).filter(r => r.data.action === 'add-exit').length, 1, 'repeated Show does not replay the request');
});

test('inactive approval can be checked without login and resumes only after fresh approval', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'office', final: 'direct' };
  const app = await page(officeSnapshot({ backend_state: 'NeedsMachineAuth' }), '', {}, initial);
  let checks = 0;
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    if (request.data.action === 'refresh-exits') {
      checks++;
      if (checks === 1) {
        Object.assign(app.snapshot, { active: true, mode: 'discovery', state: 'discovering' });
        app.snapshot.exits.office.backend_state = '';
      } else {
        Object.assign(app.snapshot, { active: false, mode: 'discovery', state: 'stopped', error: '' });
        app.snapshot.exits.office.backend_state = 'Running';
      }
    } else {
      assert.equal(request.data.action, 'connect', 'checking approval must not relaunch browser authorization');
      Object.assign(app.snapshot, { active: true, mode: 'proxy', state: 'running', active_revision: app.config.revision });
    }
    return response({ ok: true });
  };
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#connection-setup-retry').textContent, '检查批准状态');
  assert.equal(app.$('#connection-setup-retry').hidden, false);
  await app.$('#connection-setup-retry').emit('click');
  assert.equal(app.$('#connection-setup-title').textContent, '正在检查设备批准状态');
  await app.$('#connection-setup-retry').emit('click');
  assert.equal(checks, 1, 'an active check is not started twice');
  Object.assign(app.snapshot, { active: false, state: 'failed', error: 'administrator approval pending' });
  app.snapshot.exits.office.backend_state = 'NeedsMachineAuth';
  await app.poll();
  assert.equal(app.$('#connection-setup-title').textContent, '等待管理员批准设备');
  assert.equal(app.$('#connection-setup-retry').hidden, false);
  await app.poll();
  assert.equal(checks, 1, 'state polling never starts an unbounded discovery loop');
  await app.$('#connection-setup-retry').emit('click');
  assert.deepEqual(mutations(app).map(r => r.data.action), ['refresh-exits', 'refresh-exits', 'connect']);
});

test('canceling inactive approval prevents a later status update from connecting', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'office', final: 'direct' };
  const app = await page(officeSnapshot({ backend_state: 'NeedsMachineAuth' }), '', {}, initial);
  await app.$('#desktop-connect').emit('click');
  await app.$('#connection-setup-cancel').emit('click');
  app.snapshot.exits.office.backend_state = 'Running';
  await app.poll();
  assert.deepEqual(mutations(app).map(mutationAction), ['cancel']);
});

test('a browser panel cannot consume a Connect intent intended for the native window', async () => {
  const browser = await page({ desktop: true, connect_request: 'native-only' }, '', { page: 'home' }, guidedDocument());
  await browser.windowEvents.emit('tailsplit-window-shown');
  await browser.poll();
  assert.equal(browser.requests.some(r => r.path === '/api/connect-request'), false);
  assert.equal(browser.snapshot.connect_request, 'native-only');
  assert.equal(mutations(browser).length, 0);
  const native = await page({ desktop: true }, 'mygo-macos', { page: 'home' }, guidedDocument());
  guidedBackend(native, { deferAdd: true });
  native.snapshot.connect_request = browser.snapshot.connect_request;
  await native.windowEvents.emit('tailsplit-window-shown');
  assert.equal(native.snapshot.connect_request, '');
  assert.equal(mutations(native).filter(r => r.data.action === 'add-exit').length, 1);
});

test('home includes an authenticated runtime-discovered private-domain owner', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct' };
  initial.values.exits[0].tailnet = '';
  const app = await page({ ...officeSnapshot({ magic_dns_suffix: 'learned.ts.net' }), active: true, mode: 'proxy', state: 'running',
    traffic: { session_id: 'compiled-owner', sampled_at: new Date().toISOString(), routes: {}, private_exits: ['office'] } }, '', { page: 'home' }, initial);
  const rows = () => app.$('#home-routes').children.map(item => item.children[0]);
  assert.deepEqual(rows().map(row => row.dataset.exit), ['direct', 'main', 'office']);
  assert.equal(rows()[2].children[1].textContent, '私有域名');
  app.snapshot.active_revision = 'different-session-document';
  await app.poll();
  assert.deepEqual(rows().map(row => row.dataset.exit), ['direct', 'main'], 'runtime metadata cannot be attributed to a different saved document');
});

test('home canonicalizes private suffix ownership like the compiler', async () => {
  for (const [primary, secondary] of [['office.ts.net.', 'office.ts.net'], ['*.OFFICE.ts.net.', '.office.ts.net'], ['bücher.example.', 'xn--bcher-kva.example']]) {
    const initial = multiExitDocument('100.101.0.11');
    initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct' };
    initial.values.dns = { magic_domains: [primary] };
    initial.values.exits[0].magic_dns_suffix = secondary;
    const app = await page({}, '', { page: 'home' }, initial);
    assert.deepEqual(app.$('#home-routes').children.map(item => item.children[0].dataset.exit), ['direct', 'main'], `${primary} and ${secondary} have the same owner`);
  }
});

test('home does not merge compiler-distinct numeric domain suffixes through URL IPv4 parsing', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct' };
  initial.values.dns = { magic_domains: ['01.02.03.04'] };
  initial.values.exits[0].magic_dns_suffix = '0x1.0x2.0x3.0x4';
  const app = await page({}, '', { page: 'home' }, initial);
  assert.deepEqual(app.$('#home-routes').children.map(item => item.children[0].dataset.exit), ['direct', 'main', 'office']);
});

test('only the designated browser fallback may claim its matching native Connect request', async () => {
  const fallback = await page({ desktop: true }, '', { page: 'home', hash: '#token=fixture&connect=fallback-click' }, guidedDocument());
  guidedBackend(fallback, { deferAdd: true });
  fallback.snapshot.connect_request = 'fallback-click';
  await fallback.poll();
  assert.equal(fallback.snapshot.connect_request, '');
  assert.equal(mutations(fallback).filter(r => r.data.action === 'add-exit').length, 1);
  const stale = await page({ desktop: true }, '', { page: 'home', hash: '#token=fixture&connect=old-click' }, guidedDocument());
  stale.snapshot.connect_request = 'new-native-click';
  await stale.poll();
  assert.equal(stale.requests.some(r => r.path === '/api/connect-request'), false, 'an old fallback tab cannot consume later native clicks');
});

test('a reused browser fallback receives an intent from a new fragment without reloading', async () => {
  const app = await page({ desktop: true }, '', { page: 'home' }, guidedDocument());
  guidedBackend(app, { deferAdd: true });
  app.snapshot.connect_request = 'reused-browser-click';
  app.location.hash = '#token=fixture&connect=reused-browser-click';
  await app.windowEvents.emit('hashchange');
  assert.equal(app.snapshot.connect_request, '');
  assert.equal(mutations(app).filter(r => r.data.action === 'add-exit').length, 1);
});

test('an unclaimed browser fallback request survives reload while the desktop is busy', async () => {
  const storage = new Map();
  const app = await page({ desktop: true, active: true, state: 'discovering', mode: 'discovery', connect_request: 'reload-pending' }, '',
    { page: 'home', hash: '#token=fixture&connect=reload-pending', storage }, guidedDocument());
  assert.equal(mutations(app).length, 0);
  assert.equal(JSON.parse(storage.get('tailsplit-browser-connect')).request, 'reload-pending');
  const reloaded = await page(app.snapshot, '', { page: 'home', hash: '#token=fixture', storage }, app.config);
  assert.equal(mutations(reloaded).length, 0);
  guidedBackend(reloaded, { deferAdd: true });
  Object.assign(reloaded.snapshot, { active: false, state: 'stopped', mode: '' });
  await reloaded.poll();
  assert.equal(reloaded.snapshot.connect_request, '');
  assert.equal(storage.has('tailsplit-browser-connect'), false, 'a successful claim removes the remembered marker');
  assert.equal(mutations(reloaded).filter(r => r.data.action === 'add-exit').length, 1);
});

test('browser fallback persistence is scoped to the current panel capability', async () => {
  const storage = new Map([['tailsplit-browser-connect', JSON.stringify({ token: 'another-panel', request: 'old-request' })]]);
  const app = await page({ desktop: true, connect_request: 'old-request' }, '', { page: 'home', storage }, guidedDocument());
  assert.equal(app.requests.some(r => r.path === '/api/connect-request'), false);
  assert.equal(storage.has('tailsplit-browser-connect'), false);
  assert.equal(mutations(app).length, 0);
});

test('cancellation stops the helper despite a failed or stalled claim endpoint and cannot replay', async () => {
  for (const native of ['', 'mygo-windows']) {
    for (const stalled of [false, true]) {
      const storage = new Map();
      const id = 'cancel-before-claim';
      const app = await page({ desktop: true, active: true, state: 'authorizing', mode: 'login', connect_request: id }, native,
        { page: 'home', hash: `#token=fixture&connect=${id}`, storage }, guidedDocument());
      app.override = request => request.path === '/api/connect-request' ? stalled ? deferred().promise : response({ error: 'claim failed' }, false) : undefined;
      const cancel = app.$('#connection-setup-cancel').emit('click');
      await settle();
      assert.equal(mutations(app).filter(r => r.path === '/api/cancel').length, 1, 'disconnect must start without waiting for a claim');
      await cancel;
      assert.equal(app.requests.some(r => r.path === '/api/connect-request'), false);
      assert.equal(app.snapshot.connect_request, '');
      assert.equal(storage.has('tailsplit-browser-connect'), false);
      const reloaded = await page(app.snapshot, native, { page: 'home', storage }, app.config);
      const other = await page(app.snapshot, 'mygo-windows', { page: 'home', storage: new Map() }, app.config);
      assert.equal(mutations(reloaded).length + mutations(other).length, 0, 'cancel cannot replay in this tab or another native panel');
    }
  }
});

test('an in-flight native claim cannot resume setup after cancellation', async () => {
  for (const succeeds of [false, true]) {
    const app = await page({ desktop: true }, 'mygo-windows', { page: 'home' }, guidedDocument());
    guidedBackend(app, { deferAdd: true });
    await app.$('#home-setup').emit('click');
    const backend = app.override;
    const claim = deferred();
    app.snapshot.connect_request = 'in-flight-cancel';
    app.override = request => {
      if (request.path === '/api/connect-request') return claim.promise;
      return backend(request);
    };
    const poll = app.poll();
    await settle();
    assert.equal(app.requests.filter(r => r.path === '/api/connect-request').length, 1);
    const cancel = app.$('#connection-setup-cancel').emit('click');
    await settle();
    assert.equal(mutations(app).filter(r => r.path === '/api/cancel').length, 1);
    await cancel;
    claim.resolve(succeeds ? response({ claimed: true }) : response({ error: 'late claim failure' }, false));
    await poll;
    assert.deepEqual(mutations(app).map(mutationAction), ['add-exit', 'cancel']);
    assert.equal(app.$('#connection-setup').hidden, true);
    assert.equal(app.$('#notice').hidden, true, 'a late canceled claim failure cannot replace the cancellation result');
  }
});

test('cancellation names an in-flight action before its helper starts and prevents late startup', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', { page: 'home' }, guidedDocument());
  guidedBackend(app, { deferAdd: true });
  const backend = app.override;
  const add = deferred();
  app.override = request => {
    if (request.path === '/api/action' && request.data.action === 'add-exit') {
      const result = backend(request);
      return add.promise.then(() => {
        if (!app.canceledOperations.has(request.data.operation_id)) Object.assign(app.snapshot, { active: true, state: 'authorizing', mode: 'login' });
        return result;
      });
    }
    return backend(request);
  };
  const start = app.$('#home-setup').emit('click');
  await settle();
  app.snapshot.connect_request = 'cancel-busy-action';
  assert.equal(app.$('#connection-setup-cancel').disabled, false);
  await app.$('#connection-setup-cancel').emit('click');
  assert.equal(mutations(app).filter(r => r.path === '/api/cancel').length, 1, 'Stop must dispatch before Add returns');
  const startID = mutations(app).find(r => r.data.action === 'add-exit').data.operation_id;
  assert.ok(startID);
  assert.equal(app.requests.find(r => r.path === '/api/cancel').data.operation_id, startID, 'cancellation uses the ID known before Add responds');
  assert.equal(app.storage.has('tailsplit-setup-cancel'), false, 'server acknowledgment revokes further work for the ID');
  add.resolve();
  await start;
  assert.deepEqual(mutations(app).map(mutationAction), ['add-exit', 'cancel']);
  assert.equal(app.snapshot.active, false);
  assert.equal(app.storage.has('tailsplit-setup-cancel'), false);
  assert.equal(app.$('#connection-setup').hidden, true);
});

test('cancellation dispatches during a node save and prevents its late result from connecting', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', {}, guidedDocument());
  guidedBackend(app);
  await app.$('#desktop-connect').emit('click');
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  const backend = app.override;
  const save = deferred();
  app.override = request => {
    if (request.path === '/api/config' && request.method === 'POST') return save.promise.then(() => backend(request));
    return backend(request);
  };
  const apply = app.$('#apply-exit-picker').emit('click');
  await settle();
  app.snapshot.connect_request = 'cancel-busy-save';
  assert.equal(app.$('#connection-setup-cancel').disabled, false);
  await app.$('#connection-setup-cancel').emit('click');
  assert.equal(mutations(app).filter(r => r.path === '/api/cancel').length, 1);
  const saveID = mutations(app).find(r => r.path === '/api/config').data.operation_id;
  assert.equal(app.requests.find(r => r.path === '/api/cancel').data.operation_id, saveID);
  save.resolve();
  await apply;
  assert.equal(mutations(app).filter(r => r.path === '/api/cancel').length, 1);
  assert.equal(mutations(app).some(r => r.data.action === 'connect'), false);
  assert.equal(app.storage.has('tailsplit-setup-cancel'), false);
});

test('cancellation sends despite lost state polling and retries after reconnection', async () => {
  const app = await page({ desktop: true, active: true, state: 'authorizing', mode: 'login', connect_request: 'cancel-offline' }, '',
    { page: 'home', hash: '#token=fixture&connect=cancel-offline' }, guidedDocument());
  let offline = true;
  app.override = request => {
    if (offline && request.path === '/api/state') return response({ error: 'state unavailable' }, false);
    if (request.path !== '/api/cancel') return undefined;
    assert.deepEqual(request.data, { request: 'cancel-offline', operation_id: 'existing-operation' });
    if (offline) return response({ error: 'disconnect unavailable' }, false);
    Object.assign(app.snapshot, { active: false, state: 'stopped', mode: '', connect_request: '' });
    return response({ ok: true });
  };
  await app.poll();
  assert.equal(app.$('#home-status').textContent, '页面连接中断');
  assert.equal(app.$('#home-connect').disabled, false, 'Cancel remains usable after a state failure');
  await app.$('#home-connect').emit('click');
  assert.equal(mutations(app).length, 1, 'a lost poll cannot suppress the disconnect request');
  assert.equal(JSON.parse(app.storage.get('tailsplit-browser-connect')).request, 'cancel-offline');
  assert.equal(JSON.parse(app.storage.get('tailsplit-setup-cancel')).request, 'cancel-offline');
  assert.equal(app.storage.has('tailsplit-connect-request'), false, 'failed cancellation has not been acknowledged');
  assert.equal(app.$('#connection-setup-cancel').textContent, '重试取消');
  offline = false;
  await app.poll();
  assert.equal(mutations(app).length, 2);
  assert.equal(app.snapshot.active, false);
  assert.equal(app.snapshot.connect_request, '');
  assert.equal(app.storage.has('tailsplit-setup-cancel'), false);
  assert.equal(app.storage.has('tailsplit-browser-connect'), false);
  assert.equal(app.requests.some(r => r.path === '/api/connect-request'), false);
});

test('failed cancellation survives a reload and retries Stop before considering Connect', async () => {
  for (const id of ['', 'cancel-reload']) {
    const storage = new Map([
      ['tailsplit-setup-cancel', JSON.stringify({ token: 'fixture', request: id, operation_id: 'reload-operation' })],
      ...(id ? [['tailsplit-browser-connect', JSON.stringify({ token: 'fixture', request: id })]] : []),
    ]);
    const snapshot = { desktop: true, active: false, state: 'stopped', connect_request: id };
    const failed = await page(snapshot, '', { page: 'home', storage, override: request => request.path === '/api/cancel' ? response({ error: 'still offline' }, false) : undefined }, guidedDocument());
    assert.ok(mutations(failed).length > 0);
    assert.ok(mutations(failed).every(r => r.path === '/api/cancel'), 'each retry must cancel its original target rather than disconnect arbitrarily');
    assert.equal(failed.requests.some(r => r.path === '/api/connect-request'), false);
    assert.equal(storage.has('tailsplit-setup-cancel'), true);
    const reloaded = await page(snapshot, '', { page: 'home', storage, override: request => {
      if (request.path === '/api/state') return response(snapshot);
      if (request.path === '/api/cancel') {
        assert.deepEqual(request.data, { request: id, operation_id: 'reload-operation' });
        snapshot.connect_request = '';
        return response({ ok: true });
      }
    } }, guidedDocument());
    assert.deepEqual(mutations(reloaded).map(mutationAction), ['cancel']);
    assert.equal(reloaded.requests.some(r => r.path === '/api/connect-request'), false);
    assert.equal(storage.has('tailsplit-setup-cancel'), false);
    assert.equal(storage.has('tailsplit-browser-connect'), false);
    assert.equal(reloaded.$('#connection-setup').hidden, true);
    const other = await page(snapshot, 'mygo-windows', { storage: new Map() }, reloaded.config);
    assert.equal(mutations(other).length, 0, 'a fresh panel cannot replay the canceled intent');
  }
});

test('late claim results cannot clear cancellation markers before Stop acknowledges', async () => {
  for (const claimed of [false, true]) {
    const app = await page({ desktop: true }, 'mygo-windows', { page: 'home' }, guidedDocument());
    guidedBackend(app, { deferAdd: true });
    await app.$('#home-setup').emit('click');
    const backend = app.override;
    const claim = deferred(), stop = deferred();
    app.snapshot.connect_request = 'cancel-before-ack';
    app.override = request => {
      if (request.path === '/api/connect-request') return claim.promise;
      if (request.path === '/api/cancel') return stop.promise.then(() => {
        Object.assign(app.snapshot, { connect_request: '', active: false, state: 'stopped', mode: '' });
        app.canceledOperations.add(request.data.operation_id);
        return response({ ok: true });
      });
      return backend(request);
    };
    const poll = app.poll();
    await settle();
    const cancel = app.$('#connection-setup-cancel').emit('click');
    await settle();
    claim.resolve(response({ claimed }));
    await poll;
    assert.equal(JSON.parse(app.storage.get('tailsplit-setup-cancel')).request, 'cancel-before-ack');
    assert.equal(app.storage.has('tailsplit-connect-request'), false, 'a late claim cannot acknowledge an unfinished cancellation');
    await app.$('#connection-setup-cancel').emit('click');
    assert.equal(mutations(app).filter(r => r.path === '/api/cancel').length, 1, 'only one Stop may be in flight');
    stop.resolve();
    await cancel;
    assert.equal(app.storage.has('tailsplit-setup-cancel'), false);
    assert.deepEqual(mutations(app).map(mutationAction), ['add-exit', 'cancel']);
  }
});

test('restored cancellations are capability-scoped and preserve a newer explicit Connect request', async () => {
  const foreignStorage = new Map([['tailsplit-setup-cancel', JSON.stringify({ token: 'other-panel', request: 'old' })]]);
  const ordinary = await page({ desktop: true, connect_request: 'old' }, '', { page: 'home', storage: foreignStorage }, guidedDocument());
  assert.equal(mutations(ordinary).length, 0);
  assert.equal(foreignStorage.has('tailsplit-setup-cancel'), false);
  const storage = new Map([
    ['tailsplit-setup-cancel', JSON.stringify({ token: 'fixture', request: 'old-canceled-click' })],
    ['tailsplit-browser-connect', JSON.stringify({ token: 'fixture', request: 'new-click' })],
  ]);
  const snapshot = { desktop: true, active: false, state: 'stopped', connect_request: 'new-click' };
  const app = await page(snapshot, '', { page: 'home', storage, override: request => {
    if (request.path === '/api/state') return response(snapshot);
    if (request.path === '/api/cancel') {
      assert.equal(request.data.request, 'old-canceled-click');
      assert.equal(snapshot.connect_request, 'new-click');
      return response({ ok: true });
    }
    if (request.path === '/api/connect-request') {
      assert.equal(request.data.request, 'new-click');
      snapshot.connect_request = '';
      return response({ claimed: true });
    }
  } }, guidedDocument());
  assert.deepEqual(mutations(app).map(mutationAction), ['cancel', 'add-exit']);
  assert.equal(storage.has('tailsplit-setup-cancel'), false);
});

test('a fresh fallback cannot claim a cancellation left by a closed original tab', async () => {
  const localStorage = new Map();
  const id = 'cross-tab-canceled';
  const snapshot = { desktop: true, active: true, state: 'authorizing', mode: 'login', operation_id: 'original-op', connect_request: id };
  const original = await page(snapshot, '', { page: 'home', hash: `#token=fixture&connect=${id}`, localStorage }, guidedDocument());
  original.override = request => request.path === '/api/cancel' ? response({ error: 'not delivered' }, false) : undefined;
  await original.$('#connection-setup-cancel').emit('click');
  assert.equal(original.snapshot.connect_request, id, 'the failed request never reached the controller');
  assert.equal(JSON.parse(localStorage.get(`tailsplit-setup-cancel:request:${id}`)).acknowledged, undefined);
  // Drop all original session state: only origin-wide storage survives closure.
  const fresh = await page({ ...snapshot, active: false, state: 'stopped', mode: '' }, '', {
    page: 'home', hash: `#token=fixture&connect=${id}`, localStorage, storage: new Map(),
    override: request => request.path === '/api/cancel' ? response({ error: 'still unavailable' }, false) : undefined,
  }, guidedDocument());
  assert.ok(mutations(fresh).length > 0);
  assert.ok(mutations(fresh).every(request => request.path === '/api/cancel'));
  assert.equal(fresh.requests.some(request => request.path === '/api/connect-request'), false);
  assert.equal(JSON.parse(fresh.storage.get('tailsplit-setup-cancel')).operation_id, 'original-op');
});

test('a delayed claim in another panel stays canceled even after the original receives acknowledgment', async () => {
  const localStorage = new Map();
  const id = 'cross-tab-late-claim';
  const snapshot = { desktop: true, active: true, state: 'authorizing', mode: 'login', operation_id: 'cross-tab-op', connect_request: id };
  const original = await page(snapshot, '', { page: 'home', hash: `#token=fixture&connect=${id}`, localStorage }, guidedDocument());
  const claimant = await page(snapshot, 'mygo-windows', { page: 'home', localStorage }, guidedDocument());
  const claim = deferred();
  claimant.override = request => request.path === '/api/connect-request' ? claim.promise : undefined;
  Object.assign(claimant.snapshot, { active: false, state: 'stopped', mode: '' });
  const polling = claimant.poll();
  await settle();
  assert.equal(claimant.requests.filter(request => request.path === '/api/connect-request').length, 1);
  await original.$('#connection-setup-cancel').emit('click');
  assert.equal(JSON.parse(localStorage.get(`tailsplit-setup-cancel:request:${id}`)).acknowledged, true, 'retain a tombstone after successful cancellation');
  claim.resolve(response({ claimed: true }));
  await polling;
  assert.equal(mutations(claimant).length, 0, 'a late successful claim must never restart setup');
  assert.equal(claimant.$('#connection-setup').hidden, true);
});

test('restoring an old cancellation never changes a newer running session', async () => {
  const storage = new Map([['tailsplit-setup-cancel', JSON.stringify({ token: 'fixture', request: 'old-request', operation_id: 'old-operation' })]]);
  const app = await page({ desktop: true, active: true, state: 'running', mode: 'tun', operation_id: 'new-operation', connect_request: 'new-request', backend_state: 'Running' }, '', { page: 'home', storage }, guidedDocument());
  const cancel = mutations(app);
  assert.equal(cancel.length, 1);
  assert.equal(cancel[0].path, '/api/cancel');
  assert.deepEqual(cancel[0].data, { request: 'old-request', operation_id: 'old-operation' });
  assert.equal(app.snapshot.active, true);
  assert.equal(app.snapshot.operation_id, 'new-operation');
  assert.equal(app.snapshot.connect_request, 'new-request');
  assert.equal(app.$('#home-status').textContent, '已连接');
  assert.equal(storage.has('tailsplit-setup-cancel'), false);
});

test('a cancellation from another capability cannot suppress the matching native request', async () => {
  const id = 'same-public-id';
  const localStorage = new Map([[`tailsplit-setup-cancel:request:${id}`, JSON.stringify({ token: 'another-capability', request: id, operation_id: id })]]);
  const app = await page({ desktop: true, connect_request: id }, 'mygo-windows', { page: 'home', localStorage }, guidedDocument());
  assert.equal(app.requests.filter(request => request.path === '/api/connect-request').length, 1);
  assert.equal(app.requests.some(request => request.path === '/api/cancel'), false);
  assert.equal(mutations(app).filter(request => request.data.action === 'add-exit').length, 1);
});

test('guided native setup retains one operation ID through login, save and connect', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', {}, guidedDocument());
  guidedBackend(app);
  app.snapshot.connect_request = 'native-flow-identity';
  await app.poll();
  await pickerNode(app, '100.101.0.11').choice.emit('click');
  await app.$('#apply-exit-picker').emit('click');
  const requests = mutations(app);
  assert.deepEqual(requests.map(request => request.data.action || 'save'), ['add-exit', 'save', 'connect']);
  assert.ok(requests.every(request => request.data.operation_id === 'native-flow-identity'));
});

test('another panel resumes operation-only cancellation after the native request was already claimed', async () => {
  const localStorage = new Map([['tailsplit-setup-cancel:operation:claimed-operation', JSON.stringify({ token: 'fixture', request: '', operation_id: 'claimed-operation' })]]);
  const app = await page({ desktop: true, active: true, state: 'authorizing', mode: 'login', operation_id: 'claimed-operation', connect_request: '' }, '', { page: 'home', localStorage }, guidedDocument());
  assert.deepEqual(mutations(app).map(mutationAction), ['cancel']);
  assert.deepEqual(mutations(app)[0].data, { request: '', operation_id: 'claimed-operation' });
  assert.equal(app.snapshot.active, false);
  assert.equal(localStorage.has('tailsplit-setup-cancel:operation:claimed-operation'), false);
});

test('acknowledged native tombstones suppress a fresh stale fallback without another stop', async () => {
  const id = 'acknowledged-request';
  const localStorage = new Map([[`tailsplit-setup-cancel:request:${id}`, JSON.stringify({ token: 'fixture', request: id, operation_id: 'old-operation', acknowledged: true })]]);
  const app = await page({ desktop: true, connect_request: id }, '', { page: 'home', hash: `#token=fixture&connect=${id}`, localStorage }, guidedDocument());
  assert.equal(app.requests.some(request => request.path === '/api/connect-request'), false);
  assert.equal(mutations(app).length, 0);
  assert.equal(app.storage.has('tailsplit-browser-connect'), false);
});

test('an unreadable shared cancellation record cannot silently grant native setup', async () => {
  const id = 'unreadable-cancellation';
  const localStorage = new Map([[`tailsplit-setup-cancel:request:${id}`, '{broken']]);
  const app = await page({ desktop: true, connect_request: id }, 'mygo-windows', { page: 'home', localStorage }, guidedDocument());
  assert.equal(app.requests.some(request => request.path === '/api/connect-request'), false);
  assert.equal(mutations(app).length, 0);
  localStorage.delete(`tailsplit-setup-cancel:request:${id}`);
  await app.poll();
  assert.equal(app.requests.filter(request => request.path === '/api/connect-request').length, 1);
  assert.equal(mutations(app).filter(request => request.data.action === 'add-exit').length, 1);
});

test('adopting a shared cancellation clears the matching local setup intent', async () => {
  const localStorage = new Map();
  const app = await page({ desktop: true }, '', { page: 'home', localStorage }, guidedDocument());
  const backend = guidedBackend(app, { deferAdd: true });
  await app.$('#home-setup').emit('click');
  const operationID = mutations(app)[0].data.operation_id;
  localStorage.set(`tailsplit-setup-cancel:operation:${operationID}`, JSON.stringify({ token: 'fixture', request: '', operation_id: operationID }));
  await app.poll();
  assert.deepEqual(mutations(app).map(mutationAction), ['add-exit', 'cancel']);
  assert.equal(app.$('#connection-setup').hidden, true);
  backend.finishAdd();
  await app.poll();
  assert.equal(app.$('#exit-picker-dialog').open || false, false);
  assert.equal(mutations(app).some(request => request.data.action === 'connect'), false);
});

test('active private ownership is pinned to compilation instead of later SDK suffix changes', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct' };
  initial.values.dns = { magic_domains: ['primary.ts.net'] };
  initial.values.exits[0].tailnet = '';
  const snapshot = { ...officeSnapshot({ magic_dns_suffix: 'old-office.ts.net' }), active: true, mode: 'proxy', state: 'running',
    traffic: { session_id: 'frozen-policy', sampled_at: new Date().toISOString(), routes: {}, private_exits: ['main', 'office'] } };
  const app = await page(snapshot, '', { page: 'home' }, initial);
  const exits = () => app.$('#home-routes').children.map(item => item.children[0].dataset.exit);
  assert.deepEqual(exits(), ['direct', 'main', 'office']);
  app.snapshot.exits.office.magic_dns_suffix = 'primary.ts.net';
  await app.poll();
  assert.deepEqual(exits(), ['direct', 'main', 'office'], 'a new live suffix cannot hide an existing compiled owner');
  app.snapshot.traffic.private_exits = ['main'];
  app.snapshot.exits.office.magic_dns_suffix = 'new-office.ts.net';
  await app.poll();
  assert.deepEqual(exits(), ['direct', 'main'], 'a live-only suffix cannot invent an owner absent from the compiled policy');
});

test('pending primary machine approval explains the administrator action and does not relaunch login', async () => {
  const initial = { ...doc(), values: { ...doc().values, tailscale: { exit_node: '100.64.0.10' } } };
  const app = await page({ desktop: true, state: 'authenticating', active: true, mode: 'login', backend_state: 'NeedsMachineAuth' }, '', {}, initial);
  assert.equal(app.$('#connection-status span').textContent, '等待管理员批准设备');
  assert.match(app.$('#desktop-hint').textContent, /管理员.*批准此设备/);
  assert.ok(app.$('#desktop-login').disabled);
  await app.$('#desktop-login').emit('click');
  assert.equal(mutations(app).length, 0);
  await app.$('#choose-exit').emit('click');
  assert.equal(app.$('#exit-empty-title').textContent, '等待管理员批准设备');
  assert.match(app.$('#exit-empty-detail').textContent, /管理员.*批准此设备/);
});

test('focused additional device approval does not appear as signed-in primary or generic login', async () => {
  const initial = multiExitDocument('100.101.0.11');
  const snapshot = { desktop: true, state: 'authenticating', active: true, mode: 'login', focus: 'office', backend_state: 'Running', exits: { office: { backend_state: 'NeedsMachineAuth', exit_nodes: [] } } };
  const app = await page(snapshot, '', { language: 'en', preference: 'en' }, initial);
  assert.equal(app.$('#connection-status span').textContent, 'Awaiting device approval');
  assert.match(app.$('#desktop-hint').textContent, /administrator.*approve this device/);
  app.snapshot = { ...snapshot, state: 'authenticated', active: false, focus: '', exits: { office: { backend_state: 'Running', exit_nodes: [] } } };
  await app.poll();
  assert.doesNotMatch(app.$('#desktop-hint').textContent, /administrator/);
});

test('automatic repair is offered only for a server-validated repair and cancel leaves the source untouched', async () => {
  for (const repairable of [false, true]) {
    const app = await page({ desktop: true }, '', {}, { ...doc(), valid: false, repairable, error: 'obsolete dns.listen' });
    assert.equal(app.$('#notice-repair').hidden, !repairable);
    if (repairable) {
      const pending = app.$('#notice-repair').emit('click');
      assert.ok(app.$('#repair-config-dialog').open);
      assert.ok(app.$('#notice-rebuild').disabled);
      app.$('#repair-config-dialog').returnValue = 'cancel'; app.$('#repair-config-dialog').close();
      await pending;
      assert.equal(mutations(app).length, 0);
      assert.equal(app.$('#notice-repair').disabled, false);
    }
  }
});

test('automatic repair uses the saved revision and retains exits instead of rebuilding', async () => {
  const original = multiExitDocument('100.101.0.11');
  const app = await page(officeSnapshot(), '', {}, { ...original, valid: false, repairable: true, error: 'obsolete dns.listen' });
  app.override = request => {
    if (request.path === '/api/config-repair') {
      assert.equal(request.data.revision, 'original');
      app.config = { ...original, revision: 'repaired', backup_path: '/config/source.backup' };
      app.snapshot.revision = 'repaired';
      return response(app.config);
    }
  };
  const pending = app.$('#notice-repair').emit('click');
  app.$('#repair-config-dialog').returnValue = 'repair'; app.$('#repair-config-dialog').close();
  await pending;
  assert.deepEqual(mutations(app).map(request => request.path), ['/api/config-repair']);
  assert.equal(app.$('#notice-title').textContent, '配置已自动修复');
  assert.match(app.$('#notice-detail').textContent, /出口和分流规则已保留/);
  assert.match(app.$('#notice-detail').textContent, /source.backup/);
  assert.ok(app.$('#notice-repair').hidden);
  assert.equal(app.$('#raw-yaml').value, original.yaml);
});

test('automatic repair conflicts keep the source and require reloading', async () => {
  const original = { ...doc(), valid: false, repairable: true, error: 'obsolete dns.listen' };
  const app = await page({ desktop: true }, '', {}, original);
  app.override = request => request.path === '/api/config-repair' ? response({ code: 'conflict', error: 'changed on disk' }, false) : undefined;
  const pending = app.$('#notice-repair').emit('click');
  app.$('#repair-config-dialog').returnValue = 'repair'; app.$('#repair-config-dialog').close();
  await pending;
  assert.equal(app.$('#raw-yaml').value, original.yaml);
  assert.ok(app.$('#notice-repair').disabled);
  assert.ok(!app.$('#notice-reload').hidden);
});

test('configuration recovery only appears for invalid saved files and cancel or Escape changes nothing', async () => {
  const healthy = await page({ desktop: true });
  assert.ok(healthy.$('#notice-rebuild').hidden);
  const invalid = { ...doc(), valid: false, error: 'unknown field listen' };
  for (const answer of ['cancel', '']) {
    const app = await page({ desktop: true }, '', {}, invalid);
    assert.ok(!app.$('#notice-rebuild').hidden);
    assert.equal(app.$('#notice-rebuild').disabled, false);
    const pending = app.$('#notice-rebuild').emit('click');
    assert.ok(app.$('#rebuild-config-dialog').open);
    app.$('#rebuild-config-dialog').returnValue = answer;
    app.$('#rebuild-config-dialog').close();
    await pending;
    assert.equal(mutations(app).length, 0);
    assert.equal(app.$('#notice-rebuild').disabled, false);
  }
});

test('rebuilding invalid configuration backs up through the API and returns to empty exits', async () => {
  const invalid = { ...doc(), valid: false, parsed: false, error: 'broken YAML' };
  const app = await page({ desktop: true }, '', {}, invalid);
  const rebuilt = { ...doc('tailscale: null\n', 'rebuilt'), values: { tailscale: null, exits: [] }, backup_path: '/config/config.yaml.backup-1' };
  app.override = request => {
    if (request.path === '/api/config-rebuild') {
      assert.equal(request.data.revision, 'original');
      app.config = rebuilt; app.snapshot.revision = 'rebuilt';
      return response(rebuilt);
    }
  };
  const pending = app.$('#notice-rebuild').emit('click');
  app.$('#rebuild-config-dialog').returnValue = 'rebuild';
  app.$('#rebuild-config-dialog').close();
  await pending;
  assert.deepEqual(mutations(app).map(request => request.path), ['/api/config-rebuild']);
  assert.ok(app.$('#notice-rebuild').hidden);
  assert.ok(!app.$('#page-connection').hidden);
  assert.equal(app.$('#notice-title').textContent, '配置已重建');
  assert.match(app.$('#notice-detail').textContent, /config.yaml.backup-1/);
  app.$('#ui-language').value = 'en';
  await app.$('#ui-language').emit('change');
  assert.equal(app.$('#notice-title').textContent, 'Configuration rebuilt');
  assert.match(app.$('#notice-detail').textContent, /Original configuration backed up to/);
});

test('rebuild failures retain source and retry while conflicts require reloading', async () => {
  for (const code of ['rebuild', 'conflict']) {
    const invalid = { ...doc(), valid: false, error: 'unknown field' };
    const app = await page({ desktop: true }, '', {}, invalid);
    app.override = request => request.path === '/api/config-rebuild' ? response({ code, error: 'backup failed' }, false) : undefined;
    const pending = app.$('#notice-rebuild').emit('click');
    app.$('#rebuild-config-dialog').returnValue = 'rebuild'; app.$('#rebuild-config-dialog').close();
    await pending;
    assert.equal(app.$('#raw-yaml').value, invalid.yaml);
    assert.match(app.$('#notice-detail').textContent, /backup failed/);
    assert.equal(app.$('#notice-rebuild').disabled, code === 'conflict');
    assert.equal(app.$('#notice-reload').hidden, code !== 'conflict');
  }
});

test('rebuild explicitly disconnects active Global and never reconnects', async () => {
  const invalid = { ...doc(), valid: false, error: 'unknown field' };
  const app = await page({ desktop: true, state: 'running', active: true, mode: 'tun', routing_mode: 'global' }, '', {}, invalid);
  app.override = request => {
    if (request.path === '/api/action') { app.snapshot = { ...app.snapshot, active: false, state: 'stopped', mode: '' }; return response({ ok: true }); }
    if (request.path === '/api/config-rebuild') return response({ code: 'rebuild', error: 'disk full' }, false);
  };
  const pending = app.$('#notice-rebuild').emit('click');
  app.$('#rebuild-config-dialog').returnValue = 'rebuild'; app.$('#rebuild-config-dialog').close();
  await pending;
  assert.deepEqual(mutations(app).map(request => request.data.action || request.path), ['disconnect', '/api/config-rebuild']);
  assert.equal(app.snapshot.active, false);
  assert.match(app.$('#notice-detail').textContent, /disk full/);
});

test('a changed file during rebuild confirmation is never overwritten', async () => {
  const invalid = { ...doc(), valid: false, error: 'unknown field' };
  const app = await page({ desktop: true }, '', {}, invalid);
  const pending = app.$('#notice-rebuild').emit('click');
  app.config = doc('# external fix\n', 'updated'); app.snapshot.revision = 'updated';
  await app.poll();
  app.$('#rebuild-config-dialog').returnValue = 'rebuild'; app.$('#rebuild-config-dialog').close();
  await pending;
  assert.equal(mutations(app).length, 0);
});

test('home fans out saved destinations and only marks confirmed routes as in effect', async () => {
  const documentWith = (rules) => { const value = multiExitDocument('100.101.0.11'); value.values.rules = rules; return value; };
  const split = { mode: 'split', default_exit: 'main', global_exit: 'office', final: 'direct', source: 'builtin',
    routes: [{ exit: 'office', domains: ['work.example'] }, { exit: 'office', ips: ['10.0.0.0/8'] }] };
  const rows = (app) => app.$('#home-routes').children.map(item => item.children[0]);
  const summary = (app) => rows(app).map(row => [row.dataset.route, row.children[0].textContent, row.children[1].textContent]);

  const idle = await page(officeSnapshot(), '', { page: 'home' }, documentWith(split));
  assert.deepEqual(summary(idle), [['direct', '直连', '直连规则 · 未匹配流量'], ['exit', '主出口', '默认代理出口'], ['exit', 'Office', '私有域名 · 2 个规则组']]);
  assert.ok(rows(idle).every(row => row.className.endsWith('tone-planned')), 'saved routes stay planned while disconnected');
  assert.equal(idle.$('#home-wires').children.length, 3);
  assert.equal(idle.$('#home-wires-live').children.length, 0, 'no confirmed wire before a running session');
  assert.equal(idle.$('#split-map').dataset.connected, 'false');

  const running = { ...officeSnapshot(), active: true, mode: 'tun', state: 'running', routing_mode: 'split' };
  const live = await page(running, '', { page: 'home' }, documentWith(split));
  assert.equal(live.$('#split-map').dataset.connected, 'true');
  assert.ok(rows(live).every(row => row.className.endsWith('tone-ok')));
  assert.equal(live.$('#home-wires-live').children.length, 3);

  const global = await page(officeSnapshot(), '', { page: 'home' }, documentWith({ ...split, mode: 'global' }));
  assert.deepEqual(summary(global), [['exit', 'Office', '全部接管流量']], 'Global suppresses Direct');
});

test('home derives per-route rates from real byte deltas and retains destination controls', async () => {
  const sample = (seconds, routes, session = 'engine-1') => ({ session_id: session, sampled_at: new Date(1700000000000 + seconds * 1000).toISOString(), routes });
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', default_exit: 'main', source: 'builtin', final: 'direct', routes: [{ exit: 'office', domains: ['work.example'] }] };
  const app = await page({ ...officeSnapshot(), active: true, mode: 'tun', state: 'running', routing_mode: 'split',
    traffic: sample(0, { main: { upload_bytes: 100, download_bytes: 200 }, office: { upload_bytes: 10, download_bytes: 20 } }) }, '', { page: 'home' }, initial);
  const rows = () => app.$('#home-routes').children.map(item => item.children[0]);
  const rates = id => {
    const row = rows().find(row => row.dataset.exit === id);
    return row.children[3].children.map(reading => reading.children[1].textContent);
  };
  assert.deepEqual(rates('main'), ['—', '—'], 'one sample cannot establish speed');
  const original = rows()[1];
  app.snapshot.traffic = sample(4, { main: { upload_bytes: 8292, download_bytes: 12583112 }, office: { upload_bytes: 4106, download_bytes: 8212 } });
  await app.poll();
  assert.deepEqual(rates('main'), ['2.0 KB/s', '3.0 MB/s']);
  assert.deepEqual(rates('office'), ['1.0 KB/s', '2.0 KB/s']);
  assert.deepEqual(rates('direct'), ['—', '—'], 'an absent counter is not zero traffic');
  assert.equal(rows()[1], original, 'sample updates preserve focused controls');
  await app.poll();
  assert.deepEqual(rates('main'), ['2.0 KB/s', '3.0 MB/s'], 'duplicate samples never create zero rates');
  app.snapshot.traffic = sample(8, { main: { upload_bytes: 8292, download_bytes: 12583112 } });
  await app.poll();
  assert.deepEqual(rates('main'), ['0 B/s', '0 B/s'], 'zero is valid only after equal consecutive counters');
  app.snapshot.traffic = sample(10, { main: { upload_bytes: 8293, download_bytes: 12583113 } });
  await app.poll();
  assert.deepEqual(rates('main'), ['1 B/s', '1 B/s'], 'sub-byte-per-second values still have a defined unit');
  app.snapshot.traffic = sample(12, { main: { upload_bytes: 1, download_bytes: 2 } }, 'engine-2');
  await app.poll();
  assert.deepEqual(rates('main'), ['—', '—'], 'new sessions reset the baseline');
  app.snapshot.traffic = sample(16, { main: { upload_bytes: 4097, download_bytes: 8194 } }, 'engine-2');
  await app.poll();
  assert.deepEqual(rates('main'), ['1.0 KB/s', '2.0 KB/s']);
  app.snapshot.traffic = sample(40, { main: { upload_bytes: 12000, download_bytes: 16000 } }, 'engine-2');
  await app.poll();
  assert.deepEqual(rates('main'), ['—', '—'], 'stale samples do not produce misleading averages');
  app.snapshot.traffic = sample(44, { main: { upload_bytes: 0, download_bytes: 0 } }, 'engine-2');
  await app.poll();
  assert.deepEqual(rates('main'), ['—', '—'], 'counter resets are not negative rates');
  Object.assign(app.snapshot, { active: false, state: 'stopped' });
  await app.poll();
  assert.deepEqual(rates('main'), ['—', '—'], 'disconnect clears readings');
});

test('route flow follows only positive per-route byte deltas and stops without fresh data', async () => {
  const sample = (seconds, routes) => ({ session_id: 'flow-engine', sampled_at: new Date(1700000000000 + seconds * 1000).toISOString(), routes });
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', final: 'direct', default_exit: 'main', routes: [{ exit: 'office', domains: ['work.example'] }] };
  const zero = { upload_bytes: 0, download_bytes: 0 };
  const app = await page({ ...officeSnapshot(), active: true, mode: 'tun', state: 'running', routing_mode: 'split',
    traffic: sample(0, { direct: zero, main: zero, office: zero }) }, '', { page: 'home' }, initial);
  const active = () => app.$('#home-wires-flow').children.filter(path => path.dataset.active === 'true')
    .map(path => `${path.dataset.exit}:${path.dataset.direction}`);
  assert.deepEqual(active(), [], 'one sample must not animate');
  const moving = { direct: { upload_bytes: 1024, download_bytes: 0 }, main: { upload_bytes: 0, download_bytes: 2048 }, office: zero };
  app.snapshot.traffic = sample(4, moving);
  await app.poll();
  assert.deepEqual(active(), ['direct:up', 'main:down'], 'idle Office stays still and upload/download follow their own routes');
  await app.$('[data-page="settings"]').emit('click');
  assert.equal(app.$('#split-map').dataset.flowPaused, 'true', 'leaving Home pauses its motion');
  await app.$('[data-page="home"]').emit('click');
  assert.equal(app.$('#split-map').dataset.flowPaused, 'false');
  app.snapshot.traffic = sample(8, moving);
  await app.poll();
  assert.deepEqual(active(), [], 'an idle interval stops motion');
  app.snapshot.traffic = sample(12, { ...moving, office: { upload_bytes: 4096, download_bytes: 4096 } });
  await app.poll();
  assert.deepEqual(active(), ['office:up', 'office:down']);
  await app.flushTimers();
  assert.deepEqual(active(), [], 'a stalled poll expires flow instead of leaving it running');
  app.snapshot.traffic = sample(16, { direct: { upload_bytes: 2048, download_bytes: 0 }, main: zero, office: zero });
  await app.poll();
  Object.assign(app.snapshot, { active: false, state: 'stopped' });
  await app.poll();
  assert.deepEqual(active(), [], 'disconnect removes the active flow overlay');
  const html = readFileSync(join(__dirname, '../assets/index.html'), 'utf8');
  assert.doesNotMatch(html, /id="(?:home-final-route|home-policy-note|home-preview)"/);
});

test('native connection intent is globally consumed and cannot replay in another page', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', { page: 'home' }, guidedDocument());
  guidedBackend(app, { deferAdd: true });
  app.snapshot.connect_request = 'claimed-once';
  await app.windowEvents.emit('tailsplit-window-shown');
  assert.equal(app.snapshot.connect_request, '', 'the controller acknowledgment clears the intent');
  assert.equal(app.requests.filter(r => r.path === '/api/connect-request').length, 1);
  await app.$('#connection-setup-cancel').emit('click');
  const fresh = await page(app.snapshot, 'mygo-windows', { page: 'home', storage: new Map() }, app.config);
  assert.equal(mutations(fresh).length, 0, 'a fresh WebView must not connect after cancellation');
  fresh.override = request => request.path === '/api/connect-request' ? response({ claimed: false }) : undefined;
  fresh.snapshot.connect_request = 'claimed-once';
  await fresh.windowEvents.emit('tailsplit-window-shown');
  assert.equal(mutations(fresh).length, 0, 'a delayed stale state cannot replay a globally claimed token');
});

test('home compares saved routes with the running revision after an external edit', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct' };
  const app = await page({ ...officeSnapshot(), active: true, mode: 'proxy', state: 'running', active_revision: 'original' }, '', { page: 'home' }, initial);
  assert.equal(app.$('#split-map').dataset.connected, 'true');
  app.config = { ...initial, revision: 'external', yaml: initial.yaml + '# external change\n' };
  app.snapshot.revision = 'external';
  await app.poll();
  assert.equal(app.$('#split-map').dataset.connected, 'false');
  assert.equal(app.$('#home-description').textContent, '已保存的配置尚未应用；当前连接仍使用之前的配置。');
  assert.equal(app.$('#home-wires-live').children.length, 0);
  app.snapshot.active_revision = 'external';
  await app.poll();
  assert.equal(app.$('#split-map').dataset.connected, 'true', 'the diagram becomes active only after the engine uses the new revision');
});

test('home marks an offline or missing secondary node unavailable while the default stays connected', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct', routes: [{ exit: 'office', domains: ['work.example'] }] };
  const snapshot = { ...officeSnapshot(), active: true, mode: 'tun', state: 'running' };
  snapshot.exits.office.exit_nodes[0].online = false;
  const app = await page(snapshot, '', { page: 'home' }, initial);
  const office = () => app.$('#home-routes').children.map(item => item.children[0]).find(row => row.dataset.exit === 'office');
  assert.equal(app.$('#home-status').textContent, '已连接');
  assert.match(office().className, /tone-warn/);
  assert.equal(office().children[1].textContent, '出口不可用');
  assert.equal(app.$('#home-wires-flow').children.some(path => path.dataset.exit === 'office'), false);
  app.snapshot.exits.office.exit_nodes = [];
  await app.poll();
  assert.match(office().className, /tone-warn/, 'a missing selected node is not a working route either');
});

test('home includes implicit private-domain exits with compiler ownership precedence', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', global_exit: 'lab', final: 'direct' };
  initial.values.dns = { magic_domains: ['shared.ts.net'] };
  initial.values.exits.push({ id: 'lab', name: 'Lab', exit_node: '100.102.0.10', magic_dns_suffix: 'lab.ts.net' },
    { id: 'shadow', name: 'Shadow', exit_node: '100.103.0.10', magic_dns_suffix: 'office.ts.net' });
  const app = await page({}, '', { page: 'home' }, initial);
  const rows = app.$('#home-routes').children.map(item => item.children[0]);
  assert.deepEqual(rows.map(row => row.dataset.exit), ['direct', 'main', 'office', 'lab']);
  assert.equal(rows.find(row => row.dataset.exit === 'office').children[1].textContent, '私有域名');
  initial.values.rules.mode = 'global';
  const global = await page({}, '', { page: 'home' }, initial);
  assert.deepEqual(global.$('#home-routes').children.map(item => item.children[0].dataset.exit), ['lab']);
});

test('guided setup can confirm the same configured node after it recovers without rewriting configuration', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', default_exit: 'main', final: 'direct' };
  const snapshot = officeSnapshot(); snapshot.exit_nodes[0].online = false;
  const app = await page(snapshot, '', {}, initial);
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    assert.deepEqual(actionFields(request), { action: 'connect', revision: 'original' });
    Object.assign(app.snapshot, { active: true, mode: 'proxy', state: 'running', active_revision: 'original' });
    return response({ ok: true });
  };
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  assert.equal(app.$('#apply-exit-picker').disabled, true);
  app.snapshot.exit_nodes[0].online = true;
  await app.poll();
  await pickerNode(app, '100.64.0.10').choice.emit('click');
  assert.equal(app.$('#apply-exit-picker').disabled, false);
  assert.equal(app.$('#apply-exit-picker').textContent, '保存并连接');
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(app.$('#exit-picker-dialog').open, false);
  assert.deepEqual(mutations(app).map(r => r.data.action || 'save'), ['connect']);
});

test('Windows missing-service setup directs users to Setup without promising an in-app installer', async () => {
  const app = await page({ desktop: true }, 'mygo-windows', { page: 'home' }, guidedDocument());
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    Object.assign(app.snapshot, { state: 'failed', active: false, error: 'Run TailSplit Setup', error_code: 'service-setup-required' });
    return response({ error: 'Run TailSplit Setup' }, false);
  };
  await app.$('#home-setup').emit('click');
  assert.match(app.$('#connection-setup-detail').textContent, /TailSplit 安装程序.*Windows/);
  assert.equal(app.$('#connection-setup-retry').textContent, '重试连接');
});

test('home retains each missing explicit route when no exits remain', async () => {
  const initial = guidedDocument();
  initial.values.rules = { mode: 'split', final: 'direct', source: 'builtin',
    routes: [{ exit: 'office', domains: ['work.example'] }, { exit: 'media', domains: ['media.example'] }] };
  const app = await page({ desktop: true }, '', { page: 'home' }, initial);
  const rows = app.$('#home-routes').children.map(item => item.children[0]);
  assert.deepEqual(rows.map(row => row.dataset.exit), ['direct', 'main', 'office', 'media']);
  assert.equal(rows[2].children[0].textContent, '缺少出口 · office');
  assert.equal(rows[3].children[0].textContent, '缺少出口 · media');
});

test('home Setup starts guided setup and direct destination opens routing rules', async () => {
  const app = await page({ desktop: true }, '', { page: 'home' }, guidedDocument());
  assert.equal(app.$('#page-home').hidden, false);
  assert.equal(app.$('#home-setup').hidden, false);
  assert.equal(app.$('#home-setup').disabled, false);
  assert.equal(app.$('#home-toggle-control').hidden, true);
  guidedBackend(app);
  await app.$('#home-setup').emit('click');
  assert.ok(app.requests.some(request => request.path === '/api/action' && request.data.action === 'add-exit'));
  assert.equal(app.$('#exit-picker-dialog').open, true);
  await app.$('#cancel-exit-picker').emit('click');
  const direct = app.$('#home-routes').children[0].children[0];
  await app.$('#home-routes').emit('click', { target: direct });
  assert.equal(app.$('#page-settings').hidden, false);
  assert.equal(app.$('#section-routing').hidden, false);
});

test('home Setup completes first connection in both languages and yields to the switch', async () => {
  for (const language of ['zh', 'en']) {
    const app = await page({ desktop: true }, '', { page: 'home', language }, guidedDocument());
    const backend = guidedBackend(app);
    assert.equal(app.$('#home-setup').textContent, language === 'en' ? 'Set up' : '开始设置');
    await app.$('#home-setup').emit('click');
    assert.equal(app.$('#home-setup').hidden, true);
    await pickerNode(app, '100.101.0.11').choice.emit('click');
    await app.$('#apply-exit-picker').emit('click');
    await app.poll();
    assert.deepEqual(mutations(app).map(r => r.data.action || 'save'), ['add-exit', 'save', 'connect']);
    assert.equal(app.config.values.rules.default_exit, backend.lastAdded);
    await app.$('[data-page="home"]').emit('click');
    assert.equal(app.$('#home-setup').hidden, true);
    assert.equal(app.$('#home-toggle-control').hidden, false);
    assert.equal(app.$('#home-connect').attributes.get('aria-checked'), 'true');
  }
});

test('home Setup guards duplicate requests and returns after cancellation or installation failure', async () => {
  const app = await page({ desktop: true }, '', { page: 'home' }, guidedDocument());
  guidedBackend(app, { deferAdd: true });
  await app.$('#home-setup').emit('click');
  assert.equal(app.$('#home-setup').disabled, true);
  await app.$('#home-setup').emit('click');
  assert.equal(mutations(app).filter(r => r.data.action === 'add-exit').length, 1);
  await app.$('#connection-setup-cancel').emit('click');
  assert.equal(app.$('#home-setup').hidden, false);
  assert.equal(app.$('#home-setup').disabled, false);
  const failed = await page({ desktop: true }, '', { page: 'home' }, guidedDocument());
  const backend = guidedBackend(failed, { failAdd: true });
  await failed.$('#home-setup').emit('click');
  assert.equal(failed.$('#home-setup').textContent, '重试设置');
  assert.equal(failed.$('#home-setup').hidden, false);
  assert.equal(failed.$('#home-setup').disabled, false);
  backend.failAdd = false;
  await failed.$('#home-setup').emit('click');
  assert.equal(failed.$('#exit-picker-dialog').open, true);
});

test('home Setup respects read-only access and preserves configured or direct-only connections', async () => {
  const readonly = await page({}, '', { page: 'home' }, guidedDocument());
  assert.equal(readonly.$('#home-setup').disabled, true);
  await readonly.$('#home-setup').emit('click');
  assert.equal(mutations(readonly).length, 0);
  const configured = await page(exitsSnapshot(), '', { page: 'home' }, multiExitDocument());
  assert.equal(configured.$('#home-setup').hidden, true);
  assert.equal(configured.$('#home-toggle-control').hidden, false);
  const direct = await page({ desktop: true }, '', { page: 'home' }, doc());
  assert.equal(direct.$('#home-setup').hidden, true, 'a deliberate direct-only policy does not require a Tailnet setup');
});

function pickerNode(app, id) {
  const row = app.$('#exit-list').children.find(row => row.children[0].dataset.exitChoice === id);
  assert.ok(row, `missing picker row ${id}`);
  return { choice: row.children[0], latency: row.children[1], ping: row.children[2] };
}

for (const kind of ['metadata', 'node', 'settings']) test(`active Global ${kind} applies use shared consent and a separate disconnect`, async () => {
  for (const outcome of ['cancel', 'escape', 'success', 'disconnect-failure', 'save-failure']) {
    const initial = activeGlobalGatewayDocument();
    initial.values.exits[0].magic_dns_suffix = 'old.ts.net';
    const snapshot = activeGlobalGatewaySnapshot();
    snapshot.exits.office.magic_dns_suffix = 'new.ts.net';
    snapshot.exits.office.exit_nodes.push({ id: '100.101.0.12', name: 'next', online: true });
    const app = await page(snapshot, '', {}, initial);
    app.override = request => {
      if (request.path === '/api/preview') return response({ ...initial, yaml: initial.yaml + '# edited\n',
        values: { ...initial.values, log_level: 'debug' } });
      if (request.path === '/api/action') {
        assert.equal(request.data.action, 'disconnect');
        if (outcome === 'disconnect-failure') return response({ error: 'disconnect failed' }, false);
        Object.assign(app.snapshot, { active: false, state: 'stopped', mode: '' });
        return response({ ok: true });
      }
      if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
      assert.equal(app.snapshot.active, false);
      if (outcome === 'save-failure') return response({ error: 'save failed' }, false);
      app.config = { ...initial, revision: 'saved' }; app.snapshot.revision = 'saved';
      return response(app.config);
    };
    let applying;
    if (kind === 'settings') {
      await app.input('#level', 'debug'); await app.flushTimers();
      applying = app.$('#save').emit('click');
    } else {
      await extraRow(app, 'office').emit('click');
      if (kind === 'node') await pickerNode(app, '100.101.0.12').choice.emit('click');
      assert.equal(app.$('#apply-exit-picker').disabled, false);
      applying = app.$('#apply-exit-picker').emit('click');
    }
    await settle();
    assert.equal(app.$('#add-exit-consent-dialog').open, true, `${kind}/${outcome}`);
    assert.deepEqual(mutations(app), []);
    assert.match(app.$('#add-exit-consent-description').textContent, /即使保存失败/);
    if (outcome === 'escape') await app.$('#add-exit-consent-dialog').emit('cancel');
    answerAddConsent(app, ['cancel', 'escape'].includes(outcome) ? 'cancel' : 'apply');
    await applying;
    assert.deepEqual(mutations(app).map(request => request.path === '/api/config' ? 'save' : request.data.action),
      ['cancel', 'escape'].includes(outcome) ? [] : outcome === 'disconnect-failure' ? ['disconnect'] : ['disconnect', 'save']);
    assert.equal(app.snapshot.active, ['cancel', 'escape', 'disconnect-failure'].includes(outcome));
    if (outcome !== 'success' && kind !== 'settings') assert.equal(app.$('#exit-picker-dialog').open, true);
  }
});

test('Global settings apply rechecks the latest revision after protection consent', async () => {
  const initial = activeGlobalGatewayDocument();
  const app = await page(activeGlobalGatewaySnapshot(), '', {}, initial);
  app.override = request => request.path === '/api/preview' ? response({ ...initial, yaml: initial.yaml + '# draft\n',
    values: { ...initial.values, log_level: 'debug' } }) : undefined;
  await app.input('#level', 'debug'); await app.flushTimers();
  const applying = app.$('#save').emit('click'); await settle();
  assert.equal(app.$('#add-exit-consent-dialog').open, true);
  app.snapshot.revision = 'native-edit'; await app.poll();
  answerAddConsent(app, 'apply'); await applying;
  assert.deepEqual(mutations(app), []);
  assert.equal(app.snapshot.active, true);
  assert.equal(app.$('#level').value, 'debug');
});

test('Global can switch explicitly to a direct-only Split policy without choosing an unused default', async () => {
  const initial = activeGlobalGatewayDocument(); initial.values.rules.default_exit = '-';
  const app = await page(activeGlobalGatewaySnapshot(), '', {}, initial);
  assert.equal(app.$('#kill-switch').disabled, false);
  app.override = request => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    assert.deepEqual(request.data.changes, { 'rules.mode': 'split' });
    app.config = { ...initial, revision: 'saved', values: { ...initial.values, rules: { ...initial.values.rules, mode: 'split' } } };
    app.snapshot.revision = 'saved'; return response(app.config);
  };
  app.$('#kill-switch').value = 'split'; await app.$('#kill-switch').emit('change');
  assert.ok(!app.$('#add-exit-consent-dialog').open);
  assert.deepEqual(mutations(app).map(request => request.path), ['/api/config']);
});

test('isolated optional devices clear cached availability and ping and provide a sign-in action', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  await extraRow(app, 'office').emit('click');
  app.override = request => request.path === '/api/exit-ping' ? response({ id: request.data.id, latency_ms: 12, direct: true }) : undefined;
  await pickerNode(app, '100.101.0.11').ping.emit('click');
  assert.equal(pickerNode(app, '100.101.0.11').latency.children[0].textContent, '12 ms');
  app.snapshot.exits.office.phase = 'isolated';
  await app.poll();
  assert.match(app.$('#tailnet-identity').textContent, /未加入本次连接/);
  assert.equal(app.$('#picker-login').hidden, false);
  assert.equal(pickerNode(app, '100.101.0.11').choice.disabled, true);
  assert.equal(pickerNode(app, '100.101.0.11').ping.disabled, true);
  app.snapshot.exits.office.phase = ''; await app.poll();
  assert.equal(pickerNode(app, '100.101.0.11').latency.children[0].textContent, '—', 'isolated session invalidates old measurements');
});

test('Global omitted devices are not presented as available and remain selectable after consent', async () => {
  const snapshot = activeGlobalGatewaySnapshot();
  snapshot.phase = 'omitted'; snapshot.backend_state = 'Running';
  snapshot.exit_nodes = [{ id: '100.64.0.10', online: true }];
  snapshot.exits.spare = { phase: 'omitted', backend_state: '', exit_online: false, exit_nodes: [] };
  const app = await page(snapshot, '', {}, activeGlobalGatewayDocument());
  assert.equal(app.$('#main-exit-status').className, 'exit-status unknown');
  assert.match(app.$('#desktop-network-name').textContent, /全局模式未使用此设备/);
  assert.equal(app.$('#connection-exit').children.find(option => option.value === 'spare').disabled, false);
  await extraRow(app, 'spare').emit('click');
  assert.match(app.$('#tailnet-identity').textContent, /全局模式未使用此设备/);
  assert.equal(pickerNode(app, '100.102.0.11').ping.disabled, true);
});

test('default proxy exit is editable independently of unmatched traffic and keeps the rule list', async () => {
  const initial = multiExitDocument('100.101.0.11');
  initial.values.rules = { mode: 'split', source: 'builtin', final: 'direct', default_exit: 'main' };
  const app = await page(officeSnapshot(), '', {}, initial);
  await app.input('#default-exit', 'office');
  app.override = request => {
    if (request.path !== '/api/preview') return undefined;
    assert.deepEqual(request.data.changes, { 'rules.default_exit': 'office' });
    return response({ ...initial, yaml: initial.yaml + '\n# default exit changed\n', values: { ...initial.values, rules: { ...initial.values.rules, default_exit: 'office' } } });
  };
  await app.flushTimers();
  assert.equal(app.$('#default-exit').value, 'office');
  assert.equal(app.$('#final-route').value, 'direct');
  assert.equal(app.$('#rule-source-type').value, 'builtin');
  assert.equal(app.$('#save').disabled, false);
  assert.equal(app.requests.some(r => r.path === '/api/action'), false);
});
