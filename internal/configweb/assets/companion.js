(() => {
  'use strict';

  const toggle = document.querySelector('#page-companion');
  const errorLabel = document.querySelector('#page-companion-error');
  const locale = window.TailSplitI18n;
  const preferences = window.TailSplitCompanionPreferences;
  let enabled = false;
  let ready = false;
  let saving = false;
  let suspended = false;
  let loading = null;
  let errorKey = '';
  let pet = null;
  let generation = 0;
  toggle.checked = false;
  toggle.disabled = true;

  function showError(key) {
    errorKey = key;
    errorLabel.textContent = key ? locale.t(key) : '';
    errorLabel.hidden = !key;
  }

  function readEnabled(value) {
    if (typeof value?.enabled !== 'boolean') throw new Error('Invalid companion preference.');
    return value.enabled;
  }

  async function sync() {
    if (!ready || !enabled || suspended || document.hidden) {
      generation++;
      pet?.destroy();
      pet = null;
      return;
    }
    if (pet || saving) return;
    const current = ++generation;
    const next = window.Peetty.createPeetty({
      figure: 'miru', size: 88, activity: 'lively',
      language: locale.language, greet: false, chat: false, zIndex: 20,
    });
    pet = next;
    try {
      await next.mount();
      if (current !== generation || pet !== next) {
        next.destroy();
        return;
      }
      document.querySelector('peetty-companion').shadowRoot
        .querySelector('[data-action="hide"]').addEventListener('click', () => { void save(false); });
    } catch (_) {
      next.destroy();
      if (pet === next) {
        pet = null;
        showError('无法显示页面小人，请重新打开配置页面。');
      }
    }
  }

  async function save(value) {
    if (!ready || saving) { toggle.checked = enabled; return; }
    const previous = enabled;
    enabled = value;
    toggle.checked = enabled;
    saving = true;
    toggle.disabled = true;
    showError('');
    void sync();
    try {
      enabled = readEnabled(await preferences.save(value));
    } catch (_) {
      enabled = previous;
      showError('无法保存页面小人偏好，请重试。');
    } finally {
      saving = false;
      toggle.disabled = false;
      toggle.checked = enabled;
      void sync();
    }
  }

  function load() {
    if (ready || loading) return loading;
    loading = (async () => {
      try {
        enabled = readEnabled(await preferences.load());
        ready = true;
        toggle.checked = enabled;
        toggle.disabled = false;
        showError('');
        void sync();
      } catch (_) {
        showError('无法读取页面小人偏好，请重新打开配置页面。');
      } finally { loading = null; }
    })();
    return loading;
  }

  toggle.addEventListener('change', () => { void save(toggle.checked); });
  locale.subscribe(() => { pet?.setLanguage(locale.language); showError(errorKey); });
  document.addEventListener('visibilitychange', () => { void sync(); });
  window.addEventListener('pagehide', () => { suspended = true; void sync(); });
  window.addEventListener('pageshow', () => { suspended = false; if (ready) void sync(); else void load(); });
  void load();
})();
