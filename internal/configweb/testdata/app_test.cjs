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
  showModal() { this.open = true; }
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
  const fields = [level, mtu, tun, ruleSource];
  const pages = ['connection', 'logs', 'settings'].map((name) => {
    const nav = $(`[data-page="${name}"]`); nav.dataset.page = name; return nav;
  });
  const sections = ['general', 'network', 'dns', 'routing', 'advanced', 'about'].map((name) => {
    const nav = $(`[data-section="${name}"]`); nav.dataset.section = name; return nav;
  });
  const modes = ['split', 'global'].map((mode) => {
    const button = $(`[data-routing-mode="${mode}"]`); button.dataset.routingMode = mode; return button;
  });
  const desktopActions = ['connect', 'disconnect', 'login', 'switch-network', 'refresh-exits'].map((action) => {
    const selector = action === 'refresh-exits' ? '#refresh-exits' : `#desktop-${action}`;
    const button = $(selector); button.dataset.desktopAction = action; return button;
  });
  const appPages = ['connection', 'logs', 'settings'].map((name) => { const element = $(`#page-${name}`); element.id = `page-${name}`; return element; });
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
  const app = { $, requests, messages, windowEvents: window, config: initialDocument, snapshot: { state: 'stopped', revision: 'original', ...snapshot },
    language: { preference: localeOptions.preference || 'auto', language: localeOptions.language || 'zh' }, override: () => undefined };
  const document = {
    documentElement: new Element(), hidden: false, querySelector: $, createElement: (tag) => { const element = new Element(); element.tagName = tag; return element; },
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

test('changing network requires confirmation and refreshes the network and exits', async () => {
  const app = await page({ desktop: true, network: 'old.ts.net', backend_state: 'Running',
    exit_nodes: [{ id: 'old-exit', name: 'Old exit', online: true }] });
  const dialog = app.$('#switch-network-dialog');
  const button = app.$('#desktop-switch-network');
  await button.emit('click');
  assert.equal(dialog.open, true);
  assert.equal(app.requests.some((r) => r.path === '/api/action'), false);
  dialog.returnValue = 'cancel';
  await dialog.emit('close');
  assert.equal(app.requests.some((r) => r.path === '/api/action'), false);
  app.override = (request) => {
    if (request.path !== '/api/action') return undefined;
    assert.equal(request.data.action, 'switch-network');
    app.snapshot.network = 'new.ts.net';
    app.snapshot.exit_nodes = [{ id: 'new-exit', name: 'New exit', online: true }];
    return response({ ok: true });
  };
  await button.emit('click');
  dialog.returnValue = 'switch-network';
  await dialog.emit('close');
  await settle();
  assert.equal(app.$('#desktop-network-name').textContent, 'new.ts.net');
  assert.equal(exitRadio(app, 'new-exit').disabled, false);
  assert.equal(app.$('#exit-list').children[0].children[1].children[0].textContent, 'New exit');
  assert.equal(exitRadio(app, 'old-exit'), undefined);
  assert.equal(app.requests.filter((r) => r.path === '/api/action').length, 1);
});

test('network switch conflicts stay visible across revision reloads until recovery', async () => {
  for (const recovery of ['reload', 'login']) {
    const app = await page({ desktop: true, network: 'old.ts.net', backend_state: 'Running' });
    const conflict = { zh: '配置已在切换期间更新，请重试。', en: 'Configuration changed during switching. Try again.' };
    app.override = (request) => {
      if (request.path === '/api/action' && request.data.action === 'switch-network') {
        app.snapshot = { ...app.snapshot, network: '', backend_state: 'NeedsLogin', revision: 'external' };
        return response({ error: conflict.en, error_i18n: conflict }, false);
      }
      if (request.path === '/api/config') return response(doc(yaml + '# External edit\n', 'external'));
      return undefined;
    };
    await app.$('#desktop-switch-network').emit('click');
    app.$('#switch-network-dialog').returnValue = 'switch-network';
    await app.$('#switch-network-dialog').emit('close');
    await settle();
    assert.match(app.$('#raw-yaml').value, /External edit/);
    assert.equal(app.$('#desktop-network-name').textContent, '未登录');
    assert.equal(app.$('#notice').hidden, false);
    assert.equal(app.$('#notice-detail').textContent, conflict.zh);
    await app.poll();
    app.i18n.setLanguage('en');
    assert.equal(app.$('#notice').hidden, false);
    assert.equal(app.$('#notice-detail').textContent, conflict.en);
    await app.$(recovery === 'reload' ? '#reload' : '#desktop-login').emit('click');
    assert.equal(app.$('#notice').hidden, true, `error was not cleared by ${recovery}`);
  }
});

test('changing network protects drafts and blocks duplicate requests', async () => {
  const app = await page({ desktop: true, network: 'old.ts.net' });
  const button = app.$('#desktop-switch-network');
  await app.$('#yaml-tab').emit('click');
  await app.input('#raw-yaml', 'unfinished: [');
  assert.equal(button.disabled, true);
  await button.emit('click');
  assert.equal(app.$('#switch-network-dialog').open, undefined);
  assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
  assert.equal(app.requests.some((r) => r.path === '/api/action'), false);

  const clean = await page({ desktop: true, network: 'old.ts.net' });
  const pending = deferred();
  clean.override = (request) => request.path === '/api/action' ? pending.promise : undefined;
  await clean.$('#desktop-switch-network').emit('click');
  clean.$('#switch-network-dialog').returnValue = 'switch-network';
  await clean.$('#switch-network-dialog').emit('close');
  assert.equal(clean.$('#desktop-switch-network').disabled, true);
  await clean.$('#desktop-switch-network').emit('click');
  pending.resolve(response({ error: 'Fixture logout failed' }, false));
  await settle();
  assert.equal(clean.$('#notice-detail').textContent, 'Fixture logout failed');
  assert.equal(clean.$('#desktop-switch-network').disabled, false);
  assert.equal(clean.requests.filter((r) => r.path === '/api/action').length, 1);
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
      const app = await page({ desktop: true, active: true, mode, state, backend_state });
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
  values: { log_level: 'info', tailscale: { exit_node: exit }, rules: { mode }, tun: { enabled: false, mtu: 1400 } },
});
const exitsSnapshot = (changes = {}) => ({
  desktop: true, backend_state: 'Running', hostname: 'my-device', network: 'example.ts.net',
  exit_nodes: [
    { id: '100.64.0.10', name: 'home', online: true, selected: true },
    { id: '100.64.0.20', name: 'travel', online: true },
    { id: '100.64.0.30', name: 'backup', online: false },
  ], ...changes,
});
const exitRadio = (app, id) => app.$('#exit-list').children.find((row) => row.children[0].value === id)?.children[0];

test('pending exit and mode selections block network switching until canceled', async () => {
  for (const selection of ['exit', 'mode']) {
    const app = await page(exitsSnapshot(), '', {}, exitDocument());
    const button = app.$('#desktop-switch-network');
    assert.equal(button.hidden, false);
    assert.equal(button.disabled, false);
    if (selection === 'exit') await exitRadio(app, '100.64.0.20').emit('change');
    else await app.$('[data-routing-mode="global"]').emit('click');
    assert.equal(button.disabled, true, selection);
    await button.emit('click');
    assert.equal(app.$('#switch-network-dialog').open, undefined);
    assert.equal(app.$('#connection-apply').hidden, false);
    assert.equal(app.requests.some((r) => r.method === 'POST'), false);
    await app.$('#cancel-connection-change').emit('click');
    assert.equal(button.disabled, false);
    assert.equal(app.$('#connection-apply').hidden, true);
  }
});

test('network confirmation rechecks drafts and pending connection choices', async () => {
  for (const change of ['draft', 'exit', 'mode']) {
    const app = await page(exitsSnapshot(), '', {}, exitDocument());
    const dialog = app.$('#switch-network-dialog');
    await app.$('#desktop-switch-network').emit('click');
    assert.equal(dialog.open, true);
    if (change === 'draft') {
      await app.$('#yaml-tab').emit('click');
      await app.input('#raw-yaml', 'unfinished: [');
    } else if (change === 'exit') await exitRadio(app, '100.64.0.20').emit('change');
    else await app.$('[data-routing-mode="global"]').emit('click');
    dialog.returnValue = 'switch-network';
    await dialog.emit('close');
    await settle();
    assert.equal(app.requests.some((r) => r.path === '/api/action'), false, change);
    assert.equal(app.requests.some((r) => r.path === '/api/config' && r.method === 'POST'), false, change);
    if (change === 'draft') assert.equal(app.$('#raw-yaml').value, 'unfinished: [');
    else assert.equal(app.$('#connection-apply').hidden, false);
  }
});

test('backend transition states block network switching until the device settles', async () => {
  for (const transition of [
    { state: 'starting' }, { state: 'stopping' }, { state: 'logging-out' },
    { active: true, state: 'checking-exit', mode: '' },
  ]) {
    const app = await page(exitsSnapshot(transition), '', {}, exitDocument());
    const button = app.$('#desktop-switch-network');
    assert.equal(button.disabled, true, transition.state);
    if (transition.state === 'logging-out') assert.equal(app.$('#desktop-network-name').textContent, '正在退出登录');
    await button.emit('click');
    assert.equal(app.$('#switch-network-dialog').open, undefined);
    assert.equal(app.requests.some((r) => r.path === '/api/action'), false);
    app.snapshot = { ...app.snapshot, active: false, state: 'stopped', mode: '' };
    await app.poll();
    assert.equal(button.disabled, false);
  }
});

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
  await exitRadio(app, '100.64.0.20').emit('change');
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
  await exitRadio(app, '100.64.0.20').emit('change');
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
    app.config = exitDocument('100.64.0.10', 'global', 'saved-before-failure');
    app.config.values.tun.enabled = true;
    app.config.yaml = app.config.yaml.replace('enabled: false', 'enabled: true');
    Object.assign(app.snapshot, { active: false, mode: '', state: 'failed', error: failure, revision: 'saved-before-failure' });
    return response({ code: 'apply', error: failure }, false);
  };
  await app.$('[data-routing-mode="global"]').emit('click');
  await app.$('#apply-connection-change').emit('click');
  assert.match(app.$('#raw-yaml').value, /mode: global/);
  assert.equal(app.$('#connection-apply').hidden, true);
  assert.equal(app.$('#notice').hidden, false);
  assert.equal(app.$('#notice-title').textContent, '无法应用连接更改');
  assert.equal(app.$('#notice-detail').textContent, failure);
  await app.poll();
  assert.equal(app.$('#notice').hidden, false);
  await app.$('[data-page="settings"]').emit('click');
  await app.$('#reload').emit('click');
  assert.equal(app.$('#notice').hidden, true);
  assert.equal(app.$('#connection-state').textContent, '连接异常');
});

test('mode selection is confirmed with the exit and enables system routing only for global mode', async () => {
  const app = await page(exitsSnapshot({ active: true, mode: 'proxy', state: 'running' }), '', {}, exitDocument());
  await exitRadio(app, '100.64.0.20').emit('change');
  await app.$('[data-routing-mode="global"]').emit('click');
  assert.match(app.$('#connection-consequence').textContent, /网络会短暂中断/);
  assert.equal(app.$('#apply-connection-change').textContent, '应用连接更改');
  app.override = (request) => request.path === '/api/config' && request.method === 'POST'
    ? response({ code: 'apply', error: 'Reconnect failed' }, false) : undefined;
  await app.$('#apply-connection-change').emit('click');
  const saved = app.requests.find((r) => r.path === '/api/config' && r.method === 'POST');
  assert.deepEqual(saved.data.changes, { 'tailscale.exit_node': '100.64.0.20', 'rules.mode': 'global', 'tun.enabled': true });
  assert.equal(app.$('#connection-apply').hidden, false);
  assert.match(app.$('#connection-selection').textContent, /travel/);
  assert.equal(app.$('#notice-detail').textContent, 'Reconnect failed');
});

test('settings drafts block quick apply without being saved or replaced', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  await exitRadio(app, '100.64.0.20').emit('change');
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
  const missing = exitRadio(app, '100.64.0.99');
  assert.equal(missing.checked, true);
  assert.equal(missing.disabled, true);
  assert.equal(exitRadio(app, '100.64.0.30').disabled, true);
  // A saved manual address can still run the backend availability preflight.
  assert.equal(app.$('#desktop-connect').disabled, false);
  await app.input('#exit-search', 'TRAVEL');
  assert.deepEqual(app.$('#exit-list').children.map((row) => row.children[0].value), ['100.64.0.20']);
  await app.input('#exit-search', 'nonexistent');
  assert.equal(app.$('#exit-empty').hidden, false);
  assert.equal(app.$('#exit-empty-title').textContent, '没有匹配的出口');
  assert.equal(app.requests.some((r) => r.method === 'POST'), false);
});

test('a stale selected flag cannot hide a manually saved exit or block restoring the prior exit', async () => {
  const snapshot = exitsSnapshot(); snapshot.exit_nodes[0].selected_for = '100.64.0.10';
  const app = await page(snapshot, '', {}, exitDocument());
  app.override = (request) => {
    if (request.path !== '/api/config' || request.method !== 'POST') return undefined;
    const chosen = request.data.changes['tailscale.exit_node'];
    app.config = exitDocument(chosen, 'split', chosen); app.snapshot.revision = chosen;
    // Idle reconfiguration deliberately retains the old discovery snapshot.
    return response(app.config);
  };
  await app.input('#exit-node', '100.64.0.99');
  await app.$('#select-manual-exit').emit('click');
  await app.$('#apply-connection-change').emit('click');
  assert.equal(exitRadio(app, '100.64.0.99').checked, true);
  assert.equal(exitRadio(app, '100.64.0.10').checked, false);
  await exitRadio(app, '100.64.0.10').emit('change');
  assert.equal(app.$('#connection-apply').hidden, false);
  assert.equal(app.$('#apply-connection-change').disabled, false);
  await app.$('#apply-connection-change').emit('click');
  assert.deepEqual(app.requests.filter(r => r.path === '/api/config' && r.method === 'POST').map(r => r.data.changes['tailscale.exit_node']), ['100.64.0.99', '100.64.0.10']);
});

test('selection metadata bound to the saved DNS alias identifies its discovered node', async () => {
  const alias = 'home.example.ts.net';
  const snapshot = exitsSnapshot(); snapshot.exit_nodes[0].selected_for = alias;
  const app = await page(snapshot, '', {}, exitDocument(alias));
  assert.equal(exitRadio(app, '100.64.0.10').checked, true);
  assert.equal(exitRadio(app, alias), undefined);
});

test('a stale poll cannot restore old device state after a desktop action', async () => {
  const app = await page(exitsSnapshot(), '', {}, exitDocument());
  const old = deferred(); let delay = true;
  app.override = (request) => {
    if (request.path === '/api/state' && delay) { delay = false; return old.promise; }
    if (request.path === '/api/action' && request.data.action === 'disconnect') {
      app.snapshot = { ...app.snapshot, active: false, state: 'stopped', backend_state: 'NeedsLogin', hostname: '', network: '', exit_nodes: [] };
      return response({ ok: true });
    }
    return undefined;
  };
  const polling = app.poll(); await settle();
  // Use the disconnect event to drive the shared desktop action machinery.
  app.snapshot.active = true;
  await app.poll();
  await app.$('#desktop-disconnect').emit('click');
  old.resolve(response(exitsSnapshot({ revision: 'original' }))); await polling;
  assert.equal(app.$('#tailnet-identity').textContent, '未登录 Tailscale');
});
