'use strict';
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const script = readFileSync(join(__dirname, '../assets/i18n.js'), 'utf8');
const html = readFileSync(join(__dirname, '../assets/index.html'), 'utf8');
const app = readFileSync(join(__dirname, '../assets/app.js'), 'utf8');

function languagePage(languages = ['en-US'], nodes = []) {
  let index = 0;
  const document = {
    documentElement: {}, title: '', body: {},
    createTreeWalker: () => ({ nextNode: () => nodes[index++] || null }),
    querySelectorAll: () => [],
  };
  const window = {};
  vm.runInNewContext(script, { document, window, navigator: { languages }, Intl });
  return { i18n: window.TailSplitI18n, document };
}

test('catalog covers all static app text, accessible labels and placeholders', () => {
  const { i18n } = languagePage();
  const copy = [...html.matchAll(/>([^<>]+)</g)].map(m => m[1].trim());
  copy.push(...[...html.matchAll(/(?:placeholder|aria-label|title)="([^"]+)"/g)].map(m => m[1]));
  for (const text of copy.filter(text => /[一-鿿]/.test(text))) {
    assert.ok(Object.hasOwn(i18n.catalog, text), `Missing static translation: ${text}`);
  }
  for (const [, key] of app.matchAll(/\bt\('([^']+)'/g)) {
    assert.ok(Object.hasOwn(i18n.catalog, key), `Missing dynamic translation: ${key}`);
  }
});

test('language fallback and interpolation keep external values literal', () => {
  for (const [languages, expected] of [[['zh-TW'], 'zh'], [['zh-CN'], 'zh'], [['en-GB'], 'en'], [['de-DE'], 'en'], [[], 'en']]) {
    const { i18n, document } = languagePage(languages);
    assert.equal(i18n.language, expected);
    assert.equal(document.documentElement.lang, expected === 'zh' ? 'zh-CN' : 'en');
  }
  const { i18n } = languagePage();
  assert.equal(i18n.t('行数', { count: 1 }), '1 line');
  assert.equal(i18n.t('行数', { count: 7 }), '7 lines');
  assert.equal(i18n.t('当前版本 {version}', { version: '<v1.2.3>' }), 'Installed version <v1.2.3>');
  assert.equal(i18n.t('my-device.example.ts.net'), 'my-device.example.ts.net');
  assert.equal(i18n.t('Rule-based'), 'Rule-based');
  assert.equal(i18n.t('Global (Kill-Switch)'), 'Global (Kill-Switch)');
  i18n.setLanguage('zh');
  assert.equal(i18n.t('Rule-based'), '规则分流');
  assert.equal(i18n.t('Global (Kill-Switch)'), '全局（Kill-Switch）');
  assert.equal(i18n.t('关闭'), '关闭');
  assert.equal(i18n.t('行数', { count: 1 }), '1 行');
  assert.equal(i18n.t('Save & apply'), '保存并应用');
});

test('Global Add consent explains the protection loss and failure behavior in both languages', () => {
  const dialog = html.match(/<dialog id="add-exit-consent-dialog"[\s\S]*?<\/dialog>/)?.[0];
  assert.ok(dialog);
  assert.match(dialog, /aria-labelledby="add-exit-consent-title" aria-describedby="add-exit-consent-description"/);
  assert.match(dialog, /<form method="dialog">/);
  assert.match(dialog, /value="cancel" autofocus/);
  const title = dialog.match(/<h2[^>]*>([^<]+)<\/h2>/)[1];
  const description = dialog.match(/<p[^>]*>([^<]+)<\/p>/)[1];
  const confirm = dialog.match(/value="add">([^<]+)<\/button>/)[1];
  const { i18n } = languagePage(['en']);
  assert.equal(i18n.t(title), 'Disconnect Global protection and add an exit?');
  assert.equal(i18n.t(confirm), 'Disconnect and add exit');
  assert.match(i18n.t(description), /turns off Global \(Kill-Switch\) protection/);
  assert.match(i18n.t(description), /traffic may go direct/);
  assert.match(i18n.t(description), /stays disconnected even if saving or sign-in fails/);
  assert.match(i18n.t(description), /Reconnect manually/);
  i18n.setLanguage('zh');
  for (const text of [title, description, confirm]) assert.equal(i18n.t(text), text);
});

test('removal consent retains original rules and explains replacement and network restoration', () => {
  const dialog = html.match(/<dialog id="remove-exit-dialog"[\s\S]*?<\/dialog>/)?.[0];
  assert.ok(dialog);
  const description = dialog.match(/<p[^>]*>([^<]+)<\/p>/)[1];
  const { i18n } = languagePage(['en']);
  const english = i18n.t(description);
  assert.match(english, /Sign out only this device and remove its local exit profile/);
  assert.match(english, /current connection stops and system networking is restored/);
  assert.match(english, /Existing rules are kept/);
  assert.match(english, /choose a replacement for rules that use this exit before reconnecting/);
  assert.match(english, /device record stays in the Tailscale admin console/);
  i18n.setLanguage('zh');
  const chinese = i18n.t(description);
  assert.match(chinese, /仅注销该设备并移除其本地出口配置/);
  assert.match(chinese, /当前连接将断开并恢复系统网络/);
  assert.match(chinese, /原有规则会保留/);
  assert.match(chinese, /引用此出口的规则须选择替代出口后才能重新连接/);
  assert.match(chinese, /管理后台的设备记录会保留/);
});

test('static translation preserves text nodes and skips code and detached nodes', () => {
  const label = { nodeValue: '  连接设置 ', isConnected: true, parentElement: { closest: () => null } };
  const code = { nodeValue: '连接设置', isConnected: true, parentElement: { closest: () => ({}) } };
  const detached = { nodeValue: '网络', isConnected: false, parentElement: { closest: () => null } };
  const { i18n } = languagePage(['en'], [label, code, detached]);
  assert.equal(label.nodeValue, '  Connection ');
  assert.equal(code.nodeValue, '连接设置');
  assert.equal(detached.nodeValue, '网络');
  i18n.setLanguage('zh');
  assert.equal(label.nodeValue, '  连接设置 ');
});
