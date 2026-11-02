param(
    [Parameter(Mandatory=$true)][string]$Application,
    [Parameter(Mandatory=$true)][string]$Config,
    [Parameter(Mandatory=$true)][string]$Log,
    [Parameter(Mandatory=$true)][string]$Screenshot
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
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool OpenProcessToken(IntPtr process,uint access,out IntPtr token);
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool CreateRestrictedToken(IntPtr existing,uint flags,int n1,IntPtr a1,int n2,IntPtr a2,int n3,IntPtr a3,out IntPtr token);
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool DuplicateTokenEx(IntPtr source,uint access,IntPtr attributes,int level,int type,out IntPtr token);
 public static string TokenSource = "unknown";
 [DllImport("advapi32.dll",SetLastError=true,CharSet=CharSet.Unicode)] static extern bool ConvertStringSidToSid(string sid,out IntPtr result);
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool SetTokenInformation(IntPtr token,int type,ref Label label,int size);
 [DllImport("advapi32.dll",SetLastError=true,CharSet=CharSet.Unicode)] static extern bool CreateProcessAsUser(IntPtr token,string app,string command,IntPtr processAttributes,IntPtr threadAttributes,bool inherit,uint flags,IntPtr environment,string directory,ref Startup startup,out ProcessInfo result);
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
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
 [DllImport("user32.dll")] public static extern IntPtr GetProp(IntPtr window,string name);
 static void Check(bool value,string operation="Win32") { if(!value) { int code=Marshal.GetLastWin32Error(); throw new System.ComponentModel.Win32Exception(code,operation+": "+new System.ComponentModel.Win32Exception(code).Message); } }
 public static int LaunchMedium(string app,string arguments) {
  IntPtr original=IntPtr.Zero,restricted=IntPtr.Zero,sid=IntPtr.Zero,linked=IntPtr.Zero,buffer=IntPtr.Zero;
  try {
   Check(OpenProcessToken(GetCurrentProcess(),0x008B,out original),"OpenProcessToken");
   buffer=Marshal.AllocHGlobal(IntPtr.Size);int needed;
   if(GetTokenInformation(original,19,buffer,IntPtr.Size,out needed)) {
    linked=Marshal.ReadIntPtr(buffer);
    Check(DuplicateTokenEx(linked,0x008B,IntPtr.Zero,2,1,out restricted),"DuplicateTokenEx(linked desktop token)");
    TokenSource="UAC linked desktop token";
   } else {
    // Some runner images disable UAC splitting. Keep that fallback explicit
    // in evidence, since a synthetic token is not the normal desktop token.
    Check(CreateRestrictedToken(original,5,0,IntPtr.Zero,0,IntPtr.Zero,0,IntPtr.Zero,out restricted),"CreateRestrictedToken");
    Check(ConvertStringSidToSid("S-1-16-8192",out sid),"ConvertStringSidToSid");
    Label label=new Label{sid=sid,attributes=0x20};
    Check(SetTokenInformation(restricted,25,ref label,Marshal.SizeOf(typeof(Label))+GetLengthSid(sid)),"SetTokenInformation(medium integrity)");
    TokenSource="synthetic restricted token";
   }
   Startup startup=new Startup{cb=Marshal.SizeOf(typeof(Startup))}; ProcessInfo info;
   Check(CreateProcessAsUser(restricted,app,"\""+app+"\" "+arguments,IntPtr.Zero,IntPtr.Zero,false,0x400,IntPtr.Zero,System.IO.Path.GetDirectoryName(app),ref startup,out info),"CreateProcessAsUser");
   CloseHandle(info.thread);CloseHandle(info.process);return info.pid;
  } finally { if(buffer!=IntPtr.Zero)Marshal.FreeHGlobal(buffer);if(linked!=IntPtr.Zero)CloseHandle(linked);if(sid!=IntPtr.Zero)LocalFree(sid);if(restricted!=IntPtr.Zero)CloseHandle(restricted);if(original!=IntPtr.Zero)CloseHandle(original); }
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
function Invoke-Button([System.Windows.Automation.AutomationElement]$Root, [string]$Name) {
    $button = Wait-Until { $found=Find-Button $Root $Name; if ($found -and $found.Current.IsEnabled) {$found} } "Missing or disabled button: $Name"
    $pattern = $button.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
    $pattern.Invoke()
}

$accountSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$className = 'TailSplit.Window.' + $accountSID
$arguments = '--no-connect --config "' + $Config + '" --log "' + $Log + '"'
$guiProcess = [TailSplitDesktopFixture]::LaunchMedium($Application,$arguments)
$primaryFailure = $null
$stage = 'process launched'
try {
    $integrity = [TailSplitDesktopFixture]::Integrity($guiProcess)
    if ($integrity -ne 8192) { throw "Desktop was not medium integrity: $integrity" }
    $stage = 'medium integrity verified; waiting for native window'
    $window = Wait-Until { $handle=[TailSplitDesktopFixture]::FindWindow($className,'TailSplit'); if ($handle -ne [IntPtr]::Zero -and [TailSplitDesktopFixture]::IsWindowVisible($handle)) {$handle} } 'TailSplit did not create a visible native window.' 60
    $stage = 'native window visible; waiting for WebView2'
    Wait-Until { [TailSplitDesktopFixture]::GetProp($window,'TailSplit.WebView2.Ready') -ne [IntPtr]::Zero } 'WebView2 did not load its local document.' | Out-Null
    $automation = [System.Windows.Automation.AutomationElement]::FromHandle($window)
    Wait-Until { Find-Button $automation '连接' } 'The native window has no desktop controls.' | Out-Null
    foreach ($name in @('断开','登录 Tailnet…','退出设备登录','刷新出口','查看日志','保存并应用')) {
        if (-not (Find-Button $automation $name)) { throw "Missing actual window control: $name" }
    }
    [TailSplitDesktopFixture]::SetForegroundWindow($window) | Out-Null
    Start-Sleep -Milliseconds 500
    $bounds = New-Object TailSplitDesktopFixture+Rect
    if (-not [TailSplitDesktopFixture]::GetWindowRect($window,[ref]$bounds)) { throw 'Cannot inspect native window geometry.' }
    $bitmap = New-Object Drawing.Bitmap(($bounds.right-$bounds.left),($bounds.bottom-$bounds.top))
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $graphics.CopyFromScreen($bounds.left,$bounds.top,0,0,$bitmap.Size)
        $bitmap.Save($Screenshot,[Drawing.Imaging.ImageFormat]::Png)
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
    if ((Get-Item $Screenshot).Length -lt 10000) { throw 'Native screenshot is empty or unavailable.' }
    Invoke-Button $automation '查看日志'
    Wait-Until { Find-Button $automation '刷新日志' } 'The log dialog did not open.' | Out-Null
    Invoke-Button $automation '关闭'
    Invoke-Button $automation '连接'
    Wait-Until { $button=Find-Button $automation '断开'; $button -and $button.Current.IsEnabled } 'Connect did not reach an active service session.' | Out-Null
    Invoke-Button $automation '断开'
    Wait-Until { $button=Find-Button $automation '连接'; $button -and $button.Current.IsEnabled } 'Disconnect did not finish.' | Out-Null
    [TailSplitDesktopFixture]::PostMessage($window,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    Wait-Until { -not [TailSplitDesktopFixture]::IsWindowVisible($window) } 'Window close did not hide to tray.' | Out-Null
    if (-not (Get-Process -Id $guiProcess -ErrorAction SilentlyContinue)) { throw 'Closing the window terminated the tray app.' }
    $second = Start-Process -FilePath $Application -ArgumentList '--no-connect' -Wait -PassThru
    if ($second.ExitCode -ne 0) { throw 'Second launch failed.' }
    Wait-Until { [TailSplitDesktopFixture]::IsWindowVisible($window) } 'Second launch did not activate the existing window.' | Out-Null
    @{ token_source=[TailSplitDesktopFixture]::TokenSource; medium_integrity=$true; actual_native_window=$true; webview2_loaded=$true; controls=$true; screenshot=$Screenshot; logs=$true; connect_disconnect=$true; close_to_tray=$true; single_instance=$true } | ConvertTo-Json -Compress
} catch {
    $primaryFailure = $_
    $failure = @{ stage=$stage; error=$_.Exception.Message; process=$guiProcess; integrity=$integrity; token_source=[TailSplitDesktopFixture]::TokenSource }
    try {
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
