// Custom single-select. The native element stays the source of form data;
// a combobox and a styled listbox handle all pointer and keyboard interaction.
const instances = new WeakMap();
let serial = 0;
let activeSelect = null;

/**
 * Enhance an externally labelled, single <select class="mds-select">.
 * No native picker is opened. Without JS the original select still works.
 *
 * @param {HTMLSelectElement} select
 * @returns {{trigger: HTMLButtonElement, open: Function, close: Function, refresh: Function, destroy: Function}}
 */
export function initSelect(select) {
  if (instances.has(select)) return instances.get(select);
  if (select.tagName !== 'SELECT' || select.multiple || select.size > 1) {
    throw new TypeError('initSelect() expects a single-select dropdown.');
  }
  const doc = select.ownerDocument;
  const win = doc.defaultView;
  const controller = new AbortController();
  const listen = (node, type, handler, options = {}) => node.addEventListener(type, handler, { ...options, signal: controller.signal });
  const originalHidden = select.hidden;
  const labels = [...select.labels];
  const generatedLabels = [];
  let id;
  do { id = `mds-select-${++serial}`; } while (doc.getElementById(id) || doc.getElementById(`${id}-list`));

  const wrapper = doc.createElement('span');
  wrapper.className = 'mds-select-control';
  select.before(wrapper);
  wrapper.append(select);
  select.hidden = true;

  const trigger = doc.createElement('button');
  trigger.type = 'button';
  trigger.id = id;
  trigger.className = `${select.className} mds-select-trigger`;
  trigger.setAttribute('role', 'combobox');
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  trigger.setAttribute('aria-controls', `${id}-list`);

  const menu = doc.createElement('div');
  menu.id = `${id}-list`;
  menu.className = 'mds-select-menu';
  menu.setAttribute('role', 'listbox');
  menu.hidden = true;
  const topLayer = typeof menu.showPopover === 'function';
  if (topLayer) menu.setAttribute('popover', 'manual');
  wrapper.append(trigger, menu);

  // Keep focus on the combobox. aria-activedescendant identifies the option
  // being browsed; aria-selected and the check mark reflect the committed value.
  let expanded = false;
  let anchorRect = null;
  let active = -1;
  let rows = [];
  let typed = '';
  let typedAt = 0;
  let validationFailed = false;
  let destroyed = false;
  const options = () => [...select.options];
  const groupHidden = (option) => option.parentElement.tagName === 'OPTGROUP' && option.parentElement.hidden;
  const enabled = () => options().map((option, index) => ({ option, index })).filter(({ option }) =>
    !option.disabled && !option.hidden && !groupHidden(option) && !option.parentElement.disabled);

  function nameControl() {
    let labelledBy = select.getAttribute('aria-labelledby');
    if (!labelledBy && !select.hasAttribute('aria-label')) {
      labelledBy = labels.map((label, index) => {
        if (!label.id) {
          label.id = `${id}-label-${index}`;
          generatedLabels.push([label, label.id]);
        }
        return label.id;
      }).join(' ');
    }
    for (const node of [trigger, menu]) {
      node.removeAttribute('aria-label');
      node.removeAttribute('aria-labelledby');
      if (labelledBy) node.setAttribute('aria-labelledby', labelledBy);
      else if (select.hasAttribute('aria-label')) node.setAttribute('aria-label', select.getAttribute('aria-label'));
    }
  }

  function paintActive() {
    for (const row of rows) row.toggleAttribute('data-active', Number(row.dataset.index) === active);
    if (expanded && rows.some((row) => Number(row.dataset.index) === active)) {
      trigger.setAttribute('aria-activedescendant', `${id}-option-${active}`);
      // Scroll only the menu; scrollIntoView would also move its page ancestors.
      const row = rows.find((item) => Number(item.dataset.index) === active);
      const top = row.offsetTop;
      if (top < menu.scrollTop) menu.scrollTop = top;
      else if (top + row.offsetHeight > menu.scrollTop + menu.clientHeight) menu.scrollTop = top + row.offsetHeight - menu.clientHeight;
    } else trigger.removeAttribute('aria-activedescendant');
  }

  function close() {
    if (!expanded) return;
    expanded = false;
    anchorRect = null;
    if (topLayer && menu.matches(':popover-open')) menu.hidePopover();
    menu.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
    trigger.removeAttribute('aria-activedescendant');
    typed = '';
    if (activeSelect === api) activeSelect = null;
  }

  function refresh() {
    if (destroyed) return;
    close();
    wrapper.classList.toggle('mds-select-control--inline', select.classList.contains('mds-select--inline'));
    wrapper.classList.toggle('mds-select-control--text', select.classList.contains('mds-select--text'));
    trigger.className = `${select.className} mds-select-trigger`;
    nameControl();
    trigger.textContent = select.selectedOptions[0]?.label || select.dataset.placeholder || 'Select an option';
    trigger.disabled = select.matches(':disabled') || enabled().length === 0;
    for (const attribute of ['aria-describedby', 'aria-invalid', 'aria-required']) {
      if (select.hasAttribute(attribute)) trigger.setAttribute(attribute, select.getAttribute(attribute));
      else trigger.removeAttribute(attribute);
    }
    if (select.required) trigger.setAttribute('aria-required', 'true');
    if (validationFailed && !select.validity.valid) trigger.setAttribute('aria-invalid', 'true');
    const available = new Set(enabled().map(({ index }) => index));
    rows = [];
    menu.replaceChildren();
    let group = null;
    let container = menu;
    options().forEach((option, index) => {
      if (option.hidden || groupHidden(option)) return;
      const parent = option.parentElement.tagName === 'OPTGROUP' ? option.parentElement : null;
      if (parent !== group) {
        group = parent;
        container = menu;
        if (group) {
          container = doc.createElement('div');
          container.setAttribute('role', 'group');
          container.setAttribute('aria-label', group.label);
          const heading = doc.createElement('div');
          heading.className = 'mds-select-group';
          heading.setAttribute('aria-hidden', 'true');
          heading.textContent = group.label;
          container.append(heading);
          menu.append(container);
        }
      }
      const row = doc.createElement('div');
      row.className = 'mds-select-option';
      row.id = `${id}-option-${index}`;
      row.dataset.index = index;
      row.setAttribute('role', 'option');
      row.setAttribute('aria-selected', String(index === select.selectedIndex));
      if (!available.has(index)) row.setAttribute('aria-disabled', 'true');
      row.textContent = option.label;
      rows.push(row);
      container.append(row);
    });
  }

  function place() {
    const rect = trigger.getBoundingClientRect();
    anchorRect = rect;
    const viewport = doc.documentElement.clientWidth;
    const width = Math.min(Math.max(rect.width, 180), viewport - 16);
    menu.style.width = `${width}px`;
    const below = win.innerHeight - rect.bottom - 14;
    const above = rect.top - 14;
    const height = Math.min(menu.scrollHeight, 280);
    const down = below >= height || below >= above;
    const limit = Math.max(0, Math.min(280, down ? below : above));
    menu.style.maxHeight = `${limit}px`;
    const left = win.getComputedStyle(trigger).direction === 'rtl' ? rect.right - width : rect.left;
    menu.style.left = `${Math.max(8, Math.min(left, viewport - width - 8))}px`;
    menu.style.top = `${down ? rect.bottom + 6 : Math.max(8, rect.top - 6 - Math.min(height, limit))}px`;
  }

  function open() {
    if (destroyed || expanded) return;
    refresh();
    if (trigger.disabled) return;
    activeSelect?.close();
    activeSelect = api;
    // Popover places the menu above overflow-clipped groups without moving it
    // out of its theme. Older WebViews use a fixed portal with copied tokens.
    if (!topLayer) {
      const computed = win.getComputedStyle(wrapper);
      for (const property of computed) if (property.startsWith('--mds-')) menu.style.setProperty(property, computed.getPropertyValue(property));
      menu.style.direction = computed.direction;
      (select.closest('dialog') || doc.body).append(menu);
    }
    menu.hidden = false;
    if (topLayer) menu.showPopover();
    expanded = true;
    trigger.setAttribute('aria-expanded', 'true');
    active = enabled().some(({ index }) => index === select.selectedIndex) ? select.selectedIndex : enabled()[0].index;
    trigger.focus({ preventScroll: true });
    place();
    paintActive();
  }

  function choose(index) {
    if (!enabled().some((item) => item.index === index)) return;
    const changed = select.selectedIndex !== index;
    select.selectedIndex = index;
    refresh();
    if (changed) {
      select.dispatchEvent(new Event('input', { bubbles: true }));
      select.dispatchEvent(new Event('change', { bubbles: true }));
    }
  }

  listen(trigger, 'click', () => { if (expanded) close(); else open(); });
  listen(trigger, 'keydown', (event) => {
    if (trigger.disabled || event.isComposing) return;
    const key = event.key;
    if (key === 'Escape') {
      if (expanded) { event.preventDefault(); event.stopPropagation(); close(); }
      return;
    }
    if (key === 'Tab') { close(); return; }
    if (['Enter', ' ', 'ArrowDown', 'ArrowUp', 'Home', 'End'].includes(key)) {
      event.preventDefault();
      if (key === 'Enter' || (key === ' ' && (!typed || Date.now() - typedAt > 700))) {
        if (expanded) choose(active); else open();
        return;
      }
      const wasOpen = expanded;
      if (!expanded) open();
      const list = enabled().map(({ index }) => index);
      if (!list.length) return;
      if (key === 'Home') active = list[0];
      else if (key === 'End') active = list[list.length - 1];
      else if (wasOpen && key.startsWith('Arrow')) {
        const offset = key === 'ArrowDown' ? 1 : -1;
        active = list[Math.max(0, Math.min(list.length - 1, list.indexOf(active) + offset))];
      }
      paintActive();
      if (key !== ' ') return;
    }
    if (key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    const now = Date.now();
    typed = now - typedAt < 700 ? typed + key.toLocaleLowerCase() : key.toLocaleLowerCase();
    typedAt = now;
    const search = [...typed].every((character) => character === typed[0]) ? typed[0] : typed;
    const list = enabled();
    const start = list.findIndex(({ index }) => index === (expanded ? active : select.selectedIndex));
    const ordered = [...list.slice(start + 1), ...list.slice(0, start + 1)];
    // A longer prefix keeps the current match; repeated letters cycle matches.
    if (search.length > 1 && list[start]?.option.label.toLocaleLowerCase().startsWith(search)) ordered.unshift(list[start]);
    const match = ordered.find(({ option }) => option.label.toLocaleLowerCase().startsWith(search));
    if (match) { if (expanded) { active = match.index; paintActive(); } else choose(match.index); }
  });
  listen(menu, 'pointerdown', (event) => { if (event.pointerType === 'mouse') event.preventDefault(); });
  listen(menu, 'pointermove', (event) => {
    const row = event.target.closest('[role="option"]');
    if (row && !row.hasAttribute('aria-disabled')) { active = Number(row.dataset.index); paintActive(); }
  });
  listen(menu, 'click', (event) => {
    const row = event.target.closest('[role="option"]');
    if (!row || row.hasAttribute('aria-disabled')) return;
    choose(Number(row.dataset.index));
    trigger.focus({ preventScroll: true });
  });
  listen(doc, 'pointerdown', (event) => { if (!trigger.contains(event.target) && !menu.contains(event.target)) close(); }, { capture: true });
  listen(doc, 'focusin', (event) => { if (!trigger.contains(event.target) && !menu.contains(event.target)) close(); });
  listen(doc, 'scroll', (event) => {
    if (!expanded || !anchorRect || menu.contains(event.target)) return;
    // Unrelated scrollers do not detach the menu from its trigger.
    const rect = trigger.getBoundingClientRect();
    if (['left', 'top', 'width', 'height'].some(key => rect[key] !== anchorRect[key])) close();
  }, { capture: true });
  listen(win, 'resize', close);
  listen(select, 'input', refresh);
  listen(select, 'change', refresh);
  listen(select, 'invalid', (event) => {
    event.preventDefault();
    validationFailed = true;
    trigger.setAttribute('aria-invalid', 'true');
    trigger.focus();
  });
  for (const label of labels) listen(label, 'click', (event) => {
    if (trigger.contains(event.target) || menu.contains(event.target)) return;
    event.preventDefault();
    trigger.focus();
  });
  if (select.form) listen(select.form, 'reset', (event) => {
    // Reset's default action runs after the event, and can be cancelled.
    queueMicrotask(() => { if (!event.defaultPrevented) { validationFailed = false; refresh(); } });
  });
  const observer = new MutationObserver(refresh);
  observer.observe(select, { childList: true, characterData: true, subtree: true, attributes: true });
  for (let parent = select.parentElement; parent; parent = parent.parentElement) {
    if (parent.tagName === 'FIELDSET') observer.observe(parent, { attributes: true, attributeFilter: ['disabled'] });
  }

  const api = {
    trigger, open, close, refresh,
    destroy() {
      close();
      destroyed = true;
      controller.abort();
      observer.disconnect();
      menu.remove();
      wrapper.replaceWith(select);
      select.hidden = originalHidden;
      for (const [label, generated] of generatedLabels) if (label.id === generated) label.removeAttribute('id');
      instances.delete(select);
    },
  };
  instances.set(select, api);
  refresh();
  return api;
}
