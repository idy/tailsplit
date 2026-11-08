(() => {
  'use strict';

  const $ = (selector) => document.querySelector(selector);
  const locale = window.TailSplitI18n;
  const t = (key, values) => locale.t(key, values);
  function backendError(value) {
    return value?.error_i18n ? { messages: value.error_i18n, message: value.error || '' } : value?.error || '';
  }
  function message(value) {
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
    const actionbar = $('#actionbar');
    new window.ResizeObserver(() => {
      const height = actionbar.getBoundingClientRect().height;
      if (height > 0) document.documentElement.style.setProperty('--actionbar-height', `${height}px`);
      else document.documentElement.style.removeProperty('--actionbar-height');
    }).observe(actionbar);
  }
  const macWindow = window.webkit?.messageHandlers?.tailsplitWindow;
  const windowsWindow = window.chrome?.webview;
  const windowHost = macWindow || windowsWindow;
  if (windowHost) {
    document.documentElement.dataset.windowChrome = macWindow ? 'macos' : 'windows';
    $('#window-controls').hidden = !windowsWindow;
    const isTitlebarBackground = (event) => !event.target.closest('button, a, input, select, textarea, [role="button"]');
    let titlebarPress = null;
    $('#titlebar').addEventListener('mousedown', (event) => {
      titlebarPress = null;
      if (event.button !== 0 || event.detail > 1 || !isTitlebarBackground(event)) return;
      event.preventDefault();
      titlebarPress = { x: event.clientX, y: event.clientY };
    });
    // Hand off only an actual drag. Entering a native move loop on mouse-down
    // would consume the click sequence needed for double-click zoom.
    window.addEventListener('mousemove', (event) => {
      if (!(event.buttons & 1)) titlebarPress = null;
      if (!titlebarPress || Math.hypot(event.clientX - titlebarPress.x, event.clientY - titlebarPress.y) < 4) return;
      titlebarPress = null;
      windowHost.postMessage('drag');
    });
    window.addEventListener('mouseup', () => { titlebarPress = null; });
    window.addEventListener('blur', () => { titlebarPress = null; });
    $('#titlebar').addEventListener('dblclick', (event) => {
      if (event.button !== 0 || !isTitlebarBackground(event)) return;
      event.preventDefault();
      windowHost.postMessage('zoom');
    });
    document.querySelectorAll('[data-window-action]').forEach((button) => {
      button.addEventListener('click', () => windowHost.postMessage(button.dataset.windowAction));
    });
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
    mode: 'form', page: 'connection', section: 'general', valid: false, parsed: false, values: {}, savedValues: {}, savedValid: false, loaded: false,
    dirty: false, busy: false, loading: false, switching: false, validating: false,
    previewing: false, external: false,
    generation: 0, previewed: 0, pending: null, timer: null, toastTimer: null,
    customRuleSource: '',
  };
  const isLocked = () => state.busy || state.loading || state.switching;

  const fragment = new URLSearchParams(location.hash.slice(1));
  let token = fragment.get('token');
  try {
    if (token) sessionStorage.setItem('tailsplit-config-token', token);
    else token = sessionStorage.getItem('tailsplit-config-token');
  } catch (_) { /* The current tab can still use the fragment in private mode. */ }
  if (location.hash) history.replaceState(null, '', location.pathname);

  async function api(path, data) {
    const response = await fetch(`/api/${path}`, {
      method: data === undefined ? 'GET' : 'POST',
      headers: { Authorization: `Bearer ${token || ''}`, 'Accept-Language': locale.language, ...(data === undefined ? {} : { 'Content-Type': 'application/json' }) },
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

  let activeNotice = null;
  // Operation failures survive automatic document reloads until a retry or explicit reload.
  let operationError = null;
  let activeToast = '';
  function notice(title, detail, kind = 'error', canReload = false) {
    activeNotice = { title, detail };
    $('#notice-title').textContent = t(title);
    $('#notice-detail').textContent = message(detail);
    $('#notice').classList.toggle('error', kind === 'error');
    $('#notice-reload').hidden = !canReload;
    $('#notice').hidden = false;
  }

  function clearNotice() {
    if (operationError) {
      notice(operationError.title, operationError.detail, 'error', operationError.canReload || state.external);
    } else if (state.external) {
      notice(t('磁盘中的配置已更新'), t('当前草稿仍保留。重新读取最新配置后再保存，避免覆盖其他更改。'), 'warning', true);
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
      ...exitList(values).map((exit) => [exit.id, exitLabel(exit.id, values)])];
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
    $('#refresh-exits').hidden = state.page !== 'connection';
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
    clearTimeout(state.timer); state.timer = null; state.generation++;
    state.previewed = state.generation; state.pending = null;
    state.saved = state.base = state.draft = doc.yaml;
    state.revision = doc.revision;
    state.values = state.savedValues = doc.values || {};
    state.savedValid = doc.valid;
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
    const labels = { connection: t('出口节点列表'), logs: t('日志'), settings: t('设置') };
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
    if (isLocked()) return;
    state.busy = true; updateActions();
    try {
      if (!(await flushPreview()) || !state.valid || state.external) return;
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
  const isDesktopBusy = () => desktopPending !== '';
  let desktopLive = null;
  let connectionLost = false;
  let quickBusy = false;
  let removingExit = false;
  let stateRequest = 0;
  let selectedExit = null;
  // The picker edits one exit at a time: '' is the primary device.
  let pickerExit = '';
  let pendingExitPicker = '';
  let extraExitsSignature = '';
  let exitListSignature = '';
  let pickerOriginalExit = null;
  let pickerCommitted = false;
  let pickerOpener = null;
  let pingBusy = '';
  let pingGeneration = 0;
  const pingResults = new Map();
  const exitDialog = $('#exit-picker-dialog');
  const networkActive = (live = desktopLive) => Boolean(live?.active && ['tun', 'proxy'].includes(live.mode));
  const needsAuthentication = (live = desktopLive) => live?.backend_state === 'NeedsLogin' || live?.state === 'login' && !live?.focus;
  const signedIn = (live = desktopLive) => !needsAuthentication(live) && (live?.backend_state === 'Running' || live?.state === 'authenticated' && !live?.focus);
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
    return id !== null && (id ? exitLive(id).backend_state === 'Running' : signedIn());
  }
  function exitLabel(exit = '', values = state.savedValues) {
    const id = exitKey(exit);
    if (id === null) return t('未设置');
    return id ? String(exitList(values).find((item) => item.id === id)?.name || exitLive(id).network || t('新出口')) :
      String(fieldValue(values, 'tailscale.name') || '') || t('主出口');
  }
  function namedExitSelection(exit, nodeID) {
    const network = exitLive(exit.id).network || exit.tailnet || '';
    const node = exitNodes(exit.id).find((node) => node.id === nodeID);
    const device = node?.name || nodeID;
    const automatic = exit.auto_name || !exit.name;
    return { ...exit, exit_node: nodeID, tailnet: exit.tailnet || network,
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
    const current = String(fieldValue(state.savedValues, connectionExitField()));
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
    return networkActive() ? exitCanConnect(exit) : Boolean(exit?.exit_node);
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
    select.title = t(networkActive() ? '选择后保存并重新连接，网络会短暂中断。' : '仅保存选择，保持未连接。');
    $('#connection-exit-control').hidden = state.page !== 'connection';
    $('#exit-label').hidden = state.page === 'connection';
    setStatusText('#connection-exit-label', t(savedMode() === 'global' ? '全局出口' : '默认出口'));
    window.TailSplitSelect?.sync();
  }
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
        option.disabled = !exitCanConnect(exit); return option;
      }));
      select.value = records.some((exit) => exit.id === current) ? current : '';
    }
    const stale = state.external || state.revision !== defaultExitContext.revision || connectionExitField() !== defaultExitContext.field;
    if (stale && !defaultExitBusy) defaultExitError(t('配置文件已更新，请取消后重新选择。'));
    const locked = defaultExitBusy || isLocked() || isDesktopBusy() || quickBusy || connectionLost || state.dirty || stale;
    const global = defaultExitContext.field === 'rules.global_exit';
    setStatusText('#default-exit-heading', t(global ? '选择全局出口' : '选择默认出口'));
    setStatusText('#default-exit-choice-label', t(global ? '全局出口' : '默认出口'));
    select.disabled = locked || !records.some(exitCanConnect);
    $('#confirm-default-exit').disabled = locked || !exitCanConnect(records.find((exit) => exit.id === select.value));
    $('#confirm-default-exit').textContent = t(defaultExitBusy ? '正在应用…' : '确认并连接');
    $('#cancel-default-exit').disabled = defaultExitBusy;
    $('#default-exit-manage').disabled = defaultExitBusy;
    $('#default-exit-empty').hidden = records.some(exitCanConnect);
    $('#default-exit-manage').hidden = records.some(exitCanConnect);
    window.TailSplitSelect?.sync();
  }
  const connectionChanges = () => ({
    ...(selectedExit !== null && selectedExit !== savedExit(pickerExit) ? pickerExit ?
      { exits: exitList().map((exit) => exit.id === pickerExit ? namedExitSelection(exit, selectedExit) : exit) } :
      { 'tailscale.exit_node': selectedExit, ...(fieldValue(state.savedValues, 'tailscale.tailnet') || !desktopLive?.network ? {} : { 'tailscale.tailnet': desktopLive.network }) } : {}),
  });
  function modeNeedsExit(values) {
    return fieldValue(values, 'rules.mode') === 'global' || fieldValue(values, 'rules.final') !== 'direct' ||
      Boolean(fieldValue(values, 'rules.source')) || ['rules.proxy_domains', 'rules.proxy_ips', 'rules.routes', 'dns.magic_domains'].some((field) => {
        const items = fieldValue(values, field); return Array.isArray(items) && items.length > 0;
      });
  }
  function exitNodes(exit = '') {
    const live = exitLive(exit);
    const nodes = (Array.isArray(live.exit_nodes) ? live.exit_nodes : []).map((node) => ({
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
  function clearPingResults() {
    pingGeneration++;
    pingResults.clear();
    announcePing('');
  }
  function updateDesktop(live) {
    if (desktopLive && (live.network !== desktopLive.network || live.hostname !== desktopLive.hostname || signedIn(live) !== signedIn())) clearPingResults();
    // A device that signs out or changes Tailnet invalidates its own results.
    const previous = desktopLive?.exits || {};
    desktopLive = live;
    for (const [id, exit] of Object.entries(previous)) {
      const current = exitLive(id);
      if (current.network !== exit.network || current.hostname !== exit.hostname || current.backend_state !== exit.backend_state) {
        for (const key of [...pingResults.keys()]) if (key.startsWith(`${id}|`)) pingResults.delete(key);
      }
    }
    const online = new Set(['', ...exitList().map((exit) => exit.id)].flatMap((exit) =>
      exitNodes(exit).filter((node) => node.online).map((node) => pingKey(node.id, exit))));
    for (const [key] of pingResults) {
      if (!online.has(key)) pingResults.delete(key);
    }
    if (pendingExitPicker && exitSignedIn(pendingExitPicker) && !exitDialog.open && state.page === 'connection' && !isDesktopBusy()) {
      const exit = pendingExitPicker;
      pendingExitPicker = '';
      if (openExitPicker($('#add-exit'), exit)) return;
    }
    renderConnectionCenter();
  }
  function renderConnectionCenter() {
    const live = desktopLive || {};
    const connected = networkActive(live);
    const authenticated = signedIn(live);
    const discovering = live.active && !connected;
    const locked = isLocked() || isDesktopBusy() || quickBusy || connectionLost;
    const changing = Boolean(Object.keys(connectionChanges()).length);
    const hasPrimary = Boolean(state.savedValues.tailscale);
    const records = configuredExits();
    const hasTailnet = records.length > 0;
    const proxyPolicy = modeNeedsExit(state.savedValues);
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
    const targetReady = records.some(exit => exit.id === targetID && exit.exit_node);
    $('#kill-switch-control').hidden = !live.desktop;
    killSwitch.value = mode;
    killSwitch.disabled = !live.desktop || locked || discovering || changing || state.dirty || state.external || !state.loaded || !state.savedValid || connected && !targetReady;
    killSwitch.title = connected && !targetReady ? t(mode === 'global' ? '请先在设置中选择默认代理出口。' : '请先在设置中选择全局出口。') :
      t(mode === 'global' ? '关闭后使用规则分流，保留现有规则。' : '开启全局代理；出口掉线时阻断流量，手动断开恢复系统网络。');
    const activeExit = connectionExit();
    renderConnectionExit();
    renderDefaultExitDialog();
    const needsDefaultChoice = (hasTailnet || proxyPolicy) && !connectionExitRecord();
    const activeExitReady = exitSignedIn(activeExit);
    const activeExitNodes = exitNodes(activeExit);
    const chosenNode = pickerNodes.find((node) => node.id === chosen || node.name === chosen || selectedExit === null && node.configured);
    const savedRoutes = fieldValue(state.savedValues, 'rules.routes');
    const referencedExits = new Set([
      fieldValue(state.savedValues, 'rules.default_exit'), fieldValue(state.savedValues, 'rules.final'),
      ...(Array.isArray(savedRoutes) ? savedRoutes : []).filter(route => route && (route.domains?.length || route.ips?.length)).map(route => route.exit),
    ]);
    const extrasBlocked = mode === 'split' && exitList().some(exit => referencedExits.has(exit.id) && exit.exit_node && exitLive(exit.id).backend_state === 'NeedsLogin');
    $('#desktop-controls').hidden = !live.desktop;
    $('#desktop-connect').hidden = Boolean(live.active);
    $('#desktop-disconnect').hidden = !live.active;
    $('#desktop-connect').disabled = !live.desktop || locked || state.dirty || state.external || !state.savedValid || changing || !needsDefaultChoice && (extrasBlocked || (hasTailnet || proxyPolicy) && (!activeExitReady || !savedExit(activeExit) || activeExitNodes.some((node) => node.configured && !node.online && !node.missing)));
    $('#desktop-disconnect').disabled = !live.desktop || isDesktopBusy() || quickBusy || connectionLost;
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
    $('#startup-setting').hidden = !live.desktop;
    $('#desktop-startup').disabled = !live.desktop || locked;
    $('#desktop-startup').checked = live.startup === true;
    const transitioning = ['starting', 'stopping', 'authorizing', 'logging-out'].includes(live.state) || discovering;
    const network = live.state === 'logging-out' ? t('正在退出登录') : live.network ||
      (authenticated ? t('已登录（网络名称未提供）') : live.backend_state === 'NeedsLogin' ? t('未登录') : transitioning ? t('正在读取网络…') : t('尚未读取网络'));
    setStatusText('#desktop-network-name', network);
    $('#desktop-network-name').title = network;
    const identity = authenticated ? live.hostname || t('已登录') :
      needsAuthentication(live) ? t('未登录 Tailscale') : discovering ? t('正在读取设备') : t('未登录 Tailscale');
    const extra = exitLive(pickerExit);
    const extraIdentity = !pickerExit ? '' : exitSignedIn(pickerExit) ? [extra.hostname, extra.network].filter(Boolean).join(' · ') || t('已登录') :
      extra.backend_state === 'NeedsLogin' ? t('未登录 Tailscale') : t('尚未读取设备');
    setStatusText('#tailnet-identity', connectionLost ? t('页面连接中断') : pickerExit ? extraIdentity : identity);
    $('#picker-login').hidden = exitSignedIn(pickerExit);
    $('#picker-login').disabled = !live.desktop || locked || transitioning;
    $('#exit-danger-zone').hidden = !records.some((record) => record.id === (pickerExit || 'main'));
    $('#exit-remove').disabled = !live.desktop || locked || transitioning || state.dirty || state.external;
    $('#exit-remove').textContent = removingExit ? t('正在删除…') : t('删除出口…');
    setStatusText('#heading-exits', exitLabel(pickerExit));
    setStatusText('#main-exit-name', exitLabel());
    $('#main-exit-status').className = `exit-status ${exitStatus('')}`;
    $('#main-exit-badge').hidden = activeExit !== '';
    $('#main-exit-badge').textContent = mode === 'global' ? t('全局出口') : t('默认');
    $('#add-exit').disabled = locked || !state.loaded || state.dirty || exitList().length >= 8;
    $('#add-exit').title = state.dirty ? t('请先保存或放弃更改，再添加出口。') : '';
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
      live.preview ? t('界面预览：操作仅模拟界面状态，不会创建网络连接。') :
      state.dirty ? t('请先保存配置，再连接。') : changing ? t('连接选项尚未应用，请先确认选择。') :
      needsDefaultChoice ? t('未设置默认出口') : !activeExitReady && hasTailnet ? t('先登录 Tailscale，再选择出口并连接。') : hasTailnet && !savedExit(activeExit) ? t('尚未选择出口') : '';
    setStatusText('#desktop-hint', hint);
    $('#desktop-hint').hidden = !hint || state.dirty || changing;
    $('#desktop-connect').title = hint || t('连接 TailSplit');
    $('#connection-apply').hidden = exitDialog.open || !changing && !quickBusy;
    const name = (pickerExit ? `${exitLabel(pickerExit)} · ` : '') + (chosenNode?.name || chosen || t('尚未选择出口'));
    const selection = t('已选：{name} · {mode}', { name, mode: mode === 'global' ? t('全局代理') : t('规则分流') });
    const consequence = connected ? t('确认后保存并重新连接，网络会短暂中断。') : t('仅保存选择，保持未连接。');
    const applyLabel = quickBusy ? t('正在应用…') : connected ? t('切换出口') : t('保存选择');
    const cannotApply = locked || discovering || state.dirty || state.external || !changing || selectedExit !== null && (!chosenNode || !chosenNode.online || chosenNode.missing === true);
    for (const prefix of ['connection', 'exit-dialog']) {
      setStatusText(`#${prefix}-selection`, selection);
      setStatusText(`#${prefix}-consequence`, consequence);
    }
    for (const id of ['apply-connection-change', 'apply-exit-picker']) {
      $(`#${id}`).textContent = id === 'apply-exit-picker' && !changing && !quickBusy ? t(connected ? '切换出口' : '保存选择') : applyLabel;
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
    $('#picker-go-connection').hidden = Boolean(pickerExit) || signedIn();
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
      return [exit.id, exitLabel(exit.id), exit.exit_node, node?.name || '', node?.online, live.network || '', live.backend_state || '', exitStatus(exit.id)];
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
      detail.textContent = backend === 'NeedsLogin' ? t('未登录') : network || (backend === 'Running' ? t('已登录（网络名称未提供）') : t('尚未读取网络'));
      address.append(exitName, detail);
      if (configured && backend === 'Running' && !online) {
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
      if (backend === 'NeedsLogin') {
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
    $('#exit-empty').hidden = visible.length > 0;
    const ready = exitSignedIn(pickerExit);
    setStatusText('#exit-empty-title', discovering ? t('正在读取出口') : ready ? t('未发现可用出口') : t('尚未读取出口'));
    setStatusText('#exit-empty-detail', ready ? t('确认设备已在 Tailnet 中提供出口，并获准使用，然后刷新。') : t('登录 Tailscale 后，刷新可用出口。'));
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
    const generation = pingGeneration;
    pingBusy = key; pingResults.delete(key); renderConnectionCenter();
    announcePing(t('正在 Ping {name}', { name: node.name }));
    try {
      const result = await api('exit-ping', exit ? { id, exit } : { id });
      if (generation !== pingGeneration || !exitNodes(exit).some((node) => node.id === id && node.online)) return;
      if (result.id !== id || !Number.isFinite(result.latency_ms) || result.latency_ms < 0 || typeof result.direct !== 'boolean') throw new Error(t('未收到有效的 Ping 结果，请重试。'));
      pingResults.set(key, result);
      announcePing(t('{name}：{latency}', { name: node.name, latency: pingLabel(result) }));
    } catch (error) {
      if (generation !== pingGeneration) return;
      pingResults.set(key, { error });
      announcePing(`${t('{name}：测试失败，可重新 Ping。', { name: node.name })} ${message(error)}`);
    } finally { pingBusy = ''; renderConnectionCenter(); }
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
  $('#picker-go-connection').addEventListener('click', () => { exitDialog.close(); showPage('connection'); });
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
    if (!Object.keys(changes).length) return;
    const applied = await saveExitChanges(changes, '连接选项已保存', () => { selectedExit = null; });
    if (applied && exitDialog.open) { pickerCommitted = true; exitDialog.close(); }
  }
  $('#apply-connection-change').addEventListener('click', applyConnectionChanges);
  $('#apply-exit-picker').addEventListener('click', applyConnectionChanges);
  async function desktopAction(action, exit = '') {
    if (isDesktopBusy() || quickBusy || connectionLost || !desktopLive?.desktop) return false;
    operationError = null; clearNotice();
    stateRequest++;
    desktopPending = action;
    renderConnectionCenter();
    try {
      await api('action', { action, ...(exit ? { exit } : {}) });
      return true;
    } catch (error) { operationError = { title: '操作未完成', detail: error }; clearNotice(); return false; }
    finally { desktopPending = ''; await refreshState(); renderConnectionCenter(); }
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
    operationError = null; clearNotice();
    stateRequest++;
    quickBusy = true; state.busy = true; updateActions();
    try {
      const doc = await api('config', { yaml: state.saved, changes, revision: state.revision });
      onSaved?.();
      useDocument(doc);
      toast(t(success));
      return true;
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      operationError = { title: error.code === 'conflict' ? '配置文件已更新' : '无法应用连接更改', detail: error, canReload: error.code === 'conflict' };
      clearNotice();
      return false;
    } finally { quickBusy = false; state.busy = false; await refreshState(); updateActions(); }
  }
  function newExitID() {
    const taken = new Set(exitList().map((exit) => exit.id));
    for (let index = 1; ; index++) {
      const id = `exit-${index}`;
      if (!taken.has(id)) return id;
    }
  }
  $('#add-exit').addEventListener('click', async () => {
    if ($('#add-exit').disabled) return;
    const id = newExitID();
    // Login needs a stopped core. Stop first, so saving a first pending exit
    // never tries to reconnect through a device that is not authorized yet.
    if (networkActive() && !(await desktopAction('disconnect'))) return;
    const exits = [...exitList(), { id, auto_name: true, exit_node: '' }];
    const changes = { exits };
    if (!configuredExits().length) { changes['rules.default_exit'] = '-'; changes['rules.global_exit'] = '-'; }
    if (!(await saveExitChanges(changes, '出口已添加'))) return;
    pendingExitPicker = id;
    if (!(await exitAction('login-exit', id))) pendingExitPicker = '';
  });
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
    await saveExitChanges({ [field]: id }, '连接选项已保存');
  });
  async function openDefaultExitDialog() {
    if (defaultExitDialog.open || isLocked() || state.dirty || state.external || connectionLost) return;
    defaultExitContext = { revision: state.revision, field: connectionExitField() };
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
  async function connectWithExit() {
    if ($('#desktop-connect').disabled || state.external || defaultExitDialog.open) return;
    if ((configuredExits().length || modeNeedsExit(state.savedValues)) && !connectionExitRecord()) {
      return openDefaultExitDialog();
    }
    return desktopAction('connect');
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
    defaultExitDialog.close(); showPage('connection'); $('#add-exit').focus({ preventScroll: true });
  });
  $('#confirm-default-exit').addEventListener('click', async () => {
    if (!defaultExitDialog.open || !defaultExitContext || defaultExitBusy || isLocked() || isDesktopBusy() || state.dirty || connectionLost) return;
    if (state.external || state.revision !== defaultExitContext.revision || connectionExitField() !== defaultExitContext.field) {
      defaultExitError(t('配置文件已更新，请取消后重新选择。')); return;
    }
    const id = $('#default-exit-choice').value;
    if (!exitCanConnect(configuredExits().find((exit) => exit.id === id))) return;
    const { field } = defaultExitContext;
    defaultExitBusy = true; renderDefaultExitDialog();
    try {
      if (id !== String(fieldValue(state.savedValues, field)) && !(await saveExitChanges({ [field]: id }, '连接选项已保存'))) {
        defaultExitError(operationError?.detail?.message || String(operationError?.detail || t('无法保存默认出口，请重试。')));
        return;
      }
      defaultExitDialog.close();
      if (state.external || $('#desktop-connect').disabled || connectionExitRecord()?.id !== id) {
        notice(t('操作未完成'), t('默认出口已保存，但当前不可用，请检查后再连接。'));
        return;
      }
      await desktopAction('connect');
    } finally {
      defaultExitBusy = false; renderDefaultExitDialog();
    }
  });
  document.querySelectorAll('[data-desktop-action]').forEach((button) => button.addEventListener('click', () =>
    button.dataset.desktopAction === 'connect' ? connectWithExit() : desktopAction(button.dataset.desktopAction)));
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
  const updatesBusy = () => updatePending || updateLive?.updates?.checking || updateLive?.updates?.downloading || updateLive?.updates?.installing;

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
      updates.installing ? t('安装中…') : updates.checking ? t('检查中…') : updatePending ? pendingLabels[updatePending] :
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

  function renderConnection(live) {
    const labels = {
      starting: live.mode === 'discovery' ? t('正在读取设备') : t('正在连接'), connecting: t('正在连接'),
      stopping: t('正在断开'), failed: t('连接异常'), authorizing: t('等待授权'), authenticated: t('已登录'),
      blocked: t('出口不可用 · 已阻断'), 'checking-exit': t('正在检查出口'), 'waiting-exit': t('等待出口上线'),
      'missing-exit': t('正在确认出口'), login: t('等待 Tailscale 登录'), authenticating: t('正在登录'),
      discovering: t('正在读取出口'), 'logging-out': t('正在退出登录'),
    };
    const connected = !connectionLost && live.active && ['tun', 'proxy'].includes(live.mode) && live.state === 'running';
    $('#connection-status').classList.toggle('connected', connected);
    $('#connection-status').classList.toggle('attention', !connectionLost && ['blocked', 'waiting-exit', 'missing-exit'].includes(live.state));
    $('#connection-status').classList.toggle('error', connectionLost || live.state === 'failed');
    setStatusText('#connection-status span', connectionLost ? t('页面连接中断') : live.preview ? t('预览环境') : connected ? t('已连接') : labels[live.state] || t('未连接'));
    $('#connection-status').title = $('#connection-status span').textContent;

    $('#sidebar-status').classList.toggle('connected', connected);
    const selected = connectionExit();
    const selectedLabel = selected === null ? (configuredExits().length || modeNeedsExit(state.savedValues) ? t('未设置默认出口') : '') : exitLive(selected).exit || savedExit(selected);
    setStatusText('#exit-label', connectionLost ? t('请确认 TailSplit 仍在运行') : selectedLabel || (live.preview ? t('更改仅保存到预览副本') : t('本机配置')));
    $('#exit-label').title = $('#exit-label').textContent;
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
          if (generation === state.generation && !state.dirty && !isLocked()) {
            useDocument(doc);
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
  document.addEventListener('visibilitychange', refreshPage);
  $('.brand').addEventListener('click', (event) => { event.preventDefault(); showPage('connection'); });
  if (!token) {
    $('#loading').hidden = true;
    notice(t('从 TailSplit 打开配置页面'), t('请点击菜单栏中的 TailSplit，再选择“设置与工具 → 配置面板…”。'));
  } else {
    loadLanguage().then(load).then(refreshState);
    setInterval(refreshPage, 4000);
  }
})();
