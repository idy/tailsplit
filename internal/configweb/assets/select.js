(() => {
  'use strict';

  // Upgrade existing single-select fields while preserving their values, option
  // lists and input/change events as the form model. All menus share one style.
  const instances = new Set();
  const upgraded = new WeakMap();
  let active = null;
  let sequence = 0;

  function icon(name) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('class', 'icon'); svg.setAttribute('aria-hidden', 'true');
    const use = document.createElementNS(svg.namespaceURI, 'use');
    use.setAttribute('href', `#i-${name}`); svg.append(use);
    return svg;
  }

  class SelectMenu {
    constructor(select) {
      this.select = select;
      this.labels = [...(select.labels || [])];
      this.index = -1;
      this.signature = '';
      this.prefix = '';
      this.typedAt = 0;
      const id = `select-menu-${++sequence}`;
      this.wrapper = document.createElement('div'); this.wrapper.className = 'select-control';
      this.wrapper.dataset.variant = select.dataset.selectVariant || 'field';
      this.button = document.createElement('button'); this.button.type = 'button';
      this.button.className = 'select-trigger'; this.button.id = `${id}-trigger`;
      this.button.setAttribute('role', 'combobox');
      this.button.setAttribute('aria-haspopup', 'listbox');
      this.button.setAttribute('aria-expanded', 'false');
      this.button.setAttribute('aria-controls', id);
      this.text = document.createElement('span'); this.text.className = 'select-value';
      const caret = icon('chevron'); caret.classList.add('select-caret');
      this.button.append(this.text, caret);
      this.menu = document.createElement('div'); this.menu.id = id;
      this.menu.className = 'select-menu'; this.menu.hidden = true;
      this.menu.setAttribute('role', 'listbox');
      this.menu.setAttribute('aria-labelledby', this.button.id);
      select.before(this.wrapper);
      this.wrapper.append(select, this.button);
      (select.closest('dialog') || document.body).append(this.menu);
      select.classList.add('select-native'); select.tabIndex = -1;
      select.setAttribute('aria-hidden', 'true');
      this.button.addEventListener('click', () => this.menu.hidden ? this.open() : this.close());
      this.button.addEventListener('keydown', (event) => this.keydown(event));
      this.menu.addEventListener('pointerdown', (event) => { if (event.pointerType !== 'touch') event.preventDefault(); });
      for (const label of this.labels) label.addEventListener('click', (event) => {
        if (event.target.closest('button')) return;
        event.preventDefault(); this.button.focus();
      });
      select.addEventListener('input', () => this.sync());
      select.addEventListener('change', () => this.sync());
      // Async preferences can change disabled/options after the event returns.
      this.observer = new MutationObserver(() => this.sync());
      this.observer.observe(select, { attributes: true, childList: true, subtree: true, characterData: true,
        attributeFilter: ['disabled', 'hidden', 'label', 'selected', 'aria-label', 'aria-describedby', 'aria-invalid', 'title'] });
      const fieldset = select.closest('fieldset');
      if (fieldset) this.observer.observe(fieldset, { attributes: true, attributeFilter: ['disabled', 'hidden'] });
      this.sync();
    }

    options() { return [...this.select.options]; }
    enabled(index) {
      const option = this.select.options[index];
      return Boolean(option && !option.disabled && !option.hidden && !option.closest('optgroup[disabled]'));
    }
    name() {
      const explicit = this.select.getAttribute('aria-label');
      if (explicit) return explicit;
      const ids = this.select.getAttribute('aria-labelledby');
      if (ids) return ids.split(/\s+/).map(id => document.getElementById(id)?.textContent || '').join(' ').trim();
      return this.labels.map(label => {
        const copy = label.cloneNode(true);
        copy.querySelectorAll('select, .select-control').forEach(node => node.remove());
        return copy.textContent.trim();
      }).join(' ').trim();
    }
    sync() {
      const selected = this.select.options[this.select.selectedIndex];
      this.text.textContent = selected?.label || '';
      this.button.disabled = this.select.matches(':disabled');
      const name = this.name();
      this.button.setAttribute('aria-label', name ? `${name}: ${this.text.textContent}` : this.text.textContent);
      this.button.title = this.select.title || this.text.textContent;
      for (const attribute of ['aria-describedby', 'aria-invalid', 'aria-required']) {
        const value = this.select.getAttribute(attribute);
        if (value === null) this.button.removeAttribute(attribute);
        else this.button.setAttribute(attribute, value);
      }
      const signature = JSON.stringify([this.select.selectedIndex, this.options().map((option, index) =>
        [option.value, option.label, this.enabled(index), option.hidden])]);
      if (signature !== this.signature) {
        this.signature = signature;
        this.render();
      }
      if (active === this && (this.button.disabled || this.select.closest('[hidden]'))) this.close(false);
    }
    render() {
      const fragment = document.createDocumentFragment();
      this.items = this.options().map((option, index) => {
        const item = document.createElement('div'); item.className = 'select-option';
        item.id = `${this.menu.id}-${index}`;
        item.setAttribute('role', 'option');
        item.setAttribute('aria-selected', String(index === this.select.selectedIndex));
        item.setAttribute('aria-disabled', String(!this.enabled(index)));
        item.hidden = option.hidden;
        const mark = icon('check'); mark.classList.add('select-check');
        const label = document.createElement('span'); label.textContent = option.label;
        item.append(mark, label);
        item.addEventListener('click', () => this.choose(index));
        item.addEventListener('pointermove', () => { if (this.enabled(index)) this.highlight(index, false); });
        fragment.append(item);
        return item;
      });
      this.menu.replaceChildren(fragment);
      if (!this.enabled(this.index)) this.index = this.options().findIndex((_, index) => this.enabled(index));
      if (active === this) this.highlight(this.index, false);
    }
    open() {
      this.sync();
      if (this.button.disabled || !this.options().length) return;
      if (active && active !== this) active.close(false);
      active = this;
      this.prefix = '';
      this.index = this.enabled(this.select.selectedIndex) ? this.select.selectedIndex : this.options().findIndex((_, i) => this.enabled(i));
      this.menu.hidden = false;
      this.button.setAttribute('aria-expanded', 'true');
      this.button.focus({ preventScroll: true });
      this.position();
      this.highlight(this.index);
    }
    position() {
      const bounds = this.button.getBoundingClientRect();
      this.anchor = { top: bounds.top, left: bounds.left };
      const edge = 8, gap = 4;
      const width = Math.max(0, document.documentElement.clientWidth - 2 * edge);
      this.menu.style.maxWidth = `${width}px`;
      this.menu.style.minWidth = `${Math.min(Math.max(bounds.width, 180), width)}px`;
      this.menu.style.maxHeight = '320px';
      const below = innerHeight - bounds.bottom - edge - gap;
      const above = bounds.top - edge - gap;
      const up = this.menu.scrollHeight > below && above > below;
      this.menu.style.maxHeight = `${Math.max(0, Math.min(320, up ? above : below))}px`;
      const menu = this.menu.getBoundingClientRect();
      this.menu.style.left = `${Math.max(edge, Math.min(bounds.left, innerWidth - menu.width - edge))}px`;
      this.menu.style.top = `${up ? Math.max(edge, bounds.top - menu.height - gap) : bounds.bottom + gap}px`;
    }
    highlight(index, scroll = true) {
      this.index = index;
      this.items.forEach((item, i) => item.classList.toggle('is-active', i === index));
      if (this.items[index]) {
        this.button.setAttribute('aria-activedescendant', this.items[index].id);
        if (scroll) {
          // Scroll only the option list. scrollIntoView can also move WebKit's
          // page and immediately dismiss the menu via the document handler.
          const item = this.items[index];
          if (item.offsetTop < this.menu.scrollTop) this.menu.scrollTop = item.offsetTop;
          else if (item.offsetTop + item.offsetHeight > this.menu.scrollTop + this.menu.clientHeight) {
            this.menu.scrollTop = item.offsetTop + item.offsetHeight - this.menu.clientHeight;
          }
        }
      } else this.button.removeAttribute('aria-activedescendant');
    }
    move(step) {
      const available = this.options().map((_, i) => i).filter(i => this.enabled(i));
      if (!available.length) return;
      const position = available.indexOf(this.index);
      this.highlight(available[(position + step + available.length) % available.length]);
    }
    choose(index) {
      if (this.button.disabled || !this.enabled(index)) return;
      const changed = this.select.selectedIndex !== index;
      this.select.selectedIndex = index;
      this.close();
      if (changed) {
        this.select.dispatchEvent(new Event('input', { bubbles: true }));
        this.select.dispatchEvent(new Event('change', { bubbles: true }));
      }
      this.sync();
    }
    close(focus = true) {
      this.menu.hidden = true;
      this.button.setAttribute('aria-expanded', 'false');
      this.button.removeAttribute('aria-activedescendant');
      if (active === this) active = null;
      if (focus && !this.button.disabled && this.button.isConnected) this.button.focus();
    }
    keydown(event) {
      const opened = !this.menu.hidden;
      if (event.key === 'Tab') { if (opened) this.close(false); return; }
      if (event.key === 'Escape') {
        if (opened) { event.preventDefault(); event.stopPropagation(); this.close(); }
        return;
      }
      if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
        event.preventDefault();
        if (!opened) this.open();
        else if (event.key === 'ArrowDown') this.move(1);
        else if (event.key === 'ArrowUp') this.move(-1);
        else if (event.key === 'Enter' || event.key === ' ') this.choose(this.index);
        if (event.key === 'Home' || event.key === 'End') {
          const available = this.options().map((_, i) => i).filter(i => this.enabled(i));
          this.highlight(event.key === 'Home' ? available[0] : available.at(-1));
        }
        return;
      }
      if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.preventDefault();
        if (!opened) this.open();
        const now = Date.now();
        this.prefix = (now - this.typedAt > 700 ? '' : this.prefix) + event.key.toLocaleLowerCase();
        this.typedAt = now;
        const match = this.options().findIndex((option, index) => this.enabled(index) && option.label.toLocaleLowerCase().startsWith(this.prefix));
        if (match >= 0) this.highlight(match);
      }
    }
    destroy() { this.observer.disconnect(); this.close(false); this.menu.remove(); instances.delete(this); }
  }

  document.addEventListener('pointerdown', (event) => {
    if (active && !active.wrapper.contains(event.target) && !active.menu.contains(event.target)) active.close(false);
  });
  document.addEventListener('focusin', (event) => {
    if (active && !active.wrapper.contains(event.target) && !active.menu.contains(event.target)) active.close(false);
  });
  document.addEventListener('scroll', (event) => {
    if (!active || active.menu.contains(event.target)) return;
    // A live log tail may scroll independently; dismiss only if scrolling moves
    // the trigger itself, not merely because a sibling viewport emitted scroll.
    const bounds = active.button.getBoundingClientRect();
    if (active.anchor && (Math.abs(bounds.top - active.anchor.top) > .5 || Math.abs(bounds.left - active.anchor.left) > .5)) active.close(false);
  }, true);
  window.addEventListener('resize', () => active?.position());

  window.TailSplitSelect = {
    sync() {
      for (const instance of instances) {
        if (!instance.select.isConnected) instance.destroy();
      }
      for (const select of document.querySelectorAll('select')) {
        if (select.multiple || select.size > 1) continue;
        let instance = upgraded.get(select);
        if (!instance) {
          instance = new SelectMenu(select);
          upgraded.set(select, instance); instances.add(instance);
        } else instance.sync();
      }
    },
    close() { active?.close(false); },
  };
})();
