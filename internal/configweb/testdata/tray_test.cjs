const { test } = require('node:test');
const assert = require('node:assert/strict');
const { popupView } = require('../assets/tray.js');
const config = { valid: true, values: {
  tailscale: { name: 'Personal', exit_node: '100.64.0.1' },
  exits: [{ id: 'office', name: 'Office', exit_node: '100.64.0.2' }],
  rules: { mode: 'split', default_exit: 'office', global_exit: 'office' },
} };

test('additional traffic exit stays connected when primary needs login', () => {
  const view = popupView(config, { active: true, mode: 'tun', state: 'running', routing_mode: 'split',
    traffic_exit: 'office', backend_state: 'NeedsLogin', exit_nodes: [],
    exits: { office: { backend_state: 'Running', exit: '100.64.0.2' } } });
  assert.equal(view.status, '已连接');
  assert.equal(view.gateway, 'Office');
  assert.equal(view.setup, false);
});

test('active helper selection wins over config changed since connection', () => {
  const changed = structuredClone(config);
  changed.values.rules = { mode: 'global', global_exit: 'main' };
  const view = popupView(changed, { active: true, mode: 'tun', state: 'running',
    routing_mode: 'split', traffic_exit: 'office', exits: { office: { exit: '100.64.0.2' } } });
  assert.equal(view.mode, 'split');
  assert.equal(view.gateway, 'Office');
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
  for (const doc of [{ valid: false }, { valid: true, values: { tailscale: null, exits: [], rules: { default_exit: '-' } } }]) {
    const view = popupView(doc, { active: false });
    assert.equal(view.setup, true);
    assert.equal(view.gateway, '');
  }
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

test('an unnamed gateway uses its node name without appending an IP', () => {
  const unnamed = structuredClone(config);
  delete unnamed.values.exits[0].name;
  const view = popupView(unnamed, { exits: { office: { exit_nodes: [{ id: '100.64.0.2', name: 'tokyo-node' }] } } });
  assert.equal(view.gateway, 'tokyo-node');
});
