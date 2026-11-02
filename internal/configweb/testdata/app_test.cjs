'use strict';
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = readFileSync(process.argv[2] || join(__dirname, '../assets/app.js'), 'utf8');
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
    if (this.disabled && ['click', 'input', 'keydown'].includes(name)) return;
    await Promise.all((this.listeners[name] || []).map((callback) => callback({
      target: this, preventDefault() {}, ...event,
    })));
  }
}

async function page() {
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
  $('#yaml-editor').hidden = true;
  const navigation = $('[data-section="connection"]'); navigation.dataset.section = 'connection';
  const events = new Element();
  const requests = [];
  const timers = new Map();
  let timer = 0;
  let poll;
  const app = { $, requests, snapshot: { state: 'stopped', revision: 'original' }, override: () => undefined };
  const document = {
    hidden: false, querySelector: $, createElement: () => new Element(),
    createTextNode: () => new Element(), createDocumentFragment: () => new Element(),
    querySelectorAll: (selector) => selector === '[data-field]' ? fields :
      selector === '[data-section]' ? [navigation] :
      selector === '.editor-tabs button, [data-section]' ? [$('#form-tab'), $('#yaml-tab'), navigation] : [],
    addEventListener: (...args) => events.addEventListener(...args),
  };
  vm.runInNewContext(source, {
    document, window: new Element(), URLSearchParams, location: { hash: '#token=fixture' },
    history: { replaceState() {} }, sessionStorage: { setItem() {}, getItem() {} },
    navigator: { clipboard: { writeText: async () => {} } },
    setTimeout: (callback) => { timers.set(++timer, callback); return timer; },
    clearTimeout: (id) => timers.delete(id), setInterval: (callback) => { poll = callback; },
    fetch: async (path, options) => {
      const request = { path, method: options.method, data: options.body && JSON.parse(options.body) };
      requests.push(request);
      const overridden = app.override(request);
      if (overridden !== undefined) return overridden;
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
  });
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
