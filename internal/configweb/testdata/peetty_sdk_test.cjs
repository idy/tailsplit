const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const { join } = require('node:path');
const { pathToFileURL } = require('node:url');
const { runInNewContext } = require('node:vm');

function sdk() {
  let perches = [];
  const document = {
    currentScript: null, nodeType: 9,
    elementsFromPoint: (x, y) => perches.filter((perch) => perch.el &&
      x >= perch.left && x <= perch.right && y >= perch.y && y <= perch.y + 3 &&
      (!perch.supportedAt || perch.supportedAt(x))).map((perch) => perch.el),
  };
  const context = { document, navigator: { language: 'en' }, requestAnimationFrame: () => 1 };
  const bundle = readFileSync(join(__dirname, '../assets/vendor/peetty/peetty.global.js'), 'utf8');
  runInNewContext(bundle, context);
  const Companion = context.Peetty.Peetty;
  Companion.setTestPerches = (value) => { perches = value; };
  return Companion;
}

function stationary(Companion, perches, x = 150, y = 440) {
  const pet = {
    x, y, mode: 'ground',
    view: { reducedMotion: true, height: 88, anchor: [30, 85], motion: { exclaim() {} } },
    overlay: { host: { style: {} }, menu: { classList: { remove() {} } } },
    scan: () => perches, setPetVisible() {}, place() {},
    land(perch) { this.perch = perch; this.mode = 'ground'; },
    park() { this.mode = 'paused'; },
  };
  pet.overlay.host.contains = () => false;
  pet.perchOptions = { host: pet.overlay.host, width: 60, height: 88, anchor: [30, 85] };
  for (const { el } of perches) {
    if (!el) continue;
    el.contains ||= (node) => node === el;
    el.closest ||= () => null;
    el.getRootNode ||= () => ({ nodeType: 9 });
  }
  Companion.setTestPerches(perches);
  pet.settle = (...args) => Companion.prototype.settle.call(pet, ...args);
  pet.leap = (...args) => Companion.prototype.leap.call(pet, ...args);
  pet.startFall = (...args) => Companion.prototype.startFall.call(pet, ...args);
  return pet;
}

test('reduced-motion entrance places the companion without starting a fall', () => {
  const Companion = sdk();
  const target = { el: {}, left: 100, right: 200, y: 450, clear: true };
  const pet = stationary(Companion, [target]);
  Companion.prototype.enter.call(pet);
  assert.equal(pet.perch, target);
  assert.equal(pet.mode, 'ground');
  assert.equal(pet.y, target.y);
});

test('reduced-motion drops snap to the nearest safe spot and retain horizontal placement', () => {
  const Companion = sdk();
  const far = { el: {}, left: 10, right: 50, y: 50, clear: true };
  const unsafe = { el: {}, left: 140, right: 160, y: 440, clear: false };
  const near = { el: {}, left: 100, right: 200, y: 450, clear: true };
  const pet = stationary(Companion, [far, unsafe, near]);
  pet.startFall();
  assert.equal(pet.perch, near);
  assert.equal(pet.x, 150);
  assert.equal(pet.y, 450);
  assert.equal(pet.mode, 'ground');
});

for (const [side, release] of [['left', 80], ['right', 220]]) {
  test(`a reduced-motion drop near the ${side} rounded corner finds actual support`, () => {
    const Companion = sdk();
    const panel = { el: {}, left: 100, right: 200, y: 450, clear: true,
      supportedAt: (x) => x >= 112 && x <= 188 };
    const pet = stationary(Companion, [panel], release, 448);
    pet.startFall();
    assert.equal(pet.perch, panel);
    assert.ok(panel.supportedAt(pet.x));
    assert.ok(side === 'left' ? pet.x < 113 : pet.x > 187, 'landing stays close to the nearest supported edge');
    const landed = pet.x;
    pet.startFall();
    assert.equal(pet.x, landed, 'a support recheck does not repeat an unsupported landing');
    assert.equal(pet.mode, 'ground');
  });
}

test('an unsupported panel is skipped in favor of clear floor', () => {
  const Companion = sdk();
  const panel = { el: {}, left: 100, right: 200, y: 450, clear: true, supportedAt: () => false };
  const floor = { el: null, left: 30, right: 970, y: 700, clear: true };
  const pet = stationary(Companion, [panel, floor]);
  pet.startFall();
  assert.equal(pet.perch, floor);
  assert.equal(pet.mode, 'ground');
});

test('no supported landing parks instead of repeating an invalid drop', () => {
  const Companion = sdk();
  const panel = { el: {}, left: 100, right: 200, y: 450, clear: true, supportedAt: () => false };
  const pet = stationary(Companion, [panel]);
  pet.startFall();
  assert.equal(pet.perch, undefined);
  assert.equal(pet.mode, 'paused');
});

test('the explicit move menu action selects another safe surface without an animated jump', () => {
  const Companion = sdk();
  const current = { el: {}, left: 100, right: 200, y: 450, clear: true };
  const other = { el: {}, left: 210, right: 300, y: 500, clear: true };
  const pet = stationary(Companion, [current, other]);
  pet.perch = current;
  pet.say = () => assert.fail('an available explicit move must not fall back to a greeting');
  Companion.prototype.onMenu.call(pet, 'move');
  assert.equal(pet.perch, other);
  assert.equal(pet.mode, 'ground');
  assert.equal(pet.x, other.left);
});

test('focusing an overlapped field repositions without jumping in reduced motion', () => {
  const Companion = sdk();
  const field = {
    getBoundingClientRect: () => ({ left: 0, right: 100, top: 400, bottom: 500 }),
    matches: () => false, getRootNode: () => ({ activeElement: null }), contains: () => false,
  };
  const current = { el: field, left: 0, right: 100, y: 450, clear: true };
  const other = { el: {}, left: 110, right: 200, y: 450, clear: true };
  const pet = stationary(Companion, [current, other], 50, 450);
  pet.perch = current;
  pet.halfWidth = 35;
  Companion.prototype.makeRoom.call(pet, field);
  assert.equal(pet.perch, other);
  assert.equal(pet.mode, 'ground');
});

test('enabling reduced motion interrupts an in-flight jump and an active walk', () => {
  const Companion = sdk();
  const pet = new Companion({ chat: false, language: 'en' });
  let settled = false;
  pet.view = { reducedMotion: true, step() {} };
  pet.overlay = {};
  pet.mode = 'jump';
  pet.action = 'walk';
  pet.settle = () => { settled = true; pet.mode = 'ground'; };
  pet.setAction = (action) => { pet.action = action; };
  pet.ground = () => { assert.equal(pet.action, 'idle'); };
  pet.place = () => {};
  pet.updateChatAction = () => {};
  pet.tick(16);
  assert.equal(settled, true);
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

test('source adaptations handle CRLF checkouts identically to LF', async () => {
  const tools = await import(pathToFileURL(join(__dirname, '../../../scripts/peetty-vendor-tools.mjs')).href);
  const pet = [
    '  private enter() {',
    '  private leap(exclude: Element | null = null): boolean {',
    '  private startFall(calm = false) {',
    '    const now = this.clock;\n    switch (this.mode) {',
  ].join('\n');
  assert.equal(tools.adaptPet(pet.replace(/\n/g, '\r\n')), tools.adaptPet(pet));
  const perches = '      if (text.length > MAX_TEXT || !spend(result.budget, Math.ceil(text.length / 64))) { result.complete = false; break; }\n';
  assert.equal(tools.adaptPerches(perches.replace(/\n/g, '\r\n')), tools.adaptPerches(perches));
  assert.equal(tools.normalizeSource('a\r\nb\rc\n'), 'a\nb\nc\n');
});
