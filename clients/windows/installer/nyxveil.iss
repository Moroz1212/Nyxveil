; Nyxveil Windows Client 1.0.0 — Inno Setup (fail-closed)
; Output: Nyxveil-Setup-v1.0.0.exe
; Authenticode: NOT SIGNED (expected SmartScreen warning)

#define MyAppName "Nyxveil"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "Nyxveil"
#define MyAppExeName "Nyxveil.exe"
#define ServiceExeName "Nyxveil.Service.exe"
#define ServiceName "NyxveilClientService"

[Setup]
AppId={{A7C2E91F-4B6D-4E2A-9F1C-8D3B5A0E7C21}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={autopf}\Nyxveil\Client
DefaultGroupName=Nyxveil
DisableProgramGroupPage=yes
OutputDir=..\dist
OutputBaseFilename=Nyxveil-Setup-v{#MyAppVersion}
Compression=lzma2
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern
UninstallDisplayIcon={app}\{#MyAppExeName}
CloseApplications=force
RestartApplications=no

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "..\dist\payload\gui\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\dist\payload\{#ServiceExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\payload\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\third_party\wintun\LICENSE.txt"; DestDir: "{app}\licenses"; DestName: "WINTUN-LICENSE.txt"; Flags: ignoreversion
Source: "..\third_party\WINTUN.md"; DestDir: "{app}\licenses"; Flags: ignoreversion
Source: "..\VERSION"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\docs\*"; DestDir: "{app}\docs"; Flags: ignoreversion recursesubdirs skipifsourcedoesntexist

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
; Post-install GUI must run as ORIGINAL interactive user (not elevated Setup token).
Filename: "{app}\{#MyAppExeName}"; Description: "Запустить Nyxveil"; Flags: nowait postinstall skipifsilent runasoriginaluser

[Code]
var
  GUninstallCleanupOK: Boolean;

function WaitServiceState(const Name, WantToken: String; TimeoutMs: Integer): Boolean;
var
  ResultCode: Integer;
  Elapsed: Integer;
  OutFile: String;
  Lines: TArrayOfString;
  I: Integer;
begin
  Result := False;
  OutFile := ExpandConstant('{tmp}\nv-svc-query.txt');
  Elapsed := 0;
  while Elapsed < TimeoutMs do
  begin
    Exec('cmd.exe', '/C sc.exe query "' + Name + '" > "' + OutFile + '" 2>&1', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if LoadStringsFromFile(OutFile, Lines) then
    begin
      for I := 0 to GetArrayLength(Lines) - 1 do
      begin
        if Pos(WantToken, Lines[I]) > 0 then
        begin
          Result := True;
          Exit;
        end;
        if (WantToken = 'STOPPED') and (Pos('1060', Lines[I]) > 0) then
        begin
          Result := True;
          Exit;
        end;
      end;
    end;
    Sleep(500);
    Elapsed := Elapsed + 500;
  end;
end;

function WaitServiceStopped(const Name: String; TimeoutMs: Integer): Boolean;
begin
  Result := WaitServiceState(Name, 'STOPPED', TimeoutMs);
end;

function ExecChecked(const Filename, Params: String; const ExpectedZero: Boolean): Boolean;
var
  ResultCode: Integer;
  Ok: Boolean;
begin
  Ok := Exec(Filename, Params, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if not Ok then
  begin
    Result := False;
    Exit;
  end;
  if ExpectedZero and (ResultCode <> 0) then
  begin
    Result := False;
    Exit;
  end;
  Result := True;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
begin
  NeedsRestart := False;
  Exec('sc.exe', 'stop {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if not WaitServiceStopped('{#ServiceName}', 30000) then
  begin
    Result := 'NyxveilClientService did not stop. Close the VPN session and retry installation.';
    Exit;
  end;
  Result := '';
end;

{ External SCM transaction lives in Nyxveil.Service.exe -finalize-scm (create/config/start/pipe + rollback). }
function FinalizeInstallService(const BinPath: String): String;
var
  ResultCode: Integer;
  Ok: Boolean;
  TokenPath: String;
begin
  Result := '';
  TokenPath := ExpandConstant('{tmp}\nyxveil-orig-user.sid');
  { 1) ORIGINAL interactive user emits SID token (no write to protected ProgramData). }
  Ok := ExecAsOriginalUser(BinPath, '-write-sid-token=' + TokenPath, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if (not Ok) or (ResultCode <> 0) then
  begin
    Result := 'Failed to capture original-user SID token (ExecAsOriginalUser -write-sid-token).';
    Exit;
  end;
  { 2) Elevated: protect ProgramData\Nyxveil\Client then install SID from token. }
  if not ExecChecked(BinPath, '-protect-client-data-dir', True) then
  begin
    Result := 'Failed to protect Client data directory ACL.';
    Exit;
  end;
  if not ExecChecked(BinPath, '-install-sid-from-token=' + TokenPath, True) then
  begin
    Result := 'Failed to install authorized SID into protected ProgramData.';
    Exit;
  end;
  if not ExecChecked(BinPath, '-lock-sid-acl', True) then
  begin
    Result := 'Failed to lock authorized-user.sid ACL (-lock-sid-acl).';
    Exit;
  end;
  { 3) Elevated SCM transaction with automatic orphan rollback + pipe readiness. }
  if not ExecChecked(BinPath, '-finalize-scm', True) then
  begin
    Result := 'Service finalize failed (-finalize-scm). Orphan service rolled back if created.';
    Exit;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  BinPath: String;
  Err: String;
begin
  if CurStep = ssPostInstall then
  begin
    BinPath := ExpandConstant('{app}\{#ServiceExeName}');
    Err := FinalizeInstallService(BinPath);
    if Err <> '' then
      RaiseException(Err);
  end;
end;

function InitializeUninstall(): Boolean;
var
  ResultCode: Integer;
  BinPath: String;
begin
  GUninstallCleanupOK := False;
  Exec('sc.exe', 'stop {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if not WaitServiceStopped('{#ServiceName}', 30000) then
  begin
    MsgBox('NyxveilClientService did not stop. Uninstall aborted to preserve recovery journal.', mbError, MB_OK);
    Result := False;
    Exit;
  end;
  { Deterministic network recovery BEFORE any ProgramData delete. }
  BinPath := ExpandConstant('{app}\{#ServiceExeName}');
  if FileExists(BinPath) then
  begin
    if not ExecChecked(BinPath, '-uninstall-network-cleanup', True) then
    begin
      MsgBox('Network recovery cleanup failed. Uninstall aborted; recovery journal preserved.', mbError, MB_OK);
      Result := False;
      Exit;
    end;
  end;
  GUninstallCleanupOK := True;
  Result := True;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  DataDir: String;
  BinPath: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    BinPath := ExpandConstant('{app}\{#ServiceExeName}');
    if FileExists(BinPath) then
      Exec(BinPath, '-uninstall-network-cleanup', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Exec('sc.exe', 'stop {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if not WaitServiceStopped('{#ServiceName}', 20000) then
    begin
      MsgBox('Service stop failed during uninstall; ProgramData recovery state preserved.', mbError, MB_OK);
      GUninstallCleanupOK := False;
    end;
    Exec('sc.exe', 'delete {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    { Only delete ProgramData after confirmed cleanup — never destroy journal while dirty. }
    if GUninstallCleanupOK then
    begin
      DataDir := ExpandConstant('{commonappdata}\Nyxveil\Client');
      DelTree(DataDir, True, True, True);
    end;
  end;
end;
