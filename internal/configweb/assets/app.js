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
    'rules.final': 'direct', 'rules.gfwlist': '', 'rules.proxy_domains': [],
    'rules.direct_domains': [], 'rules.proxy_ips': [], 'rules.direct_ips': [],
    'tun.enabled': false, 'tun.mtu': 1400, 'app.connect_on_start': false,
  };
  const state = {
    saved: '', base: '', draft: '', revision: '', changes: Object.create(null),
    mode: 'form', page: 'connection', valid: false, parsed: false, values: {}, savedValues: {}, savedValid: false, loaded: false,
    dirty: false, busy: false, loading: false, switching: false, validating: false,
    previewing: false, external: false,
    generation: 0, previewed: 0, pending: null, timer: null, toastTimer: null,
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
    if (state.external) {
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
    let value = values;
    for (const key of path.split('.')) {
      if (value === null || typeof value !== 'object' || !Object.hasOwn(value, key)) return defaults[path];
      value = value[key];
    }
    return value === null || value === undefined ? defaults[path] : value;
  }

  function fillFields(values) {
    for (const control of controls) {
      const value = fieldValue(values, control.dataset.field);
      if (control.type === 'checkbox') control.checked = value === true;
      else if (control.dataset.kind === 'list') control.value = Array.isArray(value) ? value.join('\n') : '';
      else control.value = String(value ?? '');
    }
    updateGlobalMode();
  }

  function updateGlobalMode() {
    const global = fieldValue(state.values, 'rules.mode') === 'global';
    $('#global-hint').hidden = !global;
    $('#tun-enabled').disabled = global || isLocked();
  }

  function updateActions() {
    const locked = isLocked();
    $('#save').disabled = !state.loaded || !state.dirty || !state.valid || locked || state.previewing || state.external;
    $('#discard').disabled = !state.dirty || locked;
    $('#validate').disabled = !state.loaded || locked || state.validating || state.previewing;
    $('#reload').disabled = locked;
    $('#save').textContent = state.busy ? t('正在应用…') : t('保存并应用');
    $('#save-state').classList.toggle('dirty', state.dirty);
    $('#save-state').classList.toggle('invalid', !state.valid && state.loaded && !state.previewing);
    $('#save-state span:last-child').textContent = state.busy ? t('正在保存并应用配置') :
      state.previewing ? t('正在检查更改…') : !state.valid && state.loaded ? t('配置需要修正') :
      state.dirty ? t('有未保存的更改') : t('所有更改已保存');
    $('#preview-draft').textContent = state.dirty ? t('草稿') : t('已保存');
    $('#preview-draft').classList.toggle('dirty', state.dirty);
    for (const control of controls) control.disabled = locked;
    $('#raw-yaml').disabled = locked;
    for (const button of document.querySelectorAll('.editor-tabs button, [data-section]')) button.disabled = locked;
    $('#settings-draft').hidden = !state.dirty;
    $('#actionbar').hidden = !state.loaded || (state.page !== 'settings' && !state.dirty);
    updateGlobalMode();
    if (state.loaded) renderConnectionCenter();
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
  }

  async function runPreview() {
    const generation = state.generation;
    const data = state.mode === 'yaml' ? { yaml: state.draft } : { yaml: state.base, changes: state.changes };
    const promise = api('preview', data);
    state.pending = { generation, promise };
    try {
      const doc = await promise;
      if (generation !== state.generation) return;
      state.draft = doc.yaml; state.values = doc.values || {};
      state.previewed = generation;
      state.valid = doc.valid; state.parsed = doc.parsed;
      state.dirty = state.draft !== state.saved;
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

  for (const control of controls) {
    control.addEventListener('input', () => {
      if (isLocked()) return;
      let value;
      if (control.type === 'checkbox') value = control.checked;
      else if (control.dataset.kind === 'list') value = control.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
      else if (control.dataset.kind === 'int') value = control.value === '' ? 0 : Number(control.value);
      else value = control.value;
      state.changes[control.dataset.field] = value;
      if (control.dataset.field === 'rules.mode' && value === 'global') {
        state.changes['tun.enabled'] = true;
        $('#tun-enabled').checked = true;
      }
      updateGlobalMode(); schedulePreview();
    });
  }
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
    if (name === 'logs') refreshDesktopLogs();
  }
  function renderPageTitle() {
    const labels = { connection: t('连接页面'), logs: t('日志'), settings: t('设置') };
    $('#crumb-section').textContent = labels[state.page];
  }
  for (const nav of document.querySelectorAll('[data-page]')) nav.addEventListener('click', () => showPage(nav.dataset.page));
  async function showSection(nav) {
    if (!(await switchMode('form'))) return false;
    for (const item of document.querySelectorAll('[data-section]')) {
      item.classList.toggle('selected', item === nav);
      if (item === nav) item.setAttribute('aria-current', 'page');
      else item.removeAttribute('aria-current');
    }
    for (const page of document.querySelectorAll('.settings-page')) page.hidden = page.id !== `section-${nav.dataset.section}`;
    return true;
  }
  for (const nav of document.querySelectorAll('[data-section]')) nav.addEventListener('click', () => showSection(nav));
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

  let desktopBusy = false;
  let desktopPending = '';
  let desktopLive = null;
  let connectionLost = false;
  let quickBusy = false;
  let stateRequest = 0;
  let selectedExit = null;
  let selectedMode = null;
  let exitListSignature = '';
  const networkActive = (live = desktopLive) => Boolean(live?.active && ['tun', 'proxy'].includes(live.mode));
  const signedIn = (live = desktopLive) => live?.backend_state === 'Running' || live?.state === 'authenticated';
  const savedExit = () => String(fieldValue(state.savedValues, 'tailscale.exit_node') || '');
  const savedMode = () => fieldValue(state.savedValues, 'rules.mode') === 'global' ? 'global' : 'split';
  const connectionChanges = () => ({
    ...(selectedExit !== null && selectedExit !== savedExit() ? { 'tailscale.exit_node': selectedExit } : {}),
    ...(selectedMode !== null && selectedMode !== savedMode() ? { 'rules.mode': selectedMode, ...(selectedMode === 'global' ? { 'tun.enabled': true } : {}) } : {}),
  });
  function exitNodes() {
    const nodes = (Array.isArray(desktopLive?.exit_nodes) ? desktopLive.exit_nodes : []).map((node) => ({
      id: String(node.id || node.ID || ''), name: String(node.name || node.Name || node.id || node.ID || ''),
      online: node.online === true || node.Online === true, selected: node.selected === true || node.Selected === true,
    })).filter((node) => node.id);
    const configured = savedExit();
    const directMatch = nodes.some((node) => node.id === configured || node.name === configured);
    for (const node of nodes) node.configured = node.id === configured || node.name === configured || !directMatch && node.selected;
    if (configured && !nodes.some((node) => node.configured)) nodes.unshift({ id: configured, name: configured, online: false, configured: true, missing: true });
    return nodes;
  }
  function updateDesktop(live) {
    desktopLive = live;
    renderConnectionCenter();
  }
  function renderConnectionCenter() {
    const live = desktopLive || {};
    const connected = networkActive(live);
    const authenticated = signedIn(live);
    const discovering = live.active && !connected;
    const locked = isLocked() || desktopBusy || quickBusy || connectionLost;
    const changing = Boolean(Object.keys(connectionChanges()).length);
    const hasTailnet = Boolean(state.savedValues.tailscale);
    const nodes = exitNodes();
    const chosen = selectedExit ?? savedExit();
    const mode = selectedMode ?? savedMode();
    const chosenNode = nodes.find((node) => node.id === chosen || node.name === chosen || selectedExit === null && node.configured);
    $('#desktop-controls').hidden = !live.desktop;
    $('#desktop-connect').hidden = Boolean(live.active);
    $('#desktop-disconnect').hidden = !live.active;
    $('#desktop-connect').disabled = !live.desktop || locked || state.dirty || !state.savedValid || changing || hasTailnet && (!authenticated || !savedExit() || nodes.some((node) => node.configured && !node.online && !node.missing));
    $('#desktop-disconnect').disabled = !live.desktop || desktopBusy || quickBusy || connectionLost;
    $('#desktop-disconnect').textContent = connected ? t('断开') : t('取消');
    for (const button of document.querySelectorAll('[data-desktop-action]')) {
      if (['connect', 'disconnect'].includes(button.dataset.desktopAction)) continue;
      button.disabled = !live.desktop || locked || discovering || button.dataset.desktopAction === 'login' && connected;
    }
    $('#desktop-login').hidden = authenticated;
    $('#account-menu').hidden = !authenticated;
    $('#desktop-logout').disabled = !live.desktop || locked || discovering;
    $('#startup-setting').hidden = !live.desktop;
    $('#desktop-startup').disabled = !live.desktop || locked;
    $('#desktop-startup').checked = live.startup === true;
    const identity = authenticated ? [live.hostname, live.network].filter(Boolean).join(' · ') || t('已登录') :
      discovering ? t('正在读取设备') : t('未登录 Tailscale');
    setStatusText('#tailnet-identity', connectionLost ? t('页面连接中断') : identity);
    const current = connected ? live.exit || savedExit() : savedExit();
    setStatusText('#current-exit', current ? t(connected ? '当前出口：{name}' : '已保存出口：{name}', { name: current }) : t('尚未选择出口'));
    const hint = connectionLost ? t('请确认 TailSplit 仍在运行') : live.error ? message(backendError(live)) :
      desktopPending ? t('正在处理…') : quickBusy ? t('正在应用连接更改…') :
      !live.desktop ? t('此页面仅编辑配置，连接操作请使用 TailSplit 桌面应用。') :
      live.preview ? t('界面预览：操作仅模拟界面状态，不会创建网络连接。') :
      state.dirty ? t('请先保存配置，再连接。') : changing ? t('连接选项尚未应用，请先确认选择。') :
      !authenticated && hasTailnet ? t('先登录 Tailscale，再选择出口并连接。') : t('连接和设备登录分别控制；关闭窗口后仍保留在托盘。');
    setStatusText('#desktop-hint', hint);
    for (const button of document.querySelectorAll('[data-routing-mode]')) {
      const selected = button.dataset.routingMode === mode;
      button.classList.toggle('selected', selected);
      button.setAttribute('aria-pressed', String(selected));
      button.disabled = locked || discovering || !state.loaded;
    }
    $('#mode-description').textContent = mode === 'global' ? t('接管整机流量；出口不可用时阻断，手动断开后恢复系统网络。') : t('按分流规则选择直连或出口。');
    $('#connection-apply').hidden = !changing && !quickBusy;
    const name = chosenNode?.name || chosen || t('尚未选择出口');
    $('#connection-selection').textContent = t('已选：{name} · {mode}', { name, mode: mode === 'global' ? t('全局代理') : t('规则分流') });
    $('#connection-consequence').textContent = connected ? t('确认后保存并重新连接，网络会短暂中断。') : t('仅保存选择，保持未连接。');
    $('#apply-connection-change').textContent = quickBusy ? t('正在应用…') : !connected ? t('保存选择') :
      selectedExit !== null && selectedMode !== null ? t('应用连接更改') : selectedExit !== null ? t('切换出口') : t('应用模式');
    $('#apply-connection-change').disabled = locked || discovering || state.dirty || state.external || !changing || selectedExit !== null && Boolean(chosenNode && !chosenNode.online);
    $('#cancel-connection-change').disabled = quickBusy;
    $('#connection-draft-warning').hidden = !state.dirty;
    $('#select-manual-exit').disabled = locked || discovering || !$('#exit-node').value.trim();
    $('#exit-node').disabled = locked || discovering;
    renderExitList(nodes, locked || discovering);
    renderConnection(live);
  }
  function renderExitList(nodes, locked) {
    const query = $('#exit-search').value.trim().toLocaleLowerCase();
    const visible = nodes.filter((node) => `${node.name} ${node.id}`.toLocaleLowerCase().includes(query));
    const signature = JSON.stringify([visible, selectedExit, locked, locale.language]);
    if (signature !== exitListSignature) {
      exitListSignature = signature;
      const focused = document.activeElement?.name === 'exit-selection' ? document.activeElement.value : null;
      let restoreFocus = null;
      const fragment = document.createDocumentFragment();
      for (const node of visible) {
        const selected = selectedExit === null ? node.configured : node.id === selectedExit;
        const row = document.createElement('label');
        row.className = `exit-row${selected ? ' selected' : ''}${!node.online ? ' unavailable' : ''}`;
        const radio = document.createElement('input');
        radio.type = 'radio'; radio.name = 'exit-selection'; radio.value = node.id;
        radio.checked = Boolean(selected); radio.disabled = locked || !node.online;
        if (node.id === focused && !radio.disabled) restoreFocus = radio;
        radio.addEventListener('change', () => { selectedExit = node.configured ? null : node.id; renderConnectionCenter(); });
        const info = document.createElement('span'); info.className = 'exit-info';
        const name = document.createElement('span'); name.className = 'exit-name'; name.textContent = node.name; name.title = node.name;
        const address = document.createElement('span'); address.className = 'exit-address'; address.textContent = node.id;
        info.append(name, address);
        const status = document.createElement('span');
        status.className = `exit-state${node.configured ? ' current' : selected ? ' pending' : ''}`;
        status.textContent = node.missing ? t('已保存 · 未发现') : !node.online ? (node.configured ? t('已选 · 不可用') : t('离线或已过期')) :
          node.configured ? t('当前选择') : selected ? t('待切换') : t('在线');
        row.append(radio, info, status); fragment.append(row);
      }
      $('#exit-list').replaceChildren(fragment);
      if (restoreFocus) restoreFocus.focus();
    }
    $('#exit-count').textContent = t('可用出口数量', { count: nodes.filter((node) => node.online).length });
    $('#exit-empty').hidden = visible.length > 0;
    $('#exit-empty-title').textContent = query ? t('没有匹配的出口') : signedIn() ? t('未发现可用出口') : t('尚未读取出口');
    $('#exit-empty-detail').textContent = query ? t('试试其他名称或 Tailscale IP。') : signedIn() ? t('确认设备已在 Tailnet 中提供出口，并获准使用，然后刷新。') : t('登录 Tailscale 后，刷新可用出口。');
  }
  $('#exit-search').addEventListener('input', () => renderConnectionCenter());
  $('#exit-node').addEventListener('input', () => renderConnectionCenter());
  $('#select-manual-exit').addEventListener('click', () => {
    const value = $('#exit-node').value.trim();
    if (!value || isLocked() || quickBusy) return;
    const node = exitNodes().find((node) => node.id === value || node.name === value);
    if (node && !node.online) { notice(t('出口不可用'), t('请选择在线出口，或刷新列表后重试。')); return; }
    selectedExit = node?.configured || value === savedExit() ? null : node?.id || value;
    renderConnectionCenter();
  });
  for (const button of document.querySelectorAll('[data-routing-mode]')) button.addEventListener('click', () => {
    selectedMode = button.dataset.routingMode === savedMode() ? null : button.dataset.routingMode;
    renderConnectionCenter();
  });
  $('#cancel-connection-change').addEventListener('click', () => { selectedExit = selectedMode = null; renderConnectionCenter(); });
  $('#apply-connection-change').addEventListener('click', async () => {
    if (quickBusy || desktopBusy || isLocked() || state.dirty || state.external || connectionLost) return;
    const changes = connectionChanges();
    if (!Object.keys(changes).length) return;
    stateRequest++;
    quickBusy = true; state.busy = true; updateActions();
    try {
      const doc = await api('config', { yaml: state.saved, changes, revision: state.revision });
      selectedExit = selectedMode = null;
      useDocument(doc);
      toast(t('连接选项已保存'));
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      notice(error.code === 'conflict' ? t('配置文件已更新') : t('无法应用连接更改'), error, 'error', error.code === 'conflict');
    } finally { quickBusy = false; state.busy = false; await refreshState(); updateActions(); }
  });
  async function desktopAction(action) {
    if (desktopBusy || quickBusy || connectionLost || !desktopLive?.desktop) return;
    stateRequest++;
    desktopBusy = true; desktopPending = action;
    renderConnectionCenter();
    try {
      await api('action', { action });
      if (action === 'logout') selectedExit = selectedMode = null;
    } catch (error) { notice(t('操作未完成'), error); }
    finally { desktopBusy = false; desktopPending = ''; await refreshState(); renderConnectionCenter(); }
  }
  document.querySelectorAll('[data-desktop-action]').forEach((button) => button.addEventListener('click', () => desktopAction(button.dataset.desktopAction)));
  $('#desktop-startup').addEventListener('change', (event) => desktopAction(event.target.checked ? 'startup-on' : 'startup-off'));
  $('#desktop-logout').addEventListener('click', async () => {
    const dialog = $('#logout-dialog');
    dialog.returnValue = 'cancel'; dialog.showModal();
    const answer = await new Promise((resolve) => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));
    if (answer === 'logout') { $('#account-menu').open = false; await desktopAction('logout'); }
  });
  let logsError = null;
  let logsEmpty = false;
  let logsBusy = false;
  async function refreshDesktopLogs() {
    if (logsBusy) return;
    if (!desktopLive?.logs_available) { $('#desktop-log-text').textContent = t('此页面无法读取日志，请从桌面应用打开。'); return; }
    logsBusy = true; $('#desktop-log-refresh').disabled = true;
    try {
      const text = (await api('logs')).text;
      logsError = null; logsEmpty = !text;
      $('#desktop-log-text').textContent = text || t('暂无日志。');
    } catch (error) {
      logsError = error; logsEmpty = false;
      $('#desktop-log-text').textContent = message(error);
    } finally { logsBusy = false; $('#desktop-log-refresh').disabled = false; }
  }
  $('#desktop-log-refresh').addEventListener('click', refreshDesktopLogs);

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
      'missing-exit': t('请重新选择出口'), login: t('等待 Tailscale 登录'), authenticating: t('正在登录'),
      discovering: t('正在读取出口'), 'logging-out': t('正在退出登录'),
    };
    const connected = !connectionLost && live.active && ['tun', 'proxy'].includes(live.mode) && live.state === 'running';
    $('#connection-status').classList.toggle('connected', connected);
    $('#connection-status').classList.toggle('attention', !connectionLost && ['blocked', 'waiting-exit', 'missing-exit'].includes(live.state));
    $('#connection-status').classList.toggle('error', connectionLost || live.state === 'failed');
    setStatusText('#connection-status span', connectionLost ? t('页面连接中断') : live.preview ? t('预览环境') : connected ? t('已连接') : labels[live.state] || t('未连接'));
    $('#connection-status').title = $('#connection-status span').textContent;
    const connectionState = connectionLost ? t('页面连接中断') : connected ? t('已连接') :
      ['authenticated', 'stopped', undefined].includes(live.state) ? t('未连接') : labels[live.state] || t('未连接');
    setStatusText('#connection-state', connectionState);
    for (const kind of ['connected', 'attention', 'error']) $('#connection-state').classList.toggle(kind, $('#connection-status').classList.contains(kind));
    setStatusText('#exit-label', connectionLost ? t('请确认 TailSplit 仍在运行') : live.exit || (live.preview ? t('更改仅保存到预览副本') : t('本机配置')));
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
      renderConnection(live);
      renderConnectionCenter();
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
    } finally { languageBusy = false; event.target.disabled = false; }
  });
  locale.subscribe(() => {
    renderPageTitle();
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
    if (logsError) $('#desktop-log-text').textContent = message(logsError);
    else if (logsEmpty) $('#desktop-log-text').textContent = t('暂无日志。');
  });

  window.addEventListener('beforeunload', (event) => { if (state.dirty || Object.keys(connectionChanges()).length) { event.preventDefault(); event.returnValue = ''; } });
  document.addEventListener('visibilitychange', refreshState);
  $('.brand').addEventListener('click', (event) => { event.preventDefault(); showPage('connection'); });
  if (!token) {
    $('#loading').hidden = true;
    notice(t('从 TailSplit 打开配置页面'), t('请点击菜单栏中的 TailSplit，再选择“设置与工具 → 配置面板…”。'));
  } else {
    loadLanguage().then(load).then(refreshState);
    setInterval(refreshState, 4000);
  }
})();
