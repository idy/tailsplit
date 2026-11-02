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
    dirty: false, busy: false, previewing: false, external: false,
    generation: 0, pending: null, timer: null, toastTimer: null,
  };

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
    $('#tun-enabled').disabled = global || state.busy;
  }

  function updateActions() {
    $('#save').disabled = !state.loaded || !state.dirty || !state.valid || state.busy || state.previewing || state.external;
    $('#discard').disabled = !state.dirty || state.busy;
    $('#validate').disabled = !state.loaded || state.busy || state.previewing;
    $('#reload').disabled = state.busy;
    $('#save').textContent = state.busy ? '正在应用…' : '保存并应用';
    $('#save-state').classList.toggle('dirty', state.dirty);
    $('#save-state').classList.toggle('invalid', !state.valid && state.loaded && !state.previewing);
    $('#save-state span:last-child').textContent = state.busy ? '正在保存并应用配置' :
      state.previewing ? '正在检查更改…' : !state.valid && state.loaded ? '配置需要修正' :
      state.dirty ? '有未保存的更改' : '所有更改已保存';
    $('#preview-draft').textContent = state.dirty ? '草稿' : '已保存';
    $('#preview-draft').classList.toggle('dirty', state.dirty);
    for (const control of controls) control.disabled = state.busy;
    $('#raw-yaml').disabled = state.busy;
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
    try {
      useDocument(await api('config'));
      await refreshState();
    } catch (error) {
      $('#loading').hidden = true;
      notice('无法读取本机配置', error.message || '请从 TailSplit 菜单重新打开页面。');
    }
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
    if (state.timer) {
      clearTimeout(state.timer); state.timer = null;
      await runPreview();
    } else if (state.pending) {
      try { await state.pending.promise; } catch (_) { /* runPreview reports it. */ }
    }
  }

  for (const control of controls) {
    control.addEventListener('input', () => {
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
  $('#raw-yaml').addEventListener('input', (event) => { state.draft = event.target.value; schedulePreview(); });
  $('#raw-yaml').addEventListener('keydown', (event) => {
    if (event.key !== 'Tab') return;
    event.preventDefault();
    const input = event.target;
    input.setRangeText('  ', input.selectionStart, input.selectionEnd, 'end');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });

  async function switchMode(mode) {
    if (state.busy || mode === state.mode) return true;
    await flushPreview();
    if (mode === 'form' && !state.parsed) {
      notice('暂时无法切换到表单', '请先修正 YAML 语法，原文草稿会保留。');
      return false;
    }
    state.base = state.draft; state.changes = Object.create(null);
    if (mode === 'yaml') $('#raw-yaml').value = state.draft;
    else fillFields(state.values);
    showMode(mode); return true;
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
    if (state.busy) return;
    if (state.dirty) {
      const dialog = $('#discard-dialog');
      dialog.returnValue = 'cancel'; dialog.showModal();
      const answer = await new Promise((resolve) => dialog.addEventListener('close', () => resolve(dialog.returnValue), { once: true }));
      if (answer !== 'discard') return;
    }
    await load();
  }
  $('#reload').addEventListener('click', reload);
  $('#discard').addEventListener('click', reload);
  $('#notice-reload').addEventListener('click', reload);

  $('#validate').addEventListener('click', async () => {
    await flushPreview();
    $('#validate').disabled = true;
    try {
      await api('validate', { yaml: state.draft });
      state.valid = true; clearNotice(); toast('配置校验通过');
    } catch (error) {
      state.valid = false; notice('配置未通过校验', error.message);
    } finally { updateActions(); }
  });

  $('#save').addEventListener('click', async () => {
    if (state.busy) return;
    await flushPreview();
    if (!state.valid || state.external) return;
    state.busy = true; updateActions();
    try {
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
    await flushPreview();
    try { await navigator.clipboard.writeText(state.draft); toast('YAML 已复制'); }
    catch (_) { toast('无法访问剪贴板，请在 YAML 原文中选择并复制。'); }
  }
  $('#copy-preview').addEventListener('click', copy);
  $('#copy-raw').addEventListener('click', copy);

  async function refreshState() {
    if (!state.loaded || document.hidden) return;
    try {
      const live = await api('state');
      const labels = { starting: live.mode === 'discovery' ? '正在读取设备' : '正在连接', stopping: '正在断开', failed: '连接异常', authorizing: '等待授权', authenticated: '已登录' };
      const connected = live.active && ['tun', 'proxy'].includes(live.mode) && !['failed', 'starting', 'stopping', 'authorizing'].includes(live.state);
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
      if (live.revision && live.revision !== state.revision && !state.busy) {
        if (!state.dirty) useDocument(await api('config'));
        else { state.external = true; clearNotice(); updateActions(); }
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
    load();
    setInterval(refreshState, 4000);
  }
})();
