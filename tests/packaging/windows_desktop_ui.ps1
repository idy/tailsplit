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
public static class TailSplitDesktopFixture {
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct Startup { public int cb; public string reserved,desktop,title; public int x,y,xsize,ysize,xchars,ychars,fill,flags; public short show,reserved2; public IntPtr reservedptr,input,output,error; }
 [StructLayout(LayoutKind.Sequential)] public struct ProcessInfo { public IntPtr process,thread; public int pid,tid; }
 [StructLayout(LayoutKind.Sequential)] public struct Label { public IntPtr sid; public int attributes; }
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int left,top,right,bottom; }
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
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string klass,string title);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window,uint message,IntPtr wparam,IntPtr lparam);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out Rect result);
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
function Find-Button([System.Windows.Automation.AutomationElement]$Root, [string]$Name) {
    $condition = New-Object System.Windows.Automation.AndCondition(
        (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Button)),
        (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$Name)))
    return $Root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
}
function Wait-Connected([System.Windows.Automation.AutomationElement]$Root, [string]$Failure) {
    # Disconnect is enabled as soon as a session starts; only the page's
    # running state proves the SYSTEM service accepted this user's session.
    $condition = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'已连接')
    Wait-Until { $Root.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition) } $Failure 60 | Out-Null
}
function Invoke-Button([System.Windows.Automation.AutomationElement]$Root, [string]$Name) {
    $button = Wait-Until { $found=Find-Button $Root $Name; if ($found -and $found.Current.IsEnabled) {$found} } "Missing or disabled button: $Name"
    $pattern = $button.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
    $pattern.Invoke()
}

$credential = Get-Content -LiteralPath $Credentials -Raw | ConvertFrom-Json
$accountSID = $credential.sid
$className = 'TailSplit.Window.' + $accountSID
$arguments = '--no-connect --config "' + $Config + '" --log "' + $Log + '"'
$guiProcess = [TailSplitDesktopFixture]::LaunchStandard($credential.username,$credential.password,$Application,$arguments,$false)
$primaryFailure = $null
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
        $handle=[TailSplitDesktopFixture]::FindWindow($className,'TailSplit')
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
    Wait-Until { Find-Button $automation '连接' } 'The native window has no desktop controls.' | Out-Null
    foreach ($name in @('断开','登录 Tailnet…','退出设备登录','刷新出口','查看日志','保存并应用','最大化或还原')) {
        if (-not (Find-Button $automation $name)) { throw "Missing actual window control: $name" }
    }
    $stage = 'maximizing and restoring the native window'
    Invoke-Button $automation '最大化或还原'
    Wait-Until { [TailSplitDesktopFixture]::IsZoomed($window) } 'Window did not maximize.' | Out-Null
    $maximized = New-Object TailSplitDesktopFixture+Rect
    if (-not [TailSplitDesktopFixture]::GetWindowRect($window,[ref]$maximized)) { throw 'Cannot inspect maximized window bounds.' }
    $monitor = New-Object TailSplitDesktopFixture+MonitorInfo
    $monitor.size = [Runtime.InteropServices.Marshal]::SizeOf([type]'TailSplitDesktopFixture+MonitorInfo')
    $monitorHandle = [TailSplitDesktopFixture]::MonitorFromWindow($window,2)
    if (-not [TailSplitDesktopFixture]::GetMonitorInfo($monitorHandle,[ref]$monitor)) { throw 'Cannot inspect monitor work area.' }
    # Windows may place the invisible resize frame just outside the work area.
    # A loose 64px allowance permits that frame but rejects a whole extra display.
    if ($maximized.left -lt ($monitor.work.left - 64) -or $maximized.top -lt ($monitor.work.top - 64) -or
        $maximized.right -gt ($monitor.work.right + 64) -or $maximized.bottom -gt ($monitor.work.bottom + 64) -or
        $maximized.left -gt $monitor.work.left -or $maximized.top -gt $monitor.work.top -or
        $maximized.right -lt $monitor.work.right -or $maximized.bottom -lt $monitor.work.bottom) {
        throw 'Maximized window does not fit the monitor work area.'
    }
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
    Invoke-Button $automation '查看日志'
    Wait-Until { Find-Button $automation '刷新日志' } 'The log dialog did not open.' | Out-Null
    Invoke-Button $automation '关闭'
    $stage = 'connecting from native window'
    Invoke-Button $automation '连接'
    Wait-Connected $automation 'Connect did not reach a running service session.'
    $stage = 'disconnecting from native window'
    Invoke-Button $automation '断开'
    Wait-Until { $button=Find-Button $automation '连接'; $button -and $button.Current.IsEnabled } 'Disconnect did not finish.' | Out-Null
    [TailSplitDesktopFixture]::PostMessage($window,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    Wait-Until { -not [TailSplitDesktopFixture]::IsWindowVisible($window) } 'Window close did not hide to tray.' | Out-Null
    if (-not (Get-Process -Id $guiProcess -ErrorAction SilentlyContinue)) { throw 'Closing the window terminated the tray app.' }
    $secondExit = [TailSplitDesktopFixture]::LaunchStandard($credential.username,$credential.password,$Application,'--no-connect',$true)
    if ($secondExit -ne 0) { throw 'Second launch failed.' }
    Wait-Until { [TailSplitDesktopFixture]::IsWindowVisible($window) } 'Second launch did not activate the existing window.' | Out-Null
    if ($KeepRunning) {
        # Users usually update while connected; Setup must end this session.
        $stage = 'reconnecting before the caller upgrades the running app'
        Invoke-Button $automation '连接'
        Wait-Connected $automation 'Reconnect before upgrade did not reach a running service session.'
    }
    @{ startup_ms=$startupMilliseconds; startup_phases=$nativePhases.ToArray(); token_source=[TailSplitDesktopFixture]::TokenSource; medium_integrity=$true; actual_native_window=$true; webview2_loaded=$true; controls=$true; maximize_restore=$true; screenshot=$Screenshot; logs=$true; connect_disconnect=$true; close_to_tray=$true; single_instance=$true; process=$guiProcess; left_connected=[bool]$KeepRunning } | ConvertTo-Json -Depth 4 -Compress
} catch {
    $primaryFailure = $_
    $failure = @{ stage=$stage; error=$_.Exception.Message; process=$guiProcess; integrity=$integrity; token_source=[TailSplitDesktopFixture]::TokenSource; elapsed_ms=$startupClock.ElapsedMilliseconds; startup_phases=$nativePhases.ToArray() }
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
        $hiddenWindow = [TailSplitDesktopFixture]::FindWindow($className,'TailSplit')
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
