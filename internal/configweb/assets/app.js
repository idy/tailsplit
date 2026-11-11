(() => {
  'use strict';

  const $ = (selector) => document.querySelector(selector);
  const locale = window.TailSplitI18n;
  const t = (key, values) => locale.t(key, values);
  function backendError(value) {
    return value?.error_i18n ? { messages: value.error_i18n, message: value.error || '' } : value?.error || '';
  }
  function message(value) {
    if (value?.key) return t(value.key, value.values);
    const text = value && typeof value === 'object' ? value.messages?.[locale.language] ?? value.message ?? '' : value || '';
    return t(text);
  }
  // Polling should announce a status only when its text actually changes.
  function setStatusText(selector, text) {
    const element = $(selector);
    if (element.textContent !== text) element.textContent = text;
  }
  const controls = [...document.querySelectorAll('[data-field]')];
  // Keep both footer dividers level when translated actions wrap or resize.
  if (window.ResizeObserver) {
    const actionbar = $('#actionbar'), workspaceBody = $('#workspace-body');
    const observer = new window.ResizeObserver(() => {
      const height = actionbar.getBoundingClientRect().height;
      if (height > 0) document.documentElement.style.setProperty('--actionbar-height', `${height}px`);
      else document.documentElement.style.removeProperty('--actionbar-height');
      // Hidden-overflow gutters can use a different native width than styled
      // scrollbars. Match the body's actual gutter to keep centered edges aligned.
      const gutter = workspaceBody.offsetWidth - workspaceBody.clientWidth;
      document.documentElement.style.setProperty('--workspace-scrollbar-width', `${gutter}px`);
    });
    observer.observe(actionbar);
    observer.observe(workspaceBody);
  }
  const mygoWindow = window.mygo?.window;
  if (mygoWindow) {
    document.documentElement.dataset.windowChrome = window.mygo.platform === 'darwin' ? 'macos' : 'windows';
    document.documentElement.dataset.windowEngine = 'mygo';
    $('#window-controls').hidden = window.mygo.platform !== 'win32';
    const actions = { minimize: () => mygoWindow.minimize(), zoom: () => mygoWindow.toggleMaximize(), close: () => mygoWindow.close() };
    document.querySelectorAll('[data-window-action]').forEach((button) => {
      button.addEventListener('click', () => actions[button.dataset.windowAction]?.().catch((error) => console.error(error)));
    });
    // MyGo's native drag regions handle background dragging and double-clicks.
  }
  const defaults = {
    listen: '127.0.0.1:17890', interface: '', log_level: 'info',
    'tailscale.hostname': '', 'tailscale.exit_node': '', 'tailscale.tailnet': '', 'tailscale.domains': [],
    'dns.direct': 'system', 'dns.proxy': '8.8.8.8', 'dns.direct_ipv6': true,
    'dns.fake_ip': true, 'dns.magic_domains': [], 'rules.mode': 'split',
    'rules.final': 'direct', 'rules.source': '', 'rules.proxy_domains': [],
    'rules.direct_domains': [], 'rules.proxy_ips': [], 'rules.direct_ips': [],
    'tun.enabled': false, 'tun.mtu': 1400, 'app.connect_on_start': false,
    'tailscale.name': '', exits: [], 'rules.routes': [], 'rules.global_exit': 'main', 'rules.default_exit': 'main',
  };
  const state = {
    saved: '', base: '', draft: '', revision: '', changes: Object.create(null),
    mode: 'form', page: 'home', section: 'general', valid: false, parsed: false, values: {}, savedValues: {}, savedValid: false, loaded: false,
    dirty: false, busy: false, loading: false, switching: false, validating: false,
    previewing: false, external: false, savedError: '', repairable: false,
    generation: 0, previewed: 0, pending: null, timer: null, toastTimer: null,
    customRuleSource: '',
  };
  const isLocked = () => state.busy || state.loading || state.switching;

  const fragment = new URLSearchParams(location.hash.slice(1));
  let browserConnectRequest = fragment.get('connect') || '';
  let token = fragment.get('token');
  try {
    if (token) sessionStorage.setItem('tailsplit-config-token', token);
    else token = sessionStorage.getItem('tailsplit-config-token');
  } catch (_) { /* The current tab can still use the fragment in private mode. */ }
  const nativeJournalScopeKey = 'tailsplit-native-journal-scope';
  const validJournalScope = value => typeof value === 'string' && /^[A-Za-z0-9_-]{16,64}$/.test(value);
  let nativeJournalScope = validJournalScope(fragment.get('journal')) ? fragment.get('journal') : '';
  let nativeJournalBackup = fragment.get('journal_backup') === '1';
  let nativeCancelHost = fragment.get('cancel_host') === '1';
  let nativeCancelGuard = fragment.get('cancel_guard') === '1';
  if (mygoWindow) {
    try {
      if (fragment.has('journal')) {
        if (nativeJournalScope) sessionStorage.setItem(nativeJournalScopeKey, JSON.stringify({ token, scope: nativeJournalScope, backup: nativeJournalBackup, host: nativeCancelHost, guard: nativeCancelGuard }));
        else sessionStorage.removeItem(nativeJournalScopeKey);
      } else {
        const remembered = JSON.parse(sessionStorage.getItem(nativeJournalScopeKey) || 'null');
        if (remembered?.token === token && validJournalScope(remembered.scope)) {
          nativeJournalScope = remembered.scope;
          nativeJournalBackup = remembered.backup === true;
          nativeCancelHost = remembered.host === true;
          nativeCancelGuard = remembered.guard === true;
        } else sessionStorage.removeItem(nativeJournalScopeKey);
      }
    } catch (_) { /* The fragment still supplies this native load's scope and negotiated store. */ }
  }
  const nativeJournalPrefix = mygoWindow && nativeJournalScope ? `tailsplit-native-cancel-${nativeJournalScope}-` : '';
  const browserConnectKey = 'tailsplit-browser-connect';
  function rememberBrowserConnect(request) {
    browserConnectRequest = request;
    try {
      if (request) sessionStorage.setItem(browserConnectKey, JSON.stringify({ token, request }));
      else sessionStorage.removeItem(browserConnectKey);
    } catch (_) { /* The current page can still handle its URL-bound intent. */ }
  }
  function forgetBrowserConnect(request) {
    if (browserConnectRequest === request) rememberBrowserConnect('');
  }
  try {
    if (!fragment.has('connect')) {
      const remembered = JSON.parse(sessionStorage.getItem(browserConnectKey) || 'null');
      if (remembered?.token === token && typeof remembered.request === 'string') browserConnectRequest = remembered.request;
    }
    rememberBrowserConnect(browserConnectRequest);
  } catch (_) { /* Malformed or unavailable storage never grants an intent. */ }
  if (location.hash) history.replaceState(null, '', location.pathname);

  async function api(path, data) {
    // No native intent may be claimed or helper started without a live recovery
    // sink. Cancellation and disconnection remain available in degraded storage.
    const startsOperation = path === 'connect-request' || path === 'action' &&
      ['connect', 'add-exit', 'login', 'login-exit', 'switch-network', 'refresh-exits'].includes(data?.action) ||
      path === 'config' && data !== undefined;
    if (mygoWindow && startsOperation && !nativeRecoveryAvailable({
      request: path === 'connect-request' ? data?.request || '' : '',
      operation_id: data?.operation_id || desktopLive?.operation_id || '',
    })) throw nativeRecoveryError();
    const response = await fetch(`/api/${path}`, {
      method: data === undefined ? 'GET' : 'POST',
      headers: { Authorization: `Bearer ${token || ''}`, 'Accept-Language': locale.language, ...(mygoWindow && ['state', 'connect-request'].includes(path) ? { 'X-TailSplit-Panel': 'native' } : {}), ...(data === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: data === undefined ? undefined : JSON.stringify(data),
      cache: 'no-store',
    });
    const value = await response.json();
    if (!response.ok) {
      const error = new Error(value.error || t('请求未完成，请重试。'));
      error.code = value.code;
      error.messages = value.error_i18n;
      throw error;
    }
    return value;
  }

  window.TailSplitCompanionPreferences = {
    load: () => api('companion'),
    save: (enabled) => api('companion', { enabled }),
  };

  let activeNotice = null;
  // Operation failures survive automatic document reloads until a retry or explicit reload.
  let operationError = null;
  let activeToast = '';
  function notice(title, detail, kind = 'error', canReload = false) {
    activeNotice = { title, detail };
    $('#notice-title').textContent = t(title);
    $('#notice-detail').textContent = message(detail);
    $('#notice').classList.toggle('error', kind === 'error');
    $('#notice').classList.toggle('success', kind === 'success');
    $('#notice-reload').hidden = !canReload;
    $('#notice').hidden = false;
  }

  function clearNotice() {
    if (operationError) {
      notice(operationError.title, operationError.detail, 'error', operationError.canReload || state.external);
    } else if (state.external) {
      notice(t('磁盘中的配置已更新'), t('当前草稿仍保留。重新读取最新配置后再保存，避免覆盖其他更改。'), 'warning', true);
    } else if (state.loaded && !state.savedValid) {
      notice('配置需要修正', state.savedError || t('请检查 YAML 内容。'));
    } else {
      activeNotice = null;
      $('#notice').hidden = true;
    }
  }

  function toast(message) {
    clearTimeout(state.toastTimer);
    activeToast = message;
    $('#toast').textContent = t(message);
    $('#toast').hidden = false;
    state.toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 3200);
  }

  function fieldValue(values, path) {
    // Read existing YAML without rewriting it until the source is edited.
    if (path === 'rules.source' && values.rules && values.rules.source == null) {
      return values.rules.gfwlist ?? defaults[path];
    }
    let value = values;
    for (const key of path.split('.')) {
      if (value === null || typeof value !== 'object' || !Object.hasOwn(value, key)) return defaults[path];
      value = value[key];
    }
    return value === null || value === undefined ? defaults[path] : value;
  }

  // rules.final can name any configured exit; rebuild its choices from the draft.
  function renderFinalOptions(values) {
    const select = $('#final-route');
    const current = String(fieldValue(values, 'rules.final'));
    const target = String(state.changes['rules.default_exit'] ?? fieldValue(values, 'rules.default_exit'));
    const options = [['direct', t('直连')], ['proxy', `${exitLabel(target, values)} · ${t('默认出口')}`],
      ...configuredExits(values).map((exit) => [exit.id, exitLabel(exit.id, values)])];
    if (!options.some(([value]) => value === current)) options.push([current, current]);
    select.replaceChildren(...options.map(([value, label]) => {
      const option = document.createElement('option'); option.value = value; option.textContent = label; return option;
    }));
  }
  function renderExitOptions(values, selector, field) {
    const select = $(selector);
    const current = String(fieldValue(values, field));
    const records = configuredExits(values);
    const choices = [['-', t('未设置'), ''], ...records.map((exit) => [exit.id, exitLabel(exit.id, values), exit.exit_node])];
    if (!choices.some(([id]) => id === current)) choices.push([current, t('未设置'), '']);
    select.replaceChildren(...choices.map(([id, name, node]) => {
      const option = document.createElement('option'); option.value = id;
      option.textContent = records.some((exit) => exit.id === id) && !node ? `${name} · ${t('尚未选择出口')}` : name;
      option.disabled = id !== '-' && !node; return option;
    }));
  }
  function draftRoutingMode() {
    return (state.changes['rules.mode'] ?? fieldValue(state.values, 'rules.mode')) === 'global' ? 'global' : 'split';
  }
  let routesDraft = [];
  const lines = (text) => text.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  function renderRoutes(values) {
    const routes = fieldValue(values, 'rules.routes');
    routesDraft = (Array.isArray(routes) ? routes : []).filter((route) => route && typeof route === 'object').map((route) => ({
      exit: String(route.exit || 'main'),
      domains: Array.isArray(route.domains) ? route.domains.map(String) : [],
      ips: Array.isArray(route.ips) ? route.ips.map(String) : [],
    }));
    paintRoutes(values);
  }
  function editRoutes() {
    if (isLocked()) return;
    state.changes['rules.routes'] = routesDraft.map((route) => ({
      exit: route.exit, ...(route.domains.length ? { domains: route.domains } : {}), ...(route.ips.length ? { ips: route.ips } : {}),
    }));
    schedulePreview();
  }
  function paintRoutes(values = state.values) {
    const choices = configuredExits(values).map((exit) => [exit.id, exitLabel(exit.id, values)]);
    const fragment = document.createDocumentFragment();
    routesDraft.forEach((route, index) => {
      const group = document.createElement('div'); group.className = 'route-group';
      const header = document.createElement('div'); header.className = 'route-group-header';
      const label = document.createElement('label'); label.setAttribute('for', `route-${index}-exit`); label.textContent = t('出口');
      const select = document.createElement('select'); select.id = `route-${index}-exit`;
      const known = choices.some(([id]) => id === route.exit) ? choices : [...choices, [route.exit, route.exit]];
      select.replaceChildren(...known.map(([id, name]) => {
        const option = document.createElement('option'); option.value = id; option.textContent = name; return option;
      }));
      select.value = route.exit;
      select.addEventListener('change', () => { route.exit = select.value; editRoutes(); });
      const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'link-button danger'; remove.textContent = t('删除');
      remove.setAttribute('aria-label', t('删除规则组 {index}', { index: index + 1 }));
      remove.addEventListener('click', () => { routesDraft.splice(index, 1); paintRoutes(); editRoutes(); });
      header.append(label, select, remove);
      const pair = document.createElement('div'); pair.className = 'rule-pair';
      for (const [key, title, placeholder] of [['domains', t('域名'), 'example.com'], ['ips', 'CIDR', '203.0.113.0/24']]) {
        const cell = document.createElement('div');
        const caption = document.createElement('label'); caption.setAttribute('for', `route-${index}-${key}`); caption.textContent = title;
        const area = document.createElement('textarea'); area.id = `route-${index}-${key}`; area.className = 'mono'; area.rows = 3;
        area.spellcheck = false; area.placeholder = placeholder; area.value = route[key].join('\n');
        area.addEventListener('input', () => { route[key] = lines(area.value); editRoutes(); });
        cell.append(caption, area); pair.append(cell);
      }
      group.append(header, pair); fragment.append(group);
    });
    $('#route-groups').replaceChildren(fragment);
    $('#route-groups').hidden = routesDraft.length === 0;
  }
  $('#add-route').addEventListener('click', () => {
    if (isLocked() || state.mode !== 'form' || draftRoutingMode() === 'global') return;
    const extra = exitList(state.values)[0];
    routesDraft.push({ exit: extra ? extra.id : 'main', domains: [], ips: [] });
    paintRoutes(); editRoutes();
  });

  function fillFields(values) {
    renderFinalOptions(values);
    renderExitOptions(values, '#global-exit', 'rules.global_exit');
    renderExitOptions(values, '#default-exit', 'rules.default_exit');
    renderRoutes(values);
    for (const control of controls) {
      const value = fieldValue(values, control.dataset.field);
      if (control.type === 'checkbox') control.checked = value === true;
      else if (control.dataset.kind === 'list') control.value = Array.isArray(value) ? value.join('\n') : '';
      else control.value = String(value ?? '');
    }
    const source = $('#rule-source').value;
    $('#rule-source-type').value = source === 'builtin' ? 'builtin' : source ? 'custom' : '';
    if (source && source !== 'builtin') state.customRuleSource = source;
    updateRuleSourceMode();
    updateGlobalMode();
  }

  function ruleSourceMissing() {
    return state.mode === 'form' && draftRoutingMode() === 'split' && $('#rule-source-type').value === 'custom' && !$('#rule-source').value.trim();
  }

  function updateRuleSourceMode() {
    const mode = $('#rule-source-type').value;
    $('#custom-rule-source').hidden = mode !== 'custom';
    $('#custom-rule-source-note').hidden = mode !== 'custom';
    $('#builtin-rule-hint').hidden = mode !== 'builtin';
    $('#rule-source-type').disabled = isLocked();
    $('#rule-source-error').hidden = !ruleSourceMissing();
    $('#rule-source').setAttribute('aria-invalid', String(ruleSourceMissing()));
  }

  function updateGlobalMode() {
    const mode = draftRoutingMode();
    const global = mode === 'global';
    $('#global-hint').hidden = !global;
    $('#tun-enabled').disabled = global || isLocked();
    $('#global-exit-row').hidden = !global;
    $('#global-rules-note').hidden = !global;
    const hasExits = configuredExits(state.values).length > 0;
    $('#global-exit').disabled = !global || isLocked() || !hasExits;
    $('#default-exit').disabled = global || isLocked() || !hasExits;
    for (const row of document.querySelectorAll('[data-primary-field]')) row.hidden = !state.values.tailscale;
    for (const id of ['split-routing-options', 'split-routing-fields']) {
      $(`#${id}`).hidden = global;
      $(`#${id}`).disabled = global || isLocked();
    }
    $('#split-routing-notes').hidden = global;
    for (const button of document.querySelectorAll('[data-routing-mode]')) {
      const selected = button.dataset.routingMode === mode;
      button.classList.toggle('selected', selected);
      button.setAttribute('aria-pressed', String(selected));
      button.disabled = isLocked() || !state.loaded;
    }
  }

  function updateActions() {
    const locked = isLocked();
    const sourceMissing = ruleSourceMissing();
    $('#save').disabled = !state.loaded || !state.dirty || !state.valid || sourceMissing || locked || state.previewing || state.external;
    $('#discard').disabled = !state.dirty || locked;
    $('#validate').disabled = !state.loaded || sourceMissing || locked || state.validating || state.previewing;
    $('#reload').disabled = locked;
    $('#notice-edit').hidden = $('#notice-rebuild').hidden = !state.loaded || state.savedValid;
    $('#notice-edit').disabled = locked || Boolean(recoveryAction);
    $('#notice-repair').hidden = !state.loaded || state.savedValid || !state.repairable;
    $('#notice-repair').disabled = !canRecoverConfig() || !state.repairable;
    $('#notice-repair').textContent = t(recoveryAction === 'repair' ? '正在修复…' : '自动修复配置…');
    $('#notice-rebuild').disabled = !canRecoverConfig();
    $('#notice-rebuild').textContent = t(recoveryAction === 'rebuild' ? '正在重建…' : '备份并重建配置…');
    $('#save').textContent = state.busy ? t('正在应用…') : t('保存并应用');
    $('#save-state').classList.toggle('dirty', state.dirty);
    $('#save-state').classList.toggle('invalid', (!state.valid || sourceMissing) && state.loaded && !state.previewing);
    $('#save-state span:last-child').textContent = state.busy ? t('正在保存并应用配置') :
      state.previewing ? t('正在检查更改…') : (!state.valid || sourceMissing) && state.loaded ? t('配置需要修正') :
      state.dirty ? t('有未保存的更改') : t('所有更改已保存');
    $('#preview-draft').textContent = state.dirty ? t('草稿') : t('已保存');
    $('#preview-draft').classList.toggle('dirty', state.dirty);
    for (const control of controls) control.disabled = locked;
    $('#add-route').disabled = locked || state.mode !== 'form' || draftRoutingMode() === 'global' || configuredExits(state.values).length === 0;
    for (const control of document.querySelectorAll('#route-groups select, #route-groups textarea, #route-groups button')) control.disabled = locked;
    $('#log-level').disabled = locked || state.mode === 'yaml' && (!state.parsed || state.previewing);
    $('#raw-yaml').disabled = locked;
    for (const button of document.querySelectorAll('.editor-tabs button, [data-section]')) button.disabled = locked;
    $('#settings-section-select').disabled = locked;
    $('#settings-tools').hidden = state.page !== 'settings';
    $('#reload').hidden = state.page !== 'settings';
    $('#refresh-exits').hidden = !['home', 'connection'].includes(state.page);
    $('#desktop-log-refresh').hidden = state.page !== 'logs';
    setStatusText('#sidebar-proxy', String(fieldValue(state.savedValues, 'listen') || '127.0.0.1:17890'));
    $('#settings-draft').hidden = !state.dirty;
    $('#actionbar').hidden = !state.loaded || (state.page !== 'settings' && !state.dirty);
    updateRuleSourceMode();
    updateGlobalMode();
    if (state.loaded) renderConnectionCenter();
    window.TailSplitSelect?.sync();
  }

  function paintPreview() {
    const lines = state.draft.split('\n');
    const fragment = document.createDocumentFragment();
    for (const line of lines) {
      const row = document.createElement('span');
      row.className = 'code-line';
      if (line.trimStart().startsWith('#')) {
        const comment = document.createElement('span');
        comment.className = 'code-comment'; comment.textContent = line; row.append(comment);
      } else {
        const match = line.match(/^(\s*)([\w.-]+:)(.*)$/);
        if (match) {
          row.append(document.createTextNode(match[1]));
          const key = document.createElement('span'); key.className = 'code-key'; key.textContent = match[2];
          const value = document.createElement('span'); value.className = 'code-value'; value.textContent = match[3];
          row.append(key, value);
        } else row.textContent = line || '​';
      }
      fragment.append(row);
    }
    $('#yaml-preview').replaceChildren(fragment);
    $('#line-count').textContent = t('行数', { count: lines.length });
  }

  function useDocument(doc) {
    acceptSetupDocument(doc);
    clearTimeout(state.timer); state.timer = null; state.generation++;
    state.previewed = state.generation; state.pending = null;
    state.saved = state.base = state.draft = doc.yaml;
    state.revision = doc.revision;
    state.values = state.savedValues = doc.values || {};
    state.savedValid = doc.valid;
    state.repairable = doc.repairable === true;
    state.savedError = backendError(doc);
    state.changes = Object.create(null);
    state.valid = doc.valid; state.parsed = doc.parsed;
    state.dirty = false; state.previewing = false; state.external = false; state.loaded = true;
    $('#file-location').title = doc.path || '';
    $('#file-location').textContent = t('当前设备');
    fillFields(state.values);
    $('#raw-yaml').value = state.draft;
    paintPreview();
    $('#loading').hidden = true; $('#editor-layout').hidden = false;
    $(`#page-${state.page}`).hidden = false;
    renderPageTitle();
    if (!doc.parsed) { showMode('yaml'); showPage('settings'); }
    if (!doc.valid) notice(t('配置需要修正'), backendError(doc) || t('请检查 YAML 内容。'));
    else clearNotice();
    updateActions();
  }

  async function load() {
    operationError = null;
    state.loading = true; updateActions();
    try {
      useDocument(await api('config'));
    } catch (error) {
      $('#loading').hidden = true;
      notice(t('无法读取本机配置'), error.message ? error : t('请从 TailSplit 菜单重新打开页面。'));
    } finally { state.loading = false; updateActions(); }
    await refreshState();
  }

  function showMode(mode) {
    state.mode = mode;
    const raw = mode === 'yaml';
    $('#form-editor').hidden = raw;
    $('.yaml-preview').hidden = raw;
    $('#yaml-editor').hidden = !raw;
    $('#editor-layout').classList.toggle('raw-mode', raw);
    $('#form-tab').classList.toggle('selected', !raw);
    $('#yaml-tab').classList.toggle('selected', raw);
    $('#form-tab').setAttribute('aria-selected', String(!raw));
    $('#yaml-tab').setAttribute('aria-selected', String(raw));
    $('#form-tab').tabIndex = raw ? -1 : 0;
    $('#yaml-tab').tabIndex = raw ? 0 : -1;
    $('#settings-subnav').classList.toggle('raw-mode', raw);
    renderPageTitle();
  }

  async function runPreview() {
    const generation = state.generation;
    const raw = state.mode === 'yaml';
    const data = raw ? { yaml: state.draft, ...(Object.keys(state.changes).length ? { changes: state.changes } : {}) } : { yaml: state.base, changes: state.changes };
    const promise = api('preview', data);
    state.pending = { generation, promise };
    try {
      const doc = await promise;
      if (generation !== state.generation) return;
      state.draft = doc.yaml; state.values = doc.values || {};
      state.previewed = generation;
      state.valid = doc.valid; state.parsed = doc.parsed;
      state.dirty = state.draft !== state.saved;
      if (raw) {
        state.base = state.draft; state.changes = Object.create(null);
        $('#raw-yaml').value = state.draft;
        if (doc.parsed) $('#log-level').value = String(fieldValue(state.values, 'log_level'));
      }
      paintPreview();
      if (doc.valid) clearNotice();
      else notice(t('配置需要修正'), backendError(doc) || t('请检查字段内容。'));
    } catch (error) {
      if (generation !== state.generation) return;
      state.valid = false;
      notice(t('暂时无法检查配置'), error.message ? error : t('请确认 TailSplit 仍在运行。'));
    } finally {
      if (generation === state.generation) {
        state.previewing = false;
        state.pending = null;
        updateActions();
      }
    }
  }

  function schedulePreview() {
    state.generation++; state.dirty = true; state.previewing = true;
    clearTimeout(state.timer);
    state.timer = setTimeout(() => { state.timer = null; runPreview(); }, 300);
    updateActions();
  }

  async function flushPreview() {
    while (state.previewed !== state.generation) {
      const generation = state.generation;
      if (state.timer) {
        clearTimeout(state.timer); state.timer = null;
        await runPreview();
      } else if (state.pending?.generation === generation) {
        try { await state.pending.promise; } catch (_) { /* runPreview reports it. */ }
      } else {
        await runPreview();
      }
      if (generation === state.generation) return state.previewed === generation;
    }
    return true;
  }

  async function editField(control) {
    if (isLocked()) return;
    const previous = fieldValue(state.values, control.dataset.field);
    let value;
    if (control.type === 'checkbox') value = control.checked;
    else if (control.dataset.kind === 'list') value = control.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
    else if (control.dataset.kind === 'int') value = control.value === '' ? 0 : Number(control.value);
    else value = control.value;
    state.changes[control.dataset.field] = value;
    if (control.dataset.field === 'rules.default_exit') {
      const final = $('#final-route').value;
      renderFinalOptions(state.values); $('#final-route').value = final;
    }
    if (control.dataset.field === 'rules.source' && $('#rule-source-type').value === 'custom') {
      state.customRuleSource = value;
    }
    if (control.dataset.field === 'rules.mode' && value === 'global') {
      state.changes['tun.enabled'] = true;
      $('#tun-enabled').checked = true;
    }
    updateGlobalMode(); schedulePreview();
    if (state.mode === 'yaml') {
      // Logs remains editable outside Settings; patch the latest source before
      // allowing further raw edits, rather than silently dropping the field.
      state.switching = true; updateActions();
      try {
        if (!(await flushPreview())) {
          state.changes = Object.create(null);
          if (control.type === 'checkbox') control.checked = previous === true;
          else control.value = Array.isArray(previous) ? previous.join('\n') : String(previous ?? '');
          state.dirty = state.draft !== state.saved;
        }
      } finally { state.switching = false; updateActions(); }
    }
  }
  for (const control of controls) control.addEventListener('input', () => editField(control));
  $('#rule-source-type').addEventListener('change', async () => {
    if (isLocked()) return;
    const mode = $('#rule-source-type').value;
    $('#rule-source').value = mode === 'custom' ? state.customRuleSource : mode;
    updateRuleSourceMode();
    await editField($('#rule-source'));
  });
  $('#raw-yaml').addEventListener('input', (event) => {
    if (isLocked()) return;
    state.draft = event.target.value; schedulePreview();
  });
  $('#raw-yaml').addEventListener('keydown', (event) => {
    if (event.key !== 'Tab') return;
    event.preventDefault();
    const input = event.target;
    input.setRangeText('  ', input.selectionStart, input.selectionEnd, 'end');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });

  async function switchMode(mode) {
    if (isLocked()) return false;
    if (mode === state.mode) return true;
    state.switching = true; updateActions();
    try {
      if (!(await flushPreview())) {
        notice(t('暂时无法切换编辑方式'), t('请先修正表单输入或重试，当前草稿会保留。'));
        return false;
      }
      if (mode === 'form' && !state.parsed) {
        notice(t('暂时无法切换到表单'), t('请先修正 YAML 语法，原文草稿会保留。'));
        return false;
      }
      state.base = state.draft; state.changes = Object.create(null);
      if (mode === 'yaml') $('#raw-yaml').value = state.draft;
      else fillFields(state.values);
      showMode(mode); return true;
    } finally { state.switching = false; updateActions(); }
  }
  $('#form-tab').addEventListener('click', () => switchMode('form'));
  $('#yaml-tab').addEventListener('click', () => switchMode('yaml'));
  $('.editor-tabs').addEventListener('keydown', async (event) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const mode = event.key === 'Home' ? 'form' : event.key === 'End' ? 'yaml' : state.mode === 'form' ? 'yaml' : 'form';
    if (await switchMode(mode)) $(`#${mode}-tab`).focus();
  });
  function showPage(name) {
    const enteringLogs = name === 'logs' && state.page !== name;
    state.page = name;
    syncRouteMotion();
    for (const nav of document.querySelectorAll('[data-page]')) {
      const active = nav.dataset.page === name;
      nav.classList.toggle('active', active);
      if (active) nav.setAttribute('aria-current', 'page');
      else nav.removeAttribute('aria-current');
    }
    for (const page of document.querySelectorAll('.app-page')) page.hidden = page.id !== `page-${name}`;
    renderPageTitle();
    updateActions();
    if (name === 'logs') {
      if (enteringLogs) logsFollow = true;
      showLatestDesktopLogs(); refreshDesktopLogs();
    }
  }
  function renderPageTitle() {
    const labels = { home: t('首页'), connection: t('出口节点列表'), logs: t('日志'), settings: t('设置') };
    // Settings reads as a parent/child path, matching its sidebar submenu.
    const nested = state.page === 'settings';
    $('#crumb-parent').hidden = !nested;
    $('#crumb-sep').hidden = !nested;
    const section = [...document.querySelectorAll('[data-section]')].find((item) => item.classList.contains('selected'));
    $('#crumb-section').textContent = !nested ? labels[state.page] :
      state.mode === 'yaml' ? t('YAML 原文') : section?.textContent || labels.settings;
  }
  for (const nav of document.querySelectorAll('[data-page]')) nav.addEventListener('click', () => showPage(nav.dataset.page));
  async function showSection(nav) {
    if (!(await switchMode('form'))) {
      $('#settings-section-select').value = state.section;
      window.TailSplitSelect?.sync();
      return false;
    }
    for (const item of document.querySelectorAll('[data-section]')) {
      item.classList.toggle('selected', item === nav);
      if (item === nav) item.setAttribute('aria-current', 'page');
      else item.removeAttribute('aria-current');
    }
    for (const page of document.querySelectorAll('.settings-page')) page.hidden = page.id !== `section-${nav.dataset.section}`;
    state.section = nav.dataset.section;
    $('#settings-section-select').value = state.section;
    renderPageTitle();
    window.TailSplitSelect?.sync();
    return true;
  }
  for (const nav of document.querySelectorAll('[data-section]')) nav.addEventListener('click', () => showSection(nav));
  // Narrow windows collapse the sidebar to icons, so the submenu becomes a picker.
  $('#settings-section-select').addEventListener('change', (event) => {
    const nav = [...document.querySelectorAll('[data-section]')].find((item) => item.dataset.section === event.target.value);
    if (nav) showSection(nav);
  });
  $('#view-settings-draft').addEventListener('click', () => showPage('settings'));

  async function reload() {
    if (isLocked()) return;
    if (state.dirty) {
      const dialog = $('#discard-dialog');
      dialog.returnValue = 'cancel'; dialog.showModal();
      const answer = await new Promise((resolve) => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));
      if (answer !== 'discard') return;
    }
    if (!isLocked()) await load();
  }
  $('#reload').addEventListener('click', reload);
  $('#discard').addEventListener('click', reload);
  $('#notice-reload').addEventListener('click', reload);
  $('#notice-edit').addEventListener('click', () => { showMode('yaml'); showPage('settings'); $('#raw-yaml').focus(); });

  let recoveryAction = '';
  function canRecoverConfig() {
    return state.loaded && !state.savedValid && state.revision && !state.external && !isLocked() &&
      !recoveryAction && !isDesktopBusy() && !quickBusy && !connectionLost;
  }
  async function recoverConfig(action) {
    if (!canRecoverConfig() || action === 'repair' && !state.repairable) return;
    const revision = state.revision, generation = state.generation;
    let recovered = false;
    const repair = action === 'repair';
    const dialog = $(repair ? '#repair-config-dialog' : '#rebuild-config-dialog');
    recoveryAction = action; updateActions();
    try {
      dialog.returnValue = 'cancel'; dialog.showModal();
      const answer = await new Promise((resolve) => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));
      if (answer !== action) return;
      if (state.revision !== revision || state.generation !== generation || state.external || isLocked() || connectionLost || isDesktopBusy()) return;
      // Confirmation explicitly covers disconnecting and losing Global protection.
      // The server rechecks inactivity under the app's operation gate.
      if (desktopLive?.active && !(await desktopAction('disconnect'))) return;
      if (state.revision !== revision || state.external || connectionLost || desktopLive?.active) return;
      operationError = null; stateRequest++;
      state.busy = true; updateActions();
      try {
        const doc = await api(`config-${action}`, { revision });
        useDocument(doc);
        pendingExitPicker = ''; pendingAddedExits.clear();
        showMode('form'); showPage('connection');
        notice(repair ? '配置已自动修复' : '配置已重建', {
          key: repair ? '已移除废弃字段 dns.listen，出口和分流规则已保留。原配置已备份至：{path}。' : '原配置已备份至：{path}。请添加出口并重新配置分流规则。',
          values: { path: doc.backup_path },
        }, 'success');
        recovered = true;
      } catch (error) {
        if (error.code === 'conflict') state.external = true;
        operationError = { title: error.code === 'conflict' ? '配置文件已更新' : repair ? '无法自动修复配置' : '无法重建配置', detail: error, canReload: error.code === 'conflict' };
        clearNotice();
      } finally { state.busy = false; await refreshState(); }
    } finally {
      recoveryAction = ''; updateActions();
      if (recovered) $('#add-exit').focus({ preventScroll: true });
    }
  }
  $('#notice-rebuild').addEventListener('click', () => recoverConfig('rebuild'));
  $('#notice-repair').addEventListener('click', () => recoverConfig('repair'));

  $('#validate').addEventListener('click', async () => {
    if (isLocked() || state.validating || !(await flushPreview())) return;
    const generation = state.generation;
    state.validating = true; updateActions();
    try {
      await api('validate', { yaml: state.draft });
      if (generation !== state.generation) return;
      state.valid = true; clearNotice(); toast(t('配置校验通过'));
    } catch (error) {
      if (generation !== state.generation) return;
      state.valid = false; notice(t('配置未通过校验'), error);
    } finally { state.validating = false; updateActions(); }
  });

  $('#save').addEventListener('click', async () => {
    if (isLocked() || !canStartNativeOperation()) return;
    state.busy = true; updateActions();
    try {
      if (!(await flushPreview()) || !state.valid || state.external) return;
      const generation = state.generation, revision = state.revision;
      if (state.draft !== state.saved && !(await disconnectForGlobalApply(fieldValue(state.values, 'rules.mode'),
        () => generation === state.generation && revision === state.revision && !state.external &&
          (!desktopLive?.revision || desktopLive.revision === revision)))) return;
      const doc = await api('config', { yaml: state.draft, revision: state.revision });
      useDocument(doc);
      toast(t('配置已保存并应用'));
      await refreshState();
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      notice(error.code === 'conflict' ? t('配置文件已更新') : t('配置未能应用'), error, 'error', error.code === 'conflict');
    } finally { state.busy = false; updateActions(); }
  });

  async function copy() {
    if (isLocked() || !(await flushPreview())) return;
    try { await navigator.clipboard.writeText(state.draft); toast(t('YAML 已复制')); }
    catch (_) { toast(t('无法访问剪贴板，请在 YAML 原文中选择并复制。')); }
  }
  $('#copy-preview').addEventListener('click', copy);
  $('#copy-raw').addEventListener('click', copy);

  let desktopPending = '';
  let desktopOperation = '';
  let quickOperation = '';
  const newOperationID = () => window.crypto.randomUUID();
  let globalConsentPending = false;
  const isDesktopBusy = () => desktopPending !== '' || globalConsentPending || Boolean(pendingSetupCancel);
  let desktopLive = null;
  let connectionLost = false;
  let quickBusy = false;
  let removingExit = false;
  let addingExit = false;
  // An explicit Connect intent may span installation, sign-in and node selection.
  // Ordinary Add/Sign in never creates this intent and therefore never connects.
  let connectionSetup = null;
  let setupAdvancing = false;
  let pendingSetupCancel = null;
  let setupCancelSending = false;
  let setupCancelError = null;
  let setupCancelJournalConfirmed = false;
  let setupCancelGeneration = 0;
  const nativeCancelGuards = new Map();
  const nativeCancelKey = cancel => JSON.stringify([cancel.request || '', cancel.operation_id || '']);
  const setupCancelKey = 'tailsplit-setup-cancel';
  const sharedCancelPrefix = 'tailsplit-setup-cancel:';
  let sharedCancelReadable = true;
  const sharedCancelKeys = cancel => [
    ...(cancel.request ? [`${sharedCancelPrefix}request:${cancel.request}`] : []),
    ...(cancel.operation_id ? [`${sharedCancelPrefix}operation:${cancel.operation_id}`] : []),
  ];
  function sharedSetupCancel(request, operationID) {
    sharedCancelReadable = true;
    // This private profile had no source writes before host negotiation. A
    // disabled origin store cannot contain a prior cancellation from this panel.
    if (mygoWindow && !nativeJournalBackup) return null;
    for (const key of sharedCancelKeys({ request, operation_id: operationID })) {
      try {
        const cancel = JSON.parse(localStorage.getItem(key) || 'null');
        if (cancel?.token === token && typeof cancel.request === 'string' && typeof cancel.operation_id === 'string' && sharedCancelKeys(cancel).includes(key)) return cancel;
      } catch (_) { sharedCancelReadable = false; }
    }
    return null;
  }
  function decodeNativeJournal(name, value) {
    if (!nativeJournalPrefix || !name?.startsWith(nativeJournalPrefix)) return [];
    try {
      const record = JSON.parse(decodeURIComponent(value));
      return record?.scope === nativeJournalScope && typeof record.request === 'string' && typeof record.operation_id === 'string' &&
        Object.keys(record).length === 3 && (record.request || record.operation_id) ? [{ name, value, record }] : [];
    } catch (_) { return []; }
  }
  function nativeCookieRecords() {
    if (!nativeJournalPrefix) return [];
    try {
      return document.cookie.split(';').flatMap(part => {
        const separator = part.indexOf('=');
        return separator < 0 ? [] : decodeNativeJournal(part.slice(0, separator).trim(), part.slice(separator + 1));
      });
    } catch (_) { return []; }
  }
  function nativeBackupRecords() {
    if (!nativeJournalPrefix || !nativeJournalBackup) return [];
    const records = [];
    try {
      for (let index = 0; index < localStorage.length; index++) {
        const name = localStorage.key(index);
        if (name?.startsWith(nativeJournalPrefix)) records.push(...decodeNativeJournal(name, localStorage.getItem(name)));
      }
    } catch (_) { /* Delivery may still be preserved by the cookie store. */ }
    return records;
  }
  function nativeJournalRecords() { return [...nativeCookieRecords(), ...nativeBackupRecords()]; }
  function nativeRecoveryAvailable(target = {}) {
    if (!mygoWindow) return true;
    if (!nativeJournalPrefix) return false;
    // The extra field invalidates this as cancellation intent. Probe at least
    // the actual target's encoded size so a tiny write cannot mask quota loss.
    const name = `${nativeJournalPrefix}probe`;
    const value = encodeURIComponent(JSON.stringify({ scope: nativeJournalScope,
      request: target.request || '', operation_id: target.operation_id || '', probe: Math.random().toString(36) }));
    let cookieStored = false, backupStored = false;
    try {
      document.cookie = `${name}=${value}; Max-Age=5; Path=/; SameSite=Strict`;
      cookieStored = document.cookie.split(';').some(part => part.trim() === `${name}=${value}`);
    } catch (_) { /* The negotiated backup can still provide recovery. */ }
    finally { try { document.cookie = `${name}=; Max-Age=0; Path=/; SameSite=Strict`; } catch (_) {} }
    if (nativeJournalBackup) {
      try {
        localStorage.setItem(name, value);
        backupStored = localStorage.getItem(name) === value;
      } catch (_) { /* Cookie confirmation can still admit this operation. */ }
      finally { try { if (localStorage.getItem(name) === value) localStorage.removeItem(name); } catch (_) {} }
    }
    return cookieStored || backupStored;
  }
  function nativeRecoveryError() {
    return new Error(t('此面板无法保存取消恢复记录，已暂停启动操作。请重新启动 TailSplit 后重试。'));
  }
  function canStartNativeOperation() {
    if (nativeRecoveryAvailable()) return true;
    operationError = { title: '操作未完成', detail: nativeRecoveryError() };
    clearNotice(); renderConnectionCenter();
    return false;
  }
  function journalNativeCancel(cancel, previous) {
    if (!nativeJournalPrefix) return false;
    const target = cancel || previous;
    if (!target) return false;
    const matching = nativeJournalRecords().filter(item => item.record.request === target.request && item.record.operation_id === (target.operation_id || ''));
    if (!cancel) {
      for (const item of matching) {
        try { document.cookie = `${item.name}=; Max-Age=0; Path=/; SameSite=Strict`; } catch (_) {}
        if (nativeJournalBackup) {
          try { if (localStorage.getItem(item.name) === item.value) localStorage.removeItem(item.name); } catch (_) {}
        }
      }
      return true;
    }
    const record = { scope: nativeJournalScope, request: target.request, operation_id: target.operation_id || '' };
    const name = matching[0]?.name || nativeJournalPrefix + window.crypto.randomUUID();
    const value = encodeURIComponent(JSON.stringify(record));
    let cookieStored = false, backupStored = false;
    try {
      document.cookie = `${name}=${value}; Path=/; SameSite=Strict`;
      cookieStored = nativeCookieRecords().some(item => item.name === name && item.value === value);
    } catch (_) { /* A verified native-origin backup may still be available. */ }
    if (nativeJournalBackup) {
      try {
        localStorage.setItem(name, value);
        backupStored = localStorage.getItem(name) === value;
      } catch (_) { /* Cookie delivery or an acknowledged transport may still work. */ }
    }
    return cookieStored || backupStored;
  }
  function rememberSetupCancel(cancel) {
    const previous = pendingSetupCancel;
    pendingSetupCancel = cancel;
    setupCancelJournalConfirmed = journalNativeCancel(cancel, previous);
    const serialized = cancel ? JSON.stringify({ token, request: cancel.request, operation_id: cancel.operation_id || '' }) : '';
    try {
      if (cancel) sessionStorage.setItem(setupCancelKey, serialized);
      else sessionStorage.removeItem(setupCancelKey);
    } catch (_) { /* Cancellation remains pending in this page without storage. */ }
    // A native profile whose origin store was disabled before first use relies
    // on its cookie journal and host transport, without touching that store.
    if (mygoWindow && !nativeJournalBackup) return;
    // Separate keys prevent concurrent tabs from overwriting each other's
    // cancellations. They survive the originating tab closing before delivery.
    for (const key of sharedCancelKeys(cancel || previous || {})) {
      try {
        if (cancel) localStorage.setItem(key, serialized);
        else {
          const stored = JSON.parse(localStorage.getItem(key) || 'null');
          if (stored?.token === token && stored.request === previous?.request && stored.operation_id === previous?.operation_id) {
            // Keep the request tombstone: a claim may already have succeeded in
            // another tab but its response can arrive after cancellation's ack.
            if (key === `${sharedCancelPrefix}request:${stored.request}`) localStorage.setItem(key, JSON.stringify({ ...stored, acknowledged: true }));
            else localStorage.removeItem(key);
          }
        }
      } catch (_) { /* Keep retrying from this tab if shared storage is unavailable. */ }
    }
  }
  function adoptSharedSetupCancel() {
    if (pendingSetupCancel) return true;
    const cancel = sharedSetupCancel(desktopLive?.connect_request, desktopLive?.operation_id);
    if (!cancel) return false;
    if (cancel.operation_id && connectionSetup?.operation_id === cancel.operation_id) {
      setupCancelGeneration++;
      connectionSetup = null;
      pendingExitPicker = ''; pendingAddedExits.clear();
      if (exitDialog.open) exitDialog.close();
    }
    if (cancel.operation_id && defaultExitContext?.operation_id === cancel.operation_id) {
      setupCancelGeneration++;
      defaultExitDialog.close();
    }
    if (cancel.acknowledged === true) {
      if (cancel.request === desktopLive?.connect_request) markNativeConnectHandled(cancel.request);
      return true;
    }
    rememberSetupCancel({ request: cancel.request, operation_id: cancel.operation_id });
    return true;
  }
  try {
    const remembered = JSON.parse(sessionStorage.getItem(setupCancelKey) || 'null');
    if (remembered?.token === token && typeof remembered.request === 'string') rememberSetupCancel({ request: remembered.request, operation_id: remembered.operation_id || '' });
    else sessionStorage.removeItem(setupCancelKey);
  } catch (_) { /* Unavailable or malformed storage cannot restore a cancellation. */ }
  if (!pendingSetupCancel) {
    const recovered = nativeJournalRecords()[0]?.record;
    if (recovered) rememberSetupCancel({ request: recovered.request, operation_id: recovered.operation_id });
  }
  let lastNativeConnect = '';
  try { lastNativeConnect = sessionStorage.getItem('tailsplit-connect-request') || ''; } catch (_) {}
  let stateRequest = 0;
  let selectedExit = null;
  // The picker edits one exit at a time: '' is the primary device.
  let pickerExit = '';
  let pendingExitPicker = '';
  const pendingAddedExits = new Set();
  let extraExitsSignature = '';
  let exitListSignature = '';
  let pickerOriginalExit = null;
  let pickerCommitted = false;
  let pickerOpener = null;
  let pingBusy = '';
  let pingGeneration = 0;
  const pingResults = new Map();
  const pingExitGenerations = new Map();
  const exitPingGeneration = (exit) => pingExitGenerations.get(exit) || 0;
  const exitDialog = $('#exit-picker-dialog');
  const networkActive = (live = desktopLive) => Boolean(live?.active && ['tun', 'proxy'].includes(live.mode));
  const needsApproval = (live) => live?.backend_state === 'NeedsMachineAuth';
  function sessionNeedsApproval(live) {
    if (!live || ['failed', 'stopping'].includes(live.state)) return false;
    // Login/discovery without focus belongs to the primary device, even when
    // another device is the saved gateway. Traffic helpers report their owner.
    const id = connectionSetup?.stage === 'approval' ? connectionSetup.exit : live.focus ||
      (['tun', 'proxy'].includes(live.mode) ? live.traffic_exit || connectionExitRecord()?.id : '') || 'main';
    return needsApproval(id === 'main' ? live : live.exits?.[id]);
  }
  const approvalHelp = () => t('请让 Tailnet 管理员在 Tailscale 管理后台批准此设备，再重试登录。');
  const needsAuthentication = (live = desktopLive) => live?.backend_state === 'NeedsLogin' || live?.state === 'login' && !live?.focus;
  const excludedDevice = (live) => ['isolated', 'omitted'].includes(live?.phase);
  const signedIn = (live = desktopLive) => !excludedDevice(live) && !needsAuthentication(live) && (live?.backend_state === 'Running' || live?.state === 'authenticated' && !live?.focus);
  function deviceSessionNote(exit = '') {
    const phase = exitLive(exit).phase;
    return phase === 'omitted' ? t(networkActive() ? '全局模式未使用此设备；断开后可刷新或登录。' : '此设备尚未刷新；请刷新或登录。') :
      phase === 'isolated' ? t('此设备未加入本次连接；请登录后重新连接。') : '';
  }
  const exitList = (values = state.savedValues) => {
    const exits = fieldValue(values, 'exits');
    return (Array.isArray(exits) ? exits : []).filter((exit) => exit && typeof exit === 'object' && typeof exit.id === 'string' && exit.id);
  };
  const configuredExits = (values = state.savedValues) => [
    ...(values.tailscale && typeof values.tailscale === 'object' ? [{ ...values.tailscale, id: 'main' }] : []),
    ...exitList(values),
  ];
  // Configuration uses main; picker requests retain the empty-string alias.
  const exitKey = (exit) => exit === 'main' ? '' : exit;
  function exitLive(exit = '') {
    const id = exitKey(exit);
    return id === null ? {} : (id ? desktopLive?.exits?.[id] : desktopLive) || {};
  }
  function exitSignedIn(exit = '') {
    const id = exitKey(exit);
    return id !== null && !excludedDevice(exitLive(id)) && (id ? exitLive(id).backend_state === 'Running' : signedIn());
  }
  function exitLabel(exit = '', values = state.savedValues) {
    const id = exitKey(exit);
    if (id === null) return t('未设置');
    return id ? String(exitList(values).find((item) => item.id === id)?.name || exitLive(id).network || t('新出口')) :
      String(fieldValue(values, 'tailscale.name') || '') || t('主出口');
  }
  function namedExitSelection(exit, nodeID) {
    const live = exitLive(exit.id);
    const network = live.network || exit.tailnet || '';
    const node = exitNodes(exit.id).find((node) => node.id === nodeID);
    const device = node?.name || nodeID;
    const automatic = exit.auto_name || !exit.name;
    return { ...exit, exit_node: nodeID, tailnet: exit.tailnet || network,
      ...(live.magic_dns_suffix ? { magic_dns_suffix: live.magic_dns_suffix } : {}),
      ...(automatic ? { auto_name: true, name: network ? `${device}@${network}` : device } : {}) };
  }
  function savedExit(exit = '') {
    const id = exitKey(exit);
    return id === null ? '' : String((id ? exitList().find((item) => item.id === id)?.exit_node : fieldValue(state.savedValues, 'tailscale.exit_node')) || '');
  }
  const pingKey = (id, exit = pickerExit) => exit ? `${exit}|${id}` : id;
  const savedMode = () => fieldValue(state.savedValues, 'rules.mode') === 'global' ? 'global' : 'split';
  const connectionExitField = () => savedMode() === 'global' ? 'rules.global_exit' : 'rules.default_exit';
  function connectionExitRecord() {
    const current = String(fieldValue(state.savedValues, connectionExitField())).trim() || 'main';
    const selected = configuredExits().find((exit) => exit.id === current);
    return selected?.exit_node ? selected : undefined;
  }
  const connectionExit = () => {
    const id = connectionExitRecord()?.id;
    return id === 'main' ? '' : id ?? null;
  };
  function exitCanConnect(exit) {
    if (!exit?.exit_node) return false;
    const id = exit.id;
    return exitSignedIn(id) && !exitNodes(id).some((node) => node.configured && !node.online && !node.missing);
  }
  let connectionExitSignature = '';
  function gatewaySelectable(exit) {
    if (!networkActive()) return Boolean(exit?.exit_node);
    if (exitCanConnect(exit)) return true;
    // Global observes only its chosen device. Offer uncached alternatives with
    // explicit consent to disconnect before saving; never test them by teardown.
    const live = exitLive(exit?.id);
    return savedMode() === 'global' && Boolean(exit?.exit_node) && exit.id !== connectionExitRecord()?.id &&
      (live.phase === 'omitted' || !live.backend_state && !live.exit && !live.exit_nodes?.length && live.exit_online !== false);
  }
  function renderConnectionExit() {
    const select = $('#connection-exit');
    const records = configuredExits();
    const signature = JSON.stringify([locale.language, records.map(exit => [exit.id, exitLabel(exit.id), gatewaySelectable(exit)])]);
    if (signature !== connectionExitSignature) {
      connectionExitSignature = signature;
      const unset = document.createElement('option'); unset.value = ''; unset.textContent = t('未设置'); unset.disabled = true;
      select.replaceChildren(unset, ...records.map(exit => {
        const option = document.createElement('option'); option.value = exit.id;
        option.textContent = exitLabel(exit.id); option.disabled = !gatewaySelectable(exit); return option;
      }));
    }
    select.value = connectionExitRecord()?.id || '';
    select.disabled = !desktopLive?.desktop || isLocked() || isDesktopBusy() || quickBusy || connectionLost || state.dirty || state.external || Boolean(desktopLive?.active && !networkActive()) || !records.some(gatewaySelectable);
    select.title = t(networkActive() ? savedMode() === 'global' ? '切换设备需先确认断开全局保护，保存后保持未连接。' : '选择后保存并重新连接，网络会短暂中断。' : '仅保存选择，保持未连接。');
    $('#connection-exit-control').hidden = state.page !== 'connection';
    setStatusText('#connection-exit-label', t(savedMode() === 'global' ? '全局出口' : '默认出口'));
    window.TailSplitSelect?.sync();
  }
  function markNativeConnectHandled(request) {
    lastNativeConnect = request;
    try { sessionStorage.setItem('tailsplit-connect-request', request); } catch (_) {}
    forgetBrowserConnect(request);
  }
  async function consumeNativeConnect() {
    if (adoptSharedSetupCancel()) return;
    if (!sharedCancelReadable) { notice('操作未完成', t('当前步骤未完成，请重试。')); return; }
    const generation = setupCancelGeneration;
    const request = desktopLive?.connect_request;
    // MyGo owns ordinary native requests. A browser may handle only the exact
    // intent placed in the URL when the host explicitly opened its fallback.
    if (!mygoWindow && (!browserConnectRequest || browserConnectRequest !== request)) return;
    if (!request || request === lastNativeConnect) return;
    if (!connectionSetup && !(networkActive() && desktopLive.state === 'running') && (isLocked() || isDesktopBusy() || desktopLive.active)) return;
    try {
      const result = await api('connect-request', { request, operation_id: request });
      if (generation !== setupCancelGeneration || adoptSharedSetupCancel() || !sharedCancelReadable) return;
      if (!result.claimed) {
        const currentFallback = browserConnectRequest === request;
        forgetBrowserConnect(request);
        if (result.interrupted && currentFallback) notice('操作未完成', t('原生窗口的连接请求未能确认，请在当前面板重新开启或取消连接。'));
        return;
      }
    } catch (error) {
      if (generation === setupCancelGeneration && !adoptSharedSetupCancel() && request !== lastNativeConnect) notice('操作未完成', message(error));
      return;
    }
    if (request === lastNativeConnect) return; // Another consumer in this page may already have handled it.
    markNativeConnectHandled(request);
    if (connectionSetup || networkActive()) return;
    showPage('connection');
    if (state.dirty) { notice('操作未完成', t('请先保存配置，再连接。')); return; }
    if (!state.savedValid || state.external) { clearNotice(); showPage('settings'); return; }
    await connectWithExit(request);
  }
  function setupFailed(detail) {
    if (!connectionSetup) return;
    const wasChoosingNode = connectionSetup.stage === 'node';
    connectionSetup.stage = 'failed';
    connectionSetup.error = detail || t('当前步骤未完成，请重试。');
    if (wasChoosingNode && exitDialog.open) exitDialog.close();
    renderConnectionSetup();
  }
  function acceptSetupDocument(doc) {
    if (!connectionSetup || doc.revision === connectionSetup.revision) return;
    // Only the exact document committed by this Add may advance the intent.
    if (connectionSetup.added && desktopLive?.added_exit === connectionSetup.exit && desktopLive.added_revision === doc.revision) {
      connectionSetup.revision = doc.revision;
    } else setupFailed(t('配置已更改，自动连接已暂停。请检查配置后重新开启。'));
  }
  function renderConnectionSetup() {
    const live = desktopLive || {};
    const service = live.state === 'authorizing';
    const serviceError = live.error_code === 'service-installation';
    const manualService = live.error_code === 'service-setup-required';
    const setup = connectionSetup;
    const panel = $('#connection-setup');
    panel.hidden = !setup && !service && !serviceError && !pendingSetupCancel;
    if (panel.hidden) return;
    const failed = pendingSetupCancel ? Boolean(setupCancelError) : setup?.stage === 'failed' || serviceError;
    const checkingApproval = setup?.stage === 'approval' && live.active;
    const approvalRetry = setup?.stage === 'approval' && !live.active;
    const approval = setup?.stage === 'approval' || sessionNeedsApproval(live);
    const approvalError = setup?.approvalError || (live.state === 'failed' ? backendError(live) : '');
    const title = pendingSetupCancel ? '正在取消连接' : failed ? serviceError ? '后台服务需要安装或更新' : '连接设置未完成' : service ? '需要安装或更新后台服务' :
      checkingApproval ? '正在检查设备批准状态' : approval ? '等待管理员批准设备' : setup?.stage === 'node' ? '选择节点后即可连接' : setup?.stage === 'connecting' ? '正在连接 TailSplit' : '正在设置出口';
    const detail = pendingSetupCancel ? [message(setupCancelError), t('取消尚未确认；恢复连接后会自动重试，也可以再次取消。')].filter(Boolean).join(' ') : failed ? manualService ? '请重新运行 TailSplit 安装程序修复 Windows 后台服务，完成后再重试连接。' : setup?.error || backendError(live) : service ? '请完成已打开的系统安装器，应用会继续这次操作。可以取消，稍后重试。' :
      checkingApproval ? '正在刷新设备状态，不会重新发起登录。' : approval ? approvalError || (approvalRetry ? t('设备获批后，点击“检查批准状态”以继续连接。') : approvalHelp()) : setup?.stage === 'node' ? '选择一个在线节点并确认；应用会保存出口并继续这次连接。' :
      setup?.stage === 'connecting' ? '正在使用已确认的出口连接。' : '正在准备设备授权。完成浏览器登录后会自动打开节点选择，再继续连接。';
    setStatusText('#connection-setup-title', t(title));
    setStatusText('#connection-setup-detail', [message(detail), !pendingSetupCancel && nativeCancelGuarded() ? t('恢复记录不可用；此面板意外关闭时会取消当前操作。') : ''].filter(Boolean).join(' '));
    panel.classList.toggle('error', failed);
    $('#connection-setup-retry').hidden = Boolean(pendingSetupCancel) || !failed && !approvalRetry;
    setStatusText('#connection-setup-retry', t(approvalRetry ? '检查批准状态' : serviceError ? '安装服务并继续连接' : '重试连接'));
    $('#connection-setup-retry').disabled = isLocked() || isDesktopBusy() || quickBusy || connectionLost || state.dirty || state.external || !state.savedValid || networkActive();
    $('#connection-setup-cancel').hidden = !setup && !live.active && !pendingSetupCancel;
    $('#connection-setup-cancel').disabled = !live.desktop || setupCancelSending || !nativeCancelReady();
    $('#connection-setup-cancel').title = nativeCancelGuard && !nativeCancelReady() ? t('正在准备安全取消，请等待宿主确认后重试。') : '';
    setStatusText('#connection-setup-cancel', t(pendingSetupCancel && !setupCancelSending ? '重试取消' : failed && !live.active ? '关闭' : '取消连接'));
  }
  function currentCancelTarget() {
    if (pendingSetupCancel) return pendingSetupCancel;
    const request = desktopLive?.connect_request;
    const canceledRequest = request && (mygoWindow || browserConnectRequest === request) ? request : '';
    const operationID = desktopOperation || quickOperation || connectionSetup?.operation_id || (desktopLive?.active ? desktopLive.operation_id : '') || canceledRequest;
    return operationID || canceledRequest ? { request: canceledRequest, operation_id: operationID || '' } : null;
  }
  function requestNativeCancelGuard(cancel) {
    if (!nativeCancelGuard || !nativeCancelHost || !nativeJournalScope) return false;
    const key = nativeCancelKey(cancel);
    const guard = nativeCancelGuards.get(key);
    if (guard?.armed) return true;
    if (!guard || Date.now() - guard.requested > 1000) {
      nativeCancelGuards.set(key, { armed: false, requested: Date.now() });
      const fragment = new URLSearchParams({ scope: nativeJournalScope, request: cancel.request, operation_id: cancel.operation_id || '' });
      try { location.assign(`/native-cancel-arm#${fragment}`); } catch (_) { /* A later refresh can retry; unsafe Cancel stays disabled. */ }
    }
    return false;
  }
  function nativeCancelGuarded(cancel = currentCancelTarget()) {
    return Boolean(mygoWindow && nativeCancelGuard && cancel && nativeCancelGuards.get(nativeCancelKey(cancel))?.armed);
  }
  function nativeCancelReady(cancel = currentCancelTarget()) {
    if (!mygoWindow || !nativeCancelGuard || !cancel) return true;
    return nativeCancelGuards.get(nativeCancelKey(cancel))?.armed === true || nativeRecoveryAvailable(cancel) || requestNativeCancelGuard(cancel);
  }
  window.addEventListener('tailsplit-native-cancel-armed', event => {
    const ack = event.detail;
    if (!mygoWindow || !nativeCancelGuard || ack?.scope !== nativeJournalScope) return;
    const guard = nativeCancelGuards.get(nativeCancelKey(ack));
    if (!guard) return;
    guard.armed = true;
    renderConnectionCenter();
  });
  let nativeCancelConfirmation = null;
  function matchesNativeCancelAck(ack, confirmation = nativeCancelConfirmation) {
    return mygoWindow && confirmation && ack?.scope === nativeJournalScope &&
      ack.request === confirmation.cancel.request && ack.operation_id === (confirmation.cancel.operation_id || '');
  }
  window.addEventListener('tailsplit-native-cancel-admitted', event => {
    if (matchesNativeCancelAck(event.detail)) nativeCancelConfirmation.admit();
  });
  window.addEventListener('tailsplit-native-cancel-ack', event => {
    const confirmation = nativeCancelConfirmation;
    const ack = event.detail;
    if (!matchesNativeCancelAck(ack, confirmation)) return;
    confirmation.admit();
    confirmation.resolve();
  });
  async function deliverNativeCancel(cancel) {
    let confirmed = false, resolveAck, admit;
    const ack = new Promise(resolve => { resolveAck = () => { confirmed = true; resolve(); }; });
    const admitted = new Promise(resolve => { admit = resolve; });
    const confirmation = { cancel, resolve: resolveAck, admit };
    const host = nativeCancelHost && Boolean(nativeJournalScope);
    if (host) nativeCancelConfirmation = confirmation;
    try {
      // Assign only requests navigation. Guard-capable hosts must acknowledge
      // admission before legacy delivery; a pre-armed target covers source loss.
      if (host) {
        const fragment = new URLSearchParams({ scope: nativeJournalScope, request: cancel.request, operation_id: cancel.operation_id || '' });
        try { location.assign(`/native-cancel#${fragment}`); } catch (_) { /* The original transports remain available. */ }
      }
      if (host && nativeCancelGuard) {
        let timer;
        try { await Promise.race([admitted, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(t('取消尚未由宿主接收，请重试。'))), 1000); })]); }
        finally { clearTimeout(timer); }
        if (confirmed) return;
      }
      const legacy = (async () => {
        let timer;
        try {
          await Promise.race([
            window.mygo.call('TailSplitPanel.Cancel', cancel.request, cancel.operation_id || ''),
            new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(t('请求未完成，请重试。'))), 1000); }),
          ]);
        } catch (error) { if (!confirmed) await api('cancel', { request: cancel.request, operation_id: cancel.operation_id || '' }); }
        finally { clearTimeout(timer); }
      })();
      if (!host) { await legacy; return; }
      try { await Promise.race([legacy, ack]); }
      catch (error) {
        let timer;
        try { await Promise.race([ack, new Promise((_, reject) => { timer = setTimeout(() => reject(error), 1000); })]); }
        finally { clearTimeout(timer); }
      }
    } finally { if (nativeCancelConfirmation === confirmation) nativeCancelConfirmation = null; }
  }
  async function dispatchSetupCancel() {
    if (!pendingSetupCancel || setupCancelSending || !desktopLive?.desktop) return false;
    const cancel = pendingSetupCancel;
    if (mygoWindow) setupCancelJournalConfirmed = journalNativeCancel(cancel);
    if (mygoWindow && nativeCancelGuard && !setupCancelJournalConfirmed && !requestNativeCancelGuard(cancel)) {
      setupCancelError = t('正在准备安全取消，请等待宿主确认后重试。');
      renderConnectionCenter();
      return false;
    }
    if (!cancel.request && !cancel.operation_id) { rememberSetupCancel(null); return true; }
    setupCancelSending = true;
    setupCancelError = mygoWindow && !nativeCancelGuard && !setupCancelJournalConfirmed ? t('取消尚未确认，恢复日志也未能保存。请保留此面板并重试，仍失败时可从菜单栏或托盘退出 TailSplit。') : null;
    operationError = null; clearNotice(); stateRequest++;
    renderConnectionCenter();
    try {
      // The controller revokes this exact operation, including commands still
      // awaiting admission. A retry must never stop a different, newer session.
      if (mygoWindow) await deliverNativeCancel(cancel);
      else await api('cancel', { request: cancel.request, operation_id: cancel.operation_id || '' });
      if (cancel.request) markNativeConnectHandled(cancel.request);
      rememberSetupCancel(null);
      return !pendingSetupCancel;
    } catch (error) {
      setupCancelError = mygoWindow && !nativeCancelGuard && !setupCancelJournalConfirmed ? t('取消尚未确认，恢复日志也未能保存。请保留此面板并重试，仍失败时可从菜单栏或托盘退出 TailSplit。') : error;
      return false;
    } finally { setupCancelSending = false; renderConnectionCenter(); }
  }
  async function cancelConnectionSetup() {
    const target = currentCancelTarget();
    // A click cannot commit a storage-free cancellation until the host has
    // acknowledged its exact loss guard. No setup invalidation precedes this.
    if (mygoWindow && nativeCancelGuard && target && !journalNativeCancel(target) && !requestNativeCancelGuard(target)) {
      operationError = { title: '操作未完成', detail: t('正在准备安全取消，请等待宿主确认后重试。') };
      clearNotice(); renderConnectionCenter(); return;
    }
    const canceledRequest = target?.request || '';
    const operationID = target?.operation_id || '';
    setupCancelGeneration++;
    connectionSetup = null;
    pendingExitPicker = ''; pendingAddedExits.clear();
    if (exitDialog.open) exitDialog.close();
    if (!pendingSetupCancel && (operationID || canceledRequest)) rememberSetupCancel({ request: canceledRequest, operation_id: operationID || '' });
    if (await dispatchSetupCancel()) await refreshState();
    renderConnectionCenter();
  }
  async function beginConnectionSetup(exit = '', operationID = '') {
    if (isLocked() || isDesktopBusy() || quickBusy || connectionLost || !desktopLive?.desktop || !state.loaded || !state.savedValid || state.dirty || state.external || networkActive()) return;
    if (!canStartNativeOperation()) return;
    // Retry may be requested after a local conflict while authorization is alive.
    if (desktopLive.active && !(await desktopAction('disconnect'))) return;
    connectionSetup = { exit, operation_id: operationID || newOperationID(), revision: state.revision, field: connectionExitField(), stage: 'prepare', added: false, loginRequested: false };
    operationError = null; clearNotice(); showPage('connection'); renderConnectionSetup();
    await advanceConnectionSetup();
  }
  async function advanceConnectionSetup() {
    const setup = connectionSetup;
    if (!setup || setupAdvancing || setup.stage === 'failed' || isLocked() || isDesktopBusy() || quickBusy || connectionLost) return;
    if (!state.savedValid) { setupFailed(state.savedError || t('配置需要修正')); return; }
    if (state.dirty || state.external || setup.revision !== state.revision || setup.field !== connectionExitField()) {
      setupFailed(t('配置已更改，自动连接已暂停。请检查配置后重新开启。')); return;
    }
    if (!['prepare', 'approval'].includes(setup.stage) && desktopLive?.state === 'failed') { setupFailed(backendError(desktopLive) || operationError?.detail); return; }
    if (setup.stage === 'login' && setup.loginRequested && !desktopLive?.active && desktopLive?.state === 'stopped' && !exitSignedIn(setup.exit)) {
      setupFailed(t('登录已取消，可以重试连接。')); return;
    }
    if (setup.stage === 'connecting') {
      if (desktopLive?.state === 'running' && networkActive()) { connectionSetup = null; toast(t('已连接')); renderConnectionSetup(); }
      else if (!desktopLive?.active) setupFailed(t('连接没有启动，请重试。'));
      return;
    }
    if (setup.stage === 'node') return; // Only the user's node confirmation advances this stage.
    setupAdvancing = true;
    try {
      if (!setup.exit) {
        if (configuredExits().length) { connectionSetup = null; await openDefaultExitDialog(setup.operation_id); return; }
        if (!(await addExit(true))) { if (connectionSetup === setup) setupFailed(operationError?.detail); return; }
      }
      if (connectionSetup !== setup) return;
      const record = configuredExits().find(exit => exit.id === setup.exit);
      if (!record) {
        if (desktopLive?.active) return; // A staged Add is intentionally absent until authentication completes.
        setupFailed(backendError(desktopLive) || t('出口尚未添加完成，请重试。')); return;
      }
      if (needsApproval(exitLive(setup.exit))) {
        setup.stage = 'approval';
        return; // Administrator approval must not relaunch browser sign-in.
      }
      if (setup.stage === 'approval') {
        if (!exitSignedIn(setup.exit)) {
          if (!desktopLive.active && exitLive(setup.exit).backend_state === 'NeedsLogin') setupFailed(t('设备需要重新登录，请重试连接。'));
          return; // A status refresh can temporarily clear cached device metadata.
        }
        setup.stage = 'login';
      }
      if (!exitSignedIn(setup.exit)) {
        if (!setup.loginRequested) {
          setup.stage = 'login'; setup.loginRequested = true;
          const ok = await desktopAction(setup.exit === 'main' ? 'login' : 'login-exit', setup.exit === 'main' ? '' : setup.exit);
          if (!ok && connectionSetup === setup) setupFailed(operationError?.detail);
        }
        if (connectionSetup !== setup || setup.stage === 'failed' || !exitSignedIn(setup.exit)) return;
      }
      if (desktopLive.active) return; // Wait for helper cleanup before saving or starting traffic.
      if (!exitCanConnect(record)) {
        setup.stage = 'node'; pendingExitPicker = '';
        openExitPicker($('#desktop-connect'), exitKey(setup.exit));
        return;
      }
      if (String(fieldValue(state.savedValues, setup.field)) !== setup.exit) {
        setup.stage = 'saving';
        const ok = await saveExitChanges({ [setup.field]: setup.exit }, '', doc => { if (connectionSetup === setup) setup.revision = doc.revision; });
        if (!ok) { if (connectionSetup === setup) setupFailed(operationError?.detail); return; }
      }
      if (connectionSetup !== setup || state.dirty || state.external || state.revision !== setup.revision || setup.field !== connectionExitField() || !exitCanConnect(configuredExits().find(exit => exit.id === setup.exit))) return;
      setup.stage = 'connecting';
      if (!(await desktopAction('connect')) && connectionSetup === setup) setupFailed(operationError?.detail);
      if (connectionSetup === setup && desktopLive?.state === 'running' && networkActive()) { connectionSetup = null; toast(t('已连接')); }
    } finally { setupAdvancing = false; renderConnectionSetup(); }
  }
  $('#connection-setup-cancel').addEventListener('click', cancelConnectionSetup);
  $('#connection-setup-retry').addEventListener('click', async () => {
    if (connectionSetup?.stage === 'approval') {
      if (desktopLive?.active || $('#connection-setup-retry').disabled) return;
      const setup = connectionSetup;
      setup.approvalError = '';
      const ok = await desktopAction('refresh-exits');
      if (connectionSetup === setup && !ok) setup.approvalError = operationError?.detail;
      renderConnectionSetup();
      return;
    }
    if (!connectionSetup && !requiresDefaultExit(state.savedValues)) return desktopAction('connect');
    const exit = configuredExits().some(exit => exit.id === connectionSetup?.exit) ? connectionSetup.exit : connectionExitRecord()?.id || '';
    return beginConnectionSetup(exit);
  });
  const defaultExitDialog = $('#default-exit-dialog');
  let defaultExitContext = null;
  let defaultExitBusy = false;
  let defaultExitSignature = '';
  function defaultExitError(text) {
    $('#default-exit-error').textContent = text;
    $('#default-exit-error').hidden = !text;
  }
  function renderDefaultExitDialog() {
    if (!defaultExitDialog.open || !defaultExitContext) return;
    const select = $('#default-exit-choice');
    const records = configuredExits();
    const signature = JSON.stringify([locale.language, records.map((exit) => [exit.id, exitLabel(exit.id), exitCanConnect(exit)])]);
    if (signature !== defaultExitSignature) {
      const current = defaultExitSignature ? select.value : '';
      defaultExitSignature = signature;
      const placeholder = document.createElement('option'); placeholder.value = ''; placeholder.textContent = t('请选择出口');
      placeholder.disabled = true; placeholder.hidden = true;
      select.replaceChildren(placeholder, ...records.map((exit) => {
        const option = document.createElement('option'); option.value = exit.id;
        option.textContent = exitLabel(exit.id);
        option.disabled = false; return option;
      }));
      select.value = records.some((exit) => exit.id === current) ? current : '';
    }
    const stale = state.external || state.revision !== defaultExitContext.revision || connectionExitField() !== defaultExitContext.field;
    if (stale && !defaultExitBusy) defaultExitError(t('配置文件已更新，请取消后重新选择。'));
    const locked = defaultExitBusy || isLocked() || isDesktopBusy() || quickBusy || connectionLost || state.dirty || stale;
    const global = defaultExitContext.field === 'rules.global_exit';
    setStatusText('#default-exit-heading', t(global ? '选择全局出口' : '选择默认出口'));
    setStatusText('#default-exit-choice-label', t(global ? '全局出口' : '默认出口'));
    const chosen = records.find(exit => exit.id === select.value);
    select.disabled = locked || !records.length;
    $('#confirm-default-exit').disabled = locked || !chosen;
    $('#confirm-default-exit').textContent = t(defaultExitBusy ? '正在应用…' : chosen && !exitCanConnect(chosen) ? '继续设置并连接' : '确认并连接');
    setStatusText('#default-exit-description', t('请选择出口，完成必要的登录和节点选择后会继续连接。'));
    $('#cancel-default-exit').disabled = defaultExitBusy;
    $('#default-exit-manage').disabled = defaultExitBusy;
    $('#default-exit-empty').hidden = records.length > 0;
    $('#default-exit-manage').hidden = records.length > 0;
    window.TailSplitSelect?.sync();
  }
  function pickerDNSUpdate() {
    if (!exitDialog.open || !pickerExit) return null;
    const exit = exitList().find((item) => item.id === pickerExit);
    const live = exitLive(pickerExit);
    if (!exit?.exit_node || !exitSignedIn(pickerExit) || !live.magic_dns_suffix) return null;
    const equal = (a, b) => String(a || '').toLowerCase() === String(b || '').toLowerCase();
    if (exit.tailnet && !equal(exit.tailnet, live.network) && !equal(exit.tailnet, live.magic_dns_suffix)) return null;
    const canonical = (value) => String(value || '').trim().replace(/\.$/, '').toLowerCase();
    const savedSuffix = exit.magic_dns_suffix || (/\.ts\.net$/i.test(exit.tailnet || '') ? exit.tailnet : '');
    if (canonical(savedSuffix) === canonical(live.magic_dns_suffix)) return null;
    return { ...exit, magic_dns_suffix: canonical(live.magic_dns_suffix) };
  }
  const connectionChanges = () => {
    if (selectedExit !== null && selectedExit !== savedExit(pickerExit)) return pickerExit ?
      { exits: exitList().map((exit) => exit.id === pickerExit ? namedExitSelection(exit, selectedExit) : exit) } :
      { 'tailscale.exit_node': selectedExit, ...(fieldValue(state.savedValues, 'tailscale.tailnet') || !desktopLive?.network ? {} : { 'tailscale.tailnet': desktopLive.network }) };
    const updated = pickerDNSUpdate();
    return updated ? { exits: exitList().map((exit) => exit.id === pickerExit ? updated : exit) } : {};
  };
  // Mirror Config.RequiresDefaultExit, including the bare-Tailnet fallback
  // retained when a selected default has a node. Other policy names its owners.
  function requiresDefaultExit(values) {
    const id = String(fieldValue(values, 'rules.default_exit')).trim() || 'main';
    return fieldValue(values, 'rules.mode') === 'global' || fieldValue(values, 'rules.final') === 'proxy' ||
      Boolean(fieldValue(values, 'rules.source') || values.rules?.gfwlist) || ['rules.proxy_domains', 'rules.proxy_ips'].some((field) => {
        const items = fieldValue(values, field); return Array.isArray(items) && items.length > 0;
      }) || configuredExits(values).some(exit => exit.id === id && String(exit.exit_node || '').trim());
  }
  function exitNodes(exit = '') {
    const live = exitLive(exit);
    const nodes = (!excludedDevice(live) && Array.isArray(live.exit_nodes) ? live.exit_nodes : []).map((node) => ({
      id: String(node.id || node.ID || ''), name: String(node.name || node.Name || node.id || node.ID || ''),
      online: node.online === true || node.Online === true, selected: node.selected === true || node.Selected === true,
      selectedFor: String(node.selected_for || node.SelectedFor || ''),
    })).filter((node) => node.id);
    const configured = savedExit(exit);
    const directMatch = nodes.some((node) => node.id === configured || node.name === configured);
    for (const node of nodes) node.configured = node.id === configured || node.name === configured || configured !== '' && !directMatch && node.selected && node.selectedFor === configured;
    if (configured && !nodes.some((node) => node.configured)) nodes.unshift({ id: configured, name: configured, online: false, configured: true, missing: true });
    return nodes;
  }
  function announcePing(text) {
    setStatusText('#ping-announcement', exitDialog.open ? '' : text);
    setStatusText('#picker-ping-announcement', exitDialog.open ? text : '');
  }
  function syncPingAnnouncement() {
    const pending = pingBusy && exitNodes(pickerExit).find((node) => pingKey(node.id) === pingBusy);
    announcePing(pending ? t('正在 Ping {name}', { name: pending.name }) : '');
  }
  function clearPingResults(exit) {
    if (exit === undefined) {
      pingGeneration++;
      pingExitGenerations.clear(); pingResults.clear(); pingBusy = '';
      announcePing('');
      return;
    }
    // Invalidate this device's requests as well as its cache, even if its old
    // identity later returns. Other devices keep their results and requests.
    pingExitGenerations.set(exit, exitPingGeneration(exit) + 1);
    const belongsToExit = (key) => exit ? key.startsWith(`${exit}|`) : !key.includes('|');
    for (const key of [...pingResults.keys()]) if (belongsToExit(key)) pingResults.delete(key);
    if (pingBusy && belongsToExit(pingBusy)) { pingBusy = ''; announcePing(''); }
    else if (pickerExit === exit) announcePing('');
  }
  function updateDesktop(live) {
    if (desktopLive && (live.network !== desktopLive.network || live.hostname !== desktopLive.hostname ||
        live.backend_state !== desktopLive.backend_state || live.phase !== desktopLive.phase || signedIn(live) !== signedIn())) clearPingResults('');
    // A device that signs out, disappears or changes identity invalidates itself.
    const previous = desktopLive?.exits || {};
    desktopLive = live;
    sampleTraffic(live);
    // Saving the provisional profile or opening a browser is not a completed Add.
    // Only a fresh authenticated device status can confirm success.
    for (const id of pendingAddedExits) {
      if (!exitList().some(exit => exit.id === id)) {
        if (!live.active && live.state !== 'authenticated' && !isDesktopBusy()) {
          pendingAddedExits.delete(id);
          if (pendingExitPicker === id) pendingExitPicker = '';
        }
      } else if (exitSignedIn(id) && !live.active && live.state === 'authenticated') {
        pendingAddedExits.delete(id);
        toast(t('出口已添加'));
      }
    }
    for (const [id, exit] of Object.entries(previous)) {
      const current = exitLive(id);
      if (current.network !== exit.network || current.hostname !== exit.hostname || current.backend_state !== exit.backend_state || current.phase !== exit.phase) clearPingResults(id);
    }
    const online = new Set(['', ...exitList().map((exit) => exit.id)].flatMap((exit) =>
      exitNodes(exit).filter((node) => node.online).map((node) => pingKey(node.id, exit))));
    for (const [key] of pingResults) {
      if (!online.has(key)) pingResults.delete(key);
    }
    if (pendingExitPicker && connectionSetup?.exit !== pendingExitPicker && exitList().some(exit => exit.id === pendingExitPicker) && exitSignedIn(pendingExitPicker) && !exitDialog.open && state.page === 'connection' && !isDesktopBusy()) {
      const exit = pendingExitPicker;
      pendingExitPicker = '';
      if (openExitPicker($('#add-exit'), exit)) return;
    }
    renderConnectionCenter();
  }
  function renderConnectionCenter() {
    const live = desktopLive || {};
    renderConnectionSetup();
    const connected = networkActive(live);
    const authenticated = signedIn(live);
    const discovering = live.active && !connected;
    const locked = isLocked() || isDesktopBusy() || quickBusy || connectionLost;
    const changing = Boolean(Object.keys(connectionChanges()).length);
    const hasPrimary = Boolean(state.savedValues.tailscale);
    const records = configuredExits();
    const hasTailnet = records.length > 0;
    const defaultRequired = requiresDefaultExit(state.savedValues);
    $('#main-exit-item').hidden = !hasPrimary;
    $('#exits-card').hidden = !hasTailnet;
    $('#exits-empty').hidden = hasTailnet;
    const nodes = exitNodes();
    const pickerNodes = exitNodes(pickerExit);
    const chosen = selectedExit ?? savedExit(pickerExit);
    const mode = savedMode();
    const killSwitch = $('#kill-switch');
    const targetField = mode === 'global' ? 'rules.default_exit' : 'rules.global_exit';
    const targetID = String(fieldValue(state.savedValues, targetField));
    const targetNeedsDefault = mode !== 'global' || requiresDefaultExit({ ...state.savedValues,
      rules: { ...state.savedValues.rules, mode: 'split' } });
    const targetReady = !targetNeedsDefault || records.some(exit => exit.id === targetID && exit.exit_node);
    $('#kill-switch-control').hidden = !live.desktop;
    killSwitch.value = mode;
    killSwitch.disabled = !live.desktop || locked || discovering || changing || state.dirty || state.external || !state.loaded || !state.savedValid || connected && !targetReady;
    killSwitch.title = connected && !targetReady ? t(mode === 'global' ? '请先在设置中选择默认代理出口。' : '请先在设置中选择全局出口。') :
      t(mode === 'global' ? '关闭后使用规则分流，保留现有规则。' : '开启全局代理；出口掉线时阻断流量，手动断开恢复系统网络。');
    const activeExit = connectionExit();
    renderConnectionExit();
    renderDefaultExitDialog();
    const needsDefaultChoice = defaultRequired && !connectionExitRecord();
    const activeExitReady = exitSignedIn(activeExit);
    const chosenNode = pickerNodes.find((node) => node.id === chosen || node.name === chosen || selectedExit === null && node.configured);
    const savedRoutes = fieldValue(state.savedValues, 'rules.routes');
    const referencedExits = new Set([
      ...(defaultRequired ? [fieldValue(state.savedValues, 'rules.default_exit')] : []), fieldValue(state.savedValues, 'rules.final'),
      ...(Array.isArray(savedRoutes) ? savedRoutes : []).filter(route => route && (route.domains?.length || route.ips?.length)).map(route => route.exit),
    ]);
    const requiredBlocked = mode === 'split' && records.some(exit => exit.id !== connectionExitRecord()?.id && referencedExits.has(exit.id) && exit.exit_node && exitLive(exit.id).backend_state === 'NeedsLogin');
    $('#desktop-controls').hidden = !live.desktop || state.page === 'home';
    $('#desktop-connect').hidden = Boolean(live.active || pendingSetupCancel);
    $('#desktop-disconnect').hidden = !live.active && !pendingSetupCancel;
    $('#desktop-connect').disabled = !live.desktop || locked || state.dirty || state.external || !state.savedValid || changing || (!needsDefaultChoice && requiredBlocked) || Boolean(connectionSetup && connectionSetup.stage !== 'failed');
    $('#desktop-disconnect').disabled = !live.desktop || setupCancelSending || !nativeCancelReady();
    $('#desktop-disconnect').title = nativeCancelGuard && !nativeCancelReady() ? t('正在准备安全取消，请等待宿主确认后重试。') : '';
    $('#desktop-disconnect').setAttribute('aria-checked', String(connected));
    $('#desktop-disconnect').title = connected ? t('断开') : t('取消');
    $('#desktop-disconnect').setAttribute('aria-label', $('#desktop-disconnect').title);
    if (document.activeElement === $('#desktop-connect') && $('#desktop-connect').hidden) $('#desktop-disconnect').focus();
    else if (document.activeElement === $('#desktop-disconnect') && $('#desktop-disconnect').hidden) $('#desktop-connect').focus();
    for (const button of document.querySelectorAll('[data-desktop-action]')) {
      if (['connect', 'disconnect'].includes(button.dataset.desktopAction)) continue;
      const loginRecovery = button.dataset.desktopAction === 'login' && live.mode === 'login' && needsAuthentication(live);
      button.disabled = !live.desktop || locked || discovering && !loginRecovery || button.dataset.desktopAction === 'login' && connected && !needsAuthentication(live);
    }
    $('#desktop-login').hidden = !hasPrimary || authenticated;
    if (needsApproval(live)) $('#desktop-login').disabled = true;
    $('#startup-setting').hidden = !live.desktop;
    $('#desktop-startup').disabled = !live.desktop || locked;
    $('#desktop-startup').checked = live.startup === true;
    const transitioning = ['starting', 'stopping', 'authorizing', 'logging-out'].includes(live.state) || discovering;
    const network = deviceSessionNote() || (needsApproval(live) ? t('等待管理员批准设备') : live.state === 'logging-out' ? t('正在退出登录') : live.network ||
      (authenticated ? t('已登录（网络名称未提供）') : live.backend_state === 'NeedsLogin' ? t('未登录') : transitioning ? t('正在读取网络…') : t('尚未读取网络')));
    setStatusText('#desktop-network-name', network);
    $('#desktop-network-name').title = network;
    const identity = needsApproval(live) ? t('等待管理员批准设备') : authenticated ? live.hostname || t('已登录') :
      needsAuthentication(live) ? t('未登录 Tailscale') : discovering ? t('正在读取设备') : t('未登录 Tailscale');
    const extra = exitLive(pickerExit);
    const extraIdentity = !pickerExit ? '' : needsApproval(extra) ? t('等待管理员批准设备') : exitSignedIn(pickerExit) ? [extra.hostname, extra.network].filter(Boolean).join(' · ') || t('已登录') :
      extra.backend_state === 'NeedsLogin' ? t('未登录 Tailscale') : t('尚未读取设备');
    setStatusText('#tailnet-identity', connectionLost ? t('页面连接中断') : deviceSessionNote(pickerExit) || (pickerExit ? extraIdentity : identity));
    $('#picker-login').hidden = exitSignedIn(pickerExit);
    $('#picker-login').disabled = !live.desktop || locked || transitioning || needsApproval(extra);
    $('#exit-danger-zone').hidden = !records.some((record) => record.id === (pickerExit || 'main'));
    $('#exit-remove').disabled = !live.desktop || locked || transitioning || state.dirty || state.external;
    $('#exit-remove').textContent = removingExit ? t('正在删除…') : t('删除出口…');
    setStatusText('#heading-exits', exitLabel(pickerExit));
    setStatusText('#main-exit-name', exitLabel());
    $('#main-exit-status').className = `exit-status ${exitStatus('')}`;
    $('#main-exit-badge').hidden = activeExit !== '';
    $('#main-exit-badge').textContent = mode === 'global' ? t('全局出口') : t('默认');
    $('#add-exit').disabled = addingExit || !canManageExits() || exitList().length >= 8;
    $('#add-exit').title = !live.desktop ? t('此页面仅编辑配置，连接操作请使用 TailSplit 桌面应用。') : state.dirty ? t('请先保存或放弃更改，再添加出口。') : '';
    renderExtraExits(locked || transitioning);
    const current = connected && !activeExit ? live.exit || savedExit() : savedExit();
    const currentNode = nodes.find((node) => node.id === current || node.name === current) || (!connected || !live.exit ? nodes.find((node) => node.configured) : undefined);
    setStatusText('#current-exit', currentNode?.name || current || t('尚未选择出口'));
    const currentDetail = currentNode ? !currentNode.online ? t(currentNode.missing ? '已保存 · 未发现' : '已选 · 不可用') : '' : t('选择已获准使用的设备。');
    setStatusText('#current-exit-detail', currentDetail);
    $('#current-exit-detail').hidden = !currentDetail;
    $('#choose-exit').disabled = locked || !state.loaded;
    const hint = connectionLost ? t('请确认 TailSplit 仍在运行') : live.error ? message(backendError(live)) :
      desktopPending ? t('正在处理…') : quickBusy ? t('正在应用连接更改…') :
      !live.desktop ? t('此页面仅编辑配置，连接操作请使用 TailSplit 桌面应用。') :
      live.state === 'authorizing' ? t('请在系统安装器中完成后台网络服务的安装或更新；完成后会继续当前操作。') :
      sessionNeedsApproval(live) ? approvalHelp() :
      live.preview ? t('界面预览：操作仅模拟界面状态，不会创建网络连接。') :
      state.dirty ? t('请先保存配置，再连接。') : changing ? t('连接选项尚未应用，请先确认选择。') :
      requiredBlocked ? t('分流规则引用的其他出口需要登录；请打开该出口完成登录后再连接。') :
      needsDefaultChoice ? t('开启连接后会引导添加出口、登录并选择节点。') : defaultRequired && !activeExitReady ? t('开启连接后会引导登录和选择节点。') : defaultRequired && !savedExit(activeExit) ? t('尚未选择出口') : '';
    const setupVisible = !$('#connection-setup').hidden;
    setStatusText('#desktop-hint', setupVisible && live.preview ? t('界面预览：操作仅模拟界面状态，不会创建网络连接。') : hint);
    $('#desktop-hint').hidden = changing || (setupVisible ? !live.preview : !hint);
    $('#desktop-connect').title = hint || t('连接 TailSplit');
    $('#connection-apply').hidden = exitDialog.open || !changing && !quickBusy;
    const name = (pickerExit ? `${exitLabel(pickerExit)} · ` : '') + (chosenNode?.name || chosen || t('尚未选择出口'));
    const dnsUpdate = selectedExit === null || selectedExit === savedExit(pickerExit) ? pickerDNSUpdate() : null;
    const selection = dnsUpdate ? t('更新此出口的 DNS 信息：{suffix}', { suffix: dnsUpdate.magic_dns_suffix }) :
      t('已选：{name} · {mode}', { name, mode: mode === 'global' ? t('全局代理') : t('规则分流') });
    const guidedNode = connectionSetup?.stage === 'node' && connectionSetup.exit === (pickerExit || 'main');
    const consequence = guidedNode ? t('保存节点和默认出口，然后继续这次连接。') : connected && (live.routing_mode || mode) === 'global' ? t('应用更改需先确认断开全局保护，保存后保持未连接。') :
      connected ? t('确认后保存并重新连接，网络会短暂中断。') : t('仅保存选择，保持未连接。');
    const applyLabel = quickBusy ? t('正在应用…') : guidedNode ? t('保存并连接') : dnsUpdate ? t('保存 DNS 信息') : connected ? t('切换出口') : t('保存选择');
    const cannotApply = locked || discovering || state.dirty || state.external || (!changing && !guidedNode) || (guidedNode || selectedExit !== null) && (!chosenNode || !chosenNode.online || chosenNode.missing === true);
    for (const prefix of ['connection', 'exit-dialog']) {
      setStatusText(`#${prefix}-selection`, selection);
      setStatusText(`#${prefix}-consequence`, consequence);
    }
    for (const id of ['apply-connection-change', 'apply-exit-picker']) {
      $(`#${id}`).textContent = id === 'apply-exit-picker' && !guidedNode && !changing && !quickBusy ? t(connected ? '切换出口' : '保存选择') : applyLabel;
      $(`#${id}`).disabled = cannotApply;
    }
    for (const id of ['cancel-connection-change', 'close-exit-picker', 'cancel-exit-picker', 'picker-view-settings', 'picker-go-connection']) $(`#${id}`).disabled = quickBusy;
    $('#connection-draft-warning').hidden = !state.dirty;
    $('#exit-dialog-draft-warning').hidden = !state.dirty;
    const pickerError = operationError ? message(operationError.detail) : state.external ? t('当前草稿仍保留。重新读取最新配置后再保存，避免覆盖其他更改。') : '';
    setStatusText('#exit-dialog-error', pickerError);
    $('#exit-dialog-error').hidden = !pickerError;
    renderExitList(pickerNodes, locked || discovering);
    renderConnection(live);
    renderHome(live);
  }
  function renderExitList(nodes, locked) {
    const visible = nodes;
    const signature = JSON.stringify([pickerExit, visible, selectedExit, locked, exitLive(pickerExit).ping_available === true && exitSignedIn(pickerExit), pingBusy, [...pingResults], locale.language]);
    if (signature !== exitListSignature) {
      exitListSignature = signature;
      const focused = document.activeElement?.dataset;
      let restoreFocus = null;
      const fragment = document.createDocumentFragment();
      const available = visible.filter((node) => node.online);
      const focusable = available.find((node) => selectedExit === null ? node.configured : node.id === selectedExit) || available[0];
      for (const node of visible) {
        const selected = selectedExit === null ? node.configured : node.id === selectedExit;
        const row = document.createElement('div');
        row.className = `exit-row${selected ? ' selected' : ''}`;
        const { latency, ping } = nodePingControls(node, locked);
        const choice = nodeChoiceButton(node, selected, locked);
        choice.tabIndex = !choice.disabled && node === focusable ? 0 : -1;
        choice.addEventListener('click', (event) => { selectedExit = node.configured ? null : node.id; renderConnectionCenter(); if (event.detail > 0) $('#heading-exits').focus({ preventScroll: true }); });
        choice.addEventListener('keydown', (event) => {
          const direction = ['ArrowRight', 'ArrowDown'].includes(event.key) ? 1 : ['ArrowLeft', 'ArrowUp'].includes(event.key) ? -1 : 0;
          if (!direction || !available.length) return;
          event.preventDefault();
          const next = available[(available.indexOf(node) + direction + available.length) % available.length];
          selectedExit = next.configured ? null : next.id;
          renderConnectionCenter();
          [...$('#exit-list').querySelectorAll('[data-exit-choice]')].find((button) => button.dataset.exitChoice === next.id)?.focus();
        });
        if (focused?.exitChoice === node.id && !choice.disabled) restoreFocus = choice;
        if (focused?.pingSurface === 'picker' && focused.nodePing === node.id && !ping.disabled) restoreFocus = ping;
        row.append(choice, latency, ping); fragment.append(row);
      }
      $('#exit-list').replaceChildren(fragment);
      if (restoreFocus) restoreFocus.focus();
    }
    $('#exit-count').textContent = t('可用出口数量', { count: nodes.filter((node) => node.online).length });
    renderExitEmpty(visible);
    const guidedRefresh = connectionSetup?.stage === 'node' && exitSignedIn(pickerExit) && !nodes.some(node => node.online);
    $('#picker-go-connection').hidden = !guidedRefresh && (Boolean(pickerExit) || signedIn());
    $('#picker-go-connection').textContent = t(guidedRefresh ? '重新读取节点' : '前往连接页面');
    if (guidedRefresh) $('#picker-go-connection').disabled = locked || Boolean(desktopLive?.active);
  }
  // Primary first, then additional exits: green when its node is reachable.
  function exitStatus(exit) {
    if (!exitSignedIn(exit)) return exitLive(exit).backend_state === 'NeedsLogin' ? 'signed-out' : 'unknown';
    const nodes = exitNodes(exit);
    const configured = nodes.find((node) => node.configured);
    return !savedExit(exit) ? 'attention' : configured?.online ? 'online' : 'attention';
  }
  function renderExtraExits(locked) {
    const exits = exitList();
    const rows = exits.map((exit) => {
      const live = exitLive(exit.id);
      const node = exitNodes(exit.id).find((node) => node.configured);
      return [exit.id, exitLabel(exit.id), exit.exit_node, node?.name || '', node?.online, live.network || '', live.backend_state || '', exitStatus(exit.id), live.phase || ''];
    });
    const activeExit = connectionExit();
    const signature = JSON.stringify([rows, locked, activeExit, savedMode(), locale.language]);
    if (signature === extraExitsSignature) return;
    extraExitsSignature = signature;
    const fragment = document.createDocumentFragment();
    for (const [id, label, configured, nodeName, online, network, backend, status] of rows) {
      const item = document.createElement('div'); item.className = 'exit-item'; item.setAttribute('role', 'listitem');
      const trigger = document.createElement('button'); trigger.type = 'button'; trigger.className = 'exit-trigger';
      trigger.dataset.exitRow = id; trigger.disabled = locked || !state.loaded;
      trigger.setAttribute('aria-haspopup', 'dialog');
      const dot = document.createElement('span'); dot.className = `exit-status ${status}`; dot.setAttribute('aria-hidden', 'true');
      const info = document.createElement('span'); info.className = 'exit-info';
      const title = document.createElement('span'); title.className = 'exit-title';
      const name = document.createElement('strong'); name.className = 'exit-label-name'; name.textContent = label; name.title = label;
      title.append(name);
      if (id === activeExit) {
        const badge = document.createElement('span'); badge.className = 'exit-badge';
        badge.textContent = savedMode() === 'global' ? t('全局出口') : t('默认'); title.append(badge);
      }
      const address = document.createElement('span'); address.className = 'exit-address';
      const exitName = document.createElement('span'); exitName.className = 'exit-name';
      exitName.textContent = nodeName || configured || t('尚未选择出口');
      const detail = document.createElement('span'); detail.className = 'exit-network';
      detail.textContent = backend === 'NeedsMachineAuth' ? t('等待管理员批准设备') : pendingAddedExits.has(id) && !exitSignedIn(id) ? t('等待 Tailscale 登录') :
        deviceSessionNote(id) || (backend === 'NeedsLogin' ? t('未登录') : network || (backend === 'Running' ? t('已登录（网络名称未提供）') : t('尚未读取网络')));
      address.append(exitName, detail);
      if (configured && !excludedDevice(exitLive(id)) && backend === 'Running' && !online) {
        const unavailable = document.createElement('span'); unavailable.textContent = t(nodeName ? '已选 · 不可用' : '已保存 · 未发现');
        address.append(unavailable);
      }
      info.append(title, address);
      trigger.append(dot, info);
      if (backend !== 'NeedsLogin') {
        const chevron = nodeIcon('chevron'); chevron.setAttribute('class', 'icon exit-chevron');
        trigger.append(chevron);
      }
      trigger.setAttribute('aria-label', `${label}: ${exitName.textContent} · ${detail.textContent}`);
      trigger.addEventListener('click', () => openExitPicker(trigger, id));
      item.append(trigger);
      if (backend === 'NeedsLogin' || exitLive(id).phase === 'isolated') {
        const login = document.createElement('button'); login.type = 'button'; login.className = 'button small exit-login';
        login.textContent = t('登录'); login.disabled = locked; login.dataset.exitLogin = id;
        login.setAttribute('aria-label', t('登录 {name}', { name: label }));
        login.addEventListener('click', async () => {
          pendingExitPicker = id;
          if (!(await exitAction('login-exit', id))) pendingExitPicker = '';
        });
        item.append(login);
      }
      fragment.append(item);
    }
    $('#extra-exits').replaceChildren(fragment);
  }
  function renderExitEmpty(visible) {
    const discovering = desktopPending === 'refresh-exits' || desktopLive?.mode === 'discovery' && desktopLive.active;
    const guidedEmpty = connectionSetup?.stage === 'node' && !visible.some(node => node.online);
    $('#exit-empty').hidden = visible.length > 0 && !guidedEmpty;
    const ready = exitSignedIn(pickerExit);
    const approval = needsApproval(exitLive(pickerExit));
    setStatusText('#exit-empty-title', approval ? t('等待管理员批准设备') : discovering ? t('正在读取出口') : ready ? t('未发现可用出口') : t('尚未读取出口'));
    setStatusText('#exit-empty-detail', approval ? approvalHelp() : ready ? t('确认设备已在 Tailnet 中提供出口，并获准使用，然后刷新。') : t('登录 Tailscale 后，刷新可用出口。'));
  }
  function nodeIcon(symbol) {
    const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    icon.setAttribute('class', 'icon'); icon.setAttribute('aria-hidden', 'true');
    const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
    use.setAttribute('href', `#i-${symbol}`); icon.append(use);
    return icon;
  }
  function pingLabel(result) {
    return `${new Intl.NumberFormat(locale.language, { maximumFractionDigits: 1 }).format(result.latency_ms)} ms`;
  }
  function nodeChoiceButton(node, selected, locked) {
    const choice = document.createElement('button'); choice.type = 'button'; choice.className = 'node-choice';
    choice.dataset.exitChoice = node.id;
    choice.disabled = locked || !node.online;
    choice.setAttribute('role', 'radio');
    choice.setAttribute('aria-checked', String(Boolean(selected)));
    choice.setAttribute('aria-label', t('选择 {name}', { name: node.name }));
    const name = document.createElement('span'); name.className = 'exit-name'; name.textContent = node.name; name.title = node.name;
    const check = document.createElement('span'); check.className = 'node-check';
    check.setAttribute('aria-hidden', 'true');
    check.append(nodeIcon('check')); choice.append(check, name);
    return choice;
  }
  function nodePingControls(node, locked) {
    const result = pingResults.get(pingKey(node.id));
    const pending = pingBusy === pingKey(node.id);
    const canPing = exitLive(pickerExit).ping_available === true && exitSignedIn(pickerExit);
    const band = result && !result.error && !pending ? result.latency_ms <= 500 ? 'fast' : result.latency_ms <= 1000 ? 'medium' : 'slow' : '';
    const latency = document.createElement('span');
    latency.className = `node-latency${band ? ` ${band}` : ''}${result?.error && !pending ? ' failed' : ''}`;
    const value = document.createElement('strong');
    value.textContent = pending ? t('正在测试…') : !node.online ? t(node.missing ? '未发现' : '离线') : result?.error ? t('测试失败') : result ? pingLabel(result) : '—';
    latency.append(value);
    const route = result?.relay ? t('中继 · {name}', { name: result.relay }) : t(result?.direct ? '直连' : '中继');
    latency.title = result?.error ? message(result.error) : pending ? t('正在测试…') : result ? `${pingLabel(result)} · ${route}` : t(node.online ? '尚未测试' : '节点不可用');
    const ping = document.createElement('button'); ping.type = 'button'; ping.className = 'link-button node-ping';
    ping.dataset.nodePing = node.id; ping.dataset.pingSurface = 'picker'; ping.textContent = 'Ping';
    ping.title = result?.error ? message(result.error) : t('使用 Tailscale Ping 测试延迟');
    ping.setAttribute('aria-label', t('Ping {name}', { name: node.name }));
    ping.setAttribute('aria-description', latency.title);
    ping.setAttribute('aria-busy', String(pending));
    // Keep the active ping focusable while its request is in flight.
    ping.disabled = locked || !canPing || !node.online || Boolean(pingBusy && !pending);
    ping.setAttribute('aria-disabled', String(ping.disabled || pending));
    ping.addEventListener('click', () => pingNode(node.id));
    return { latency, ping };
  }
  async function pingNode(id) {
    const exit = pickerExit;
    const key = pingKey(id, exit);
    const node = exitNodes(exit).find((node) => node.id === id);
    if (pingBusy || !node?.online || !exitSignedIn(exit) || !exitLive(exit).ping_available || isDesktopBusy() || quickBusy || isLocked() || connectionLost) return;
    const generation = pingGeneration, exitGeneration = exitPingGeneration(exit);
    const currentRequest = () => generation === pingGeneration && exitGeneration === exitPingGeneration(exit);
    pingBusy = key; pingResults.delete(key); renderConnectionCenter();
    announcePing(t('正在 Ping {name}', { name: node.name }));
    try {
      const result = await api('exit-ping', exit ? { id, exit } : { id });
      if (!currentRequest() || !exitNodes(exit).some((node) => node.id === id && node.online)) return;
      if (result.id !== id || !Number.isFinite(result.latency_ms) || result.latency_ms < 0 || typeof result.direct !== 'boolean') throw new Error(t('未收到有效的 Ping 结果，请重试。'));
      pingResults.set(key, result);
      announcePing(t('{name}：{latency}', { name: node.name, latency: pingLabel(result) }));
    } catch (error) {
      if (!currentRequest()) return;
      pingResults.set(key, { error });
      announcePing(`${t('{name}：测试失败，可重新 Ping。', { name: node.name })} ${message(error)}`);
    } finally { if (currentRequest()) pingBusy = ''; renderConnectionCenter(); }
  }
  function openExitPicker(opener = $('#choose-exit'), exit = '') {
    if (isLocked() || isDesktopBusy() || quickBusy || connectionLost || !state.loaded) return false;
    if (pickerExit !== exit) selectedExit = null;
    pickerExit = exit;
    pickerOriginalExit = selectedExit; pickerCommitted = false; pickerOpener = opener;
    exitDialog.showModal(); syncPingAnnouncement(); renderConnectionCenter();
    // Start on the dialog title; opening with the mouse should not paint a
    // focus rectangle on the selected row. Tab still reaches its radio control.
    $('#heading-exits').focus({ preventScroll: true });
    return true;
  }
  $('#choose-exit').addEventListener('click', () => openExitPicker());
  for (const id of ['close-exit-picker', 'cancel-exit-picker']) $(`#${id}`).addEventListener('click', () => { if (!quickBusy) exitDialog.close(); });
  exitDialog.addEventListener('cancel', (event) => { if (quickBusy) event.preventDefault(); });
  // Dismiss a backdrop click, but keep the dialog open during a save or when
  // the pointer drag started inside it (for example, selecting text).
  let pickerBackdropPress = false;
  function onPickerBackdrop(event) {
    if (event.target !== exitDialog) return false;
    const bounds = exitDialog.getBoundingClientRect();
    return event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom;
  }
  exitDialog.addEventListener('pointerdown', (event) => { pickerBackdropPress = onPickerBackdrop(event); });
  exitDialog.addEventListener('click', (event) => {
    const dismiss = pickerBackdropPress && onPickerBackdrop(event);
    pickerBackdropPress = false;
    if (dismiss && !quickBusy) exitDialog.close();
  });
  exitDialog.addEventListener('close', () => {
    if (!pickerCommitted && connectionSetup?.stage === 'node') connectionSetup = null;
    pickerBackdropPress = false;
    if (!pickerCommitted) selectedExit = pickerOriginalExit;
    // Additional exits never keep a staged choice outside their own picker.
    if (pickerExit) selectedExit = null;
    pickerExit = '';
    syncPingAnnouncement(); renderConnectionCenter();
    if (state.page !== 'connection') return;
    if (pickerOpener?.isConnected && !pickerOpener.disabled) pickerOpener.focus();
    else (state.savedValues.tailscale ? $('#choose-exit') : $('#add-exit')).focus();
  });
  $('#picker-go-connection').addEventListener('click', () => {
    if (connectionSetup?.stage === 'node' && exitSignedIn(pickerExit)) return desktopAction('refresh-exits');
    exitDialog.close(); showPage('connection');
  });
  $('#picker-view-settings').addEventListener('click', () => { exitDialog.close(); showPage('settings'); });
  for (const button of document.querySelectorAll('[data-routing-mode]')) button.addEventListener('click', () => {
    if (isLocked() || draftRoutingMode() === button.dataset.routingMode) return;
    $('#routing-mode').value = button.dataset.routingMode;
    return editField($('#routing-mode'));
  });
  $('#cancel-connection-change').addEventListener('click', () => { selectedExit = null; renderConnectionCenter(); });
  async function applyConnectionChanges() {
    if (quickBusy || isDesktopBusy() || isLocked() || state.dirty || state.external || connectionLost || $('#apply-connection-change').disabled) return;
    const changes = connectionChanges();
    const setup = connectionSetup?.stage === 'node' && connectionSetup.exit === (pickerExit || 'main') ? connectionSetup : null;
    if (setup && String(fieldValue(state.savedValues, setup.field)) !== setup.exit) changes[setup.field] = setup.exit;
    const hasChanges = Object.keys(changes).length > 0;
    if (!hasChanges && !setup) return;
    const applied = !hasChanges || await saveExitChanges(changes, '连接选项已保存', doc => {
      selectedExit = null;
      if (setup && connectionSetup === setup) setup.revision = doc.revision;
    });
    if (applied) selectedExit = null;
    if (applied) {
      if (setup && connectionSetup === setup) setup.stage = 'ready';
      if (exitDialog.open) { pickerCommitted = true; exitDialog.close(); }
      if (setup) await advanceConnectionSetup();
    }
  }
  $('#apply-connection-change').addEventListener('click', applyConnectionChanges);
  $('#apply-exit-picker').addEventListener('click', applyConnectionChanges);
  async function desktopAction(action, exit = '', operationID = '') {
    if (isDesktopBusy() || quickBusy || connectionLost || !desktopLive?.desktop) return false;
    if (['connect', 'add-exit', 'login', 'login-exit', 'switch-network', 'refresh-exits'].includes(action) && !canStartNativeOperation()) return false;
    const login = action === 'login' || action === 'login-exit';
    const owner = desktopLive.focus || (networkActive() ? connectionExitRecord()?.id : '') || 'main';
    if (login && networkActive() && savedMode() === 'global' && (exit || 'main') !== owner) {
      const revision = state.revision;
      if (!canManageExits() || !(await confirmGlobalLoss('login'))) return false;
      if (!canManageExits() || state.revision !== revision) return false;
      if (networkActive() && !(await desktopAction('disconnect'))) return false;
      if (!canManageExits() || state.revision !== revision || desktopLive.active) return false;
    }
    operationError = null; clearNotice();
    stateRequest++;
    desktopPending = action;
    desktopOperation = ['connect', 'add-exit', 'login', 'login-exit', 'refresh-exits'].includes(action) ? operationID || connectionSetup?.operation_id || newOperationID() : '';
    renderConnectionCenter();
    try {
      await api('action', { action, ...(exit ? { exit } : {}), ...(desktopOperation ? { operation_id: desktopOperation } : {}), ...(['add-exit', 'connect'].includes(action) ? { revision: state.revision } : {}) });
      return true;
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      operationError = { title: error.code === 'conflict' ? '配置文件已更新' : '操作未完成', detail: error, canReload: error.code === 'conflict' };
      clearNotice(); return false;
    }
    finally { desktopPending = ''; desktopOperation = ''; await refreshState(); renderConnectionCenter(); }
  }
  // Additional exits require a concrete device ID before sharing the action flow.
  async function exitAction(action, exit) {
    return exit ? desktopAction(action, exit) : false;
  }
  // Exit edits are saved directly; drafts must be resolved first like other
  // connection changes, so a structured list never overwrites pending edits.
  async function saveExitChanges(changes, success, onSaved) {
    if (quickBusy || isDesktopBusy() || isLocked() || state.dirty || state.external || connectionLost) {
      if (state.dirty) { operationError = { title: '操作未完成', detail: t('请先保存或放弃更改，再添加出口。') }; clearNotice(); }
      return false;
    }
    if (!canStartNativeOperation()) return false;
    const revision = state.revision;
    const candidateMode = changes['rules.mode'] ?? savedMode();
    if (!(await disconnectForGlobalApply(candidateMode,
      () => revision === state.revision && !state.dirty && !state.external && !isLocked() && !quickBusy &&
        (!desktopLive?.revision || desktopLive.revision === revision)))) return false;
    operationError = null; clearNotice();
    stateRequest++;
    quickOperation = connectionSetup?.operation_id || (desktopLive?.active ? desktopLive.operation_id : '') || newOperationID();
    quickBusy = true; state.busy = true; updateActions();
    try {
      const doc = await api('config', { yaml: state.saved, changes, revision: state.revision, ...(quickOperation ? { operation_id: quickOperation } : {}) });
      onSaved?.(doc);
      useDocument(doc);
      if (success) toast(t(success));
      return true;
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      operationError = { title: error.code === 'conflict' ? '配置文件已更新' : '无法应用连接更改', detail: error, canReload: error.code === 'conflict' };
      clearNotice();
      return false;
    } finally { quickBusy = false; quickOperation = ''; state.busy = false; await refreshState(); updateActions(); }
  }
  function newExitID() {
    const routes = fieldValue(state.savedValues, 'rules.routes');
    const taken = new Set([
      ...configuredExits().map((exit) => exit.id),
      ...['rules.final', 'rules.default_exit', 'rules.global_exit'].map((field) => String(fieldValue(state.savedValues, field))),
      ...(Array.isArray(routes) ? routes : []).map((route) => String(route?.exit || 'main')),
    ]);
    for (;;) {
      const bytes = window.crypto.getRandomValues(new Uint8Array(12));
      const id = `exit-${Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')}`;
      if (!taken.has(id)) return id;
    }
  }
  function canManageExits() {
    return desktopLive?.desktop && state.loaded && state.savedValid && !isLocked() && !isDesktopBusy() && !quickBusy && !connectionLost &&
      !state.dirty && !state.external && (!desktopLive.revision || desktopLive.revision === state.revision) &&
      !['starting', 'stopping', 'authorizing', 'logging-out'].includes(desktopLive.state) &&
      !(desktopLive.active && !networkActive());
  }
  async function confirmGlobalLoss(action) {
    if (globalConsentPending) return false;
    const copy = {
      add: ['断开全局保护并添加出口？', '添加出口会断开 TailSplit 并关闭全局（Kill-Switch）保护，恢复系统网络，流量可能直连。即使保存或登录失败，也会保持未连接；准备好后请手动重新连接。', '断开并添加出口'],
      login: ['断开全局保护并登录？', '登录另一个设备会断开 TailSplit 并关闭全局（Kill-Switch）保护，恢复系统网络，流量可能直连。即使登录失败，也会保持未连接；准备好后请手动重新连接。', '断开并登录'],
      gateway: ['断开全局保护并切换出口？', '切换到另一个设备前会断开 TailSplit 并关闭全局（Kill-Switch）保护，恢复系统网络，流量可能直连。仅保存选择，即使保存失败也会保持未连接；准备好后请手动重新连接。', '断开并保存选择'],
      apply: ['断开全局保护并应用更改？', '应用更改会断开 TailSplit 并关闭全局（Kill-Switch）保护，恢复系统网络，流量可能直连。即使保存失败，也会保持未连接；准备好后请手动重新连接。', '断开并应用更改'],
    }[action];
    const dialog = $('#add-exit-consent-dialog');
    $('#add-exit-consent-title').textContent = t(copy[0]);
    $('#add-exit-consent-description').textContent = t(copy[1]);
    $('#global-loss-confirm').textContent = t(copy[2]);
    $('#global-loss-confirm').value = action;
    globalConsentPending = true; renderConnectionCenter();
    try {
      dialog.returnValue = 'cancel'; dialog.showModal();
      const answer = await new Promise((resolve) => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));
      return answer === action;
    } finally { globalConsentPending = false; renderConnectionCenter(); }
  }
  async function disconnectForGlobalApply(candidateMode, current) {
    const protectedSession = () => networkActive() && (desktopLive.routing_mode || savedMode()) === 'global';
    // Switching explicitly to Split already expresses the change of protection.
    if (!protectedSession() || candidateMode !== 'global') return true;
    if (!(await confirmGlobalLoss('apply')) || !current() || connectionLost || !desktopLive?.desktop || isDesktopBusy()) return false;
    if (!canStartNativeOperation()) return false;
    if (protectedSession() && !(await desktopAction('disconnect'))) return false;
    return current() && !connectionLost && desktopLive?.desktop && !desktopLive.active;
  }
  async function addExit(forConnection = false) {
    if (addingExit || !canManageExits() || exitList().length >= 8) return false;
    if (!canStartNativeOperation()) return false;
    const revision = state.revision;
    const consentRequired = networkActive() && savedMode() === 'global';
    addingExit = true; renderConnectionCenter();
    try {
      if (consentRequired && !(await confirmGlobalLoss('add'))) return;
      if (!canManageExits() || state.revision !== revision || exitList().length >= 8) return;
      // Login needs a stopped core to release its embedded device state. The
      // consent above covers loss of Global protection; never reconnect here.
      if (networkActive() && !(await desktopAction('disconnect'))) return;
      if (!canManageExits() || state.revision !== revision || desktopLive.active || exitList().length >= 8) return;
      const id = newExitID();
      // The controller stages authentication privately. No provisional profile
      // is saved or rendered before it commits a successful device login.
      pendingAddedExits.add(id);
      pendingExitPicker = id;
      if (forConnection && connectionSetup) {
        Object.assign(connectionSetup, { exit: id, added: true, stage: 'login', loginRequested: true });
      }
      if (!(await exitAction('add-exit', id))) {
        pendingAddedExits.delete(id); pendingExitPicker = '';
        return false;
      }
      if (!exitSignedIn(id) && desktopLive?.active && desktopLive.mode === 'login') toast(t('等待 Tailscale 登录'));
      return true;
    } finally {
      addingExit = false;
      if (desktopLive) updateDesktop(desktopLive); else renderConnectionCenter();
      if (consentRequired && !exitDialog.open && !$('#add-exit').disabled) $('#add-exit').focus({ preventScroll: true });
    }
  }
  $('#add-exit').addEventListener('click', () => { connectionSetup = null; return addExit(); });
  $('#picker-login').addEventListener('click', async () => {
    const exit = pickerExit;
    exitDialog.close();
    if (!exit) { await desktopAction('login'); return; }
    pendingExitPicker = exit;
    if (!(await exitAction('login-exit', exit))) pendingExitPicker = '';
  });
  $('#exit-remove').addEventListener('click', async () => {
    if ($('#exit-remove').disabled || state.dirty || state.external) return;
    const exit = pickerExit || 'main';
    const revision = state.revision;
    const dialog = $('#remove-exit-dialog');
    dialog.returnValue = 'cancel'; dialog.showModal();
    const answer = await new Promise((resolve) => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));
    if (answer !== 'remove' || quickBusy || isDesktopBusy() || isLocked() || state.dirty || connectionLost) return;
    if (state.revision !== revision || state.external) {
      operationError = { title: '配置文件已更新', detail: t('当前草稿仍保留。重新读取最新配置后再保存，避免覆盖其他更改。'), canReload: true };
      clearNotice(); renderConnectionCenter(); return;
    }
    operationError = null; clearNotice(); stateRequest++;
    removingExit = true; quickBusy = true; state.busy = true; updateActions();
    let removed = false;
    try {
      const doc = await api('exit-remove', { exit, revision });
      selectedExit = null; pendingExitPicker = ''; clearPingResults();
      useDocument(doc); removed = true; toast(t('出口已删除'));
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      operationError = { title: error.code === 'conflict' ? '配置文件已更新' : '无法删除出口', detail: error, canReload: error.code === 'conflict' };
      clearNotice();
    } finally {
      removingExit = false; quickBusy = false; state.busy = false;
      await refreshState(); updateActions();
      if (removed && exitDialog.open) { pickerCommitted = true; exitDialog.close(); }
    }
  });
  $('#kill-switch').addEventListener('change', async () => {
    const select = $('#kill-switch');
    const mode = select.value;
    if (select.disabled || !['split', 'global'].includes(mode)) { renderConnectionCenter(); return; }
    if (mode === savedMode()) return;
    await saveExitChanges({ 'rules.mode': mode, ...(mode === 'global' ? { 'tun.enabled': true } : {}) }, '连接选项已保存');
  });
  $('#connection-exit').addEventListener('change', async () => {
    const select = $('#connection-exit');
    const id = select.value;
    const exit = configuredExits().find(exit => exit.id === id);
    if (select.disabled || !gatewaySelectable(exit) || state.external) { renderConnectionExit(); return; }
    const field = connectionExitField();
    if (id === String(fieldValue(state.savedValues, field))) return;
    if (!canStartNativeOperation()) { renderConnectionExit(); return; }
    if (networkActive() && savedMode() === 'global') {
      const revision = state.revision;
      if (!canManageExits() || !(await confirmGlobalLoss('gateway'))) { renderConnectionExit(); return; }
      if (!canManageExits() || state.revision !== revision || connectionExitField() !== field) { renderConnectionExit(); return; }
      if (!canStartNativeOperation()) { renderConnectionExit(); return; }
      if (networkActive() && !(await desktopAction('disconnect'))) return;
      if (!canManageExits() || state.revision !== revision || desktopLive.active || connectionExitField() !== field) return;
    }
    await saveExitChanges({ [field]: id }, '连接选项已保存');
  });
  async function openDefaultExitDialog(operationID = '') {
    if (defaultExitDialog.open || isLocked() || state.dirty || state.external || connectionLost) return;
    defaultExitContext = { revision: state.revision, field: connectionExitField(), operation_id: operationID || newOperationID() };
    defaultExitSignature = '';
    defaultExitError('');
    defaultExitDialog.showModal();
    renderDefaultExitDialog();
    $('#default-exit-heading').focus({ preventScroll: true });
    // A blocked startup may not yet have read the saved device's login state.
    if (!desktopLive?.active && configuredExits().some(record => record.exit_node && !exitLive(record.id).backend_state)) {
      await desktopAction('refresh-exits');
    }
  }
  async function connectWithExit(operationID = '') {
    if ($('#desktop-connect').disabled || state.external || defaultExitDialog.open) return;
    if (!canStartNativeOperation()) return;
    if (requiresDefaultExit(state.savedValues)) {
      const records = configuredExits();
      if (!records.length) return beginConnectionSetup('', operationID);
      const selected = records.find(exit => exit.id === String(fieldValue(state.savedValues, connectionExitField())));
      if (selected && !exitCanConnect(selected)) return beginConnectionSetup(selected.id, operationID);
      if (!connectionExitRecord()) {
        if (records.length === 1 && !exitCanConnect(records[0])) return beginConnectionSetup(records[0].id, operationID);
        return openDefaultExitDialog(operationID);
      }
    }
    return desktopAction('connect', '', operationID);
  }
  $('#default-exit-choice').addEventListener('change', () => { defaultExitError(''); renderDefaultExitDialog(); });
  $('#cancel-default-exit').addEventListener('click', () => { if (!defaultExitBusy) defaultExitDialog.close(); });
  defaultExitDialog.addEventListener('cancel', (event) => { if (defaultExitBusy) event.preventDefault(); });
  defaultExitDialog.addEventListener('close', () => {
    defaultExitContext = null;
    if (!defaultExitBusy) $('#desktop-connect').focus({ preventScroll: true });
  });
  $('#default-exit-manage').addEventListener('click', () => {
    if (defaultExitBusy) return;
    const operationID = defaultExitContext?.operation_id;
    defaultExitDialog.close();
    return beginConnectionSetup('', operationID);
  });
  $('#confirm-default-exit').addEventListener('click', async () => {
    if (!defaultExitDialog.open || !defaultExitContext || defaultExitBusy || isLocked() || isDesktopBusy() || state.dirty || connectionLost) return;
    if (state.external || state.revision !== defaultExitContext.revision || connectionExitField() !== defaultExitContext.field) {
      defaultExitError(t('配置文件已更新，请取消后重新选择。')); return;
    }
    const id = $('#default-exit-choice').value;
    const chosen = configuredExits().find(exit => exit.id === id);
    if (!chosen) return;
    const { field, operation_id: operationID } = defaultExitContext;
    const cancelGeneration = setupCancelGeneration;
    if (!exitCanConnect(chosen)) { defaultExitDialog.close(); await beginConnectionSetup(id, operationID); return; }
    defaultExitBusy = true; renderDefaultExitDialog();
    try {
      if (id !== String(fieldValue(state.savedValues, field)) && !(await saveExitChanges({ [field]: id }, '连接选项已保存'))) {
        defaultExitError(operationError?.detail?.message || String(operationError?.detail || t('无法保存默认出口，请重试。')));
        return;
      }
      if (cancelGeneration !== setupCancelGeneration) return;
      defaultExitDialog.close();
      if (state.external || $('#desktop-connect').disabled || connectionExitRecord()?.id !== id) {
        notice(t('操作未完成'), t('默认出口已保存，但当前不可用，请检查后再连接。'));
        return;
      }
      await desktopAction('connect', '', operationID);
    } finally {
      defaultExitBusy = false; renderDefaultExitDialog();
    }
  });
  document.querySelectorAll('[data-desktop-action]').forEach((button) => button.addEventListener('click', () =>
    button.dataset.desktopAction === 'connect' ? connectWithExit() : button.dataset.desktopAction === 'disconnect' ? cancelConnectionSetup() : desktopAction(button.dataset.desktopAction)));
  $('#desktop-startup').addEventListener('change', (event) => desktopAction(event.target.checked ? 'startup-on' : 'startup-off'));
  const logColors = ['muted', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'default'];
  function logRGBColor(red, green, blue) {
    const max = Math.max(red, green, blue), min = Math.min(red, green, blue), delta = max - min;
    if (delta < 24) return 'muted';
    const sector = max === red ? (green - blue) / delta : max === green ? (blue - red) / delta + 2 : (red - green) / delta + 4;
    const hue = ((sector + 6) % 6) * 60;
    if (hue < 30 || hue >= 345) return 'red';
    if (hue < 90) return 'yellow';
    if (hue < 165) return 'green';
    if (hue < 210) return 'cyan';
    if (hue < 270) return 'blue';
    return 'magenta';
  }
  function logIndexedColor(index) {
    if (!Number.isInteger(index) || index < 0 || index > 255) return '';
    if (index < 16) return logColors[index % 8];
    if (index >= 232) return 'muted';
    const cube = index - 16;
    const channel = (value) => value === 0 ? 0 : 55 + value * 40;
    return logRGBColor(channel(Math.floor(cube / 36)), channel(Math.floor(cube / 6) % 6), channel(cube % 6));
  }
  function renderDesktopLogs(text) {
    const output = $('#desktop-log-text');
    const fragment = document.createDocumentFragment();
    let color = '', bold = false, cursor = 0, visible = false;
    const append = (value) => {
      if (!value) return;
      const span = document.createElement('span');
      span.className = [color && `log-${color}`, bold && 'log-bold'].filter(Boolean).join(' ');
      span.textContent = value;
      fragment.append(span);
      visible = true;
    };
    // Interpret foreground SGR only. Discard other terminal commands, including
    // OSC hyperlinks, and keep all log content as text under the existing CSP.
    const controls = /(?:\x1b\[|\x9b)([0-?]*)([ -/]*)([@-~])|(?:\x1b\]|\x9d)[\s\S]*?(?:\x07|\x1b\\|\x9c|$)|(?:\x1b[P^_X]|\x90|\x98|\x9e|\x9f)[\s\S]*?(?:\x1b\\|\x9c|$)|(?:\x1b\[|\x9b)[0-?]*$|\x1b[ -/]*[0-~]|[\x00-\x08\x0b-\x1f\x7f-\x9f]/g;
    for (const match of text.matchAll(controls)) {
      append(text.slice(cursor, match.index));
      cursor = match.index + match[0].length;
      if (match[3] !== 'm' || match[2] || !/^[\d;]*$/.test(match[1])) continue;
      const codes = match[1].split(';').map(Number);
      for (let i = 0; i < codes.length; i++) {
        const code = codes[i];
        if (code === 0) { color = ''; bold = false; }
        else if (code === 1) bold = true;
        else if (code === 22) bold = false;
        else if (code === 39) color = '';
        else if (code >= 30 && code <= 37) color = logColors[code - 30];
        else if (code >= 90 && code <= 97) color = logColors[code - 90];
        else if (code === 38 || code === 48 || code === 58) {
          const mode = codes[++i];
          let next = '';
          if (mode === 5) next = logIndexedColor(codes[++i]);
          else if (mode === 2) {
            const rgb = codes.slice(i + 1, i + 4);
            i += 3;
            if (rgb.length === 3 && rgb.every((value) => Number.isInteger(value) && value >= 0 && value <= 255)) next = logRGBColor(...rgb);
          } else break;
          if (code === 38) color = next;
        }
      }
    }
    append(text.slice(cursor));
    if (visible) output.replaceChildren(fragment);
    else output.textContent = t('暂无日志。');
    return visible;
  }
  let logsError = null;
  let logsEmpty = false;
  let logsBusy = false;
  let logsText = null;
  let logsLatest = null;
  let logsFollow = true;
  function renderDesktopLogError() {
    setStatusText('#desktop-log-error', logsError ? message(logsError) : '');
    $('#desktop-log-error').hidden = !logsError;
  }
  function showLatestDesktopLogs() {
    const output = $('#desktop-log-text');
    // Retain the paused snapshot: a bounded tail can discard the entry being read.
    if (logsLatest !== null && (logsFollow || logsText === null) && logsLatest !== logsText) {
      logsEmpty = !renderDesktopLogs(logsLatest);
      logsText = logsLatest;
    }
    if (logsFollow && state.page === 'logs' && !document.hidden && output.clientHeight > 0) {
      output.scrollTop = output.scrollHeight;
    }
  }
  $('#desktop-log-text').addEventListener('scroll', (event) => {
    const output = event.target;
    if (state.page === 'logs' && !document.hidden && output.clientHeight > 0) {
      const following = output.scrollHeight - output.clientHeight - output.scrollTop <= 8;
      const resumed = following && !logsFollow;
      logsFollow = following;
      if (resumed) showLatestDesktopLogs();
    }
  });
  async function refreshDesktopLogs() {
    if (logsBusy) return;
    if (!desktopLive?.logs_available) {
      logsText = logsLatest = logsError = null;
      renderDesktopLogError();
      $('#desktop-log-text').textContent = t('此页面无法读取日志，请从桌面应用打开。');
      return;
    }
    logsBusy = true; $('#desktop-log-refresh').disabled = true;
    try {
      logsLatest = (await api('logs')).text || '';
      logsError = null; renderDesktopLogError();
      showLatestDesktopLogs();
    } catch (error) {
      logsError = error; renderDesktopLogError();
    } finally { logsBusy = false; $('#desktop-log-refresh').disabled = false; }
  }
  $('#desktop-log-refresh').addEventListener('click', () => {
    logsFollow = true; showLatestDesktopLogs(); return refreshDesktopLogs();
  });

  let updateLive = null;
  let updatePending = '';
  let updateError = '';
  let updateGeneration = 0;
  const updatesBusy = () => updatePending || updateLive?.updates?.busy || updateLive?.updates?.confirming || updateLive?.updates?.checking || updateLive?.updates?.downloading || updateLive?.updates?.installing;

  function updateBytes(bytes) {
    if (bytes < 1024) return `${Math.floor(bytes)} B`;
    if (bytes < 1024 * 1024) return `${new Intl.NumberFormat(locale.language, { minimumFractionDigits: 1, maximumFractionDigits: 1, useGrouping: false }).format(bytes / 1024)} KiB`;
    return `${new Intl.NumberFormat(locale.language, { minimumFractionDigits: 1, maximumFractionDigits: 1, useGrouping: false }).format(bytes / (1024 * 1024))} MiB`;
  }

  function updateAppUpdates(live) {
    updateLive = live;
    const updates = live?.updates;
    $('#app-updates').hidden = !updates;
    const updateButton = $('#update-pill');
    updateButton.hidden = !updates;
    if (!updates) return;
    const busy = Boolean(updatesBusy());
    updateButton.disabled = busy;
    updateButton.classList.toggle('busy', busy);
    const failed = Boolean(updateError || updates.error);
    updateButton.classList.toggle('error', !busy && failed);
    $('#update-current').textContent = t('当前版本 {version}', { version: updates.current_version || t('未知') });
    $('#update-latest').textContent = t('最新版本 {version}', { version: updates.latest_version || t('尚未检查') });
    $('#update-automatic').checked = updatePending === 'automatic-on' || updatePending !== 'automatic-off' && updates.automatic === true;
    $('#update-automatic').disabled = busy;
    $('#update-check').disabled = busy;
    $('#update-check').textContent = updates.checking ? t('正在检查…') : t('检查更新');
    $('#update-install-hint').hidden = !updates.available || live.preview;
    $('#update-preview').hidden = !live.preview;
    const checked = new Date(updates.last_checked);
    const hasChecked = checked.getTime() > 0;
    const current = !updates.available && hasChecked && Boolean(updates.latest_version);
    // An up-to-date app needs no titlebar action; Settings › About keeps the manual check.
    updateButton.hidden = current && !busy && !failed;
    $('#update-pill .icon use').setAttribute('href', busy || failed ? '#i-reload' : updates.available ? '#i-up' : current ? '#i-check' : '#i-reload');
    $('#update-last-checked').textContent = t('上次检查：{date}', { date: hasChecked ? checked.toLocaleString(locale.language === 'zh' ? 'zh-CN' : 'en') : t('尚未检查') });
    const downloaded = Math.max(0, Number(updates.downloaded_bytes) || 0);
    const total = Math.max(0, Number(updates.total_bytes) || 0);
    const percent = total > 0 ? Math.min(100, Math.floor(downloaded / total * 100)) : null;
    const pendingLabels = { check: t('检查中…'), install: t('等待确认…'), 'automatic-on': t('保存中…'), 'automatic-off': t('保存中…') };
    const label = updates.downloading ? (percent === null ? t('下载中…') : t('下载中 {percent}%', { percent })) :
      updates.installing ? t('安装中…') : updates.confirming ? t('等待确认…') : updates.checking ? t('检查中…') : updatePending ? pendingLabels[updatePending] :
      failed ? t('重试更新') : updates.available ? (live.preview ? t('模拟更新') : t('更新')) :
      current ? t('已是最新') : t('检查更新');
    setStatusText('#update-pill-version', label);
    updateButton.title = updates.downloading ?
      `${label} · ${updateBytes(downloaded)}${total > 0 ? ` / ${updateBytes(total)}` : ` · ${t('总大小待确定')}`}` :
      updates.installing ? t('正在准备安装更新，请完成系统确认或安装向导。') : message(updateError || backendError(updates)) ||
      (updates.available ? t('更新至 {version}，安装前会请求确认。', { version: updates.latest_version || t('最新版本') }) : t('检查应用更新'));
    updateButton.setAttribute('aria-label', updates.downloading ? updateButton.title : `${label}. ${updateButton.title}`);
    $('#update-error').textContent = message(updateError || backendError(updates));
    $('#update-error').hidden = !$('#update-error').textContent;
    const notes = String(updates.release_notes || '');
    $('#update-notes').hidden = !notes;
    $('#update-release-notes').textContent = notes.length > 12000 ? `${notes.slice(0, 12000)}…` : notes;
  }

  async function appUpdateAction(action) {
    if (!updateLive?.updates || updatesBusy() || action === 'install' && !updateLive.updates.available) return;
    updateGeneration++;
    updatePending = action; updateError = '';
    updateAppUpdates(updateLive);
    try {
      await api('update-action', { action });
    } catch (error) {
      updateError = error;
    } finally {
      updateGeneration++;
      await refreshState();
      updatePending = '';
      updateAppUpdates(updateLive);
    }
  }
  $('#update-check').addEventListener('click', () => appUpdateAction('check'));
  $('#update-pill').addEventListener('click', () => appUpdateAction(updateLive?.updates?.available ? 'install' : 'check'));
  $('#update-automatic').addEventListener('change', (event) => appUpdateAction(event.target.checked ? 'automatic-on' : 'automatic-off'));

  // Only new samples from the same engine session can establish a transfer rate.
  let trafficSample = null;
  let trafficRates = new Map();
  let trafficExpiryTimer = null;
  const mapRateControls = new Map();
  function sampleTraffic(live) {
    const traffic = live.traffic;
    if (!traffic?.session_id || !Number.isFinite(Date.parse(traffic.sampled_at)) || !traffic.routes || live.state !== 'running' || !networkActive(live)) {
      trafficSample = null; trafficRates.clear(); return;
    }
    const timestamp = Date.parse(traffic.sampled_at);
    if (trafficSample?.session === traffic.session_id && timestamp <= trafficSample.timestamp) {
      if (Date.now() - trafficSample.received > 10000) trafficRates.clear();
      return;
    }
    const next = new Map();
    for (const [id, counters] of Object.entries(traffic.routes)) {
      const up = counters?.upload_bytes, down = counters?.download_bytes;
      if ([up, down].every(value => Number.isSafeInteger(value) && value >= 0)) next.set(id, { up, down });
    }
    trafficRates.clear();
    const elapsed = trafficSample ? (timestamp - trafficSample.timestamp) / 1000 : 0;
    // A hidden tab or stale response must not present a long-term average as live speed.
    if (trafficSample?.session === traffic.session_id && elapsed > 0 && elapsed <= 10) {
      for (const [id, counters] of next) {
        const previous = trafficSample.routes.get(id);
        if (previous && counters.up >= previous.up && counters.down >= previous.down) trafficRates.set(id, {
          up: (counters.up - previous.up) / elapsed, down: (counters.down - previous.down) / elapsed,
        });
      }
    }
    trafficSample = { session: traffic.session_id, timestamp, routes: next, received: Date.now() };
  }
  function transferRate(value) {
    if (!Number.isFinite(value)) return '—';
    const units = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s'];
    const power = value > 0 ? Math.max(0, Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024)))) : 0;
    return `${(value / 1024 ** power).toFixed(power ? 1 : 0)} ${units[power]}`;
  }
  function syncRouteMotion() {
    $('#split-map').dataset.flowPaused = String(document.hidden || state.page !== 'home');
  }
  function renderHomeRates(applied) {
    syncRouteMotion();
    clearTimeout(trafficExpiryTimer);
    trafficExpiryTimer = null;
    const remaining = trafficSample ? 10000 - (Date.now() - trafficSample.received) : 0;
    for (const [id, controls] of mapRateControls) {
      const rate = applied && controls.ready && remaining > 0 ? trafficRates.get(id) : null;
      const up = transferRate(rate?.up), down = transferRate(rate?.down);
      if (controls.up.textContent !== up) controls.up.textContent = up;
      if (controls.down.textContent !== down) controls.down.textContent = down;
      controls.root.title = t('上传 {up} · 下载 {down}', { up, down });
      controls.root.setAttribute('aria-label', controls.root.title);
      for (const direction of ['up', 'down']) {
        if (controls.flow[direction]) controls.flow[direction].dataset.active = String(Boolean(rate && rate[direction] > 0));
      }
    }
    // A stalled poll must not leave old data animating indefinitely.
    if (applied && remaining > 0 && trafficRates.size) trafficExpiryTimer = setTimeout(() => {
      trafficRates.clear(); renderHomeRates(false);
    }, remaining);
  }
  function canonicalRouteSuffix(raw) {
    const value = String(raw || '').replace(/^\*\./, '').replace(/^\./, '').replace(/\.$/, '');
    if (!value) return '';
    // The extra label applies URL's IDNA mapping without reinterpreting a
    // numeric domain as an IPv4 address, which the compiler does not do.
    try {
      const host = new URL(`https://${value}.invalid`).hostname.toLowerCase();
      return host.endsWith('.invalid') ? host.slice(0, -8) : '';
    } catch (_) { return ''; }
  }
  // Saved policy plus authenticated runtime metadata determine private routes.
  function homeRoutes({ global, applied, blocked }) {
    const values = state.savedValues;
    const records = configuredExits(values);
    const exitRoute = (id, roles) => {
      const record = records.find(exit => exit.id === id);
      const ready = Boolean(record?.exit_node);
      const online = exitNodes(id).some(node => node.configured && node.online);
      const tone = blocked || applied && !(ready && exitSignedIn(id) && online) ? 'warn' : applied ? 'ok' : 'planned';
      const detail = blocked ? t('出口不可用 · 已阻断') : !record ? t('出口已移除，请更改分流规则') : !ready ? t('尚未选择节点') :
        applied && !exitSignedIn(id) ? t('需要登录') : applied && !online ? t('出口不可用') : roles.join(' · ');
      return { id, kind: 'exit', name: record ? exitLabel(id) : `${t('缺少出口')} · ${id}`, detail, tone };
    };
    const placeholder = { id: 'unconfigured', kind: 'exit', name: t('Tailscale 出口'), detail: t('尚未配置出口 · 点击开始设置'), tone: 'planned' };
    if (global) {
      const id = String(fieldValue(values, 'rules.global_exit')).trim() || 'main';
      return [records.length ? exitRoute(id, [t('全部接管流量')]) : placeholder];
    }
    const final = String(fieldValue(values, 'rules.final') || 'direct');
    const defaultID = String(fieldValue(values, 'rules.default_exit')).trim() || 'main';
    const roles = new Map();
    const add = (id, role) => (roles.get(id) || roles.set(id, []).get(id)).push(role);
    if (requiresDefaultExit(values)) add(defaultID, t('默认代理出口'));
    // Active ownership comes from the compiled session, never later SDK status.
    // Saved-only previews retain the compiler's canonical suffix precedence.
    const privateExits = desktopLive?.traffic?.private_exits;
    if (applied && Array.isArray(privateExits)) {
      const owners = new Set(privateExits);
      for (const exit of records) if (exit.exit_node && owners.has(exit.id)) add(exit.id, t('私有域名'));
    } else {
      const ownedSuffixes = new Set();
      if (values.tailscale) {
        const magicDomains = fieldValue(values, 'dns.magic_domains');
        const primaryDomains = [...(Array.isArray(magicDomains) ? magicDomains : []), ...(Array.isArray(values.tailscale.domains) ? values.tailscale.domains : [])];
        if (primaryDomains.length) add('main', t('私有域名'));
        for (const suffix of primaryDomains) ownedSuffixes.add(canonicalRouteSuffix(suffix));
      }
      for (const exit of exitList(values)) {
        const suffix = canonicalRouteSuffix(exit.magic_dns_suffix || (String(exit.tailnet || '').toLowerCase().endsWith('.ts.net') ? exit.tailnet : ''));
        if (exit.exit_node && suffix && !ownedSuffixes.has(suffix)) {
          ownedSuffixes.add(suffix);
          add(exit.id, t('私有域名'));
        }
      }
    }
    const groups = new Map();
    const routes = fieldValue(values, 'rules.routes');
    for (const route of Array.isArray(routes) ? routes : []) {
      if (route?.domains?.length || route?.ips?.length) groups.set(String(route.exit || 'main'), (groups.get(String(route.exit || 'main')) || 0) + 1);
    }
    for (const [id, count] of groups) add(id, t('规则组数量', { count }));
    if (final !== 'direct') add(final === 'proxy' ? defaultID : final, t('未匹配流量'));
    const direct = { id: 'direct', kind: 'direct', name: t('直连'), detail: [t('直连规则'), ...(final === 'direct' ? [t('未匹配流量')] : [])].join(' · '), tone: applied ? 'ok' : 'planned' };
    const exits = [...roles].map(([id, list]) => !records.length && id === defaultID && !groups.has(id) && ['direct', 'proxy'].includes(final) ? { ...placeholder, id } : exitRoute(id, list));
    return [direct, ...exits];
  }
  let splitMapKey = '';
  function routeWire(d, tone, index) {
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', d);
    path.setAttribute('class', `tone-${tone}`);
    path.dataset.index = String(index);
    return path;
  }
  function renderSplitMap(routes) {
    const key = JSON.stringify(routes);
    if (key === splitMapKey) return;
    splitMapKey = key;
    mapRateControls.clear();
    const row = 44, gap = 4, height = Math.max(row, routes.length * (row + gap) - gap);
    const status = { ok: t('已生效'), warn: t('需要处理'), planned: t('尚未生效') };
    const wires = [], live = [], flows = [], items = [];
    routes.forEach((route, index) => {
      const y = index * (row + gap) + row / 2;
      const d = `M0 ${height / 2} C 55 ${height / 2}, 45 ${y}, 100 ${y}`;
      wires.push(routeWire(d, route.tone, index));
      const flow = {};
      if (route.tone === 'ok') {
        live.push(routeWire(d, route.tone, index));
        for (const [direction, label] of [['up', 'upload'], ['down', 'download']]) {
          const path = routeWire(d, route.tone, index);
          path.setAttribute('class', `map-flow map-flow-${label}`);
          path.dataset.exit = route.id;
          path.dataset.direction = direction;
          path.dataset.active = 'false';
          flow[direction] = path;
          flows.push(path);
        }
      }
      const item = document.createElement('li');
      const button = document.createElement('button');
      button.type = 'button';
      button.className = `map-route tone-${route.tone}`;
      button.dataset.route = route.kind;
      button.dataset.index = String(index);
      const name = document.createElement('strong');
      name.textContent = route.name;
      name.title = route.name;
      const detail = document.createElement('small');
      detail.textContent = route.detail;
      detail.title = route.detail;
      const note = document.createElement('span');
      note.className = 'visually-hidden';
      note.textContent = `, ${status[route.tone]}`;
      button.dataset.exit = route.id;
      const rates = document.createElement('span');
      rates.className = 'map-rates';
      rates.setAttribute('role', 'group');
      const readings = {};
      for (const direction of ['up', 'down']) {
        const reading = document.createElement('span');
        reading.className = `map-rate map-rate-${direction}`;
        const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        icon.setAttribute('class', 'icon');
        icon.setAttribute('aria-hidden', 'true');
        const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
        use.setAttribute('href', '#i-up');
        icon.append(use);
        const value = document.createElement('span');
        value.textContent = '—';
        reading.append(icon, value);
        rates.append(reading);
        readings[direction] = value;
      }
      mapRateControls.set(route.id, { root: rates, ready: route.tone === 'ok', flow, ...readings });
      button.append(name, detail, note, rates);
      item.append(button);
      items.push(item);
    });
    for (const svg of [$('#home-wires'), $('#home-wires-live'), $('#home-wires-flow')]) {
      svg.setAttribute('viewBox', `0 0 100 ${height}`);
      svg.setAttribute('height', String(height));
    }
    $('#home-wires').replaceChildren(...wires);
    $('#home-wires-live').replaceChildren(...live);
    $('#home-wires-flow').replaceChildren(...flows);
    $('#home-routes').replaceChildren(...items);
  }
  function renderHome(live = desktopLive || {}) {
    const global = savedMode() === 'global';
    const connected = !connectionLost && networkActive(live) && live.state === 'running';
    const matchingState = Boolean(live.active_revision) && live.active_revision === state.revision;
    const blocked = !connectionLost && live.active && global && live.state === 'blocked';
    const setup = connectionSetup;
    const working = Boolean(live.active || pendingSetupCancel || setup && setup.stage !== 'failed');
    const error = connectionLost || live.state === 'failed' || setup?.stage === 'failed';
    const records = configuredExits();
    const first = records.length === 0 && requiresDefaultExit(state.savedValues);
    const labels = {
      authorizing: '等待安装后台服务', login: '等待 Tailscale 登录', authenticating: '正在登录',
      starting: '正在连接', connecting: '正在连接', 'checking-exit': '正在检查出口',
      discovering: '正在读取出口', stopping: '正在断开', 'logging-out': '正在退出登录',
      'waiting-exit': '等待出口上线', 'missing-exit': '正在确认出口',
    };
    const title = pendingSetupCancel ? '正在取消连接' : connectionLost ? '页面连接中断' : !state.savedValid ? '配置需要修正' : blocked ? '出口不可用 · 已阻断' :
      error ? '连接尚未完成' : connected ? '已连接' : sessionNeedsApproval(live) ? '等待管理员批准设备' :
      setup?.stage === 'node' ? '选择节点后即可连接' : working ? labels[live.state] || '正在设置出口' :
      first ? '准备好第一次连接' : '尚未连接';
    setStatusText('#home-status', t(title));
    let description = pendingSetupCancel ? $('#connection-setup-detail').textContent : !state.savedValid ? t('请先修复配置，再开启连接。') : state.dirty ? t('请先保存配置，再连接。') : state.external ? t('配置已更改，请重新读取后再连接。') :
      connectionLost ? t('请确认 TailSplit 仍在运行') : blocked ? t('全局模式正在阻断流量。手动断开会恢复系统网络。') :
      error ? message(setup?.error || operationError?.detail || backendError(live)) || t('当前步骤未完成，请重试。') :
      connected ? !matchingState ? t('已保存的配置尚未应用；当前连接仍使用之前的配置。') : t(global ? '全局流量通过所选出口；出口不可用时阻断。' : '按已保存规则分流；直连与出口各走各的路径。') :
      working ? message($('#connection-setup-detail').textContent || $('#desktop-hint').textContent) :
      first ? t('点击“开始设置”，按提示完成系统授权、Tailscale 登录和节点选择。') : t('开启即可连接。需要登录或选择节点时，应用会带你完成。');
    if (working && !pendingSetupCancel && nativeCancelGuarded()) description = t('恢复记录不可用；此面板意外关闭时会取消当前操作。');
    if (working && nativeCancelGuard && !nativeCancelReady()) description = t('正在准备安全取消，请等待宿主确认后重试。');
    if (!live.desktop && !working) description = t('此页面仅编辑配置，连接操作请使用 TailSplit 桌面应用。');
    setStatusText('#home-description', description);
    const control = $('#home-connect');
    control.disabled = !state.loaded || !live.desktop || (working ? $('#desktop-disconnect').disabled :
      setup?.stage === 'failed' ? $('#connection-setup-retry').disabled : $('#desktop-connect').disabled);
    control.setAttribute('aria-checked', String(connected));
    control.setAttribute('aria-busy', String(working && !connected && !blocked));
    const action = working ? (networkActive(live) ? '断开' : '取消连接') : error ? '重试连接' : '开启连接';
    control.setAttribute('aria-label', t(action));
    setStatusText('#home-action-label', t(action));
    const setupButton = $('#home-setup');
    const showSetup = first && !working;
    setupButton.hidden = !showSetup;
    setupButton.disabled = control.disabled || !showSetup;
    setStatusText('#home-setup', t(error ? '重试设置' : '开始设置'));
    $('#home-toggle-control').hidden = showSetup;

    // Steps describe the current task, never invented installation/authorization results.
    const showSteps = first || Boolean(setup) || working && !networkActive(live) || records.some(exit => !exit.exit_node);
    $('#home-steps').hidden = !showSteps;
    let step = -1;
    if (working || setup) {
      step = live.state === 'authorizing' ? 0 : setup?.stage === 'node' || setup?.stage === 'saving' ? 2 :
        networkActive(live) || setup?.stage === 'connecting' ? 3 : 1;
    }
    for (const [index, name] of ['service', 'login', 'node', 'connect'].entries()) {
      const item = $(`#home-step-${name}`);
      item.classList.toggle('complete', connected || step >= 0 && index < step);
      if (index === step && !error && !connected) item.setAttribute('aria-current', 'step');
      else item.removeAttribute('aria-current');
    }

    const map = $('#split-map');
    const applied = connected && state.savedValid && !state.external && matchingState && (!live.routing_mode || live.routing_mode === savedMode());
    map.dataset.connected = String(applied);
    map.dataset.global = String(global);
    map.dataset.blocked = String(blocked);
    setStatusText('#home-mode', t(global ? '全局代理' : '规则分流'));
    setStatusText('#home-capture', t(global || fieldValue(state.savedValues, 'tun.enabled') ? '整机流量接管' : '应用代理'));
    renderSplitMap(homeRoutes({ global, applied, blocked }));
    renderHomeRates(applied);
    setStatusText('#home-routing-caption', t(!state.savedValid ? '配置尚未有效，图中路径未生效。' : blocked ? '出口不可用，接管流量已阻断；此图展示已保存路由。' :
      applied ? '实线为已连接路由；上下行速度按实际转发字节计算，未取得数据时显示 —。' : '路由预览 · 虚线表示尚未生效的已保存路径。'));
  }
  async function homeConnectionAction() {
    if ($('#home-connect').disabled) return;
    if (desktopLive?.active || pendingSetupCancel || connectionSetup && connectionSetup.stage !== 'failed') {
      await cancelConnectionSetup();
    } else if (connectionSetup?.stage === 'failed') {
      const id = configuredExits().some(exit => exit.id === connectionSetup.exit) ? connectionSetup.exit : connectionExitRecord()?.id || '';
      await beginConnectionSetup(id);
    } else await connectWithExit();
    renderConnectionCenter();
  }
  $('#home-connect').addEventListener('click', homeConnectionAction);
  $('#home-setup').addEventListener('click', homeConnectionAction);
  async function openRoutingRules() {
    showPage('settings');
    const nav = [...document.querySelectorAll('[data-section]')].find(item => item.dataset.section === 'routing');
    await showSection(nav);
  }
  $('#home-edit-rules').addEventListener('click', openRoutingRules);
  $('#home-routes').addEventListener('click', async (event) => {
    const route = event.target.closest?.('[data-route]') || event.target;
    if (route.dataset?.route === 'direct') await openRoutingRules();
    else if (route.dataset?.route) showPage('connection');
  });
  // A destination row and its wire highlight together, like a port and its cable.
  function highlightRoute(event) {
    const leaving = event.type === 'pointerout' || event.type === 'focusout';
    const index = leaving ? '' : event.target.closest?.('[data-index]')?.dataset.index ?? '';
    for (const item of document.querySelectorAll('#split-map [data-index]')) item.classList.toggle('active', item.dataset.index === index);
  }
  for (const name of ['pointerover', 'pointerout', 'focusin', 'focusout']) $('#home-routes').addEventListener(name, highlightRoute);

  function renderConnection(live) {
    const labels = {
      starting: live.mode === 'discovery' ? t('正在读取设备') : t('正在连接'), connecting: t('正在连接'),
      stopping: t('正在断开'), failed: t('连接异常'), authorizing: t('等待安装后台服务'), authenticated: t('已登录'),
      blocked: t('出口不可用 · 已阻断'), 'checking-exit': t('正在检查出口'), 'waiting-exit': t('等待出口上线'),
      'missing-exit': t('正在确认出口'), login: t('等待 Tailscale 登录'), authenticating: t('正在登录'),
      discovering: t('正在读取出口'), 'logging-out': t('正在退出登录'),
    };
    const connected = !connectionLost && live.active && ['tun', 'proxy'].includes(live.mode) && live.state === 'running';
    $('#connection-status').classList.toggle('connected', connected);
    const approval = sessionNeedsApproval(live) && live.state !== 'blocked';
    $('#connection-status').classList.toggle('attention', !connectionLost && (approval || ['blocked', 'waiting-exit', 'missing-exit'].includes(live.state)));
    $('#connection-status').classList.toggle('error', connectionLost || live.state === 'failed');
    setStatusText('#connection-status span', connectionLost ? t('页面连接中断') : live.preview ? t('预览环境') : connected ? t('已连接') : approval ? t('等待管理员批准设备') : labels[live.state] || t('未连接'));
    $('#connection-status').title = $('#connection-status span').textContent;

    $('#sidebar-status').classList.toggle('connected', connected);
  }

  async function refreshState() {
    if (!state.loaded || document.hidden) return;
    const generation = updateGeneration;
    const request = ++stateRequest;
    try {
      const live = await api('state');
      if (request !== stateRequest) return;
      connectionLost = false;
      updateDesktop(live);
      if (generation === updateGeneration) updateAppUpdates(live);
      if (live.revision && live.revision !== state.revision && !isLocked()) {
        const generation = state.generation;
        if (!state.dirty) {
          const doc = await api('config');
          if (request !== stateRequest) return;
          if (generation === state.generation && !state.dirty && !isLocked()) {
            useDocument(doc);
            // Resolve Add completion against the committed inventory now,
            // without applying an older status to a newer configuration.
            if (doc.revision === live.revision) updateDesktop(live);
            return;
          }
          if (doc.revision === state.revision) return;
        }
        state.external = true; clearNotice(); updateActions();
      }
    } catch (_) {
      if (request !== stateRequest) return;
      connectionLost = true;
      renderConnectionCenter();
    } finally {
      adoptSharedSetupCancel();
      if (pendingSetupCancel) {
        if (!connectionLost && await dispatchSetupCancel()) await refreshState();
      } else { await consumeNativeConnect(); await advanceConnectionSetup(); }
    }
  }

  let languagePreference = 'auto';
  let languageBusy = false;
  let languageGeneration = 0;
  function useLanguage(value) {
    if (!['auto', 'en', 'zh'].includes(value.preference) || !['en', 'zh'].includes(value.language)) return;
    languagePreference = value.preference;
    $('#ui-language').value = languagePreference;
    locale.setLanguage(value.language);
  }
  async function loadLanguage() {
    const generation = languageGeneration;
    try {
      const value = await api('language');
      if (generation === languageGeneration) useLanguage(value);
    } catch (_) { /* Keep the browser-language fallback if settings cannot be read. */ }
  }
  $('#ui-language').addEventListener('change', async (event) => {
    if (languageBusy) return;
    const preference = event.target.value;
    languageGeneration++;
    languageBusy = true; event.target.disabled = true;
    try { useLanguage(await api('language', { preference })); }
    catch (error) {
      event.target.value = languagePreference;
      notice(t('无法保存语言偏好'), error.message ? error : t('语言设置未更改，请重试。'));
    } finally { languageBusy = false; event.target.disabled = false; window.TailSplitSelect?.sync(); }
  });
  locale.subscribe(() => {
    renderPageTitle();
    if (state.loaded) {
      const final = $('#final-route').value;
      renderFinalOptions(state.values); $('#final-route').value = final;
      const globalExit = $('#global-exit').value;
      renderExitOptions(state.values, '#global-exit', 'rules.global_exit'); $('#global-exit').value = globalExit;
      const defaultExit = $('#default-exit').value;
      renderExitOptions(state.values, '#default-exit', 'rules.default_exit'); $('#default-exit').value = defaultExit;
      paintRoutes();
    }
    updateActions();
    if (state.loaded) {
      paintPreview();
      $('#file-location').textContent = t('当前设备');
    }
    if (desktopLive) updateDesktop(desktopLive);
    if (desktopLive || connectionLost) renderConnection(desktopLive || {});
    if (updateLive) updateAppUpdates(updateLive);
    if (activeNotice && !$('#notice').hidden) {
      $('#notice-title').textContent = t(activeNotice.title);
      $('#notice-detail').textContent = message(activeNotice.detail);
    }
    if (!$('#toast').hidden) $('#toast').textContent = t(activeToast);
    renderDesktopLogError();
    if (logsEmpty) $('#desktop-log-text').textContent = t('暂无日志。');
  });

  async function refreshPage() {
    await Promise.all([refreshState(), state.page === 'logs' && !document.hidden ? refreshDesktopLogs() : null]);
  }
  window.addEventListener('beforeunload', (event) => { if (state.dirty || Object.keys(connectionChanges()).length) { event.preventDefault(); event.returnValue = ''; } });
  document.addEventListener('visibilitychange', () => { syncRouteMotion(); refreshPage(); });
  window.addEventListener('tailsplit-window-shown', refreshPage);
  window.addEventListener('storage', event => { if (event.key?.startsWith(sharedCancelPrefix)) refreshPage(); });
  window.addEventListener('hashchange', async () => {
    if (mygoWindow) return;
    const incoming = new URLSearchParams(location.hash.slice(1));
    const request = incoming.get('connect');
    if (!request) return;
    if (incoming.get('token') !== token) { location.reload(); return; }
    rememberBrowserConnect(request);
    history.replaceState(null, '', location.pathname);
    await refreshPage();
  });
  $('.brand').addEventListener('click', (event) => { event.preventDefault(); showPage('home'); });
  if (!token) {
    $('#loading').hidden = true;
    notice(t('从 TailSplit 打开配置页面'), t('请点击菜单栏中的 TailSplit，再选择“设置与工具 → 配置面板…”。'));
  } else {
    loadLanguage().then(load).then(refreshState);
    setInterval(refreshPage, 4000);
  }
})();
