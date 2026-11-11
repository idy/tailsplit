import { initSelect } from './vendor/magpie/js/select.js';

// Keep the native fields as the application's value/event model. Magpie owns
// rendering, keyboard navigation, popover placement and listener teardown.
const instances = new Map();

window.TailSplitSelect = {
  sync() {
    for (const [select, instance] of instances) {
      if (!select.isConnected) {
        instance.destroy();
        instances.delete(select);
      }
    }
    for (const select of document.querySelectorAll('select')) {
      if (select.multiple || select.size > 1) continue;
      let instance = instances.get(select);
      if (!instance) {
        select.classList.add('mds-select');
        instance = initSelect(select);
        instances.set(select, instance);
      }
      // Value property assignments do not produce DOM mutations or events.
      const label = select.selectedOptions[0]?.label || select.dataset.placeholder || '';
      if (instance.trigger.textContent !== label) {
        instance.refresh();
      }
      instance.trigger.title = select.title || label;
    }
  },
  close() {
    for (const instance of instances.values()) instance.close();
  },
};
