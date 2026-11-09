const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const { join } = require('node:path');
const { pathToFileURL } = require('node:url');
const { runInNewContext } = require('node:vm');

function sdk() {
  const context = { document: { currentScript: null }, navigator: { language: 'en' }, requestAnimationFrame: () => 1 };
  const bundle = readFileSync(join(__dirname, '../assets/vendor/peetty/peetty.global.js'), 'utf8');
  runInNewContext(bundle, context);
  return context.Peetty.Peetty;
}

test('reduced-motion entrance places the companion without starting a fall', () => {
  const Companion = sdk();
  const target = { el: {}, left: 100, right: 200, y: 450 };
  let landed = false, placed = false;
  const pet = {
    view: { reducedMotion: true, height: 88 }, overlay: { host: { style: {} } },
    scan: () => [target], setPetVisible() {},
    land(perch) { assert.equal(perch, target); landed = true; },
    place() { placed = true; },
    startFall() { assert.fail('reduced motion must not animate an entrance fall'); },
  };
  Companion.prototype.enter.call(pet);
  assert.equal(landed, true);
  assert.equal(placed, true);
  assert.equal(pet.y, target.y);
});

test('reduced motion blocks jumps and turns falls into immediate safe placement', () => {
  const Companion = sdk();
  let entered = false;
  const pet = {
    view: { reducedMotion: true },
    scan() { assert.fail('reduced motion must not plan a jump'); },
    enter() { entered = true; },
    finishChatAction() { assert.fail('reduced motion must not begin a fall'); },
  };
  assert.equal(Companion.prototype.leap.call(pet), false);
  Companion.prototype.startFall.call(pet);
  assert.equal(entered, true);
});

test('focusing an overlapped field repositions without jumping in reduced motion', () => {
  const Companion = sdk();
  let entered = false;
  const field = {
    getBoundingClientRect: () => ({ left: 0, right: 100, top: 400, bottom: 500 }),
    matches: () => false, getRootNode: () => ({ activeElement: null }),
  };
  const pet = {
    mode: 'ground', perch: { el: field, clear: true }, x: 50, y: 450, halfWidth: 35,
    view: { reducedMotion: true, anchor: [30, 85], height: 88, motion: { exclaim() {} } },
    enter() { entered = true; },
  };
  pet.leap = () => Companion.prototype.leap.call(pet);
  pet.startFall = () => Companion.prototype.startFall.call(pet);
  Companion.prototype.makeRoom.call(pet, field);
  assert.equal(entered, true);
});

test('enabling reduced motion interrupts an in-flight jump and an active walk', () => {
  const Companion = sdk();
  const pet = new Companion({ chat: false, language: 'en' });
  let entered = false;
  pet.view = { reducedMotion: true, step() {} };
  pet.overlay = {};
  pet.mode = 'jump';
  pet.action = 'walk';
  pet.enter = () => { entered = true; pet.mode = 'ground'; };
  pet.setAction = (action) => { pet.action = action; };
  pet.ground = () => { assert.equal(pet.action, 'idle'); };
  pet.place = () => {};
  pet.updateChatAction = () => {};
  pet.tick(16);
  assert.equal(entered, true);
  assert.equal(pet.mode, 'ground');
});

test('Windows asset regeneration launches npm-cli.js through Node without a command shell', async () => {
  const tools = await import(pathToFileURL(join(__dirname, '../../../scripts/peetty-vendor-tools.mjs')).href);
  const invocation = tools.npmInvocation({ platform: 'win32', execPath: 'C:\\Program Files\\nodejs\\node.exe', env: {} });
  assert.equal(invocation.command, 'C:\\Program Files\\nodejs\\node.exe');
  assert.deepEqual(invocation.args, ['C:\\Program Files\\nodejs\\node_modules\\npm\\bin\\npm-cli.js']);
  const custom = tools.npmInvocation({ platform: 'win32', execPath: 'C:\\Node\\node.exe', env: { npm_execpath: 'D:\\Tools\\npm-cli.js' } });
  assert.deepEqual(custom.args, ['D:\\Tools\\npm-cli.js']);
  assert.deepEqual(tools.npmInvocation({ platform: 'darwin', env: {} }), { command: 'npm', args: [] });
});
