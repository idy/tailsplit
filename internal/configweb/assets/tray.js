(() => {
  'use strict';

  // Overall connection state and per-device identity are deliberately separate.
  // A running helper's traffic_exit takes precedence over later saved settings.
  function popupView(document, live) {
    const values = document?.values || {};
    const active = Boolean(live?.active);
    const carrying = active && ['tun', 'proxy'].includes(live.mode);
    const mode = carrying && live.routing_mode ? live.routing_mode : values.rules?.mode || 'split';
    const selected = (carrying && live.traffic_exit) || String(values.rules?.[mode === 'global' ? 'global_exit' : 'default_exit'] ?? 'main').trim();
    const profile = selected === 'main' ? values.tailscale : (values.exits || []).find(exit => exit.id === selected);
    const device = selected === 'main' ? live || {} : live?.exits?.[selected] || {};
    const target = carrying ? device.exit || profile?.exit_node : profile?.exit_node;
    const node = (device.exit_nodes || []).find(node => node.id === target || node.name === target);
    const gateway = profile?.name || node?.name || target || '';
    const setup = !active && (!document?.valid || !profile || !target);
    let status = '未连接', detail = '按已保存的配置连接', tone = '';
    if (active) {
      tone = 'warn';
      status = '正在连接';
      detail = '可以随时取消';
      if (carrying && live.state === 'running') {
        status = '已连接'; tone = 'good';
        detail = live.mode === 'tun' ? '系统流量已接管' : '应用代理已开启';
      } else if (live.mode === 'login' || live.state === 'needs_login') {
        status = '等待登录'; detail = '请在浏览器中完成授权';
      } else if (live.mode === 'exits') {
        status = '正在读取出口';
      } else if (live.state === 'stopping') {
        status = '正在断开'; detail = '正在恢复系统网络';
      }
    } else if (setup) {
      detail = document?.valid ? '选择出口后即可连接' : '请先检查配置';
    }
    const states = {
      blocked: ['出口不可用 · 已阻断', 'error'],
      'waiting-exit': ['等待出口上线', 'warn'],
      'missing-exit': ['请重新选择出口', 'warn'],
      authorizing: ['等待安装后台服务', 'warn'],
      authenticating: ['正在登录', 'warn'],
      'logging-out': ['正在退出登录', 'warn'],
      failed: ['连接异常', 'error'],
    };
    if (states[live?.state]) [status, tone] = states[live.state];
    if (live?.error) { status = '连接异常'; tone = 'error'; }
    return { active, carrying, mode, selected, gateway, setup, status, detail, tone };
  }

  // The same pure projection is exercised by Node regression tests.
  if (typeof module !== 'undefined' && module.exports) { module.exports = { popupView }; return; }
  const $ = selector => document.querySelector(selector);
  const locale = window.TailSplitI18n;
  const t = key => locale.t(key);
  const fragment = new URLSearchParams(location.hash.slice(1));
  let token = fragment.get('token');
  try {
    if (token) sessionStorage.setItem('tailsplit-config-token', token);
    else token = sessionStorage.getItem('tailsplit-config-token');
  } catch (_) { /* The fragment also works in private WebView sessions. */ }
  if (location.hash) history.replaceState(null, '', location.pathname);
  let saved = null, live = null, pending = false, reading = false, generation = 0;
  let loadError = '', actionError = '', timer;
  const text = (selector, value) => { if ($(selector).textContent !== value) $(selector).textContent = value; };
  const errorText = error => error.messages?.[locale.language] || error.message || t('请求未完成，请重试。');

  async function api(path, data) {
    const response = await fetch(`/api/${path}`, {
      method: data === undefined ? 'GET' : 'POST', cache: 'no-store',
      headers: { Authorization: `Bearer ${token || ''}`, 'Accept-Language': locale.language,
        ...(data === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: data === undefined ? undefined : JSON.stringify(data),
      signal: AbortSignal.timeout(15000),
    });
    const value = await response.json();
    if (!response.ok) {
      const error = new Error(value.error || t('请求未完成，请重试。'));
      error.messages = value.error_i18n;
      throw error;
    }
    return value;
  }

  function render() {
    const view = popupView(saved, live);
    const unavailable = Boolean(loadError);
    text('#status', t(unavailable ? '状态不可用' : view.status));
    text('#connection-detail', t(unavailable ? '请重试或打开主界面' : pending ? '正在处理…' : view.detail));
    $('#status-dot').dataset.tone = unavailable ? 'error' : view.tone;
    $('#connect').hidden = view.setup && !unavailable;
    $('#setup').hidden = !view.setup || unavailable;
    $('#setup').disabled = pending;
    $('#connect').disabled = !live?.desktop || pending || unavailable || !saved || live.state === 'stopping';
    $('#connect').setAttribute('aria-checked', String(view.active));
    $('#connect').setAttribute('aria-label', t(view.active ? view.status === '已连接' ? '断开' : '取消' : '连接 TailSplit'));
    $('#connect').title = view.active ? t('断开后恢复系统网络') : t('连接 TailSplit');
    text('#routing-mode', saved ? t(view.mode === 'global' ? 'Global (Kill-Switch)' : 'Rule-based') : '—');
    text('#exit-label', t(view.mode === 'global' ? '全局出口' : '默认出口'));
    text('#exit-name', view.gateway || t('未设置'));
    $('#exit-name').title = view.gateway || t('未设置');
    $('#preview-note').hidden = !live?.preview;
    $('#quit').disabled = !live?.quit_available || pending || unavailable;
    $('#quit').title = t('退出后恢复系统网络');
    const error = loadError || actionError || live?.error || '';
    $('#error').hidden = !error;
    text('#error-message', error);
    $('#retry').disabled = reading || pending;
  }

  async function refresh() {
    if (reading || pending || document.hidden && document.documentElement.dataset.trayReady === 'true') return;
    reading = true;
    const requestGeneration = generation;
    try {
      const [nextSaved, nextLive, language] = await Promise.all([api('config'), api('state'), api('language')]);
      if (requestGeneration !== generation) return;
      // A save racing these reads must not combine a stale gateway with new state.
      if (nextSaved.revision !== nextLive.revision) throw new Error(t('配置文件已更新，请重新读取后重试。'));
      saved = nextSaved; live = nextLive; loadError = '';
      locale.setLanguage(language.language);
    } catch (error) { loadError = errorText(error); }
    finally {
      reading = false;
      render();
      document.documentElement.dataset.trayReady = 'true';
    }
  }

  async function action(name) {
    if (pending) return false;
    generation++; pending = true; actionError = ''; render();
    try {
      await api('action', { action: name });
      return true;
    } catch (error) { actionError = errorText(error); return false; }
    finally { pending = false; render(); }
  }

  async function openPanel() {
    if (live?.open_panel_available) {
      if (await action('open-panel')) window.mygo?.window.close().catch(() => {});
    } else {
      // Standalone previews have no native window to reveal. Keep the same
      // fragment-only capability when navigating to their full panel.
      location.assign('/#token=' + encodeURIComponent(token || ''));
    }
  }
  document.querySelectorAll('[data-open-panel]').forEach(button => button.addEventListener('click', openPanel));
  $('#setup').addEventListener('click', openPanel);
  $('#connect').addEventListener('click', async () => {
    if ($('#connect').disabled) return;
    if (await action(live.active ? 'disconnect' : 'connect')) await refresh();
  });
  $('#quit').addEventListener('click', () => { if (!$('#quit').disabled) void action('quit'); });
  $('#retry').addEventListener('click', () => { actionError = ''; void refresh(); });
  locale.subscribe(render);
  function schedule() {
    clearTimeout(timer);
    if (!document.hidden) timer = setTimeout(async () => { await refresh(); schedule(); }, 2000);
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden) void refresh(); schedule(); });
  window.addEventListener('pagehide', () => clearTimeout(timer));
  void refresh(); schedule();
})();
