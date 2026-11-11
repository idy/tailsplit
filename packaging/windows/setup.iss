; Compiled only by build.py with the pinned official Inno Setup toolchain.
; This packages ARM64 applications; it does not claim an ARM64 installer engine.
#if VER < EncodeVer(7, 1, 1)
  #error Inno Setup 7.1.1 or newer is required
#endif
#ifndef AppVersion
  #error Build with packaging/windows/build.py
#endif
#ifndef RuntimeVersion
  #error The verified bundled Evergreen runtime version is required
#endif
#if TargetArch == "amd64"
  #define NativeArchitecture "x64os"
  #define NativeMachine 34404
#elif TargetArch == "arm64"
  #define NativeArchitecture "arm64"
  #define NativeMachine 43620
#else
  #error Unsupported native architecture
#endif

[Setup]
AppId={{8ADFCBB5-3CF0-4E72-B173-706395019F21}
AppName=TailSplit
AppVersion={#AppVersion}
AppVerName=TailSplit {#AppVersion}
AppPublisher=TailSplit contributors
VersionInfoVersion={#AppFileVersion}
VersionInfoDescription=TailSplit desktop installer
DefaultDirName={autopf}\TailSplit
UsePreviousAppDir=no
DisableDirPage=yes
AppendDefaultDirName=no
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed={#NativeArchitecture}
ArchitecturesInstallIn64BitMode={#NativeArchitecture}
MinVersion=10.0.17763
OutputDir={#OutputDir}
OutputBaseFilename={#OutputBaseName}
SetupIconFile={#IconFile}
UninstallDisplayIcon={app}\TailSplit.exe
LicenseFile={#StageDir}\LICENSE
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
SetupLogging=yes
CloseApplications=no
RestartApplications=no
RestartIfNeededByRun=no
AllowCancelDuringInstall=no
ChangesAssociations=no
ChangesEnvironment=no
Uninstallable=yes

[Dirs]
Name: "{app}"

[Files]
Source: "{#StageDir}\TailSplit.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\tailsplit-core.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\config.example.yaml"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\build.json"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RuntimeFile}"; DestName: "webview2-runtime.exe"; Flags: dontcopy nocompression

[Icons]
Name: "{commonprograms}\TailSplit"; Filename: "{app}\TailSplit.exe"; WorkingDir: "{app}"

[Run]
Filename: "{app}\TailSplit.exe"; Parameters: "{code:DesktopLaunchParameters}"; WorkingDir: "{app}"; Description: "Open TailSplit"; Flags: nowait postinstall skipifsilent runasoriginaluser; Check: MayLaunchGUI

; Service state, the registered desktop SID, and user config/device state are
; deliberately not [Registry] or [UninstallDelete] entries. The service helper
; owns that state; uninstall removes only program files and the SCM service.

[Code]
const
  ServiceName = 'TailSplitNetworkService';
  DesktopKey = 'SOFTWARE\TailSplit';
  DesktopSIDValue = 'DesktopUserSID';
  RuntimeKey = 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  FileAttributeReparsePoint = $400;
  InvalidFileAttributes = $FFFFFFFF;
  ServiceStopped = 1;
  ServiceStopPending = 3;
  ServiceRunning = 4;
  ServiceQueryStatus = $4;
  ServiceStart = $10;
  ServiceStop = $20;
  DeleteAccess = $10000;
  ErrorServiceDoesNotExist = 1060;
  ErrorServiceNotActive = 1062;
  ErrorServiceMarkedForDelete = 1072;
  PayloadCount = 10;
  DesktopReconnectExitCode = 10;

type
  TServiceStatus = record
    ServiceType: LongWord;
    CurrentState: LongWord;
    ControlsAccepted: LongWord;
    Win32ExitCode: LongWord;
    ServiceSpecificExitCode: LongWord;
    CheckPoint: LongWord;
    WaitHint: LongWord;
  end;

var
  PayloadNames: array[0..9] of String;
  PreviousFiles: array[0..9] of Boolean;
  PreviousService: Boolean;
  PreviousSIDPresent: Boolean;
  PreviousSID: String;
  RequestedSID: String;
  SnapshotActive: Boolean;
  InstallComplete: Boolean;
  ServiceInstallSucceeded: Boolean;
  ReconnectDesktop: Boolean;
  RecoveryInProgress: Boolean;
  OriginalFailure: String;
  FailureExitCode: Integer;

function WinGetFileAttributes(Name: String): LongWord;
  external 'GetFileAttributesW@kernel32.dll stdcall';
function OpenSCManager(MachineName, DatabaseName: NativeUInt; Access: LongWord): THandle;
  external 'OpenSCManagerW@advapi32.dll stdcall';
function OpenService(Manager: THandle; Name: String; Access: LongWord): THandle;
  external 'OpenServiceW@advapi32.dll stdcall';
function CloseServiceHandle(Handle: THandle): Boolean;
  external 'CloseServiceHandle@advapi32.dll stdcall';
function QueryServiceStatus(Handle: THandle; var Status: TServiceStatus): Boolean;
  external 'QueryServiceStatus@advapi32.dll stdcall';
function ControlService(Handle: THandle; Control: LongWord; var Status: TServiceStatus): Boolean;
  external 'ControlService@advapi32.dll stdcall';
function DeleteService(Handle: THandle): Boolean;
  external 'DeleteService@advapi32.dll stdcall';
function StartService(Handle: THandle; ArgumentCount: LongWord; Arguments: NativeUInt): Boolean;
  external 'StartServiceW@advapi32.dll stdcall';
function WinGetLastError: LongWord;
  external 'GetLastError@kernel32.dll stdcall';
function ConvertStringSIDToSID(Text: String; var SID: NativeUInt): Boolean;
  external 'ConvertStringSidToSidW@advapi32.dll stdcall';
function IsValidSID(SID: NativeUInt): Boolean;
  external 'IsValidSid@advapi32.dll stdcall';
function LocalFree(Memory: NativeUInt): NativeUInt;
  external 'LocalFree@kernel32.dll stdcall';

procedure InitPayloadNames;
begin
  PayloadNames[0] := 'TailSplit.exe';
  PayloadNames[1] := 'tailsplit-core.exe';
  { Recovery journal indices must stay compatible with pre-MyGo installers.
    The retired DLL is backed up/restored, but is no longer a shipped file. }
  PayloadNames[2] := 'TailSplit.Window.dll';
  PayloadNames[3] := 'config.example.yaml';
  PayloadNames[4] := 'LICENSE';
  PayloadNames[5] := 'THIRD_PARTY_NOTICES.md';
  PayloadNames[6] := 'build.json';
  { Preserve Inno's previous uninstaller as well as the working application. }
  PayloadNames[7] := 'unins000.exe';
  PayloadNames[8] := 'unins000.dat';
  PayloadNames[9] := 'unins000.msg';
end;

function MayLaunchGUI: Boolean;
begin
  Result := ServiceInstallSucceeded and (OriginalFailure = '');
end;

function DesktopLaunchParameters(Param: String): String;
begin
  if ReconnectDesktop then
    Result := '--start'
  else
    Result := '--no-connect';
end;

function FixedAppDir: String;
begin
  Result := ExpandConstant('{autopf}\TailSplit');
end;

function BackupDir: String;
begin
  Result := FixedAppDir + '\.install-rollback';
end;

function JournalPath: String;
begin
  Result := BackupDir + '\journal.ini';
end;

function CorePath: String;
begin
  Result := FixedAppDir + '\tailsplit-core.exe';
end;

function PSLiteral(Value: String): String;
begin
  StringChangeEx(Value, '''', '''''', True);
  Result := '''' + Value + '''';
end;

function RunPowerShell(Script: String; var ExitCode: Integer): Boolean;
var
  Parameters: String;
begin
  { PowerShell 7 callers may inject a module path incompatible with 5.1.
    Resolve verification/ACL cmdlets only from this system host's inbox modules. }
  Script := '$env:PSModulePath=$PSHOME+''\Modules''; ' + Script;
  { No script is read from a caller-provided location or from an archive. }
  Parameters := '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "& { ' + Script + ' }"';
  Result := Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
                 Parameters, ExpandConstant('{sys}'), SW_HIDE, ewWaitUntilTerminated, ExitCode);
end;

procedure RequireFixedProtectedDirectory;
var
  Path, Parent, Script: String;
  Attributes: LongWord;
  ExitCode: Integer;
begin
  if CompareText(RemoveBackslashUnlessRoot(ExpandConstant('{app}')), FixedAppDir) <> 0 then
    RaiseException('TailSplit must be installed in the fixed protected Program Files\TailSplit directory.');
  Path := FixedAppDir;
  while Length(Path) > 3 do begin
    Attributes := WinGetFileAttributes(Path);
    if (Attributes <> InvalidFileAttributes) and ((Attributes and FileAttributeReparsePoint) <> 0) then
      RaiseException('TailSplit cannot be installed through a junction, symbolic link, or other reparse point.');
    Parent := ExtractFileDir(Path);
    if CompareText(Parent, Path) = 0 then Break;
    Path := Parent;
  end;
  { Reject a pre-created user-writable app directory instead of relaxing ACLs.
    Ignore inherit-only ACEs; enforce real owners and write access on every
    existing child before trusting old executables or a recovery journal. }
  Script := '$ErrorActionPreference=''Stop''; ' +
    '$allowed=@(''S-1-5-18'',''S-1-5-32-544'',''S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464''); ' +
    '$q=New-Object ''System.Collections.Generic.Queue[string]''; ' +
    '$q.Enqueue(' + PSLiteral(ExpandConstant('{autopf}')) + '); ' +
    '$app=' + PSLiteral(FixedAppDir) + '; if(Test-Path -LiteralPath $app){$q.Enqueue($app)}; ' +
    'while($q.Count -gt 0){$p=$q.Dequeue(); $i=Get-Item -LiteralPath $p -Force; ' +
    'if(($i.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw ''Reparse point in program directory''}; ' +
    '$a=Get-Acl -LiteralPath $p; $owner=$a.GetOwner([Security.Principal.SecurityIdentifier]).Value; ' +
    'if($allowed -notcontains $owner){throw ''Unprotected program directory owner''}; ' +
    'foreach($r in $a.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])){' +
    'if($r.AccessControlType -eq ''Allow'' -and ($r.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0 ' +
    '-and ([int]$r.FileSystemRights -band 0xD0156) -ne 0 -and $allowed -notcontains $r.IdentityReference.Value){throw ''User-writable program directory''}}; ' +
    'if($p -ne ' + PSLiteral(ExpandConstant('{autopf}')) + ' -and $i.PSIsContainer){' +
    'foreach($c in Get-ChildItem -LiteralPath $p -Force){$q.Enqueue($c.FullName)}}}; exit 0';
  if not RunPowerShell(Script, ExitCode) or (ExitCode <> 0) then
    RaiseException('TailSplit requires an administrator-owned program directory with no unprivileged write access.');
end;

procedure CreateProtectedAppDirectory;
var
  Script: String;
  ExitCode: Integer;
begin
  if not ForceDirectories(FixedAppDir) then
    RaiseException('Could not create the protected TailSplit program directory.');
  { Explicit owner and DACL, including for alternate-admin UAC. Users may read
    and execute; only SYSTEM and Administrators can modify program files. }
  Script := '$ErrorActionPreference=''Stop''; $p=' + PSLiteral(FixedAppDir) + '; ' +
    '$a=New-Object Security.AccessControl.DirectorySecurity; ' +
    '$a.SetSecurityDescriptorSddlForm(''O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)''); ' +
    'Set-Acl -LiteralPath $p -AclObject $a; exit 0';
  if not RunPowerShell(Script, ExitCode) or (ExitCode <> 0) then
    RaiseException('Could not protect the TailSplit program directory.');
end;

procedure ProtectProgramOwners;
var
  Script: String;
  ExitCode: Integer;
begin
  { A creating administrator's unelevated token must not acquire owner rights
    over executables. Set protected ownership before any SYSTEM helper runs. }
  Script := '$ErrorActionPreference=''Stop''; $owner=New-Object Security.Principal.SecurityIdentifier(''S-1-5-32-544''); ' +
    '$q=New-Object ''System.Collections.Generic.Queue[string]''; $q.Enqueue(' + PSLiteral(FixedAppDir) + '); ' +
    'while($q.Count -gt 0){$p=$q.Dequeue(); $i=Get-Item -LiteralPath $p -Force; ' +
    'if(($i.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw ''Reparse point in program directory''}; ' +
    '$a=Get-Acl -LiteralPath $p; $a.SetOwner($owner); Set-Acl -LiteralPath $p -AclObject $a; ' +
    'if($i.PSIsContainer){foreach($c in Get-ChildItem -LiteralPath $p -Force){$q.Enqueue($c.FullName)}}}; exit 0';
  if not RunPowerShell(Script, ExitCode) or (ExitCode <> 0) then
    RaiseException('Could not assign protected ownership to TailSplit program files.');
end;

function ValidDesktopSID(Text: String): Boolean;
var
  Index: Integer;
  SID: NativeUInt;
begin
  Result := False;
  if (Length(Text) < 5) or (Copy(Text, 1, 4) <> 'S-1-') then Exit;
  for Index := 3 to Length(Text) do
    if (Text[Index] <> '-') and ((Text[Index] < '0') or (Text[Index] > '9')) then Exit;
  SID := 0;
  if ConvertStringSIDToSID(Text, SID) then begin
    Result := IsValidSID(SID);
    LocalFree(SID);
  end;
end;

procedure CheckRequestedSID;
var
  Existing: String;
begin
  RequestedSID := ExpandConstant('{param:UserSID|}');
  if (RequestedSID <> '') and not ValidDesktopSID(RequestedSID) then
    RaiseException('/UserSID must contain a valid Windows user SID.');
  if RegQueryStringValue(HKLM64, DesktopKey, DesktopSIDValue, Existing) and
     (Existing <> '') and (RequestedSID <> '') and (CompareText(Existing, RequestedSID) <> 0) then
    RaiseException('The registered TailSplit desktop user cannot be changed by an upgrade.');
end;

function VersionAtLeast(Actual, Required: String): Boolean;
var
  Index, DotAt, A, B, Comparison: Integer;
  PartA, PartB: String;
begin
  Result := False;
  Comparison := 0;
  for Index := 1 to 4 do begin
    DotAt := Pos('.', Actual);
    if DotAt = 0 then begin PartA := Actual; Actual := ''; end
    else begin PartA := Copy(Actual, 1, DotAt - 1); Delete(Actual, 1, DotAt); end;
    DotAt := Pos('.', Required);
    if DotAt = 0 then begin PartB := Required; Required := ''; end
    else begin PartB := Copy(Required, 1, DotAt - 1); Delete(Required, 1, DotAt); end;
    A := StrToIntDef(PartA, -1);
    B := StrToIntDef(PartB, -1);
    if (A < 0) or (B < 0) then Exit;
    if Comparison = 0 then begin
      if A > B then Comparison := 1;
      if A < B then Comparison := -1;
    end;
  end;
  Result := (Actual = '') and (Required = '') and (Comparison >= 0);
end;

function NativeRuntimePE(FileName: String): Boolean;
var
  Data: AnsiString;
  Offset, Machine: Integer;
begin
  Result := False;
  if not LoadStringFromFile(FileName, Data) or (Length(Data) < 64) then Exit;
  if (Data[1] <> 'M') or (Data[2] <> 'Z') then Exit;
  Offset := Ord(Data[61]) + Ord(Data[62]) * 256 + Ord(Data[63]) * 65536 + Ord(Data[64]) * 16777216;
  if (Offset < 64) or (Offset > Length(Data) - 6) then Exit;
  if (Data[Offset + 1] <> 'P') or (Data[Offset + 2] <> 'E') or
     (Ord(Data[Offset + 3]) <> 0) or (Ord(Data[Offset + 4]) <> 0) then Exit;
  Machine := Ord(Data[Offset + 5]) + Ord(Data[Offset + 6]) * 256;
  Result := Machine = {#NativeMachine};
#if TargetArch == "arm64"
  { ARM64EC / ARM64X browser processes are also native Arm runtime payloads. }
  Result := Result or (Machine = $A641) or (Machine = $A64E);
#endif
end;

function RuntimeVersionAt(Root, Version: String): Boolean;
var
  Executable, FileVersion: String;
begin
  Executable := Root + '\Microsoft\EdgeWebView\Application\' + Version + '\msedgewebview2.exe';
  Result := NativeRuntimePE(Executable) and GetVersionNumbersString(Executable, FileVersion);
  if Result then Result := VersionAtLeast(FileVersion, '{#RuntimeVersion}');
end;

function RuntimeRegisteredAt(RootKey: Integer): Boolean;
var
  Version: String;
begin
  Result := False;
  if not RegQueryStringValue(RootKey, RuntimeKey, 'pv', Version) then Exit;
  if not VersionAtLeast(Version, '{#RuntimeVersion}') then Exit;
  Result := RuntimeVersionAt(ExpandConstant('{commonpf64}'), Version) or
            RuntimeVersionAt(ExpandConstant('{commonpf32}'), Version);
end;

function HasMachineRuntime: Boolean;
begin
  { Per-user HKCU belongs to the elevated account after alternate-admin UAC.
    Require the machine runtime so the actual desktop user is always covered. }
  Result := RuntimeRegisteredAt(HKLM32) or RuntimeRegisteredAt(HKLM64);
end;

procedure EnsureWebViewRuntime;
var
  Runtime, Script: String;
  ExitCode, Attempt: Integer;
begin
  if HasMachineRuntime then Exit;
  ExtractTemporaryFile('webview2-runtime.exe');
  Runtime := ExpandConstant('{tmp}\webview2-runtime.exe');
  if CompareText(GetSHA256OfFile(Runtime), '{#RuntimeSHA256}') <> 0 then
    RaiseException('The bundled WebView2 runtime failed its SHA-256 verification.');
  { Hold the verified executable open without write/delete sharing until its
    installer finishes. Authenticode is checked again on the target machine. }
  Script := '$ErrorActionPreference=''Stop''; $p=' + PSLiteral(Runtime) + '; ' +
    '$f=[IO.File]::Open($p,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read); try{' +
    '$h=(Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash; ' +
    'if($h -ne ' + PSLiteral('{#RuntimeSHA256}') + '){throw ''Runtime hash mismatch''}; ' +
    '$s=Get-AuthenticodeSignature -LiteralPath $p; ' +
    'if($s.Status -ne ''Valid'' -or !$s.SignerCertificate){throw ''Invalid WebView2 Authenticode signature''}; ' +
    '$n=$s.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName,$false); ' +
    'if($n -cne ''Microsoft Corporation''){throw ''Unexpected WebView2 publisher''}; ' +
    '$child=Start-Process -FilePath $p -ArgumentList ''/silent'',''/install'' -Wait -PassThru; ' +
    'if($child.ExitCode -ne 0 -and $child.ExitCode -ne 3010){exit $child.ExitCode}; ' +
    '}finally{$f.Dispose()}; exit 0';
  if not RunPowerShell(Script, ExitCode) or (ExitCode <> 0) then
    RaiseException('The Microsoft WebView2 runtime prerequisite failed (exit ' + IntToStr(ExitCode) + '). No TailSplit files were replaced.');
  for Attempt := 1 to 100 do begin
    if HasMachineRuntime then Exit;
    Sleep(200);
  end;
  RaiseException('A matching machine-wide WebView2 runtime is still unavailable after prerequisite installation.');
end;

procedure QuitDesktopGUI;
var
  ExitCode: Integer;
  GUI, QuitParameters: String;
  RestoreConnection: Boolean;
begin
  ReconnectDesktop := False;
  GUI := FixedAppDir + '\TailSplit.exe';
  if not FileExists(GUI) then Exit;
  { Both quit commands are control-only. The installed GUI resolves the
    registered desktop SID from protected HKLM, authenticates its pipe/server
    image and awaits process exit. In-app updates report traffic intent only
    after successful cleanup; ordinary installs/uninstalls remain disconnected. }
  RestoreConnection := (not IsUninstaller) and (ExpandConstant('{param:RestoreConnection|0}') = '1');
  QuitParameters := '--quit';
  if RestoreConnection then QuitParameters := '--quit-for-update';
  if not Exec(GUI, QuitParameters, FixedAppDir, SW_HIDE, ewWaitUntilTerminated, ExitCode) then
    RaiseException('TailSplit could not quit the registered desktop GUI.');
  if RestoreConnection and (ExitCode = DesktopReconnectExitCode) then
    ReconnectDesktop := True
  else if ExitCode <> 0 then
    RaiseException('TailSplit could not disconnect and quit the registered desktop GUI (exit ' + IntToStr(ExitCode) + ').');
end;

procedure RemoveObsoleteWindowDLL;
var
  LegacyDLL: String;
begin
  { Called only after the GUI and service stop and a protected snapshot exists.
    A failed install restores this file together with the previous desktop. }
  LegacyDLL := FixedAppDir + '\TailSplit.Window.dll';
  if FileExists(LegacyDLL) then
    if not DeleteFile(LegacyDLL) then
      RaiseException('Could not remove the obsolete TailSplit.Window.dll.');
end;

function ServiceHandle(Access: LongWord): THandle;
var
  Manager: THandle;
  Error: LongWord;
begin
  Manager := OpenSCManager(0, 0, 1);
  if Manager = 0 then RaiseException('Could not open the Windows Service Control Manager.');
  Result := OpenService(Manager, ServiceName, Access);
  Error := WinGetLastError;
  CloseServiceHandle(Manager);
  if (Result = 0) and (Error <> ErrorServiceDoesNotExist) then
    RaiseException('Could not open TailSplitNetworkService: ' + SysErrorMessage(Error));
end;

procedure StopFixedService;
var
  Handle: THandle;
  Status: TServiceStatus;
  Attempt: Integer;
  Error: LongWord;
begin
  Handle := ServiceHandle(ServiceQueryStatus or ServiceStop);
  if Handle = 0 then Exit;
  try
    if not QueryServiceStatus(Handle, Status) then RaiseException('Could not query TailSplitNetworkService.');
    if Status.CurrentState = ServiceStopped then Exit;
    if Status.CurrentState <> ServiceStopPending then
      if not ControlService(Handle, 1, Status) then begin
        Error := WinGetLastError;
        if Error <> ErrorServiceNotActive then
          RaiseException('Could not stop TailSplitNetworkService: ' + SysErrorMessage(Error));
      end;
    for Attempt := 1 to 300 do begin
      if not QueryServiceStatus(Handle, Status) then RaiseException('Could not wait for TailSplitNetworkService.');
      if Status.CurrentState = ServiceStopped then Exit;
      Sleep(100);
    end;
    RaiseException('TailSplitNetworkService did not stop within 30 seconds.');
  finally
    CloseServiceHandle(Handle);
  end;
end;

procedure RemoveFixedService;
var
  Handle: THandle;
  Error: LongWord;
begin
  StopFixedService;
  Handle := ServiceHandle(DeleteAccess);
  if Handle = 0 then Exit;
  try
    if not DeleteService(Handle) then begin
      Error := WinGetLastError;
      if Error <> ErrorServiceMarkedForDelete then
        RaiseException('Could not remove TailSplitNetworkService: ' + SysErrorMessage(Error));
    end;
  finally
    CloseServiceHandle(Handle);
  end;
end;

procedure StartPreviousService;
var
  Handle: THandle;
  Status: TServiceStatus;
begin
  Handle := ServiceHandle(ServiceQueryStatus or ServiceStart);
  if Handle = 0 then RaiseException('The previous TailSplit service registration is missing.');
  try
    if not QueryServiceStatus(Handle, Status) then RaiseException('Could not inspect the previous TailSplit service.');
    if Status.CurrentState <> ServiceRunning then
      if not StartService(Handle, 0, 0) then
        RaiseException('Could not restart the previous TailSplit service.');
  finally
    CloseServiceHandle(Handle);
  end;
end;

procedure RunCore(Command: String);
var
  ExitCode: Integer;
begin
  if not Exec(CorePath, Command, FixedAppDir, SW_HIDE, ewWaitUntilTerminated, ExitCode) then
    RaiseException('Could not run the TailSplit service helper: ' + SysErrorMessage(ExitCode));
  if ExitCode <> 0 then begin
    if FailureExitCode = 0 then FailureExitCode := ExitCode;
    RaiseException('TailSplit service helper failed: ' + Command + ' (exit ' + IntToStr(ExitCode) + ').');
  end;
end;

procedure WriteJournal(Section, Key, Value: String);
begin
  if not SetIniString(Section, Key, Value, JournalPath) then
    RaiseException('Could not write the protected TailSplit recovery journal.');
end;

procedure CreateSnapshot;
var
  Index: Integer;
begin
  if DirExists(BackupDir) then
    RaiseException('A previous protected TailSplit recovery snapshot must be recovered first.');
  if not ForceDirectories(BackupDir) then RaiseException('Could not create the protected TailSplit backup directory.');
  try
    PreviousService := RegKeyExists(HKLM64, 'SYSTEM\CurrentControlSet\Services\' + ServiceName);
    PreviousSID := '';
    PreviousSIDPresent := RegQueryStringValue(HKLM64, DesktopKey, DesktopSIDValue, PreviousSID);
    if PreviousService and not FileExists(CorePath) then
      RaiseException('The registered TailSplit service is missing its previous core executable.');
    WriteJournal('Transaction', 'HadService', IntToStr(Ord(PreviousService)));
    WriteJournal('Transaction', 'HadSID', IntToStr(Ord(PreviousSIDPresent)));
    WriteJournal('Transaction', 'SID', PreviousSID);
    for Index := 0 to PayloadCount - 1 do begin
      PreviousFiles[Index] := FileExists(FixedAppDir + '\' + PayloadNames[Index]);
      if PreviousFiles[Index] then
        if not FileCopy(FixedAppDir + '\' + PayloadNames[Index], BackupDir + '\' + PayloadNames[Index], True) then
          RaiseException('Could not back up the previous ' + PayloadNames[Index] + '.');
      WriteJournal('Files', IntToStr(Index), IntToStr(Ord(PreviousFiles[Index])));
    end;
    WriteJournal('Transaction', 'Ready', '1');
    ProtectProgramOwners;
    SnapshotActive := True;
  except
    DelTree(BackupDir, True, True, True);
    RaiseException(GetExceptionMessage);
  end;
end;

procedure LoadSnapshot;
var
  Index: Integer;
begin
  if GetIniString('Transaction', 'Ready', '', JournalPath) <> '1' then
    RaiseException('An incomplete TailSplit recovery snapshot needs administrator inspection: ' + BackupDir);
  PreviousService := GetIniInt('Transaction', 'HadService', 0, 0, 1, JournalPath) = 1;
  PreviousSIDPresent := GetIniInt('Transaction', 'HadSID', 0, 0, 1, JournalPath) = 1;
  PreviousSID := GetIniString('Transaction', 'SID', '', JournalPath);
  if PreviousSIDPresent and not ValidDesktopSID(PreviousSID) then
    RaiseException('The protected TailSplit recovery journal contains an invalid desktop SID.');
  for Index := 0 to PayloadCount - 1 do begin
    PreviousFiles[Index] := GetIniInt('Files', IntToStr(Index), 0, 0, 1, JournalPath) = 1;
    if PreviousFiles[Index] and not FileExists(BackupDir + '\' + PayloadNames[Index]) then
      RaiseException('The protected TailSplit recovery snapshot is missing ' + PayloadNames[Index] + '.');
  end;
  SnapshotActive := True;
end;

procedure RecoverPrevious;
var
  Index: Integer;
  RecoveryFailure, Command: String;
  SavedExitCode: Integer;
begin
  if not SnapshotActive or RecoveryInProgress then Exit;
  RecoveryInProgress := True;
  SavedExitCode := FailureExitCode;
  RecoveryFailure := '';
  Log('Recovering the previous TailSplit program files and idle SCM service.');
  try
    { Do not rely on a partially installed core for stopping/removing a new
      service. These SCM operations address only our fixed service name. }
    StopFixedService;
    if not PreviousService then RemoveFixedService;
    for Index := 0 to PayloadCount - 1 do begin
      if PreviousFiles[Index] then begin
        if not FileCopy(BackupDir + '\' + PayloadNames[Index], FixedAppDir + '\' + PayloadNames[Index], False) then
          RaiseException('Could not restore ' + PayloadNames[Index] + '.');
      end else if FileExists(FixedAppDir + '\' + PayloadNames[Index]) then
        if not DeleteFile(FixedAppDir + '\' + PayloadNames[Index]) then
          RaiseException('Could not remove the failed new ' + PayloadNames[Index] + '.');
    end;
    if PreviousSIDPresent then begin
      if not RegWriteStringValue(HKLM64, DesktopKey, DesktopSIDValue, PreviousSID) then
        RaiseException('Could not restore the previous TailSplit desktop SID.');
    end else begin
      if RegValueExists(HKLM64, DesktopKey, DesktopSIDValue) then
        if not RegDeleteValue(HKLM64, DesktopKey, DesktopSIDValue) then
          RaiseException('Could not remove the failed new TailSplit desktop SID registration.');
    end;
    if PreviousService then begin
      Command := 'windows-install-service';
      if PreviousSIDPresent then Command := Command + ' --user-sid ' + PreviousSID;
      try
        RunCore(Command);
      except
        { The restored previous service can still be started if its old helper
          reports an unrelated idempotent registration error. }
        Log('Previous helper reported: ' + GetExceptionMessage);
        StartPreviousService;
      end;
    end;
    SnapshotActive := False;
    DelTree(BackupDir, True, True, True);
    Log('Previous TailSplit binaries and service were restored.');
  except
    RecoveryFailure := GetExceptionMessage;
  end;
  FailureExitCode := SavedExitCode;
  RecoveryInProgress := False;
  if RecoveryFailure <> '' then begin
    Log('TailSplit recovery failed: ' + RecoveryFailure);
    { Keep the complete protected backup for a retry; never replace the
      original installation error with a secondary cleanup error. }
    if not WizardSilent then
      SuppressibleMsgBox('TailSplit recovery needs administrator attention. The backup is preserved at ' +
                        BackupDir + #13#10 + RecoveryFailure, mbError, MB_OK, IDOK);
    RaiseException('Previous TailSplit recovery is incomplete: ' + RecoveryFailure);
  end;
end;

function InitializeSetup: Boolean;
begin
  InitPayloadNames;
  Result := True;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  OriginalFailure := '';
  FailureExitCode := 0;
  ServiceInstallSucceeded := False;
  InstallComplete := False;
  try
    RequireFixedProtectedDirectory;
    if DirExists(BackupDir) then begin
      LoadSnapshot;
      RecoverPrevious;
    end;
    CheckRequestedSID;
    { Install and verify prerequisites before disturbing a working old app. }
    EnsureWebViewRuntime;
    QuitDesktopGUI;
    CreateProtectedAppDirectory;
    CreateSnapshot;
    if PreviousService then RunCore('windows-stop-service');
    StopFixedService;
    RemoveObsoleteWindowDLL;
  except
    OriginalFailure := GetExceptionMessage;
    if FailureExitCode = 0 then FailureExitCode := 1;
    Result := OriginalFailure;
    if SnapshotActive then begin
      try
        RecoverPrevious;
      except
        Log('Recovery after prepare failure: ' + GetExceptionMessage);
      end;
    end;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Command: String;
begin
  if CurStep = ssPostInstall then begin
    try
      ProtectProgramOwners;
      RequireFixedProtectedDirectory;
      Command := 'windows-install-service';
      if RequestedSID <> '' then Command := Command + ' --user-sid ' + RequestedSID;
      RunCore(Command);
      ServiceInstallSucceeded := True;
      { The helper registers a local SYSTEM SCM service and starts it idle.
        No connect command, auto-connect preference, or caller path is used. }
    except
      OriginalFailure := GetExceptionMessage;
      if FailureExitCode = 0 then FailureExitCode := 1;
      Log('Original TailSplit installation failure: ' + OriginalFailure);
      { DeinitializeSetup runs after Inno's own file rollback/termination path.
        Recovery there explicitly restores our previous payload and service. }
      RaiseException(OriginalFailure);
    end;
  end;
  if CurStep = ssDone then begin
    { Some post-install errors are reported without aborting by the engine.
      Never convert a service failure into a successful silent install. }
    if (OriginalFailure = '') and ServiceInstallSucceeded then begin
      InstallComplete := True;
      SnapshotActive := False;
      DelTree(BackupDir, True, True, True);
    end;
  end;
end;

function GetCustomSetupExitCode: Integer;
begin
  Result := FailureExitCode;
  if (OriginalFailure <> '') and (Result = 0) then Result := 1;
end;

procedure DeinitializeSetup;
var
  SavedOriginal: String;
begin
  if SnapshotActive and not InstallComplete then begin
    SavedOriginal := OriginalFailure;
    try
      RecoverPrevious;
    except
      Log('Recovery during setup termination: ' + GetExceptionMessage);
    end;
    OriginalFailure := SavedOriginal;
  end;
end;

function InitializeUninstall: Boolean;
begin
  InitPayloadNames;
  RequireFixedProtectedDirectory;
  Result := True;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then begin
    QuitDesktopGUI;
    RunCore('windows-stop-service');
    StopFixedService;
    RunCore('windows-remove-service');
    RemoveFixedService;
    { HKLM\SOFTWARE\TailSplit\DesktopUserSID, ProgramData\TailSplit, and all
      per-user config/device state remain intact by default. }
  end;
end;
