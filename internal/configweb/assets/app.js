(() => {
  'use strict';

  const $ = (selector) => document.querySelector(selector);
  const controls = [...document.querySelectorAll('[data-field]')];
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
    mode: 'form', valid: false, parsed: false, values: {}, loaded: false,
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
      headers: { Authorization: `Bearer ${token || ''}`, ...(data === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: data === undefined ? undefined : JSON.stringify(data),
      cache: 'no-store',
    });
    const value = await response.json();
    if (!response.ok) {
      const error = new Error(value.error || '请求未完成，请重试。');
      error.code = value.code;
      throw error;
    }
    return value;
  }

  function notice(title, detail, kind = 'error', canReload = false) {
    $('#notice-title').textContent = title;
    $('#notice-detail').textContent = detail;
    $('#notice').classList.toggle('error', kind === 'error');
    $('#notice-reload').hidden = !canReload;
    $('#notice').hidden = false;
  }

  function clearNotice() {
    if (state.external) {
      notice('磁盘中的配置已更新', '当前草稿仍保留。重新读取最新配置后再保存，避免覆盖其他更改。', 'warning', true);
    } else {
      $('#notice').hidden = true;
    }
  }

  function toast(message) {
    clearTimeout(state.toastTimer);
    $('#toast').textContent = message;
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
    const global = $('#routing-mode').value === 'global';
    $('#global-hint').hidden = !global;
    $('#tun-enabled').disabled = global || isLocked();
  }

  function updateActions() {
    const locked = isLocked();
    $('#save').disabled = !state.loaded || !state.dirty || !state.valid || locked || state.previewing || state.external;
    $('#discard').disabled = !state.dirty || locked;
    $('#validate').disabled = !state.loaded || locked || state.validating || state.previewing;
    $('#reload').disabled = locked;
    $('#save').textContent = state.busy ? '正在应用…' : '保存并应用';
    $('#save-state').classList.toggle('dirty', state.dirty);
    $('#save-state').classList.toggle('invalid', !state.valid && state.loaded && !state.previewing);
    $('#save-state span:last-child').textContent = state.busy ? '正在保存并应用配置' :
      state.previewing ? '正在检查更改…' : !state.valid && state.loaded ? '配置需要修正' :
      state.dirty ? '有未保存的更改' : '所有更改已保存';
    $('#preview-draft').textContent = state.dirty ? '草稿' : '已保存';
    $('#preview-draft').classList.toggle('dirty', state.dirty);
    for (const control of controls) control.disabled = locked;
    $('#raw-yaml').disabled = locked;
    for (const button of document.querySelectorAll('.editor-tabs button, [data-section]')) button.disabled = locked;
    updateGlobalMode();
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
    $('#line-count').textContent = `${lines.length} 行`;
  }

  function useDocument(doc) {
    clearTimeout(state.timer); state.timer = null; state.generation++;
    state.previewed = state.generation; state.pending = null;
    state.saved = state.base = state.draft = doc.yaml;
    state.revision = doc.revision;
    state.values = doc.values || {};
    state.changes = Object.create(null);
    state.valid = doc.valid; state.parsed = doc.parsed;
    state.dirty = false; state.previewing = false; state.external = false; state.loaded = true;
    $('#file-location').title = doc.path || '';
    $('#file-location').textContent = '当前设备';
    fillFields(state.values);
    $('#raw-yaml').value = state.draft;
    paintPreview();
    $('#loading').hidden = true; $('#editor-layout').hidden = false; $('#actionbar').hidden = false;
    if (!doc.parsed) showMode('yaml');
    if (!doc.valid) notice('配置需要修正', doc.error || '请检查 YAML 内容。');
    else clearNotice();
    updateActions();
  }

  async function load() {
    state.loading = true; updateActions();
    try {
      useDocument(await api('config'));
    } catch (error) {
      $('#loading').hidden = true;
      notice('无法读取本机配置', error.message || '请从 TailSplit 菜单重新打开页面。');
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
      else notice('配置需要修正', doc.error || '请检查字段内容。');
    } catch (error) {
      if (generation !== state.generation) return;
      state.valid = false;
      notice('暂时无法检查配置', error.message || '请确认 TailSplit 仍在运行。');
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
        notice('暂时无法切换编辑方式', '请先修正表单输入或重试，当前草稿会保留。');
        return false;
      }
      if (mode === 'form' && !state.parsed) {
        notice('暂时无法切换到表单', '请先修正 YAML 语法，原文草稿会保留。');
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
  for (const nav of document.querySelectorAll('[data-section]')) {
    nav.addEventListener('click', async () => {
      if (!(await switchMode('form'))) return;
      for (const item of document.querySelectorAll('[data-section]')) {
        item.classList.toggle('active', item === nav);
        if (item === nav) item.setAttribute('aria-current', 'page');
        else item.removeAttribute('aria-current');
      }
      for (const page of document.querySelectorAll('.settings-page')) page.hidden = page.id !== `section-${nav.dataset.section}`;
    });
  }

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
      state.valid = true; clearNotice(); toast('配置校验通过');
    } catch (error) {
      if (generation !== state.generation) return;
      state.valid = false; notice('配置未通过校验', error.message);
    } finally { state.validating = false; updateActions(); }
  });

  $('#save').addEventListener('click', async () => {
    if (isLocked()) return;
    state.busy = true; updateActions();
    try {
      if (!(await flushPreview()) || !state.valid || state.external) return;
      const doc = await api('config', { yaml: state.draft, revision: state.revision });
      useDocument(doc);
      toast('配置已保存并应用');
      await refreshState();
    } catch (error) {
      if (error.code === 'conflict') state.external = true;
      notice(error.code === 'conflict' ? '配置文件已更新' : '配置未能应用', error.message, 'error', error.code === 'conflict');
    } finally { state.busy = false; updateActions(); }
  });

  async function copy() {
    if (isLocked() || !(await flushPreview())) return;
    try { await navigator.clipboard.writeText(state.draft); toast('YAML 已复制'); }
    catch (_) { toast('无法访问剪贴板，请在 YAML 原文中选择并复制。'); }
  }
  $('#copy-preview').addEventListener('click', copy);
  $('#copy-raw').addEventListener('click', copy);

  let desktopBusy = false;
  let desktopLive = null;
  function updateDesktop(live) {
    desktopLive = live;
    $('#desktop-controls').hidden = !live.desktop;
    if (!live.desktop) return;
    $('.page-header h1').textContent = 'TailSplit';
    $('.page-header p').textContent = '连接 Tailnet、选择出口，按你的规则分流。';
    document.querySelectorAll('[data-desktop-action]').forEach((button) => { button.disabled = desktopBusy; });
    $('#desktop-connect').disabled = desktopBusy || live.active || state.dirty;
    $('#desktop-disconnect').disabled = desktopBusy || !live.active;
    $('#desktop-startup').disabled = desktopBusy;
    $('#desktop-startup').checked = live.startup === true;
    $('#desktop-hint').textContent = live.preview ? '界面预览：操作仅模拟界面状态，不会创建网络连接。' : live.error || (state.dirty ? '请先保存配置，再连接。' : '连接和设备登录分别控制；关闭窗口后仍保留在托盘。');
  }
  async function desktopAction(action) {
    if (desktopBusy) return;
    desktopBusy = true;
    if (desktopLive) updateDesktop(desktopLive);
    $('#desktop-hint').textContent = '正在处理…';
    try { await api('action', { action }); }
    catch (error) { notice('操作未完成', error.message); }
    finally { desktopBusy = false; await refreshState(); }
  }
  document.querySelectorAll('[data-desktop-action]').forEach((button) => button.addEventListener('click', () => desktopAction(button.dataset.desktopAction)));
  $('#desktop-startup').addEventListener('change', (event) => desktopAction(event.target.checked ? 'startup-on' : 'startup-off'));
  async function refreshDesktopLogs() {
    try { $('#desktop-log-text').textContent = (await api('logs')).text || '暂无日志。'; }
    catch (error) { $('#desktop-log-text').textContent = error.message; }
  }
  $('#desktop-logs').addEventListener('click', async () => { $('#desktop-log-dialog').showModal(); await refreshDesktopLogs(); });
  $('#desktop-log-refresh').addEventListener('click', refreshDesktopLogs);

  let updateLive = null;
  let updatePending = '';
  let updateError = '';
  let updateGeneration = 0;
  const updatesBusy = () => updatePending || updateLive?.updates?.checking || updateLive?.updates?.downloading || updateLive?.updates?.installing;

  function updateBytes(bytes) {
    if (bytes < 1024) return `${Math.floor(bytes)} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  }

  function updateAppUpdates(live) {
    updateLive = live;
    const updates = live?.updates;
    $('#app-updates').hidden = !updates;
    if (!updates) return;
    const busy = Boolean(updatesBusy());
    $('#update-current').textContent = `当前版本 ${updates.current_version || '未知'}`;
    $('#update-latest').textContent = `最新版本 ${updates.latest_version || '尚未检查'}`;
    $('#update-automatic').checked = updatePending === 'automatic-on' || updatePending !== 'automatic-off' && updates.automatic === true;
    $('#update-automatic').disabled = busy;
    $('#update-check').disabled = busy;
    $('#update-check').textContent = updates.checking ? '正在检查…' : '检查更新';
    $('#update-install').hidden = !updates.available;
    $('#update-install').disabled = busy;
    $('#update-install').textContent = live.preview ? '模拟安装' : updates.downloading ? '正在下载…' : updates.installing ? '正在安装…' : '下载并安装';
    $('#update-install-hint').hidden = !updates.available || live.preview;
    $('#update-preview').hidden = !live.preview;
    const checked = new Date(updates.last_checked);
    const hasChecked = checked.getTime() > 0;
    $('#update-last-checked').textContent = `上次检查：${hasChecked ? checked.toLocaleString('zh-CN') : '尚未检查'}`;
    const downloaded = Math.max(0, Number(updates.downloaded_bytes) || 0);
    const total = Math.max(0, Number(updates.total_bytes) || 0);
    const progress = $('#update-progress');
    progress.hidden = !updates.downloading;
    if (total > 0) progress.value = Math.min(1, downloaded / total);
    else progress.removeAttribute('value');
    const pendingLabels = { check: '正在请求检查…', install: '正在请求安装…', 'automatic-on': '正在保存更新偏好…', 'automatic-off': '正在保存更新偏好…' };
    $('#update-status').textContent = updates.downloading ?
      `正在下载更新：${updateBytes(downloaded)}${total > 0 ? ` / ${updateBytes(total)}（${Math.min(100, Math.floor(downloaded / total * 100))}%）` : '，总大小待确定'}。` :
      updates.installing ? '正在准备安装更新，请完成系统确认或安装向导。' : updates.checking ? '正在检查更新…' : updatePending ? pendingLabels[updatePending] :
      updates.available ? `发现新版本 ${updates.latest_version || ''}。` : updates.error ? '暂时无法确认最新版本。' :
      hasChecked && updates.latest_version ? '当前已是最新版本。' : '尚未检查更新。';
    $('#update-error').textContent = updateError || updates.error || '';
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
      updateError = error.message;
    } finally {
      updateGeneration++;
      await refreshState();
      updatePending = '';
      updateAppUpdates(updateLive);
    }
  }
  $('#update-check').addEventListener('click', () => appUpdateAction('check'));
  $('#update-install').addEventListener('click', () => appUpdateAction('install'));
  $('#update-automatic').addEventListener('change', (event) => appUpdateAction(event.target.checked ? 'automatic-on' : 'automatic-off'));

  async function refreshState() {
    if (!state.loaded || document.hidden) return;
    const generation = updateGeneration;
    try {
      const live = await api('state');
      updateDesktop(live);
      if (generation === updateGeneration) updateAppUpdates(live);
      const labels = {
        starting: live.mode === 'discovery' ? '正在读取设备' : '正在连接', connecting: '正在连接',
        stopping: '正在断开', failed: '连接异常', authorizing: '等待授权', authenticated: '已登录',
        blocked: '出口不可用 · 已阻断', 'checking-exit': '正在检查出口', 'waiting-exit': '等待出口上线',
        'missing-exit': '请重新选择出口', login: '等待 Tailscale 登录', authenticating: '正在登录',
        discovering: '正在读取出口', 'logging-out': '正在退出登录',
      };
      const connected = live.active && ['tun', 'proxy'].includes(live.mode) && live.state === 'running';
      $('#connection-status').classList.toggle('connected', connected);
      $('#connection-status span').textContent = live.preview ? '预览环境' : connected ? '已连接' : labels[live.state] || '未连接';
      $('#exit-label').textContent = live.exit || (live.preview ? '更改仅保存到预览副本' : '本机配置');
      const nodes = Array.isArray(live.exit_nodes) ? live.exit_nodes : [];
      $('#exit-nodes').replaceChildren(...nodes.map((node) => {
        const option = document.createElement('option');
        option.value = node.id || node.ID || '';
        option.label = node.name || node.Name || option.value;
        return option;
      }));
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
      $('#connection-status').classList.remove('connected');
      $('#connection-status span').textContent = '页面连接中断';
      $('#exit-label').textContent = '请确认 TailSplit 仍在运行';
    }
  }

  window.addEventListener('beforeunload', (event) => { if (state.dirty) { event.preventDefault(); event.returnValue = ''; } });
  document.addEventListener('visibilitychange', refreshState);
  $('.brand').addEventListener('click', (event) => { event.preventDefault(); document.querySelector('[data-section="connection"]').click(); });
  if (!token) {
    $('#loading').hidden = true;
    notice('从 TailSplit 打开配置页面', '请点击菜单栏中的 TailSplit，再选择“设置与工具 → 配置面板…”。');
  } else {
    load().then(refreshState);
    setInterval(refreshState, 4000);
  }
})();
