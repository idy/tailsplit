import 'package:flutter/widgets.dart';

/// English and Simplified Chinese strings, matching desktop terminology.
///
/// User-provided names, node names, log text and configuration values are never
/// translated. Unsupported system languages fall back to English.
class S {
  const S._(this.zh);

  final bool zh;

  static const en = S._(false);
  static const cn = S._(true);

  static const supportedLocales = [Locale('en'), Locale('zh')];

  static S of(BuildContext context) {
    final locale = Localizations.maybeLocaleOf(context);
    return locale?.languageCode == 'zh' ? cn : en;
  }

  String _(String english, String chinese) => zh ? chinese : english;

  // Navigation
  String get tabExit => _('Exit Node', '出口节点');
  String get tabLogs => _('Logs', '日志');
  String get tabSettings => _('Settings', '设置');

  // Connection
  String get connection => _('Connection', '连接');
  String get connected => _('Connected', '已连接');
  String get disconnected => _('Disconnected', '未连接');
  String get connecting => _('Connecting…', '正在连接…');
  String get disconnecting => _('Disconnecting…', '正在断开…');
  String get connectionFailed => _('Connection failed', '连接失败');
  String get vpnPermissionDenied =>
      _('VPN permission was not granted.', '未获得 VPN 权限。');
  String via(String exit) => _('via $exit', '经由 $exit');
  String get chooseNodeToConnect =>
      _('Choose an exit node to connect.', '选择出口节点后即可连接。');
  String get addExitToConnect =>
      _('Add an exit to connect.', '添加出口后即可连接。');
  String get routing => _('Routing', '路由');
  String get ruleBased => _('Rule-based', '规则分流');
  String get global => _('Global (Kill-Switch)', '全局（Kill-Switch）');
  String get globalShort => _('Global', '全局');
  String get routingFooter => _(
        'Rule-based sends matched domains and IPs through the exit. Global sends all traffic through it and blocks traffic if the exit goes offline; disconnecting restores normal networking.',
        '规则分流只让匹配的域名和 IP 经过出口。全局模式让所有流量经过出口，出口离线时阻断流量；断开连接即恢复正常网络。',
      );

  // Exit
  String get exit => _('Exit', '出口');
  String get addExit => _('Add exit', '添加出口');
  String get addExitTitle => _('No exit yet', '还没有出口');
  String get addExitBody => _(
        'Sign in to Tailscale to add this phone as a device in your Tailnet, then choose an exit node.',
        '登录 Tailscale，把这台手机作为设备加入你的 Tailnet，然后选择出口节点。',
      );
  String get signIn => _('Sign in', '登录');
  String get signingIn => _('Waiting for sign-in…', '等待登录…');
  String get signingInBody => _(
        'Finish signing in in the browser. TailSplit continues automatically.',
        '请在浏览器中完成登录，TailSplit 会自动继续。',
      );
  String get openSignInAgain => _('Open sign-in page', '打开登录页面');
  String get cancel => _('Cancel', '取消');
  String get noNodeSelected => _('No exit node selected', '未选择出口节点');
  String get signedOut => _('Signed out', '未登录');
  String get nodeOffline => _('Exit node offline', '出口节点离线');
  String get awaitingApproval =>
      _('Waiting for admin approval', '等待管理员批准');
  String get newExit => _('New exit', '新出口');
  String thisDevice(String name) => _('This device: $name', '本机：$name');
  String get refresh => _('Refresh', '刷新');
  String get refreshing => _('Refreshing…', '正在刷新…');
  String updated(String when) => _('Updated $when', '更新于 $when');
  String get justNow => _('just now', '刚刚');
  String minutesAgo(int n) => _('$n min ago', '$n 分钟前');
  String hoursAgo(int n) => _('$n h ago', '$n 小时前');
  String daysAgo(int n) => _('$n d ago', '$n 天前');

  // Picker
  String get chooseExitNode => _('Exit Node', '出口节点');
  String get confirm => _('Confirm', '确认');
  String get online => _('Online', '在线');
  String get offline => _('Offline', '离线');
  String lastSeen(String when) => _('Seen $when', '$when在线');
  String signedInAs(String user) => _('Signed in as $user', '已登录：$user');
  String get noExitNodes => _('No exit nodes in this Tailnet', '此 Tailnet 中没有出口节点');
  String get noExitNodesBody => _(
        'Advertise a device as an exit node and approve it in the Tailscale admin console, then refresh.',
        '在某台设备上宣告出口节点并在 Tailscale 管理后台批准，然后刷新。',
      );
  String get pickerFooterIdle =>
      _('Saving keeps TailSplit disconnected.', '保存后保持未连接。');
  String get pickerFooterActive =>
      _('Saving reconnects TailSplit.', '保存后将重新连接。');
  String get removeExit => _('Remove exit', '移除出口');
  String get removeExitTitle => _('Remove this exit?', '移除这个出口？');
  String get removeExitBody => _(
        'TailSplit disconnects, signs this phone out of the Tailnet and forgets the exit. The device record stays in the Tailscale admin console.',
        'TailSplit 会断开连接、让这台手机退出 Tailnet 并忘记该出口。设备记录仍保留在 Tailscale 管理后台。',
      );
  String get remove => _('Remove', '移除');
  String get noTrafficNoNode =>
      _('An exit without a node carries no traffic.', '未选择节点的出口不承载流量。');
  String get pingUnavailable => _(
        'Ping needs the VPN core; the picker shows reachability reported by Tailscale.',
        'Ping 需要 VPN 内核；此处显示 Tailscale 报告的在线状态。',
      );

  // Settings
  String get reconnectToApply =>
      _('Changes apply after reconnecting.', '重新连接后生效。');
  String get reconnect => _('Reconnect', '重新连接');
  String get mode => _('Mode', '模式');
  String get ruleSource => _('Rule source', '规则来源');
  String get builtinRules => _('Builtin rules', '内置规则');
  String get customRules => _('Custom URL', '自定义 URL');
  String get noRuleList => _('No rule list', '不使用规则列表');
  String get customRulesUrl => _('Rule list URL', '规则列表 URL');
  String get unmatchedTraffic => _('Unmatched traffic', '未匹配流量');
  String get direct => _('Direct', '直连');
  String get throughExit => _('Through exit', '经由出口');
  String get proxyDomains => _('Proxy domains', '代理域名');
  String get directDomains => _('Direct domains', '直连域名');
  String get proxyIps => _('Proxy IP ranges', '代理 IP 段');
  String get directIps => _('Direct IP ranges', '直连 IP 段');
  String entries(int n) => _(n == 1 ? '1 entry' : '$n entries', '$n 条');
  String get none => _('None', '无');
  String get globalHidesSplit => _(
        'Global mode keeps these rules saved and ignores them until you return to Rule-based.',
        '全局模式保留这些规则，回到规则分流后重新生效。',
      );
  String get dns => _('DNS', 'DNS');
  String get proxyDns => _('Proxy DNS', '代理 DNS');
  String get directDns => _('Direct DNS', '直连 DNS');
  String get system => _('System', '系统');
  String get fakeIp => _('FakeIP', 'FakeIP');
  String get fakeIpNote => _(
        'Answers proxied names with synthetic addresses so routing follows the domain.',
        '为代理域名返回合成地址，使路由跟随域名。',
      );
  String get network => _('Network', '网络');
  String get directIpv6 => _('Direct IPv6', '直连 IPv6');
  String get directIpv6Note =>
      _('Off answers direct AAAA queries with no address.', '关闭时直连 AAAA 查询返回空结果。');
  String get mtu => _('MTU', 'MTU');
  String get general => _('General', '通用');
  String get language => _('Language', '语言');
  String get followSystem => _('Follow system', '跟随系统');
  String get logLevel => _('Log level', '日志级别');
  String get about => _('About', '关于');
  String get version => _('Version', '版本');
  String get coreVersion => _('sing-box core', 'sing-box 内核');
  String get sourceCode => _('Source code', '源代码');
  String get licenses => _('Licenses', '许可证');
  String get unavailable => _('Unavailable', '不可用');
  String get done => _('Done', '完成');
  String get add => _('Add', '添加');
  String get edit => _('Edit', '编辑');
  String get delete => _('Delete', '删除');
  String get domainHint => _('example.com', 'example.com');
  String get cidrHint => _('192.0.2.0/24', '192.0.2.0/24');
  String get invalidDomain => _('Enter a domain such as example.com.', '请输入域名，例如 example.com。');
  String get invalidCidr => _('Enter an IP range such as 192.0.2.0/24.', '请输入 IP 段，例如 192.0.2.0/24。');
  String get invalidIp => _('Enter an IP address.', '请输入 IP 地址。');
  String get invalidUrl => _('Enter an HTTPS URL.', '请输入 HTTPS URL。');
  String get invalidMtu => _('Enter 1280–9000.', '请输入 1280–9000。');
  String get listEmpty => _('No entries', '没有条目');
  String get listFooterDomains => _(
        'Each entry matches the domain and its subdomains.',
        '每个条目匹配该域名及其子域名。',
      );
  String get listFooterIps => _('IPv4 or IPv6 CIDR ranges.', 'IPv4 或 IPv6 CIDR 段。');

  // Logs
  String get logsEmpty => _('No log output yet', '暂无日志');
  String get logsEmptyBody => _(
        'Connection and sign-in events appear here. The VPN core adds its own output while connected.',
        '连接和登录事件会显示在这里。连接后 VPN 内核也会输出日志。',
      );
  String get clear => _('Clear', '清除');
  String get copy => _('Copy', '复制');
  String get copied => _('Copied', '已复制');

  // Errors
  String get errorSignIn => _('Sign-in failed', '登录失败');
  String get errorRefresh => _('Couldn\'t refresh exit nodes', '无法刷新出口节点');
  String get errorRules => _('Couldn\'t load the rule list', '无法加载规则列表');
  String get needsSignInAgain => _(
        'This device was signed out of the Tailnet. Sign in again.',
        '这台设备已退出 Tailnet，请重新登录。',
      );
  String get retry => _('Retry', '重试');
  String get ok => _('OK', '好');

  // Hardening: connection steps, preflight and recovery.
  String get stepChecking => _('Checking exit node…', '正在检查出口节点…');
  String get stepRules => _('Loading rule list…', '正在加载规则列表…');
  String get stepStarting => _('Starting VPN…', '正在启动 VPN…');
  String get turnOffToCancel => _('Turn off to cancel.', '关闭开关可取消。');
  String get nodeMissing => _('Exit node not found', '找不到出口节点');
  String errorNodeOffline(String node) => _('$node is offline', '$node 离线');
  String get errorNodeOfflineBody => _(
        'Choose another exit node, or try again when it is back online.',
        '请选择其他出口节点，或等它恢复在线后重试。',
      );
  String get errorNodeMissing => _('Exit node not found', '找不到出口节点');
  String errorNodeMissingBody(String node) => _(
        '$node is no longer offered as an exit node in this Tailnet. Choose another.',
        '此 Tailnet 已不再提供 $node 作为出口节点，请选择其他节点。',
      );
  String get chooseNode => _('Choose node', '选择节点');
  String get awaitingApprovalBody => _(
        'An admin needs to approve this device in the Tailscale admin console before it can use exit nodes.',
        '管理员需要先在 Tailscale 管理后台批准这台设备，才能使用出口节点。',
      );
  String get errorStorage => _('Couldn\'t save changes', '无法保存更改');
  String get errorStorageBody => _(
        'Your changes apply until TailSplit closes. Free up storage and try again.',
        '更改在 TailSplit 关闭前有效。请释放存储空间后重试。',
      );
  String get errorOpenBrowser => _(
        'Couldn\'t open the sign-in page. Check that a browser is available and try again.',
        '无法打开登录页面。请确认设备上有可用的浏览器后重试。',
      );
  String get rulesFirstDownload => _(
        'TailSplit downloads the rule list once and reuses it. To connect without it, choose No rule list in Settings.',
        'TailSplit 只需下载一次规则列表并会重复使用。若要不使用规则列表连接，请在设置中选择“不使用规则列表”。',
      );
  String get causeOffline => _(
        'Can\'t reach the network. Check your internet connection and try again.',
        '无法连接网络。请检查网络连接后重试。',
      );
  String get causeTimeout => _('The request timed out. Try again.', '请求超时，请重试。');
  String get causeTls => _(
        'The secure connection failed. Check the device\'s date and time, or try another network.',
        '安全连接失败。请检查设备的日期和时间，或换个网络重试。',
      );
  String get causeRateLimited =>
      _('Too many requests. Wait a moment and try again.', '请求过于频繁，请稍后重试。');
  String get causeServer => _(
        'The server sent an unexpected response. Details are in Logs.',
        '服务器返回了无法识别的响应，详情见日志。',
      );
  String get causeRejected => _(
        'The server refused the request. Details are in Logs.',
        '服务器拒绝了请求，详情见日志。',
      );
  String get causeInvalidRules => _(
        'That address doesn\'t contain a usable rule list.',
        '该地址不是可用的规则列表。',
      );
  String get ruleList => _('Rule list', '规则列表');
  String get rulesNotDownloaded => _('Downloads on first connection', '首次连接时下载');
  String get update => _('Update', '更新');
  String get updating => _('Updating…', '正在更新…');
  String get rulesUpdateFailed => _(
        'Couldn\'t update the rule list. The saved copy is still used.',
        '规则列表更新失败，继续使用已保存的副本。',
      );
  String get rulesUpdatedReconnect =>
      _('Rule list updated. Reconnect to use it.', '规则列表已更新，重新连接后生效。');
  String get rulesUpdated => _('Rule list updated', '规则列表已更新');
  String get logFilter => _('Filter', '筛选');
  String copiedLines(int n) => _(n == 1 ? 'Copied 1 line' : 'Copied $n lines', '已复制 $n 行');
  String removed(String value) => _('Removed $value', '已移除 $value');
  String get undo => _('Undo', '撤销');
  String get removing => _('Removing…', '正在移除…');
  String get dismiss => _('Dismiss', '关闭');
  String get invalidProxyDns => _(
        'Enter an IP address, such as 8.8.8.8 or 2001:4860:4860::8888, with an optional port.',
        '请输入 IP 地址，例如 8.8.8.8 或 2001:4860:4860::8888，可附带端口。',
      );
  String get invalidDirectDns => _(
        'Enter an IP address with an optional port, or leave empty to use the system resolver.',
        '请输入 IP 地址（可附带端口），或留空使用系统 DNS。',
      );
  String get noExitNodesApproval => _(
        'Exit nodes appear after an admin approves this device.',
        '管理员批准这台设备后，出口节点会显示在这里。',
      );

  static const _months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];

  /// A calendar date for timestamps older than a month.
  String date(DateTime time) {
    final local = time.toLocal();
    return zh
        ? '${local.year}年${local.month}月${local.day}日'
        : '${_months[local.month - 1]} ${local.day}, ${local.year}';
  }
}
