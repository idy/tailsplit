(() => {
  'use strict';

  const site = window.IdySite;
  if (!site) return;

  // English stays in the HTML so the page remains readable without JavaScript.
  const english = {};
  for (const [attribute, property] of [['data-i18n', 'textContent'], ['data-i18n-html', 'innerHTML']]) {
    document.querySelectorAll(`[${attribute}]`).forEach((node) => {
      const key = node.getAttribute(attribute);
      if (key.startsWith('split')) english[key] = node[property];
    });
  }
  document.querySelectorAll('[data-i18n-aria-label]').forEach((node) => {
    const key = node.getAttribute('data-i18n-aria-label');
    if (key.startsWith('split')) english[key] = node.getAttribute('aria-label');
  });
  Object.assign(english, {
    pageTitle: document.title,
    pageDescription: document.querySelector('meta[name="description"]').content,
    splitOgDescription: document.querySelector('meta[property="og:description"]').content,
    splitDirectResult: 'A matching direct rule keeps the connection on your current network, without using the proxy exit.',
    splitProxyResult: 'A domain on your proxy list uses the selected exit. The rule’s DNS follows that exit too.',
    splitTailnetResult: 'A configured Tailnet destination uses the embedded Tailscale connection. Your device must be authorized to reach it.',
    splitCopy: 'Copy', splitCopied: 'Copied', splitCopySuccess: 'Command copied.', splitSelectText: 'Select text',
    splitCopyFailure: 'Clipboard access is unavailable. The command is selected; copy it with your keyboard.',
    splitCopyManual: 'Clipboard access is unavailable. Select and copy the command manually.',
    splitMissingAssets: 'Some downloads are not included in this release; their buttons open the release page.',
    splitNoAssets: 'This release has no matching downloads yet; download buttons open the release page.',
    splitDownloadFallback: 'Open the release page: no matching package in this release',
  });
  const chinese = {
    "splitNavRouting": "分流规则",
    "splitNavInstall": "安装",
    "pageTitle": "TailSplit — 你的设备，你的分流规则",
    "pageDescription": "TailSplit 是一款适用于 Windows 和 macOS 的桌面应用，支持按域名和 IP 设置分流规则：直连、指定代理出口，以及已配置的 Tailnet 目标。",
    "splitOgDescription": "本地流量保持直连，指定连接使用你选择的出口。在桌面窗口、macOS 菜单栏或 Windows 托盘中管理分流。",
    "splitDirectResult": "命中直连规则的连接使用当前网络，不经过代理出口。",
    "splitProxyResult": "代理列表中的域名使用选定出口，其 DNS 查询也通过该出口。",
    "splitTailnetResult": "已配置的 Tailnet 目标使用内嵌的 Tailscale 连接。你的设备必须获得访问该目标的授权。",
    "splitCopy": "复制",
    "splitCopied": "已复制",
    "splitCopySuccess": "命令已复制。",
    "splitSelectText": "选择文本",
    "splitCopyFailure": "无法访问剪贴板。命令已选中，请使用键盘复制。",
    "splitCopyManual": "无法访问剪贴板。请手动选中并复制命令。",
    "splitMissingAssets": "部分下载文件未随此版本发布；对应按钮将打开该版本页面。",
    "splitNoAssets": "此版本尚无匹配的下载文件；下载按钮将打开该版本页面。",
    "splitDownloadFallback": "打开发布页面：此版本没有匹配的安装包",
    "splitHeroTitle": "你的设备。<br>你的分流规则。",
    "splitHeroDescription": "适用于 Windows 和 macOS 的桌面应用，支持按域名和 IP 分流。直连流量留在当前网络，指定连接使用代理出口，也可访问已配置的 Tailnet 目标。",
    "splitGet": "获取 TailSplit",
    "splitDocs": "阅读文档",
    "splitConcept": "分流示意",
    "splitConceptCaption": "每个连接，匹配一条路径。",
    "splitMapTitle": "由规则决定路径",
    "splitMapDescription": "设备发出的连接先经过 TailSplit 规则匹配，再由当前网络直连、通过选定的代理出口，或前往已配置且获授权的 Tailnet 目标。",
    "splitYourDevice": "你的设备",
    "splitYourRules": "你的规则",
    "splitDirect": "直连",
    "splitCurrentNetwork": "当前网络",
    "splitProxy": "代理",
    "splitSelectedExit": "选定出口",
    "splitAuthorizedPeers": "已授权节点",
    "splitDirectRule": "直连规则",
    "splitProxyRule": "代理规则",
    "splitTailnetRule": "Tailnet 规则",
    "splitConceptDisclaimer": "仅作示意，不会建立连接或更改网络设置。",
    "splitHeroNote": "启动后驻留菜单栏或 Windows 托盘，默认不连接。连接时机由你决定。",
    "splitRoutingTitle": "分流路径，<br>由你掌握。",
    "splitRoutingDescription": "在配置面板或 YAML 编辑器中管理域名列表、IP 网段和 GFWList 规则。",
    "splitRoutingModes": "可通过应用的 HTTP/SOCKS 代理设置接入，也可启用 TUN 分流以接管系统流量。两种模式使用相同的分流规则。",
    "splitRuleOrder": "规则模式按以下顺序匹配连接：",
    "splitTailnetHeading": "已配置的 Tailnet 目标",
    "splitTailnetDescription": "MagicDNS 名称、已配置的 Tailnet 域名和 Tailnet 地址使用内嵌的 Tailscale 连接。",
    "splitDirectHeading": "你的直连例外",
    "splitDirectDescription": "直连域名、GFWList 例外和直连 IP 网段使用当前网络。",
    "splitProxyHeading": "你的代理规则",
    "splitProxyDescription": "代理 IP 网段、代理域名和命中 GFWList 代理规则的连接使用选定出口。",
    "splitRemainingHeading": "其余流量",
    "splitRemainingDescription": "其他私有地址直连，其余流量遵循你配置的默认策略。",
    "splitDnsNote": "DNS 也遵循分流规则。代理域名通过代理出口解析；Tailnet 名称使用对应的 Tailnet 解析器。",
    "splitInstallTitle": "从下载，<br>到连接。",
    "splitDownloadIntro": "在 <a href=\"https://github.com/idy/tailsplit\">GitHub</a> 开源。提供 Windows 和 macOS 桌面应用，以及 Linux、Windows 和 macOS 命令行工具。原生构建支持 x64 和 ARM64。",
    "splitWindowsDesktop": "Windows 桌面应用",
    "splitWindowsNote": "x64 需要 Windows 10 或更高版本，ARM64 需要 Windows 11。安装程序包含窗口、托盘和网络服务。Windows 可执行文件尚未签名，请验证",
    "splitChecksums": "发布文件校验和",
    "splitPeriod": "。",
    "splitMacApp": "macOS 应用",
    "splitMacNote": "需要 macOS 14 或更高版本。两种架构的 Mac 应用均经过 Developer ID 签名及 Apple 公证；每个版本都附有签名凭据。",
    "splitWindowsCli": "Windows 命令行：",
    "splitLinuxCli": "Linux 命令行：",
    "splitOr": "或",
    "splitReleaseNotes": "版本说明与全部下载",
    "splitInstallShellTitle": "在 macOS 或 Linux 上安装。",
    "splitInstallShellDescription": "安装器会识别平台并验证 SHA-256，在 macOS 上安装应用，在 Linux 上安装命令行工具，同时保留配置和设备状态。",
    "splitInstallOptions": "如需安装到当前用户目录，在管道后使用 <code>sh -s -- --cli --no-sudo</code>。详见<a href=\"https://github.com/idy/tailsplit#install\">安装器选项</a>。",
    "splitStepOneTitle": "打开 TailSplit。",
    "splitStepOneDescription": "运行 Windows EXE 安装程序，或从 DMG 安装 macOS 应用。正常打开 TailSplit；全新安装会驻留托盘或菜单栏，默认不连接。",
    "splitStepTwoTitle": "授权网络服务。",
    "splitStepTwoDescription": "Windows 安装程序会安装网络服务。在 macOS 上，首次连接会打开内置的软件包安装器。由管理员批准服务安装，应用本身仍以普通权限运行。",
    "splitStepThreeTitle": "检查规则，然后连接。",
    "splitStepThreeDescription": "登录 Tailnet，选择出口，并在窗口或配置面板中检查分流规则。设备授权会打开默认浏览器；建立连接需要另行操作。",
    "splitUpgradeNote": "从旧版 YAML 升级时，请在连接前移除已废弃的 <code>dns.listen</code> 字段。保留其他配置及设备状态。",
    "splitApplicationProxy": "应用代理",
    "splitApplicationProxyDescription": "让指定应用使用本地 HTTP/SOCKS 代理。此模式只建立一条窄范围的 DNS TUN 路由，系统代理、默认流量路由和系统 DNS 保持原状。",
    "splitSystemRouting": "系统分流",
    "splitSystemRoutingDescription": "在 YAML 中启用 <code>tun.enabled</code>，即可添加系统流量分流和 DNS 拦截。规则适用于 TUN 捕获的连接。",
    "splitDetailsTitle": "连接之前，<br>了解这些细节。",
    "splitBuildTitle": "自行构建 macOS 应用？",
    "splitBuildDescription": "准备 Go 1.26.2 或更高版本、Python 3.11+ 和 Xcode Command Line Tools，在 <a href=\"https://github.com/idy/tailsplit\">TailSplit 源码目录</a>中运行：",
    "splitBuildCaveat": "这会生成 ad-hoc 签名的开发构建。Developer ID 签名和公证请遵循单独的<a href=\"https://github.com/idy/tailsplit#readme\">发布流程文档</a>。",
    "splitFaqExitTitle": "出口不可用时会怎样？",
    "splitFaqExitAnswer": "全局模式将捕获的流量发往选定出口。出口离线时，运行中的 TUN 保持启用，并阻止这些连接，直到出口恢复。",
    "splitFaqExitCaveat": "此保护依赖 TailSplit 及其 TUN 持续运行。断开连接、退出或后台服务崩溃都会移除 TUN 和临时 DNS，恢复系统网络。它并非持续生效的系统级防火墙断网保护。",
    "splitFaqPermissionTitle": "应用代理模式为什么需要授权？",
    "splitFaqPermissionAnswer": "两种连接模式都会为 DNS 创建 TUN。应用代理模式仅添加窄范围的 DNS 路由，系统分流则添加更广泛的流量路由。这些操作由网络服务执行，管理员授权由 Windows 或 macOS 安装器处理。",
    "splitFaqLoginTitle": "登录后会立即启动代理吗？",
    "splitFaqLoginAnswer": "不会。登录仅用于授权内嵌的 Tailscale 设备，启动代理需要另行操作。启动应用与自动连接也是独立的设置；自动连接默认关闭。",
    "splitFaqSwitchTitle": "可以选择其他出口吗？",
    "splitFaqSwitchAnswer": "出口菜单列出 Tailnet 提供的出口设备，无法选择离线或已过期的设备。切换出口会保存选择，并重新连接运行中的代理，同时保留设备登录和分流规则。TailSplit 不会擅自替你选择其他出口。",
    "splitFaqUpdateTitle": "TailSplit 如何保持更新？",
    "splitFaqUpdateAnswer": "桌面应用会在启动后不久及之后每六小时检查 GitHub Releases，只提供更新的稳定版。确认后才会下载或安装，每次下载都必须通过发布校验和验证。Windows 运行已验证的安装程序并请求管理员授权；macOS 会先检查新应用的签名，再替换并重新打开。配置和设备登录均会保留。",
    "splitFaqUpdateManual": "可在托盘、菜单栏或配置面板中手动检查更新或关闭自动检查。Windows 0.7.0 之前及 macOS 0.7.1 之前的版本，需要先通过本页面手动更新一次。",
    "splitCompanion": "需要提供一个由 SOCKS5 支持的 Tailnet 出口？",
    "splitMeetRelay": "了解 TailRelay",
    "splitChooseRule": "选择一种分流规则示意",
    "splitWindowsDownload": "下载 Windows 桌面安装程序",
    "splitMacDownload": "下载 macOS 应用",
    "splitCopyInstall": "复制 TailSplit 安装命令",
    "splitCopyBuild": "复制 TailSplit 应用构建命令",
    "splitConnectionModes": "连接模式"
};

  const concept = document.querySelector('#split-concept');
  const result = document.querySelector('#split-result');
  const descriptionKeys = { direct: 'splitDirectResult', proxy: 'splitProxyResult', tailnet: 'splitTailnetResult' };
  function renderDemo() {
    if (concept && result) result.textContent = site.t(descriptionKeys[concept.dataset.route] || descriptionKeys.proxy);
  }
  if (concept && result) {
    const controls = concept.querySelector('[data-demo-controls]');
    const buttons = concept.querySelectorAll('[data-split-route]');
    if (controls) controls.hidden = false;
    buttons.forEach((button) => {
      button.addEventListener('click', () => {
        const value = button.dataset.splitRoute;
        if (!Object.hasOwn(descriptionKeys, value)) return;
        concept.dataset.route = value;
        buttons.forEach((item) => item.setAttribute('aria-pressed', String(item === button)));
        renderDemo();
      });
    });
  }

  const copyStates = new Map();
  function renderCopy(button, state) {
    button.textContent = site.t(state.value === 'success' ? 'splitCopied' : state.value === 'selected' ? 'splitSelectText' : 'splitCopy');
    state.status.textContent = state.value === 'idle' ? '' : site.t({
      success: 'splitCopySuccess', selected: 'splitCopyFailure', manual: 'splitCopyManual',
    }[state.value]);
  }
  document.querySelectorAll('[data-copy-target]').forEach((button) => {
    const command = document.getElementById(button.dataset.copyTarget);
    const status = button.closest('.command-line')?.parentElement?.querySelector('[data-copy-status]');
    if (!command || !status) return;
    const state = { status, value: 'idle' };
    copyStates.set(button, state);
    button.hidden = false;
    button.addEventListener('click', async () => {
      try {
        if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
        await navigator.clipboard.writeText(command.textContent.trim());
        state.value = 'success';
      } catch {
        state.value = 'manual';
        try {
          const selection = window.getSelection();
          if (selection) {
            const range = document.createRange();
            range.selectNodeContents(command);
            selection.removeAllRanges();
            selection.addRange(range);
            state.value = 'selected';
          }
        } catch { /* Keep the manual-copy instructions if selection is unavailable. */ }
      }
      renderCopy(button, state);
    });
  });

  const downloads = [...document.querySelectorAll('[data-download]')];
  const releasesUrl = 'https://github.com/idy/tailsplit/releases';
  const patterns = {
    'darwin-arm64': /^TailSplit_[^/]+_darwin_arm64\.dmg$/,
    'darwin-amd64': /^TailSplit_[^/]+_darwin_amd64\.dmg$/,
    'linux-amd64': /^tailsplit_[^/]+_linux_amd64\.deb$/,
    'linux-arm64': /^tailsplit_[^/]+_linux_arm64\.deb$/,
    'windows-amd64': /^TailSplit_[^/]+_windows_amd64_setup\.exe$/,
    'windows-arm64': /^TailSplit_[^/]+_windows_arm64_setup\.exe$/,
    'windows-cli-amd64': /^tailsplit_[^/]+_windows_amd64\.zip$/,
    'windows-cli-arm64': /^tailsplit_[^/]+_windows_arm64\.zip$/,
    checksums: /^SHA256SUMS$/,
  };
  let release;
  let releaseState = 'loading';
  function renderRelease() {
    let available = 0;
    for (const link of downloads) {
      const pattern = patterns[link.dataset.download];
      const asset = release && pattern ? site.asset(release, pattern) : undefined;
      // Use GitHub's actual asset URL: versions alone cannot predict published filenames.
      link.href = asset?.browser_download_url || release?.html_url || releasesUrl;
      link.dataset.downloadState = asset ? 'ready' : 'fallback';
      link.title = asset?.name || site.t(release ? 'splitDownloadFallback' : releaseState === 'loading' ? 'loadingRelease' : 'releaseUnavailable');
      if (asset) available += 1;
    }
    document.querySelectorAll('[data-release-link]').forEach((link) => {
      link.href = release?.html_url || releasesUrl;
    });
    let message = site.t(releaseState === 'loading' ? 'loadingRelease' : 'releaseUnavailable');
    if (release) {
      message = `${site.t(release.prerelease ? 'previewRelease' : 'stableRelease')}: ${release.tag_name}.`;
      if (available < downloads.length) message += ` ${site.t(available ? 'splitMissingAssets' : 'splitNoAssets')}`;
    }
    document.querySelectorAll('[data-release-status]').forEach((status) => { status.textContent = message; });
  }
  function renderDynamic() {
    document.querySelector('meta[property="og:description"]').content = site.t('splitOgDescription');
    renderDemo();
    copyStates.forEach((state, button) => renderCopy(button, state));
    renderRelease();
  }
  document.addEventListener('idy:languagechange', renderDynamic);
  site.init({ en: english, zh: chinese });

  if (downloads.length) {
    site.loadRelease('idy/tailsplit').then((published) => {
      release = published;
      releaseState = 'ready';
      renderRelease();
    }).catch(() => {
      release = undefined;
      releaseState = 'error';
      renderRelease();
    });
  }
})();
