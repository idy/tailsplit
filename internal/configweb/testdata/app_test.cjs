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
  const pages = ['connection', 'logs', 'settings'].map((name) => {
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
      assert.deepEqual(request.data.changes, { ...(saves === 1 ? { 'rules.default_exit': '-', 'rules.global_exit': '-' } : {}), exits: [saves === 1
        ? { id: 'exit-1', auto_name: true, exit_node: '' }
        : { id: 'exit-1', auto_name: true, exit_node: '100.101.0.11', tailnet: 'office.ts.net', name: 'hk-gateway@office.ts.net' }] });
      app.config = { ...app.config, revision: `saved-${saves}`,
        values: { ...app.config.values, exits: request.data.changes.exits } };
      app.snapshot.revision = app.config.revision;
      return response(app.config);
    }
    if (request.path === '/api/action') {
      assert.deepEqual(request.data, { action: 'login-exit', exit: 'exit-1' });
      app.snapshot = exitsSnapshot({ revision: app.config.revision, exits: { 'exit-1': officeSnapshot().exits.office } });
      return response({ ok: true });
    }
    return undefined;
  };
  await app.$('#add-exit').emit('click');
  assert.equal(app.$('#add-exit-dialog').open, undefined, 'no naming dialog precedes login');
  assert.equal(app.$('#exit-picker-dialog').open, true);
  assert.equal(app.$('#heading-exits').textContent, 'office.ts.net');
  await exitChoice(app, '100.101.0.11').emit('click');
  await app.$('#apply-exit-picker').emit('click');
  assert.equal(saves, 2);
  assert.equal(app.$('#exit-picker-dialog').open, false);
  await extraRow(app, 'exit-1').emit('click');
  assert.equal(app.$('#heading-exits').textContent, 'hk-gateway@office.ts.net');
  assert.equal(app.config.values.rules.default_exit, '-');
  assert.equal(app.config.values.rules.global_exit, '-');
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
    assert.deepEqual(request.data, { action: 'connect' });
    Object.assign(app.snapshot, { active: true, state: 'running', mode: 'tun', routing_mode: 'global' });
    return response({ ok: true });
  };
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#desktop-connect').hidden, true);
  assert.equal(app.$('#desktop-disconnect').hidden, false);
  assert.equal(app.$('#desktop-disconnect').disabled, false);
  assert.equal(app.$('#connection-status span').textContent, '已连接');
  assert.equal(app.$('#exit-label').textContent, 'hk-gateway');
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
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#default-exit-dialog').open, true);
  assert.equal(app.$('#confirm-default-exit').disabled, true, 'empty exits cannot start as a direct fallback');
  assert.equal(app.$('#default-exit-empty').hidden, false);
  assert.equal(app.requests.some(r => r.path === '/api/action'), false, 'the atomic removal API owns logout');
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
      assert.deepEqual(request.data, { action: 'connect' });
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

test('the default dialog discovers saved logins without choosing or connecting', async () => {
  const initial = withoutDefaultDocument(); initial.values.tailscale = null;
  const snapshot = officeSnapshot({ backend_state: '' });
  const app = await page(snapshot, '', {}, initial);
  app.override = request => {
    if (request.path !== '/api/action') return undefined;
    assert.deepEqual(request.data, { action: 'refresh-exits' });
    app.snapshot = officeSnapshot();
    return response({ ok: true });
  };
  await app.$('#desktop-connect').emit('click');
  assert.equal(app.$('#default-exit-dialog').open, true);
  assert.equal(app.$('#default-exit-choice').value, '');
  assert.equal(app.$('#default-exit-choice').disabled, false);
  assert.equal(app.$('#confirm-default-exit').disabled, true);
  assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
  assert.deepEqual(app.requests.filter(r => r.path === '/api/action').map(r => r.data.action), ['refresh-exits']);
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

test('default confirmation rejects changed configuration or an unavailable selection', async () => {
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
    assert.equal(app.$('#confirm-default-exit').disabled, true);
    await app.$('#confirm-default-exit').emit('click');
    assert.equal(app.requests.some(r => r.method === 'POST'), false);
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
  assert.equal(app.$('#desktop-connect').disabled, true);
  assert.equal(app.requests.some(r => r.method === 'POST'), false);
});

test('settings drafts block connection and retain the saved default display', async () => {
  const app = await page(officeSnapshot(), '', {}, multiExitDocument('100.101.0.11'));
  await app.input('#level', 'debug');
  assert.equal(app.$('#desktop-connect').disabled, true);
  assert.equal(app.$('#connection-exit').value, 'main');
  assert.equal(app.requests.some(r => r.path === '/api/config' && r.method === 'POST'), false);
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
