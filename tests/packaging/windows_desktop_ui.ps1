param(
    [Parameter(Mandatory=$true)][string]$Application,
    [Parameter(Mandatory=$true)][string]$Config,
    [Parameter(Mandatory=$true)][string]$Log,
    [Parameter(Mandatory=$true)][string]$Screenshot,
    [Parameter(Mandatory=$true)][string]$Credentials,
    # Leave the verified GUI running with an active session for an upgrade check.
    [switch]$KeepRunning
)
$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true') { throw 'Desktop integration runs only in disposable Windows CI.' }
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -AssemblyName System.Drawing
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class TailSplitDesktopFixture {
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct Startup { public int cb; public string reserved,desktop,title; public int x,y,xsize,ysize,xchars,ychars,fill,flags; public short show,reserved2; public IntPtr reservedptr,input,output,error; }
 [StructLayout(LayoutKind.Sequential)] public struct ProcessInfo { public IntPtr process,thread; public int pid,tid; }
 [StructLayout(LayoutKind.Sequential)] public struct Label { public IntPtr sid; public int attributes; }
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int left,top,right,bottom; }
 [StructLayout(LayoutKind.Sequential)] public struct Point { public int x,y; }
 [StructLayout(LayoutKind.Sequential)] public struct MonitorInfo { public int size; public Rect monitor,work; public uint flags; }
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool OpenProcessToken(IntPtr process,uint access,out IntPtr token);
 public static string TokenSource = "fresh standard Windows account";
 [DllImport("advapi32.dll",SetLastError=true,CharSet=CharSet.Unicode)] static extern bool CreateProcessWithLogonW(string user,string domain,string password,uint logonFlags,string app,string command,uint flags,IntPtr environment,string directory,ref Startup startup,out ProcessInfo result);
 [DllImport("kernel32.dll",SetLastError=true)] static extern uint WaitForSingleObject(IntPtr handle,uint milliseconds);
 [DllImport("kernel32.dll",SetLastError=true)] static extern bool GetExitCodeProcess(IntPtr handle,out uint code);
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool GetTokenInformation(IntPtr token,int type,IntPtr buffer,int size,out int needed);
 [DllImport("advapi32.dll")] static extern int GetLengthSid(IntPtr sid);
 [DllImport("advapi32.dll")] static extern IntPtr GetSidSubAuthorityCount(IntPtr sid);
 [DllImport("advapi32.dll")] static extern IntPtr GetSidSubAuthority(IntPtr sid,int index);
 [DllImport("kernel32.dll")] static extern IntPtr GetCurrentProcess();
 [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(uint access,bool inherit,int pid);
 [DllImport("kernel32.dll")] public static extern bool CloseHandle(IntPtr handle);
 [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr pointer);
 delegate bool EnumWindowProc(IntPtr window,IntPtr data);
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumWindowProc callback,IntPtr data);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr window,out int pid);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr window,StringBuilder name,int size);
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode)] static extern bool QueryFullProcessImageName(IntPtr process,uint flags,StringBuilder name,ref int size);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window,uint message,IntPtr wparam,IntPtr lparam);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out Rect result);
 [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr window,out Rect result);
 [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr window,ref Point point);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr SendMessageTimeout(IntPtr window,uint message,IntPtr wparam,string lparam,uint flags,uint timeout,out IntPtr result);
 [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr window);
 [DllImport("user32.dll")] public static extern IntPtr MonitorFromWindow(IntPtr window,uint flags);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern bool GetMonitorInfo(IntPtr monitor,ref MonitorInfo info);
 [DllImport("user32.dll",SetLastError=true)] public static extern bool PrintWindow(IntPtr window,IntPtr deviceContext,uint flags);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
 [DllImport("user32.dll")] public static extern IntPtr GetProp(IntPtr window,string name);
 static void Check(bool value,string operation="Win32") { if(!value) { int code=Marshal.GetLastWin32Error(); throw new System.ComponentModel.Win32Exception(code,operation+": "+new System.ComponentModel.Win32Exception(code).Message); } }
 public static int LaunchStandard(string user,string password,string app,string arguments,bool wait) {
  Startup startup=new Startup{cb=Marshal.SizeOf(typeof(Startup))}; ProcessInfo info;
  // LOGON_WITH_PROFILE and a null environment create this account's normal
  // profile/environment, including HKCU and AppData used by WebView2.
  Check(CreateProcessWithLogonW(user,".",password,1,app,"\""+app+"\" "+arguments,0x400,IntPtr.Zero,System.IO.Path.GetDirectoryName(app),ref startup,out info),"CreateProcessWithLogonW(standard account)");
  CloseHandle(info.thread);
  try {
   if(!wait)return info.pid;
   Check(WaitForSingleObject(info.process,30000)==0,"Wait for second instance");
   uint code;Check(GetExitCodeProcess(info.process,out code),"Get second instance exit code");return (int)code;
  } finally {CloseHandle(info.process);}
 }
 public static IntPtr FindAppWindow(int pid,string app,string[] classes) {
  // MyGo's class is shared by other apps. Only accept a top-level window of
  // the process this fixture launched, after checking its executable path.
  IntPtr process=OpenProcess(0x1000,false,pid);
  if(process==IntPtr.Zero)return IntPtr.Zero;
  try {
   var image=new StringBuilder(32768);int size=image.Capacity;
   if(!QueryFullProcessImageName(process,0,image,ref size) ||
      !String.Equals(image.ToString(),System.IO.Path.GetFullPath(app),StringComparison.OrdinalIgnoreCase))return IntPtr.Zero;
   IntPtr found=IntPtr.Zero;
   EnumWindowProc visit=delegate(IntPtr window,IntPtr data) {
    int owner;GetWindowThreadProcessId(window,out owner);
    if(owner!=pid)return true;
    var name=new StringBuilder(256);
    if(GetClassName(window,name,name.Capacity)==0)return true;
    foreach(string klass in classes) {
     if(String.Equals(name.ToString(),klass,StringComparison.Ordinal)) {found=window;return false;}
    }
    return true;
   };
   EnumWindows(visit,IntPtr.Zero);
   return found;
  } finally {CloseHandle(process);}
 }
 public static int Integrity(int pid) {
  IntPtr process=OpenProcess(0x1000,false,pid),token=IntPtr.Zero,buffer=IntPtr.Zero;
  if(process==IntPtr.Zero) throw new System.ComponentModel.Win32Exception();
  try {
   Check(OpenProcessToken(process,8,out token));int needed;
   GetTokenInformation(token,25,IntPtr.Zero,0,out needed);buffer=Marshal.AllocHGlobal(needed);
   Check(GetTokenInformation(token,25,buffer,needed,out needed));
   IntPtr sid=Marshal.ReadIntPtr(buffer);int count=Marshal.ReadByte(GetSidSubAuthorityCount(sid));
   return Marshal.ReadInt32(GetSidSubAuthority(sid,count-1));
  } finally {if(buffer!=IntPtr.Zero)Marshal.FreeHGlobal(buffer);if(token!=IntPtr.Zero)CloseHandle(token);CloseHandle(process);}
 }
}
'@

function Wait-Until([scriptblock]$Check, [string]$Failure, [int]$Seconds=40) {
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    do {
        $value = & $Check
        if ($value) { return $value }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    throw $Failure
}
function Broadcast-AppearanceChange {
    $result = [IntPtr]::Zero
    # The desktop and WebView2 browser processes must both receive the same
    # broadcast Windows sends after Personalization > Colors changes.
    [TailSplitDesktopFixture]::SendMessageTimeout([IntPtr]0xffff,0x1a,[IntPtr]::Zero,'ImmersiveColorSet',2,1000,[ref]$result) | Out-Null
}
function Test-NativeCanvas([IntPtr]$Window, [int]$Light) {
    $bounds = New-Object TailSplitDesktopFixture+Rect
    $client = New-Object TailSplitDesktopFixture+Rect
    $origin = New-Object TailSplitDesktopFixture+Point
    if (-not [TailSplitDesktopFixture]::GetWindowRect($Window,[ref]$bounds) -or
        -not [TailSplitDesktopFixture]::GetClientRect($Window,[ref]$client) -or
        -not [TailSplitDesktopFixture]::ClientToScreen($Window,[ref]$origin)) { throw 'Cannot inspect themed window geometry.' }
    $bitmap = New-Object Drawing.Bitmap(($bounds.right-$bounds.left),($bounds.bottom-$bounds.top))
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $context = $graphics.GetHdc()
        try {
            if (-not [TailSplitDesktopFixture]::PrintWindow($Window,$context,2)) { throw 'Cannot capture themed window.' }
        } finally { $graphics.ReleaseHdc($context) }
        # A blank point at the top center of the HTML titlebar reads the page's
        # canvas, proving its media query changed in the actual native window.
        $pixel = $bitmap.GetPixel(($origin.x-$bounds.left+[int](($client.right-$client.left)/2)),($origin.y-$bounds.top+2))
        $expected = if ($Light) { [Drawing.Color]::FromArgb(245,245,247) } else { [Drawing.Color]::FromArgb(28,28,30) }
        return $pixel.ToArgb() -eq $expected.ToArgb()
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
}
# Both the 0.7.9 upgrade baseline and the current page navigation run here.
# Match application-owned controls in either supported locale. The new Chinese
# Connection nav is also named 连接, so it must never be used as a connect action.
$UiNames = @{
    '连接'='Connect'; '连接 TailSplit'='Connect TailSplit'; '断开'='Disconnect';
    '连接页面'=@('出口节点','Exit nodes','连接','Connection'); '出口节点页面'=@('出口节点','Exit nodes'); '日志'='Logs'; '设置'='Settings'; '网络'='Network';
    '登录 Tailscale'='Sign in to Tailscale'; '添加出口'='Add exit'; 'Tailscale 设备操作'='Tailscale device actions';
    '退出登录…'='Sign out…'; '刷新'='Refresh'; '代理监听地址'='Proxy listen address';
    '登录 Tailnet…'='Sign in to Tailnet…'; '退出设备登录'='Sign out of device';
    '刷新出口'='Refresh exits'; '查看日志'='View logs';
    '保存并应用'='Save & apply'; '最大化或还原'='Maximize or restore';
    '刷新日志'='Refresh logs'; '关闭'='Close'; '确认并连接'='Confirm and connect'
}
function Find-Control([System.Windows.Automation.AutomationElement]$Root, [string]$Name, [System.Windows.Automation.ControlType]$Type) {
    foreach ($candidate in (@($Name) + @($UiNames[$Name]))) {
        if (-not $candidate) { continue }
        $condition = New-Object System.Windows.Automation.AndCondition(
            (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,$Type)),
            (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$candidate)))
        $found = $Root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
        if ($found) { return $found }
    }
    return $null
}
function Find-Button([System.Windows.Automation.AutomationElement]$Root, [string]$Name) {
    $button = Find-Control $Root $Name ([System.Windows.Automation.ControlType]::Button)
    if ($button) { return $button }
    # ARIA connection switches can be exposed as CheckBox by WebView2.
    return Find-Control $Root $Name ([System.Windows.Automation.ControlType]::CheckBox)
}
function Read-WindowText([System.Windows.Automation.AutomationElement]$Root) {
    $condition = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Document)
    foreach ($document in $Root.FindAll([System.Windows.Automation.TreeScope]::Descendants,$condition)) {
        $pattern = $null
        if ($document.TryGetCurrentPattern([System.Windows.Automation.TextPattern]::Pattern,[ref]$pattern)) {
            return $pattern.DocumentRange.GetText(-1)
        }
    }
    return ''
}
function Add-FixtureLog([string]$Text) {
    # Append through a shared handle without replacing the standard user's log
    # or its ACL while the real desktop controller also has it open.
    $stream = [IO.File]::Open($Log,[IO.FileMode]::Append,[IO.FileAccess]::Write,([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
    try {
        $data = [Text.Encoding]::UTF8.GetBytes($Text)
        $stream.Write($data,0,$data.Length)
    } finally { $stream.Dispose() }
}
function Assert-BoundedLogs([string]$Text, [string]$OldMarker, [string]$LatestMarker) {
    if (-not $Text.Contains($LatestMarker)) { throw 'The native log view did not display the latest entry.' }
    if ($Text.Contains($OldMarker)) { throw 'The native log view included entries outside the bounded tail.' }
    # The document TextPattern includes page chrome as well as the 128 KiB log
    # tail. Both UI generations fit comfortably within this extra 8 KiB budget.
    if ($Text.Length -gt ((128 -shl 10) + 8192)) { throw 'The native log view exceeded its bounded tail and page chrome budget.' }
}
function Wait-Connected([System.Windows.Automation.AutomationElement]$Root, [string]$Failure) {
    # Disconnect is enabled as soon as a session starts; only the page's
    # running state proves the SYSTEM service accepted this user's session.
    $condition = New-Object System.Windows.Automation.OrCondition(
        (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'已连接')),
        (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'Connected')))
    # This is an explicitly direct-only no-device fixture. A gateway chooser
    # here would be a regression; production proxy policy still requires one.
    Wait-Until {
        if (Find-Button $Root '确认并连接') { throw 'Direct-only fixture unexpectedly requested an exit gateway.' }
        $Root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
    } $Failure 60 | Out-Null
}
function Invoke-Button([System.Windows.Automation.AutomationElement]$Root, [string]$Name) {
    $button = Wait-Until { $found=Find-Button $Root $Name; if ($found -and $found.Current.IsEnabled) {$found} } "Missing or disabled button: $Name"
    $pattern = $null
    if ($button.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern,[ref]$pattern)) {
        $pattern.Invoke()
    } else {
        $pattern = $button.GetCurrentPattern([System.Windows.Automation.TogglePattern]::Pattern)
        $pattern.Toggle()
    }
}

$credential = Get-Content -LiteralPath $Credentials -Raw | ConvertFrom-Json
$accountSID = $credential.sid
# Retain the old class for archived upgrade fixtures; production MyGo windows
# use a generic class, so discovery also requires this GUI's PID and image path.
$windowClasses = [string[]]@('MyGoWindow', ('TailSplit.Window.' + $accountSID))
$arguments = '--no-connect --config "' + $Config + '" --log "' + $Log + '"'
$guiProcess = [TailSplitDesktopFixture]::LaunchStandard($credential.username,$credential.password,$Application,$arguments,$false)
$primaryFailure = $null
$maximizeGeometry = $null
$startupClock = [Diagnostics.Stopwatch]::StartNew()
$nativePhases = New-Object 'System.Collections.Generic.List[object]'
$stage = 'process launched'
try {
    $integrity = [TailSplitDesktopFixture]::Integrity($guiProcess)
    if ($integrity -ne 8192) { throw "Desktop was not medium integrity: $integrity" }
    $stage = 'medium integrity verified; waiting for native window'
    # A brand-new CI profile also starts the browser processes for the first
    # time. Give that one cold start a bounded budget and retain phase timings.
    $window = Wait-Until {
        $handle=[TailSplitDesktopFixture]::FindAppWindow($guiProcess,$Application,$windowClasses)
        if ($handle -ne [IntPtr]::Zero) {
            $phase = [TailSplitDesktopFixture]::GetProp($handle,'TailSplit.WebView2.StartupStage').ToInt64()
            if (($nativePhases.Count -eq 0) -or ($nativePhases[$nativePhases.Count-1].phase -ne $phase)) {
                $nativePhases.Add(@{ phase=$phase; elapsed_ms=$startupClock.ElapsedMilliseconds })
            }
            if ([TailSplitDesktopFixture]::IsWindowVisible($handle)) { $handle }
        }
    } 'TailSplit did not create a visible native window.' 120
    $startupMilliseconds = $startupClock.ElapsedMilliseconds
    $stage = 'native window visible; waiting for WebView2'
    Wait-Until { [TailSplitDesktopFixture]::GetProp($window,'TailSplit.WebView2.Ready') -ne [IntPtr]::Zero } 'WebView2 did not load its local document.' | Out-Null
    $automation = [System.Windows.Automation.AutomationElement]::FromHandle($window)
    Wait-Until {
        (Find-Button $automation '连接 TailSplit') -or (Find-Button $automation '设置') -or (Find-Button $automation '连接')
    } 'The native window has no desktop controls.' | Out-Null
    # Detect the navigation contract from real controls, including builds used
    # by upgrade tests; a version string cannot identify a fixture build's UI.
    $pageNavigation = [bool]((Find-Button $automation '连接 TailSplit') -or (Find-Button $automation '设置'))
    $connectName = if ($pageNavigation) { '连接 TailSplit' } else { '连接' }
    Wait-Until { $button=Find-Button $automation $connectName; $button -and $button.Current.IsEnabled } 'The direct-config connect action is unavailable.' | Out-Null
    $deviceAction = if (Find-Button $automation '添加出口') { '添加出口' } else { '登录 Tailscale' }
    $initialControls = if ($pageNavigation) {
        @($deviceAction,'连接页面','日志','设置','最大化或还原')
    } else {
        @('断开','登录 Tailnet…','退出设备登录','刷新出口','查看日志','保存并应用','最大化或还原')
    }
    foreach ($name in $initialControls) {
        if (-not (Find-Button $automation $name)) { throw "Missing actual window control: $name" }
    }
    if ($pageNavigation) {
        # Newer builds moved refresh to a separate node page; older upgrade
        # fixtures expose it directly on Connection. Check the actual layout.
        if (Find-Button $automation '出口节点页面') {
            Invoke-Button $automation '出口节点页面'
            Wait-Until { Find-Button $automation '刷新' } 'The exit nodes page has no refresh control.' | Out-Null
            Invoke-Button $automation '连接页面'
            Wait-Until { (Find-Button $automation '添加出口') -or (Find-Button $automation '登录 Tailscale') } 'The exit list did not reopen.' | Out-Null
        } elseif (-not (Find-Button $automation '刷新')) {
            throw 'The connection page has no refresh control.'
        }
        foreach ($name in @('断开','Tailscale 设备操作','退出登录…','保存并应用')) {
            if (Find-Button $automation $name) { throw "Unexpected signed-out connection-page control: $name" }
        }
    }
    $stage = 'checking initial and live system app appearance'
    # Only this fixture's fresh account is changed; its Windows/system mode is
    # deliberately opposite to prove the app follows AppsUseLightTheme.
    $appearance = [Microsoft.Win32.Registry]::Users.CreateSubKey("$accountSID\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize")
    $savedAppearance = @{}
    foreach ($name in @('AppsUseLightTheme','SystemUsesLightTheme')) { $savedAppearance[$name] = $appearance.GetValue($name,$null) }
    try {
        $initialLight = [int]$appearance.GetValue('AppsUseLightTheme',1)
        Wait-Until { Test-NativeCanvas $window $initialLight } 'Initial page canvas does not follow the app appearance.' | Out-Null
        foreach ($light in @(0,1)) {
            $appearance.SetValue('AppsUseLightTheme',$light,[Microsoft.Win32.RegistryValueKind]::DWord)
            $appearance.SetValue('SystemUsesLightTheme',(1-$light),[Microsoft.Win32.RegistryValueKind]::DWord)
            Broadcast-AppearanceChange
            Wait-Until { Test-NativeCanvas $window $light } "Page canvas did not follow a live app appearance change (light=$light)." | Out-Null
        }
    } finally {
        foreach ($name in $savedAppearance.Keys) {
            if ($null -eq $savedAppearance[$name]) { $appearance.DeleteValue($name,$false) }
            else { $appearance.SetValue($name,$savedAppearance[$name],[Microsoft.Win32.RegistryValueKind]::DWord) }
        }
        $appearance.Close()
        Broadcast-AppearanceChange
    }
    $stage = 'maximizing and restoring the native window'
    Invoke-Button $automation '最大化或还原'
    $maximizeGeometry = @{}
    # The maximized state can be visible before Windows finishes positioning the
    # window. Wait for both state and bounds, using the same finite wait budget.
    Wait-Until {
        if (-not [TailSplitDesktopFixture]::IsZoomed($window)) { return $false }
        $maximized = New-Object TailSplitDesktopFixture+Rect
        if (-not [TailSplitDesktopFixture]::GetWindowRect($window,[ref]$maximized)) { throw 'Cannot inspect maximized window bounds.' }
        $monitor = New-Object TailSplitDesktopFixture+MonitorInfo
        $monitor.size = [Runtime.InteropServices.Marshal]::SizeOf([type]'TailSplitDesktopFixture+MonitorInfo')
        $monitorHandle = [TailSplitDesktopFixture]::MonitorFromWindow($window,2)
        if (-not [TailSplitDesktopFixture]::GetMonitorInfo($monitorHandle,[ref]$monitor)) { throw 'Cannot inspect monitor work area.' }
        $maximizeGeometry['window'] = $maximized
        $maximizeGeometry['work'] = $monitor.work
        # Windows may place the invisible resize frame just outside the work
        # area. Keep the existing 64px allowance and require full work-area coverage.
        return ($maximized.left -ge ($monitor.work.left - 64) -and $maximized.top -ge ($monitor.work.top - 64) -and
            $maximized.right -le ($monitor.work.right + 64) -and $maximized.bottom -le ($monitor.work.bottom + 64) -and
            $maximized.left -le $monitor.work.left -and $maximized.top -le $monitor.work.top -and
            $maximized.right -ge $monitor.work.right -and $maximized.bottom -ge $monitor.work.bottom)
    } 'Maximized window does not fit the monitor work area.' | Out-Null
    Invoke-Button $automation '最大化或还原'
    Wait-Until { -not [TailSplitDesktopFixture]::IsZoomed($window) } 'Window did not restore after maximizing.' | Out-Null
    $stage = 'desktop controls found; capturing window'
    [TailSplitDesktopFixture]::SetForegroundWindow($window) | Out-Null
    Start-Sleep -Milliseconds 500
    $bounds = New-Object TailSplitDesktopFixture+Rect
    if (-not [TailSplitDesktopFixture]::GetWindowRect($window,[ref]$bounds)) { throw 'Cannot inspect native window geometry.' }
    $bitmap = New-Object Drawing.Bitmap(($bounds.right-$bounds.left),($bounds.bottom-$bounds.top))
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        # Capture the verified TailSplit HWND itself. Another account's first
        # logon/privacy experience can obscure the shared CI desktop surface.
        $context = $graphics.GetHdc()
        try {
            if (-not [TailSplitDesktopFixture]::PrintWindow($window,$context,2)) { throw 'Cannot capture the actual TailSplit window.' }
        } finally { $graphics.ReleaseHdc($context) }
        $bitmap.Save($Screenshot,[Drawing.Imaging.ImageFormat]::Png)
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
    if ((Get-Item $Screenshot).Length -lt 10000) { throw 'Native screenshot is empty or unavailable.' }
    $stage = 'saving configuration from native window'
    if ($pageNavigation) {
        Invoke-Button $automation '设置'
        Invoke-Button $automation '网络'
    }
    $listen = Wait-Until {
        $field=Find-Control $automation '代理监听地址' ([System.Windows.Automation.ControlType]::Edit)
        if ($field -and $field.Current.IsEnabled) { $field }
    } 'The proxy listener setting is unavailable.'
    $listenPattern = $listen.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
    $originalListen = $listenPattern.Current.Value
    # Change only the local listener, leaving the direct-only fixture free of
    # Tailnet configuration or browser authorization in both release versions.
    $savedListen = Wait-Until {
        $probe = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback,0)
        try {
            $probe.Start()
            $candidate = '127.0.0.1:' + $probe.LocalEndpoint.Port
            if ($candidate -ne $originalListen) { $candidate }
        } finally { $probe.Stop() }
    } 'Cannot select a different local proxy listener.'
    $listen.SetFocus()
    $listenPattern.SetValue($savedListen)
    Invoke-Button $automation '保存并应用'
    Wait-Until {
        $button=Find-Button $automation '保存并应用'
        $button -and -not $button.Current.IsEnabled -and (Get-Content -LiteralPath $Config -Raw).Contains($savedListen)
    } 'Save & apply did not persist the listener and clear the draft.' | Out-Null
    if ($pageNavigation) { Invoke-Button $automation '连接页面' }
    $stage = 'connecting from native window'
    Invoke-Button $automation $connectName
    Wait-Connected $automation 'Connect did not reach a running service session.'
    if ($pageNavigation -and (Find-Button $automation '连接 TailSplit')) { throw 'The connect action remained visible while connected.' }
    Wait-Until { $button=Find-Button $automation '断开'; $button -and $button.Current.IsEnabled } 'The running session has no disconnect action.' | Out-Null
    $stage = 'reading and refreshing bounded native logs'
    $logToken = [Guid]::NewGuid().ToString('N')
    $oldLogMarker = 'TailSplit fixture old entry ' + $logToken
    $latestLogMarker = 'TailSplit fixture latest entry ' + $logToken
    Add-FixtureLog ($oldLogMarker + "`n" + ("TailSplit fixture log padding`n" * 8192) + $latestLogMarker + "`n")
    if ($pageNavigation) { Invoke-Button $automation '日志' }
    else { Invoke-Button $automation '查看日志' }
    Wait-Until { $button=Find-Button $automation '刷新日志'; $button -and $button.Current.IsEnabled } 'The native log view did not open.' | Out-Null
    $logText = Wait-Until {
        $text=Read-WindowText $automation
        if ($text.Contains($latestLogMarker)) { $text }
    } 'The native log view did not load the current log tail.'
    Assert-BoundedLogs $logText $oldLogMarker $latestLogMarker
    $refreshedLogMarker = 'TailSplit fixture refreshed entry ' + $logToken
    Add-FixtureLog ($refreshedLogMarker + "`n")
    Invoke-Button $automation '刷新日志'
    $logText = Wait-Until {
        $text=Read-WindowText $automation
        if ($text.Contains($refreshedLogMarker)) { $text }
    } 'Refresh logs did not display a newly appended entry.'
    Assert-BoundedLogs $logText $oldLogMarker $refreshedLogMarker
    if ($pageNavigation) { Invoke-Button $automation '连接页面' }
    else { Invoke-Button $automation '关闭' }
    $stage = 'disconnecting from native window'
    Invoke-Button $automation '断开'
    $disconnected = New-Object System.Windows.Automation.OrCondition(
        (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'未连接')),
        (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'Disconnected')))
    Wait-Until {
        $button=Find-Button $automation $connectName
        $button -and $button.Current.IsEnabled -and $automation.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$disconnected)
    } 'Disconnect did not finish.' | Out-Null
    if ($pageNavigation -and (Find-Button $automation '断开')) { throw 'The disconnect action remained visible after disconnecting.' }
    [TailSplitDesktopFixture]::PostMessage($window,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    Wait-Until { -not [TailSplitDesktopFixture]::IsWindowVisible($window) } 'Window close did not hide to tray.' | Out-Null
    if (-not (Get-Process -Id $guiProcess -ErrorAction SilentlyContinue)) { throw 'Closing the window terminated the tray app.' }
    $secondExit = [TailSplitDesktopFixture]::LaunchStandard($credential.username,$credential.password,$Application,'--no-connect',$true)
    if ($secondExit -ne 0) { throw 'Second launch failed.' }
    Wait-Until { [TailSplitDesktopFixture]::IsWindowVisible($window) } 'Second launch did not activate the existing window.' | Out-Null
    if ($KeepRunning) {
        # Users usually update while connected; Setup must end this session.
        $stage = 'reconnecting before the caller upgrades the running app'
        Invoke-Button $automation $connectName
        Wait-Connected $automation 'Reconnect before upgrade did not reach a running service session.'
    }
    @{ startup_ms=$startupMilliseconds; startup_phases=$nativePhases.ToArray(); token_source=[TailSplitDesktopFixture]::TokenSource; medium_integrity=$true; actual_native_window=$true; webview2_loaded=$true; controls=$true; page_navigation=$pageNavigation; system_appearance=$true; maximize_restore=$true; screenshot=$Screenshot; config_save=$true; logs=$true; bounded_logs=$true; log_refresh=$true; connect_disconnect=$true; close_to_tray=$true; single_instance=$true; process=$guiProcess; left_connected=[bool]$KeepRunning } | ConvertTo-Json -Depth 4 -Compress
} catch {
    $primaryFailure = $_
    $failure = @{ stage=$stage; error=$_.Exception.Message; process=$guiProcess; integrity=$integrity; token_source=[TailSplitDesktopFixture]::TokenSource; elapsed_ms=$startupClock.ElapsedMilliseconds; startup_phases=$nativePhases.ToArray() }
    if ($null -ne $maximizeGeometry) { $failure['maximize_geometry'] = $maximizeGeometry }
    try {
        $instance = Get-Process -Id $guiProcess -ErrorAction SilentlyContinue
        $failure['process_running'] = $null -ne $instance
        if ($instance) { $failure['session'] = $instance.SessionId; $failure['main_window_handle'] = $instance.MainWindowHandle.ToInt64() }
        if (Test-Path -LiteralPath $Log) {
            try {
                $stream = [IO.File]::Open($Log,[IO.FileMode]::Open,[IO.FileAccess]::Read,([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
                $reader = New-Object IO.StreamReader($stream,[Text.Encoding]::UTF8)
                try {
                    $text = $reader.ReadToEnd()
                    $failure['application_log'] = $text.Substring([Math]::Max(0,$text.Length-8192))
                } finally { $reader.Dispose(); $stream.Dispose() }
            } catch { $failure['log_inspection_error'] = $_.Exception.Message }
        }
        $hiddenWindow = [TailSplitDesktopFixture]::FindAppWindow($guiProcess,$Application,$windowClasses)
        $failure['native_window_handle'] = $hiddenWindow.ToInt64()
        if ($hiddenWindow -ne [IntPtr]::Zero) { $failure['native_startup_phase'] = [TailSplitDesktopFixture]::GetProp($hiddenWindow,'TailSplit.WebView2.StartupStage').ToInt64() }
        $failure['browser_processes'] = @(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Select-Object ProcessId,ParentProcessId,SessionId)
        $processCondition = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty,$guiProcess)
        $windows = [System.Windows.Automation.AutomationElement]::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children,$processCondition)
        $windowEvidence = @()
        foreach ($item in $windows) {
            $labels = @()
            $children = $item.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition)
            for ($index=0; $index -lt [Math]::Min($children.Count,60); $index++) { $labels += $children[$index].Current.Name }
            $windowEvidence += @{ name=$item.Current.Name; class=$item.Current.ClassName; labels=$labels }
        }
        $failure['windows'] = $windowEvidence
    } catch { $failure['inspection_error'] = $_.Exception.Message }
    $failure | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path (Split-Path -Parent $Screenshot) 'ui-failure.json') -Encoding UTF8
    throw
} finally {
    # A kept GUI is closed by the caller's upgrade or its own final cleanup.
    if (-not $KeepRunning -or $null -ne $primaryFailure) {
        try {
            $closing = Start-Process -FilePath $Application -ArgumentList '--quit' -Wait -PassThru
            if (Get-Process -Id $guiProcess -ErrorAction SilentlyContinue) { throw 'The native GUI did not exit cleanly.' }
            if ($closing.ExitCode -ne 0) { throw 'The GUI quit acknowledgement failed.' }
        } catch {
            if ($null -eq $primaryFailure) { throw }
            Write-Warning ('GUI cleanup after the original failure: ' + $_.Exception.Message)
            # This fixture owns only this newly launched PID in a disposable CI VM.
            Stop-Process -Id $guiProcess -Force -ErrorAction SilentlyContinue
        }
    }
}
