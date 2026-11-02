(() => {
  'use strict';

  const concept = document.querySelector('#split-concept');
  const result = document.querySelector('#split-result');
  const descriptions = {
    direct: 'A matching direct rule keeps the connection on your current network, without using the proxy exit.',
    proxy: 'A domain on your proxy list uses the selected exit. The rule’s DNS follows that exit too.',
    tailnet: 'A configured Tailnet destination uses the embedded Tailscale connection. Your device must be authorized to reach it.'
  };

  if (concept && result) {
    const controls = concept.querySelector('[data-demo-controls]');
    const buttons = concept.querySelectorAll('[data-split-route]');
    controls.hidden = false;
    buttons.forEach((button) => {
      button.addEventListener('click', () => {
        const route = button.dataset.splitRoute;
        if (!Object.hasOwn(descriptions, route)) return;
        concept.dataset.route = route;
        buttons.forEach((item) => item.setAttribute('aria-pressed', String(item === button)));
        result.textContent = descriptions[route];
      });
    });
  }

  document.querySelectorAll('[data-copy-target]').forEach((button) => {
    const command = document.getElementById(button.dataset.copyTarget);
    const status = button.closest('.build-note')?.querySelector('[data-copy-status]');
    if (!command || !status) return;
    button.hidden = false;
    button.addEventListener('click', async () => {
      try {
        if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
        await navigator.clipboard.writeText(command.textContent.trim());
        button.textContent = 'Copied';
        status.textContent = 'App build command copied.';
      } catch {
        const selection = window.getSelection();
        if (selection) {
          const range = document.createRange();
          range.selectNodeContents(command);
          selection.removeAllRanges();
          selection.addRange(range);
        }
        button.textContent = 'Select text';
        status.textContent = 'Clipboard access is unavailable. The command is selected; copy it with your keyboard.';
      }
    });
  });
})();
