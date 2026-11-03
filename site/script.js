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
        status.textContent = 'Command copied.';
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

  const downloads = [...document.querySelectorAll('[data-download]')];
  if (downloads.length) {
    fetch('https://api.github.com/repos/idy/tailsplit/releases/latest', {
      headers: { Accept: 'application/vnd.github+json' }
    }).then((response) => {
      if (!response.ok) throw new Error('Release metadata unavailable');
      return response.json();
    }).then((release) => {
      if (release.draft || release.prerelease || !/^v\d+\.\d+\.\d+$/.test(release.tag_name) || !Array.isArray(release.assets)) return;
      const version = release.tag_name.slice(1);
      const names = {
        'darwin-arm64': `TailSplit_${version}_darwin_arm64.dmg`,
        'darwin-amd64': `TailSplit_${version}_darwin_amd64.dmg`,
        'linux-amd64': `tailsplit_${version}_linux_amd64.deb`,
        'linux-arm64': `tailsplit_${version}_linux_arm64.deb`,
        'windows-amd64': `TailSplit_${version}_windows_amd64_setup.exe`,
        'windows-arm64': `TailSplit_${version}_windows_arm64_setup.exe`,
        'windows-cli-amd64': `tailsplit_${version}_windows_amd64.zip`,
        'windows-cli-arm64': `tailsplit_${version}_windows_arm64.zip`,
        'checksums': 'SHA256SUMS'
      };
      const urls = downloads.map((link) => {
        const name = names[link.dataset.download];
        const url = `https://github.com/idy/tailsplit/releases/download/${release.tag_name}/${name}`;
        return release.assets.some((asset) => asset.name === name && asset.browser_download_url === url) ? url : null;
      });
      if (urls.some((url) => !url)) return;
      downloads.forEach((link, index) => { link.href = urls[index]; });
      document.querySelectorAll('[data-release-version]').forEach((label) => { label.textContent = release.tag_name; });
    }).catch(() => {
      // The versioned HTML links still download directly if metadata is unavailable.
    });
  }
})();
