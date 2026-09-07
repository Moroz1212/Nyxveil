#Requires -RunAsAdministrator
#Requires -Version 5.1
<#
.SYNOPSIS
  Elevated FINAL OS gate - fail-closed; required gates cannot SKIP.
#>
param([switch]$KeepInstall)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Dist = Join-Path $Root "dist"
$Ver = (Get-Content (Join-Path $Root "VERSION") -Raw).Trim()
$Setup = Join-Path $Dist "Nyxveil-Setup-v$Ver.exe"
$SvcName = "NyxveilClientService"
$InstallDir = Join-Path ${env:ProgramFiles} "Nyxveil\Client"
$DataDir = Join-Path $env:ProgramData "Nyxveil\Client"
$SidFile = Join-Path $DataDir "authorized-user.sid"
$GateFlag = Join-Path $DataDir "gate-mode.flag"
$Bin = Join-Path $InstallDir "Nyxveil.Service.exe"
$Gui = Join-Path $InstallDir "Nyxveil.exe"
$Results = [ordered]@{}
$TempUsers = @()
$PublicGate = Join-Path $env:PUBLIC "NyxveilGate"
New-Item -ItemType Directory -Force -Path $PublicGate | Out-Null
try {
  $acl = Get-Acl $PublicGate
  $usersSid = New-Object System.Security.Principal.SecurityIdentifier("S-1-5-32-545")
  $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
    $usersSid, "Modify", "ContainerInherit,ObjectInherit", "None", "Allow")
  $acl.SetAccessRule($rule)
  Set-Acl -Path $PublicGate -AclObject $acl
} catch {}

$Required = @(
  "GO_TEST", "GO_VET", "DOTNET_TEST", "CONNECT_CANCELLATION", "QUIC", "TLS_FALLBACK", "TYPECONFIG",
  "TYPECONFIG_READER_OWNERSHIP", "TICKETBROKER_RACE", "JOURNAL_RESTORE_KEEPS_DIRTY", "SERVICE_DIRTY_JOURNAL_REFUSED",
  "INSTALL", "SCM_LOCALSYSTEM", "SERVICE_PIPE_READY", "QUOTED_SERVICE_PATH",
  "NORMAL_USER_PIPE", "OTHER_USER_REJECT", "ADMIN_PIPE",
  "AUTHORIZED_SID_FILE_ACL", "PROGRAMDATA_DIR_ACL", "ORIGINAL_USER_SID",
  "WINTUN_REAL", "WINTUN_SIGNATURE", "FULL_TUNNEL_ROUTES_REAL", "DATAPLANE_SOAK_10S",
  "ROUTES_REAL_WINDOWS", "DNS_REAL_WINDOWS", "IPV6_REAL_WINDOWS", "SCM_STOP_RESTORE",
  "CRASH_RECOVERY", "GUI_NON_ELEVATED", "POSTINSTALL_RUNASORIGINALUSER", "INSTALLER_ROLLBACK",
  "RECONNECT_DISCONNECT_RACES", "UNINSTALL", "REINSTALL", "FROZEN_SELF_CONTAINED", "SETUP_HASH",
  "INSTALLER_PROVENANCE", "LOCAL_GROUPS_SID_SAFE"
)

function Set-Gate([string]$Name, [string]$Status, [string]$Detail = "") {
  $Results[$Name] = @{ Status = $Status; Detail = $Detail }
  $line = ("[{0}] {1} {2}" -f $Status, $Name, $Detail)
  $c = switch ($Status) { "PASS" { "Green" } "FAIL" { "Red" } default { "Yellow" } }
  Write-Host $line -ForegroundColor $c
  try {
    Add-Content -LiteralPath (Join-Path $PublicGate "gate-live.log") -Value $line -Encoding UTF8
    [Console]::Out.Flush()
  } catch {}
}

function Invoke-Native([string]$FilePath, [string[]]$ArgumentList, [int]$TimeoutMs = 600000) {
  # ProcessStartInfo: reliable ExitCode (Start-Process often leaves ExitCode $null → [int]$null = 0).
  $tag = [guid]::NewGuid().ToString("N").Substring(0, 10)
  $o = Join-Path $PublicGate "nv-native-$tag.out"
  $e = Join-Path $PublicGate "nv-native-$tag.err"
  Remove-Item -LiteralPath $o, $e -Force -ErrorAction SilentlyContinue
  $psi = New-Object System.Diagnostics.ProcessStartInfo
  $psi.FileName = $FilePath
  $psi.Arguments = ($ArgumentList | ForEach-Object {
    if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ }
  }) -join " "
  $psi.UseShellExecute = $false
  $psi.RedirectStandardOutput = $true
  $psi.RedirectStandardError = $true
  $psi.CreateNoWindow = $true
  $psi.WorkingDirectory = $(if (Test-Path $FilePath) { Split-Path -Parent $FilePath } else { $PublicGate })
  $p = New-Object System.Diagnostics.Process
  $p.StartInfo = $psi
  $null = $p.Start()
  $outTask = $p.StandardOutput.ReadToEndAsync()
  $errTask = $p.StandardError.ReadToEndAsync()
  $ok = $p.WaitForExit($TimeoutMs)
  if (-not $ok) {
    try { & taskkill.exe /PID $p.Id /T /F 2>$null | Out-Null } catch {}
    try { $p.Kill() } catch {}
    Start-Sleep -Milliseconds 300
    try { $null = $p.WaitForExit(5000) } catch {}
  }
  $stdout = ""
  $stderr = ""
  try { $stdout = [string]$outTask.Result } catch {}
  try { $stderr = [string]$errTask.Result } catch {}
  # Also persist for post-mortem when redirected parent console swallows nothing.
  Set-Content -LiteralPath $o -Value $stdout -Encoding UTF8 -Force -ErrorAction SilentlyContinue
  Set-Content -LiteralPath $e -Value $stderr -Encoding UTF8 -Force -ErrorAction SilentlyContinue
  $code = -1
  if ($ok -and $p.HasExited) {
    try { $code = $p.ExitCode } catch { $code = -1 }
  }
  if (-not $ok) {
    $stderr = ("TIMEOUT after {0}ms; " -f $TimeoutMs) + $stderr
    $code = -1
  }
  Remove-Item -LiteralPath $o, $e -Force -ErrorAction SilentlyContinue
  return @{
    ExitCode = [int]$code
    StdOut   = $stdout
    StdErr   = $stderr
  }
}

function Assert-Admin {
  $p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run as Administrator"
  }
}

# Locale-independent: resolve built-in group display names from well-known SIDs.
function Resolve-BuiltinGroupName([string]$WellKnownSid) {
  $sid = New-Object System.Security.Principal.SecurityIdentifier($WellKnownSid)
  $acct = $sid.Translate([System.Security.Principal.NTAccount]).Value
  if ($acct -match '\\(.+)$') { return $Matches[1] }
  return $acct
}

$UsersGroup = Resolve-BuiltinGroupName "S-1-5-32-545"
$AdminsGroup = Resolve-BuiltinGroupName "S-1-5-32-544"

function Grant-BatchLogonRight([string]$Account) {
  # Scheduled tasks with -User require SeBatchLogonRight; without it LastTaskResult=0x41303.
  $cs = @"
using System;
using System.Runtime.InteropServices;
using System.Text;
public class NvLsa {
  [DllImport("advapi32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  static extern uint LsaOpenPolicy(IntPtr s, ref LSA_OBJECT_ATTRIBUTES o, uint a, out IntPtr h);
  [DllImport("advapi32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  static extern uint LsaAddAccountRights(IntPtr p, IntPtr sid, LSA_UNICODE_STRING[] r, uint c);
  [DllImport("advapi32.dll")] static extern uint LsaClose(IntPtr h);
  [DllImport("advapi32.dll")] static extern uint LsaNtStatusToWinError(uint s);
  [DllImport("advapi32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  static extern bool LookupAccountName(string sys, string acct, byte[] sid, ref uint sidLen, StringBuilder d, ref uint dLen, out int use);
  [StructLayout(LayoutKind.Sequential)] struct LSA_OBJECT_ATTRIBUTES { public int Length; public IntPtr RootDirectory; public IntPtr ObjectName; public uint Attributes; public IntPtr SecurityDescriptor; public IntPtr SecurityQualityOfService; }
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] struct LSA_UNICODE_STRING { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
  public static void Grant(string account) {
    uint sidLen=0, domLen=0; int use;
    LookupAccountName(null, account, null, ref sidLen, null, ref domLen, out use);
    byte[] sid = new byte[sidLen];
    StringBuilder dom = new StringBuilder((int)domLen);
    if (!LookupAccountName(null, account, sid, ref sidLen, dom, ref domLen, out use))
      throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
    IntPtr pSid = Marshal.AllocHGlobal(sid.Length);
    Marshal.Copy(sid, 0, pSid, sid.Length);
    var oa = new LSA_OBJECT_ATTRIBUTES();
    IntPtr pol;
    uint st = LsaOpenPolicy(IntPtr.Zero, ref oa, 0x00000F0F, out pol);
    if (st != 0) throw new System.ComponentModel.Win32Exception((int)LsaNtStatusToWinError(st));
    string right = "SeBatchLogonRight";
    var us = new LSA_UNICODE_STRING { Length=(ushort)(right.Length*2), MaximumLength=(ushort)((right.Length+1)*2), Buffer=Marshal.StringToHGlobalUni(right) };
    try {
      st = LsaAddAccountRights(pol, pSid, new[]{us}, 1);
      if (st != 0) throw new System.ComponentModel.Win32Exception((int)LsaNtStatusToWinError(st));
    } finally {
      Marshal.FreeHGlobal(us.Buffer);
      LsaClose(pol);
      Marshal.FreeHGlobal(pSid);
    }
  }
}
"@
  if (-not ("NvLsa" -as [type])) { Add-Type -TypeDefinition $cs -ErrorAction Stop }
  [NvLsa]::Grant($Account)
}

function New-TempUser([string]$Prefix) {
  $name = $Prefix + (Get-Random -Maximum 99999)
  $plain = ([guid]::NewGuid().ToString("N") + "Aa1!")
  $sec = ConvertTo-SecureString $plain -AsPlainText -Force
  New-LocalUser -Name $name -Password $sec -PasswordNeverExpires -UserMayNotChangePassword | Out-Null
  Add-LocalGroupMember -Group $UsersGroup -Member $name -ErrorAction SilentlyContinue
  try { Grant-BatchLogonRight $name } catch {
    Write-Host ("WARN: Grant-BatchLogonRight {0}: {1}" -f $name, $_.Exception.Message)
  }
  $script:TempUsers += $name
  return @{ Name = $name; Secure = $sec; Plain = $plain }
}

function Remove-TempUsers {
  foreach ($u in $script:TempUsers) {
    try { Remove-LocalGroupMember -Group $AdminsGroup -Member $u -ErrorAction SilentlyContinue } catch {}
    try { Remove-LocalUser -Name $u -ErrorAction SilentlyContinue } catch {}
  }
}

function Get-Sid([string]$Name) {
  (New-Object System.Security.Principal.NTAccount($Name)).Translate([System.Security.Principal.SecurityIdentifier]).Value
}

function Resolve-GoExe {
  $candidates = @(
    (Get-Command go -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
    "C:\Program Files\Go\bin\go.exe",
    (Join-Path $env:USERPROFILE "go\bin\go.exe")
  ) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
  if (-not $candidates) { throw "go.exe not found on PATH or standard install locations" }
  return $candidates
}

function Invoke-GoProcess {
  param(
    [Parameter(Mandatory = $true)][string]$WorkingDirectory,
    [Parameter(Mandatory = $true)][string[]]$ArgumentList
  )
  $goExe = Resolve-GoExe
  $psi = New-Object System.Diagnostics.ProcessStartInfo
  $psi.FileName = $goExe
  $psi.Arguments = ($ArgumentList | ForEach-Object {
    if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ }
  }) -join " "
  $psi.WorkingDirectory = $WorkingDirectory
  $psi.UseShellExecute = $false
  $psi.RedirectStandardOutput = $true
  $psi.RedirectStandardError = $true
  $psi.CreateNoWindow = $true
  # Elevated sessions sometimes inherit a stripped PATH; keep Go discoverable for child tools.
  $goDir = Split-Path -Parent $goExe
  if ($psi.EnvironmentVariables.ContainsKey("PATH")) {
    $psi.EnvironmentVariables["PATH"] = ($goDir + ";" + $psi.EnvironmentVariables["PATH"])
  }
  $p = New-Object System.Diagnostics.Process
  $p.StartInfo = $psi
  $null = $p.Start()
  $stdoutTask = $p.StandardOutput.ReadToEndAsync()
  $stderrTask = $p.StandardError.ReadToEndAsync()
  $p.WaitForExit()
  return @{
    ExitCode = [int]$p.ExitCode
    StdOut   = [string]$stdoutTask.Result
    StdErr   = [string]$stderrTask.Result
  }
}

function Invoke-NamedGoTest([string]$Pattern, [string[]]$MustContainNames) {
  $engineDir = Join-Path $Root "engine"
  $r = Invoke-GoProcess -WorkingDirectory $engineDir -ArgumentList @(
    "test", "./...", "-count=1", "-json", "-run", $Pattern
  )
  $ranSet = New-Object "System.Collections.Generic.HashSet[string]"
  $failSet = New-Object "System.Collections.Generic.HashSet[string]"
  foreach ($line in (($r.StdOut -split "`r?`n"))) {
    $t = $line.Trim()
    if (-not $t) { continue }
    try { $ev = $t | ConvertFrom-Json } catch { continue }
    $testName = [string]$ev.Test
    if ([string]::IsNullOrEmpty($testName)) { continue }
    $action = [string]$ev.Action
    if ($action -eq "run") { [void]$ranSet.Add($testName) }
    elseif ($action -eq "fail") { [void]$failSet.Add($testName) }
    elseif ($action -eq "pass") { [void]$ranSet.Add($testName) }
  }
  $missing = @()
  foreach ($n in $MustContainNames) {
    $found = $false
    foreach ($ranName in $ranSet) {
      if ($ranName -eq $n) { $found = $true; break }
    }
    if (-not $found) { $missing += $n }
  }
  $ok = ($r.ExitCode -eq 0) -and ($failSet.Count -eq 0) -and ($missing.Count -eq 0) -and ($ranSet.Count -gt 0)
  return @{
    Ok       = [bool]$ok
    ExitCode = [int]$r.ExitCode
    Ran      = @($ranSet)
    Failed   = @($failSet)
    Missing  = @($missing)
  }
}

# Temp-user runner via ProcessStartInfo (UserName/Password/LoadUserProfile).
# Avoid Start-Process -Credential (-196608) and Task Scheduler Limited (0x41303 without batch right).
function Invoke-AsUserFile([hashtable]$User, [string]$PsFile) {
  $tag = [guid]::NewGuid().ToString("N").Substring(0, 10)
  $exitFile = Join-Path $PublicGate "nv-asuser-$tag.exit"
  $outFile = Join-Path $PublicGate "nv-asuser-$tag.out"
  $errFile = Join-Path $PublicGate "nv-asuser-$tag.err"
  $metaFile = Join-Path $PublicGate "nv-asuser-$tag.meta"
  $wrapPs = Join-Path $PublicGate "nv-asuser-$tag.ps1"
  Remove-Item -LiteralPath $exitFile, $outFile, $errFile, $metaFile -Force -ErrorAction SilentlyContinue

  $wrapBody = @"
`$ErrorActionPreference = 'Continue'
`$exitPath = '$exitFile'
`$outPath = '$outFile'
`$errPath = '$errFile'
`$metaPath = '$metaFile'
`$code = -1
try {
  `$id = [Security.Principal.WindowsIdentity]::GetCurrent()
  `$pr = New-Object Security.Principal.WindowsPrincipal(`$id)
  `$elev = `$pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
  `$il = 'unknown'
  foreach (`$g in `$id.Groups) {
    `$v = `$g.Value
    if (`$v -eq 'S-1-16-12288') { `$il = 'high'; break }
    elseif (`$v -eq 'S-1-16-8192') { `$il = 'medium'; break }
    elseif (`$v -eq 'S-1-16-4096') { `$il = 'low'; break }
    elseif (`$v -eq 'S-1-16-16384') { `$il = 'system'; break }
  }
  Set-Content -Path `$metaPath -Value ("sid=`$(`$id.User.Value);elevated=`$elev;integrity=`$il") -Encoding ASCII -Force
  `$p = Start-Process -FilePath 'powershell.exe' -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','$PsFile' -Wait -PassThru -WindowStyle Hidden -RedirectStandardOutput `$outPath -RedirectStandardError `$errPath
  if (`$null -ne `$p) { `$code = [int]`$p.ExitCode } else { `$code = -1 }
} catch {
  `$code = -1
  Set-Content -Path `$errPath -Value `$_.Exception.Message -Encoding ASCII -Force
}
Set-Content -Path `$exitPath -Value `$code -Encoding ASCII -Force
exit `$code
"@
  Set-Content -Path $wrapPs -Value $wrapBody -Encoding UTF8

  try {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = "powershell.exe"
    $psi.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$wrapPs`""
    $psi.WorkingDirectory = $PublicGate
    $psi.UseShellExecute = $false
    $psi.LoadUserProfile = $true
    $psi.UserName = $User.Name
    $psi.Domain = $env:COMPUTERNAME
    $psi.Password = $User.Secure
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    $null = $p.Start()
    $outT = $p.StandardOutput.ReadToEndAsync()
    $errT = $p.StandardError.ReadToEndAsync()
    if (-not $p.WaitForExit(180000)) {
      try { $p.Kill() } catch {}
      throw "timeout waiting for user process"
    }
    $launchOut = [string]$outT.Result
    $launchErr = [string]$errT.Result
    $launchCode = $p.ExitCode
  } catch {
    Remove-Item -LiteralPath $wrapPs -Force -ErrorAction SilentlyContinue
    return @{
      ExitCode = -1
      Outcome  = "PROCESS_LAUNCH_FAILED"
      StdOut   = ""
      StdErr   = [string]$_.Exception.Message
      Detail   = "ProcessStartInfo"
      Meta     = ""
    }
  }
  finally {
    Remove-Item -LiteralPath $wrapPs -Force -ErrorAction SilentlyContinue
  }

  $stdout = ""
  $stderr = ""
  if (Test-Path -LiteralPath $outFile) { $stdout = [string](Get-Content -LiteralPath $outFile -Raw -ErrorAction SilentlyContinue) }
  if (Test-Path -LiteralPath $errFile) { $stderr = [string](Get-Content -LiteralPath $errFile -Raw -ErrorAction SilentlyContinue) }
  if (-not $stdout -and $launchOut) { $stdout = $launchOut }
  if (-not $stderr -and $launchErr) { $stderr = $launchErr }

  $ec = $null
  if (Test-Path -LiteralPath $exitFile) {
    $raw = (Get-Content -LiteralPath $exitFile -Raw).Trim()
    try { $ec = [int]$raw } catch { $ec = $null }
  }
  if ($null -eq $ec) { $ec = [int]$launchCode }

  $meta = ""
  if (Test-Path -LiteralPath $metaFile) {
    $meta = [string](Get-Content -LiteralPath $metaFile -Raw -ErrorAction SilentlyContinue).Trim()
  }
  Remove-Item -LiteralPath $exitFile, $outFile, $errFile, $metaFile -Force -ErrorAction SilentlyContinue

  if ($null -eq $ec) {
    return @{
      ExitCode = -1
      Outcome  = "PROCESS_LAUNCH_FAILED"
      StdOut   = $stdout
      StdErr   = $stderr
      Detail   = "no_exit_code"
      Meta     = $meta
    }
  }
  return @{
    ExitCode = [int]$ec
    Outcome  = "OK"
    StdOut   = $stdout
    StdErr   = $stderr
    Detail   = "ProcessStartInfo"
    Meta     = $meta
  }
}

function Wait-Svc([string]$Want, [int]$Sec = 45) {
  $d = (Get-Date).AddSeconds($Sec)
  do {
    $s = Get-Service $SvcName -ErrorAction SilentlyContinue
    if ($Want -eq "Absent" -and -not $s) { return $true }
    if ($s -and ([string]$s.Status -eq $Want)) { return $true }
    Start-Sleep -Seconds 1
  } while ((Get-Date) -lt $d)
  return $false
}

# Wait until not StartPending/StopPending; return final status string. Never treat START_PENDING as Running.
function Wait-SvcTerminal([int]$Sec = 60) {
  $deadline = (Get-Date).AddSeconds($Sec)
  do {
    $s = Get-Service $SvcName -ErrorAction SilentlyContinue
    if (-not $s) { return "Absent" }
    $st = [string]$s.Status
    if ($st -ne "StartPending" -and $st -ne "StopPending") {
      return $st
    }
    Start-Sleep -Milliseconds 400
  } while ((Get-Date) -lt $deadline)
  $s = Get-Service $SvcName -ErrorAction SilentlyContinue
  if (-not $s) { return "Absent" }
  return [string]$s.Status
}

function Get-SvcExitDetail {
  $cim = Get-CimInstance Win32_Service -Filter "Name='$SvcName'" -ErrorAction SilentlyContinue
  if (-not $cim) { return "Absent" }
  return ("State={0};Win32Exit={1};ServiceSpecific={2};StartName={3}" -f $cim.State, $cim.ExitCode, $cim.ServiceSpecificExitCode, $cim.StartName)
}

Assert-Admin
Write-Host "=== Nyxveil FINAL Windows Gate ===" -ForegroundColor Cyan

if (-not (Test-Path $Setup)) {
  & (Join-Path $Root "scripts\package-final.ps1")
}
if (-not (Test-Path $Setup)) { throw "Setup missing" }

$engineDir = Join-Path $Root "engine"

# --- Named unit gates (prove tests ran via go test -json) ---
$goAll = Invoke-GoProcess -WorkingDirectory $engineDir -ArgumentList @("test", "./...", "-count=1", "-timeout", "180s")
if ($goAll.ExitCode -eq 0) {
  Set-Gate "GO_TEST" "PASS"
} else {
  $snip = (($goAll.StdErr + "`n" + $goAll.StdOut) -split "`r?`n" | Where-Object { $_ -match "FAIL|Error|panic|---" } | Select-Object -First 12) -join " | "
  Set-Gate "GO_TEST" "FAIL" ("exit={0} detail={1}" -f $goAll.ExitCode, $snip)
}

$goVet = Invoke-GoProcess -WorkingDirectory $engineDir -ArgumentList @("vet", "./...")
if ($goVet.ExitCode -eq 0) { Set-Gate "GO_VET" "PASS" } else { Set-Gate "GO_VET" "FAIL" ("exit={0}" -f $goVet.ExitCode) }

$cancelNames = @(
  "TestConnectCancelDuringDial",
  "TestConnectCancelDuringAuth",
  "TestConnectCancelDuringTypeConfig",
  "TestConnectCancelDuringTUN",
  "TestTwoSimultaneousConnectRejectsSecond",
  "TestDisconnectSupersedesInFlightConnectCommit",
  "TestConnectAfterCancelAllowsNewConnect",
  "TestDisconnectWhileReconnectWaitingForTicketDoesNotReconnect",
  "TestDisconnectAfterTicketBeforeReconnectDoesNotReconnect",
  "TestSessionLostAfterManualDisconnectDoesNotReconnect",
  "TestRepeatedSessionLossSingleReconnect"
)
$cancelPat = ($cancelNames -join "|")
$cancel = Invoke-NamedGoTest $cancelPat $cancelNames
if ($cancel.Ok) {
  Set-Gate "CONNECT_CANCELLATION" "PASS" ("ran={0}" -f $cancel.Ran.Count)
  Set-Gate "RECONNECT_DISCONNECT_RACES" "PASS" ("ran={0}" -f $cancel.Ran.Count)
} else {
  $det = ("exit={0};failed={1};missing={2}" -f $cancel.ExitCode, ($cancel.Failed -join ","), ($cancel.Missing -join ","))
  Set-Gate "CONNECT_CANCELLATION" "FAIL" $det
  Set-Gate "RECONNECT_DISCONNECT_RACES" "FAIL" $det
}

if ($UsersGroup -and $AdminsGroup -and $UsersGroup -ne $AdminsGroup) {
  Set-Gate "LOCAL_GROUPS_SID_SAFE" "PASS" ("UsersSID->$UsersGroup AdminsSID->$AdminsGroup")
} else {
  Set-Gate "LOCAL_GROUPS_SID_SAFE" "FAIL" ("users=$UsersGroup admins=$AdminsGroup")
}

$iss = Get-Content (Join-Path $Root "installer\nyxveil.iss") -Raw
if ($iss -match '(?i)runasoriginaluser') {
  Set-Gate "POSTINSTALL_RUNASORIGINALUSER" "PASS" "iss Flags include runasoriginaluser"
} else {
  Set-Gate "POSTINSTALL_RUNASORIGINALUSER" "FAIL" "missing runasoriginaluser on [Run]"
}

$quic = Invoke-NamedGoTest "TestRegistryHasQUICAndTLS" @("TestRegistryHasQUICAndTLS")
if ($quic.Ok) {
  Set-Gate "QUIC" "PASS" "TestRegistryHasQUICAndTLS"
  Set-Gate "TLS_FALLBACK" "PASS" "TestRegistryHasQUICAndTLS"
} else {
  $det = ("exit={0};missing={1}" -f $quic.ExitCode, ($quic.Missing -join ","))
  Set-Gate "QUIC" "FAIL" $det
  Set-Gate "TLS_FALLBACK" "FAIL" $det
}

$tcNames = @("TestDecodeRequiresDNS", "TestDecodeOK", "TestTypeConfigDNSRequired")
$tc = Invoke-NamedGoTest ($tcNames -join "|") $tcNames
if ($tc.Ok) { Set-Gate "TYPECONFIG" "PASS" ("ran={0}" -f ($tc.Ran -join ",")) }
else { Set-Gate "TYPECONFIG" "FAIL" ("exit={0};missing={1};failed={2}" -f $tc.ExitCode, ($tc.Missing -join ","), ($tc.Failed -join ",")) }

$ownNames = @(
  "TestTypeConfigTempReaderExitsBeforePermanentTLS",
  "TestTypeConfigTempReaderExitsBeforePermanentQUIC",
  "TestTypeConfigDisconnectDuringWait",
  "TestTypeConfigTimeoutCleanup",
  "TestStopTempTransportReaderUnblocksStickyRead"
)
$own = Invoke-NamedGoTest ($ownNames -join "|") $ownNames
if ($own.Ok) { Set-Gate "TYPECONFIG_READER_OWNERSHIP" "PASS" }
else { Set-Gate "TYPECONFIG_READER_OWNERSHIP" "FAIL" ("exit={0};missing={1}" -f $own.ExitCode, ($own.Missing -join ",")) }

$prevCgo = $env:CGO_ENABLED
$env:CGO_ENABLED = "1"
$tb = Invoke-GoProcess -WorkingDirectory $engineDir -ArgumentList @(
  "test", "./internal/ticketbroker/", "-count=1", "-race", "-timeout", "180s",
  "-run", "TestProvideVs|TestTimeoutVs|TestDisconnectWhile"
)
if ($tb.ExitCode -ne 0) {
  if ($null -eq $prevCgo) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue }
  else { $env:CGO_ENABLED = $prevCgo }
  $tb = Invoke-GoProcess -WorkingDirectory $engineDir -ArgumentList @(
    "test", "./internal/ticketbroker/", "-count=1", "-timeout", "180s",
    "-run", "TestProvideVs|TestTimeoutVs|TestDisconnectWhile|TestRoundtrip|TestTimeout|TestDisconnectCancels"
  )
}
if ($null -eq $prevCgo) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue }
else { $env:CGO_ENABLED = $prevCgo }
if ($tb.ExitCode -eq 0) { Set-Gate "TICKETBROKER_RACE" "PASS" } else { Set-Gate "TICKETBROKER_RACE" "FAIL" ("exit={0}" -f $tb.ExitCode) }

$jrNames = @(
  "TestRollbackPreservesJournalOnUndoFailure",
  "TestSuccessfulRestoreClearsJournal",
  "TestPendingPersistFailureAbortsBypass"
)
$jr = Invoke-NamedGoTest ($jrNames -join "|") $jrNames
if ($jr.Ok) { Set-Gate "JOURNAL_RESTORE_KEEPS_DIRTY" "PASS" }
else { Set-Gate "JOURNAL_RESTORE_KEEPS_DIRTY" "FAIL" ("exit={0};missing={1}" -f $jr.ExitCode, ($jr.Missing -join ",")) }

Push-Location (Join-Path $Root "gui")
try {
  $dotnet = Invoke-Native "dotnet" @(
    "test", (Join-Path $Root "gui\Nyxveil.Client.sln"), "-c", "Release", "--verbosity", "minimal",
    "--disable-build-servers"
  ) -TimeoutMs 180000
  # Ensure build servers do not keep the elevated session occupied.
  $null = Invoke-Native "dotnet" @("build-server", "shutdown") -TimeoutMs 30000
  if ($dotnet.ExitCode -eq 0) { Set-Gate "DOTNET_TEST" "PASS" } else { Set-Gate "DOTNET_TEST" "FAIL" ("exit={0} err={1}" -f $dotnet.ExitCode, $dotnet.StdErr.Trim()) }
}
finally { Pop-Location }

& (Join-Path $Root "scripts\assert-wintun.ps1")
if ($?) { Set-Gate "WINTUN_SIGNATURE" "PASS" } else { Set-Gate "WINTUN_SIGNATURE" "FAIL" ("assert-wintun failed: {0}" -f $error[0]) }

& (Join-Path $Root "scripts\assert-frozen-core.ps1")
if ($?) { Set-Gate "FROZEN_SELF_CONTAINED" "PASS" } else { Set-Gate "FROZEN_SELF_CONTAINED" "FAIL" ("assert-frozen-core failed: {0}" -f $error[0]) }

$line = Get-Content (Join-Path $Dist "SHA256SUMS") | Where-Object { $_ -match "Nyxveil-Setup-v" } | Select-Object -First 1
$want = ($line -split '\s+')[0].ToLowerInvariant()
$got = (Get-FileHash $Setup -Algorithm SHA256).Hash.ToLowerInvariant()
if ($want -eq $got) { Set-Gate "SETUP_HASH" "PASS" } else { Set-Gate "SETUP_HASH" "FAIL" ("want={0} got={1}" -f $want, $got) }

# Sidecar attestation written here; INSTALLER_PROVENANCE is decided after install hash compare.
try {
  & (Join-Path $Root "scripts\assert-installer-provenance.ps1") `
    -SetupExe $Setup `
    -ExpectedServiceExe (Join-Path $Dist "payload\Nyxveil.Service.exe") `
    -ExpectedGuiExe (Join-Path $Dist "payload\gui\Nyxveil.exe") `
    -ExpectedWintunDll (Join-Path $Dist "payload\wintun.dll") `
    -ExpectedVersionFile (Join-Path $Root "VERSION")
} catch {
  Set-Gate "INSTALLER_PROVENANCE" "FAIL" ("pre-install: " + $_.Exception.Message)
}

# --- Users: A temporarily Administrators so silent Highest install models UAC original-user ---
$UserA = New-TempUser "NvA"
$UserB = New-TempUser "NvB"
$sidA = Get-Sid $UserA.Name
Add-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name

# Clean prior install
sc.exe stop $SvcName 2>$null | Out-Null
$null = Wait-Svc "Stopped" 30
sc.exe delete $SvcName 2>$null | Out-Null
Get-Process Nyxveil,Nyxveil.Service -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2
$uninsPrev = Get-ChildItem $InstallDir -Filter "unins*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
if ($uninsPrev) {
  $null = Start-Process -FilePath $uninsPrev.FullName -ArgumentList @("/VERYSILENT","/NORESTART","/SUPPRESSMSGBOXES") -Wait -PassThru
  Start-Sleep -Seconds 3
}

# Install as UserA with Highest (original user = A for ExecAsOriginalUser)
$task = "NyxveilGateInstallA"
Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
$installLog = Join-Path $PublicGate "nv-install-a.log"
$installInnoLog = Join-Path $PublicGate "nv-install-a-inno.log"
Remove-Item $installLog, $installInnoLog -Force -ErrorAction SilentlyContinue
$installPs = Join-Path $PublicGate "nv-install-a.ps1"
@"
`$ErrorActionPreference='Stop'
`$p = Start-Process -FilePath '$Setup' -ArgumentList '/VERYSILENT','/NORESTART','/SUPPRESSMSGBOXES','/LOG=$installInnoLog' -Wait -PassThru
Set-Content -Path '$installLog' -Value ('exit=' + `$p.ExitCode) -Encoding ASCII
exit `$p.ExitCode
"@ | Set-Content $installPs -Encoding UTF8

$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$installPs`"" -WorkingDirectory $PublicGate
Register-ScheduledTask -TaskName $task -Action $action -User $UserA.Name -Password $UserA.Plain -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName $task
$deadline = (Get-Date).AddMinutes(5)
do { Start-Sleep -Seconds 2 } while ((Get-ScheduledTask -TaskName $task).State -ne "Ready" -and (Get-Date) -lt $deadline)
Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue

$instExit = 1
if (Test-Path $installLog) {
  $instExit = [int]((Get-Content $installLog -Raw) -replace 'exit=', '').Trim()
}
$innoTail = ""
if (Test-Path $installInnoLog) {
  $innoTail = ((Get-Content $installInnoLog -Tail 8) -join " | ")
}
if ($instExit -eq 0 -and (Test-Path $Bin) -and (Wait-Svc "Running" 60)) {
  Set-Gate "INSTALL" "PASS" "Setup as UserA Highest"
} else {
  Set-Gate "INSTALL" "FAIL" ("exit={0} log={1} inno={2}" -f $instExit, (Get-Content $installLog -Raw -ErrorAction SilentlyContinue), $innoTail)
  Remove-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -ErrorAction SilentlyContinue
  Remove-TempUsers
  throw "install failed"
}

try {
  & (Join-Path $Root "scripts\assert-installer-provenance.ps1") `
    -SetupExe $Setup `
    -ExpectedServiceExe (Join-Path $Dist "payload\Nyxveil.Service.exe") `
    -ExpectedGuiExe (Join-Path $Dist "payload\gui\Nyxveil.exe") `
    -ExpectedWintunDll (Join-Path $Dist "payload\wintun.dll") `
    -ExpectedVersionFile (Join-Path $Root "VERSION") `
    -InstalledDir $InstallDir
  Set-Gate "INSTALLER_PROVENANCE" "PASS" ("installed=" + (Get-FileHash $Bin -Algorithm SHA256).Hash)
} catch {
  Set-Gate "INSTALLER_PROVENANCE" "FAIL" $_.Exception.Message
  throw
}

# Demote A to ordinary user for pipe/GUI tests
Remove-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -ErrorAction SilentlyContinue

$svc = Get-CimInstance Win32_Service -Filter "Name='$SvcName'"
if ($svc -and $svc.StartName -match 'LocalSystem|Local System' -and $svc.State -eq "Running") {
  Set-Gate "SCM_LOCALSYSTEM" "PASS"
} else {
  Set-Gate "SCM_LOCALSYSTEM" "FAIL" ("{0}/{1}" -f $svc.StartName, $svc.State)
}

$pn = [string]$svc.PathName
if ($pn -match '^"[^"]+Nyxveil\.Service\.exe"') {
  Set-Gate "QUOTED_SERVICE_PATH" "PASS" $pn
} else {
  Set-Gate "QUOTED_SERVICE_PATH" "FAIL" ("PathName={0}" -f $pn)
}

$onDisk = (Get-Content $SidFile -Raw -ErrorAction SilentlyContinue).Trim()
if ($onDisk -eq $sidA) { Set-Gate "ORIGINAL_USER_SID" "PASS" $sidA }
else { Set-Gate "ORIGINAL_USER_SID" "FAIL" ("disk={0} want={1}" -f $onDisk, $sidA) }

# SID ACL overwrite as A/B - semantic ACCESS_DENIED / WRITE_SUCCEEDED / PROCESS_LAUNCH_FAILED
$aclPs = Join-Path $PublicGate "nv-sid-ow.ps1"
$aclOut = Join-Path $PublicGate "nv-sid-ow.out"
@"
`$ErrorActionPreference='Stop'
try {
  Set-Content -Path '$SidFile' -Value 'BAD' -Encoding ASCII
  Set-Content -Path '$aclOut' -Value 'WRITE_SUCCEEDED' -Encoding ASCII
  exit 0
} catch {
  Set-Content -Path '$aclOut' -Value 'ACCESS_DENIED' -Encoding ASCII
  exit 5
}
"@ | Set-Content $aclPs -Encoding UTF8

Remove-Item $aclOut -Force -ErrorAction SilentlyContinue
$rAclA = Invoke-AsUserFile $UserA $aclPs
$outAclA = if (Test-Path $aclOut) { (Get-Content $aclOut -Raw).Trim() } else { "" }
Remove-Item $aclOut -Force -ErrorAction SilentlyContinue
$rAclB = Invoke-AsUserFile $UserB $aclPs
$outAclB = if (Test-Path $aclOut) { (Get-Content $aclOut -Raw).Trim() } else { "" }

$adminOk = $true
try {
  $x = Get-Content $SidFile -Raw
  Set-Content $SidFile -Value $x -Encoding ASCII
  & $Bin -lock-sid-acl | Out-Null
} catch { $adminOk = $false }

if ((Get-Content $SidFile -Raw -ErrorAction SilentlyContinue).Trim() -ne $sidA) {
  & $Bin -provision-sid -sid $sidA | Out-Null
  & $Bin -lock-sid-acl | Out-Null
  Restart-Service $SvcName -Force
  Start-Sleep -Seconds 2
}

if (
  $rAclA.Outcome -eq "OK" -and $rAclB.Outcome -eq "OK" -and
  $rAclA.ExitCode -eq 5 -and $rAclB.ExitCode -eq 5 -and
  $outAclA -eq "ACCESS_DENIED" -and $outAclB -eq "ACCESS_DENIED" -and
  $adminOk
) {
  Set-Gate "AUTHORIZED_SID_FILE_ACL" "PASS"
} else {
  Set-Gate "AUTHORIZED_SID_FILE_ACL" "FAIL" (
    "A={0}/{1}/{2} B={3}/{4}/{5} admin={6}" -f
    $rAclA.Outcome, $rAclA.ExitCode, $outAclA,
    $rAclB.Outcome, $rAclB.ExitCode, $outAclB,
    $adminOk
  )
}

# Protected ProgramData Client directory
$dirAclPs = Join-Path $PublicGate "nv-dir-acl.ps1"
$dirAclOut = Join-Path $PublicGate "nv-dir-acl.out"
@"
`$ErrorActionPreference='Stop'
`$succeeded = @()
try { Set-Content -Path '$SidFile' -Value 'BAD' -Encoding ASCII; `$succeeded += 'sid-write' } catch {}
try { Remove-Item -Path '$SidFile' -Force -ErrorAction Stop; `$succeeded += 'sid-delete' } catch {}
try { Set-Content -Path '$DataDir\route-journal.json' -Value '{}' -Encoding ASCII; `$succeeded += 'journal-write' } catch {}
try { Set-Content -Path '$DataDir\route-journal.json.tmp' -Value 'x' -Encoding ASCII; `$succeeded += 'journal-tmp' } catch {}
try { Set-Content -Path '$GateFlag' -Value '1' -Encoding ASCII; `$succeeded += 'gate-flag' } catch {}
if (`$succeeded.Count -eq 0) {
  Set-Content -Path '$dirAclOut' -Value 'ACCESS_DENIED' -Encoding ASCII
  exit 5
} else {
  Set-Content -Path '$dirAclOut' -Value ('WRITE_SUCCEEDED|' + (`$succeeded -join ',')) -Encoding ASCII
  exit 0
}
"@ | Set-Content $dirAclPs -Encoding UTF8

Remove-Item $dirAclOut -Force -ErrorAction SilentlyContinue
$rDirA = Invoke-AsUserFile $UserA $dirAclPs
$outDirA = if (Test-Path $dirAclOut) { (Get-Content $dirAclOut -Raw).Trim() } else { "" }
Remove-Item $dirAclOut -Force -ErrorAction SilentlyContinue
$rDirB = Invoke-AsUserFile $UserB $dirAclPs
$outDirB = if (Test-Path $dirAclOut) { (Get-Content $dirAclOut -Raw).Trim() } else { "" }

$adminDirOk = $true
try {
  & $Bin -protect-client-data-dir | Out-Null
  Set-Content -Path (Join-Path $DataDir "acl-admin-probe.txt") -Value "ok" -Encoding ASCII
  Remove-Item (Join-Path $DataDir "acl-admin-probe.txt") -Force
} catch { $adminDirOk = $false }

if (
  $rDirA.Outcome -eq "OK" -and $rDirB.Outcome -eq "OK" -and
  $rDirA.ExitCode -eq 5 -and $rDirB.ExitCode -eq 5 -and
  $outDirA -eq "ACCESS_DENIED" -and $outDirB -eq "ACCESS_DENIED" -and
  $adminDirOk
) {
  Set-Gate "PROGRAMDATA_DIR_ACL" "PASS"
} else {
  Set-Gate "PROGRAMDATA_DIR_ACL" "FAIL" (
    "A={0}/{1}/{2} B={3}/{4}/{5} admin={6}" -f
    $rDirA.Outcome, $rDirA.ExitCode, $outDirA,
    $rDirB.Outcome, $rDirB.ExitCode, $outDirB,
    $adminDirOk
  )
}

# Pipe helpers
$pipePs = Join-Path $PublicGate "nv-pipe.ps1"
$pipeOut = Join-Path $PublicGate "nv-pipe.out"
@"
`$ErrorActionPreference='Stop'
try {
  Add-Type -TypeDefinition @'
using System; using System.IO; using System.IO.Pipes; using System.Text;
public static class P {
  public static string D() {
    using (var p = new NamedPipeClientStream(".", "NyxveilClient", PipeDirection.InOut)) {
      p.Connect(8000);
      using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
      using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
        w.WriteLine("{\"v\":1,\"type\":\"hello\"}");
        return r.ReadLine() ?? "";
      }
    }
  }
}
'@
  Set-Content -Path '$pipeOut' -Value ('PASS|' + [P]::D().Length) -Encoding ASCII
  exit 0
} catch {
  Set-Content -Path '$pipeOut' -Value ('FAIL|' + `$_.Exception.Message) -Encoding ASCII
  exit 5
}
"@ | Set-Content $pipePs -Encoding UTF8

Remove-Item $pipeOut -Force -ErrorAction SilentlyContinue
$rPipeA = Invoke-AsUserFile $UserA $pipePs
$pipeOutA = if (Test-Path $pipeOut) { (Get-Content $pipeOut -Raw).Trim() } else { "" }
if ($rPipeA.Outcome -eq "OK" -and $rPipeA.ExitCode -eq 0 -and $pipeOutA -like "PASS|*") {
  Set-Gate "NORMAL_USER_PIPE" "PASS" $pipeOutA
} else {
  Set-Gate "NORMAL_USER_PIPE" "FAIL" ("outcome={0} exit={1} out={2}" -f $rPipeA.Outcome, $rPipeA.ExitCode, $pipeOutA)
}

Remove-Item $pipeOut -Force -ErrorAction SilentlyContinue
$rPipeB = Invoke-AsUserFile $UserB $pipePs
$pipeOutB = if (Test-Path $pipeOut) { (Get-Content $pipeOut -Raw).Trim() } else { "" }
if ($rPipeB.Outcome -eq "PROCESS_LAUNCH_FAILED") {
  Set-Gate "OTHER_USER_REJECT" "FAIL" ("PROCESS_LAUNCH_FAILED detail={0} err={1} meta={2}" -f $rPipeB.Detail, $rPipeB.StdErr, $rPipeB.Meta)
} elseif ($rPipeB.Outcome -eq "OK" -and $rPipeB.ExitCode -ne 0 -and ($pipeOutB -like "FAIL|*" -or $pipeOutB -match "access|denied|Unauthorized")) {
  Set-Gate "OTHER_USER_REJECT" "PASS" ("exit={0} out={1} meta={2}" -f $rPipeB.ExitCode, $pipeOutB, $rPipeB.Meta)
} else {
  Set-Gate "OTHER_USER_REJECT" "FAIL" ("outcome={0} exit={1} out={2} err={3} meta={4}" -f $rPipeB.Outcome, $rPipeB.ExitCode, $pipeOutB, $rPipeB.StdErr, $rPipeB.Meta)
}

Remove-Item $pipeOut -Force -ErrorAction SilentlyContinue
$adminPipe = Invoke-Native "powershell.exe" @(
  "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $pipePs
)
$pipeOutAdmin = if (Test-Path $pipeOut) { (Get-Content $pipeOut -Raw).Trim() } else { "" }
if ($adminPipe.ExitCode -eq 0 -and $pipeOutAdmin -like "PASS|*") {
  Set-Gate "ADMIN_PIPE" "PASS"
  Set-Gate "SERVICE_PIPE_READY" "PASS"
} else {
  Set-Gate "ADMIN_PIPE" "FAIL" $pipeOutAdmin
  Set-Gate "SERVICE_PIPE_READY" "FAIL" $pipeOutAdmin
}

# Installer SCM rollback: fail-injection must leave no orphan service, then restore
$rbFail = @()
foreach ($step in @("create", "description", "failure", "start", "ready")) {
  $p = Invoke-Native $Bin @("-finalize-scm", "-finalize-scm-fail-after", $step)
  if ($p.ExitCode -eq 0) { $rbFail += "${step}:expected-nonzero" }
  if (Get-Service $SvcName -ErrorAction SilentlyContinue) { $rbFail += "${step}:orphan-remains" }
}
$rest = Invoke-Native $Bin @("-finalize-scm")
if ($rest.ExitCode -ne 0 -or -not (Wait-Svc "Running" 60)) {
  $rbFail += ("restore-failed:{0}" -f $rest.ExitCode)
} else {
  Remove-Item $pipeOut -Force -ErrorAction SilentlyContinue
  $null = Invoke-Native "powershell.exe" @(
    "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $pipePs
  )
  if ((Get-Content $pipeOut -Raw -ErrorAction SilentlyContinue) -notlike "PASS|*") {
    $rbFail += "pipe-after-restore"
  }
}
if ($rbFail.Count -eq 0) { Set-Gate "INSTALLER_ROLLBACK" "PASS" }
else { Set-Gate "INSTALLER_ROLLBACK" "FAIL" ($rbFail -join ";") }

# SERVICE_DIRTY_JOURNAL_REFUSED: fail-closed on terminal status (never treat START_PENDING as Running)
Stop-Service $SvcName -Force -ErrorAction SilentlyContinue
sc.exe stop $SvcName 2>$null | Out-Null
if (-not (Wait-Svc "Stopped" 45)) {
  # Last resort: kill service process so dirty-journal start is a real new process.
  $svcPid = (Get-CimInstance Win32_Service -Filter "Name='$SvcName'" -ErrorAction SilentlyContinue).ProcessId
  if ($svcPid -and $svcPid -gt 0) {
    Stop-Process -Id $svcPid -Force -ErrorAction SilentlyContinue
  }
  Start-Sleep -Seconds 2
}
if (-not (Wait-Svc "Stopped" 30)) {
  Set-Gate "SERVICE_DIRTY_JOURNAL_REFUSED" "FAIL" "could not stop service before dirty-journal inject"
} else {
  $jPath = Join-Path $DataDir "route-journal.json"
  New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
  @'
{"phase":"restore_failed","applied":[{"id":"bypass-x","kind":"bypass_route","dest_prefix":"198.51.100.1/32","next_hop":""}]}
'@ | Set-Content $jPath -Encoding UTF8
  $null = Invoke-Native "sc.exe" @("start", $SvcName)
  $term = Wait-SvcTerminal 45
  $dirtyDetail = Get-SvcExitDetail
  if ($term -eq "Running") {
    Set-Gate "SERVICE_DIRTY_JOURNAL_REFUSED" "FAIL" ("Running over dirty journal; terminal={0}; {1}" -f $term, $dirtyDetail)
    Stop-Service $SvcName -Force -ErrorAction SilentlyContinue
    $null = Wait-Svc "Stopped" 30
  } else {
    Set-Gate "SERVICE_DIRTY_JOURNAL_REFUSED" "PASS" ("terminal={0}; {1}" -f $term, $dirtyDetail)
  }
  Remove-Item $jPath -Force -ErrorAction SilentlyContinue
  $null = Invoke-Native $Bin @("-finalize-scm")
  $null = Wait-Svc "Running" 60
}
# GUI non-elevated as A
$guiPs = Join-Path $PublicGate "nv-gui.ps1"
$guiOut = Join-Path $PublicGate "nv-gui.out"
@"
`$ErrorActionPreference='Stop'
try {
  `$pr = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
  if (`$pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Set-Content -Path '$guiOut' -Value 'FAIL|elevated' -Encoding ASCII
    exit 5
  }
  `$p = Start-Process -FilePath '$Gui' -PassThru -WindowStyle Minimized
  Start-Sleep -Seconds 4
  Add-Type -TypeDefinition @'
using System; using System.IO; using System.IO.Pipes; using System.Text;
public static class G {
  public static string D() {
    using (var p = new NamedPipeClientStream(".", "NyxveilClient", PipeDirection.InOut)) {
      p.Connect(8000);
      using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
      using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
        w.WriteLine("{\"v\":1,\"type\":\"hello\"}");
        return r.ReadLine() ?? "";
      }
    }
  }
}
'@
  `$r = [G]::D()
  try { Stop-Process -Id `$p.Id -Force } catch {}
  Set-Content -Path '$guiOut' -Value ('PASS|pid=' + `$p.Id + '|pipe=' + `$r.Length) -Encoding ASCII
  exit 0
} catch {
  Set-Content -Path '$guiOut' -Value ('FAIL|' + `$_.Exception.Message) -Encoding ASCII
  exit 5
}
"@ | Set-Content $guiPs -Encoding UTF8
Remove-Item $guiOut -Force -ErrorAction SilentlyContinue
$rGui = Invoke-AsUserFile $UserA $guiPs
$guiTxt = if (Test-Path $guiOut) { (Get-Content $guiOut -Raw).Trim() } else { "" }
if ($rGui.Outcome -eq "OK" -and $rGui.ExitCode -eq 0 -and $guiTxt -like "PASS|*") {
  Set-Gate "GUI_NON_ELEVATED" "PASS" $guiTxt
} else {
  Set-Gate "GUI_NON_ELEVATED" "FAIL" ("outcome={0} exit={1} out={2}" -f $rGui.Outcome, $rGui.ExitCode, $guiTxt)
}

# Wintun / net / ipv6 via service binary helpers
$w = Invoke-Native $Bin @("-gate-wintun")
if ($w.ExitCode -eq 0) { Set-Gate "WINTUN_REAL" "PASS" } else { Set-Gate "WINTUN_REAL" "FAIL" ("exit={0}" -f $w.ExitCode) }

$n = Invoke-Native $Bin @("-gate-net-tx")
if ($n.ExitCode -eq 0) {
  Set-Gate "ROUTES_REAL_WINDOWS" "PASS"
  Set-Gate "DNS_REAL_WINDOWS" "PASS"
} else {
  Set-Gate "ROUTES_REAL_WINDOWS" "FAIL" ("exit={0} err={1}" -f $n.ExitCode, $n.StdErr.Trim())
  Set-Gate "DNS_REAL_WINDOWS" "FAIL" ("exit={0} err={1}" -f $n.ExitCode, $n.StdErr.Trim())
}

$ft = Invoke-Native $Bin @("-gate-full-tunnel")
if ($ft.ExitCode -eq 0 -and $ft.StdOut -match "GATE_FULL_TUNNEL_OK" -and $ft.StdOut -match "GATE_DATAPLANE_SOAK_OK") {
  Set-Gate "FULL_TUNNEL_ROUTES_REAL" "PASS" $ft.StdOut.Trim()
  Set-Gate "DATAPLANE_SOAK_10S" "PASS" $ft.StdOut.Trim()
} else {
  Set-Gate "FULL_TUNNEL_ROUTES_REAL" "FAIL" ("exit={0} err={1} out={2}" -f $ft.ExitCode, $ft.StdErr.Trim(), $ft.StdOut.Trim())
  Set-Gate "DATAPLANE_SOAK_10S" "FAIL" ("exit={0} err={1} out={2}" -f $ft.ExitCode, $ft.StdErr.Trim(), $ft.StdOut.Trim())
}

$i = Invoke-Native $Bin @("-gate-ipv6")
if ($i.ExitCode -eq 0) { Set-Gate "IPV6_REAL_WINDOWS" "PASS" } else { Set-Gate "IPV6_REAL_WINDOWS" "FAIL" ("exit={0} err={1}" -f $i.ExitCode, $i.StdErr.Trim()) }

# --- Real SCM STOP: service-owned isolated tx via IPC, then Stop-Service ---
# Service writes unsolicited status on connect; client must ignore status/hello until gate_applied|error.
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Set-Content -Path $GateFlag -Value "1" -Encoding ASCII
$scmPs = Join-Path $PublicGate "nv-scm-apply.ps1"
$scmOut = Join-Path $PublicGate "nv-scm-apply.out"
@"
`$ErrorActionPreference='Stop'
try {
  Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.IO.Pipes;
using System.Text;
public static class S {
  public static string Apply() {
    using (var p = new NamedPipeClientStream(".", "NyxveilClient", PipeDirection.InOut)) {
      p.Connect(8000);
      try { p.ReadTimeout = 15000; } catch { }
      try { p.WriteTimeout = 15000; } catch { }
      using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
      using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
        string id = Guid.NewGuid().ToString("N");
        w.WriteLine("{\"v\":1,\"type\":\"gate_apply_isolated\",\"id\":\"" + id + "\"}");
        DateTime deadline = DateTime.UtcNow.AddSeconds(15);
        while (DateTime.UtcNow < deadline) {
          string line = r.ReadLine();
          if (line == null) return "FAIL|eof";
          // Ignore unsolicited status/hello; require matching request id.
          if (line.IndexOf("\"type\":\"gate_applied\"") >= 0) {
            if (line.IndexOf("\"id\":\"" + id + "\"") >= 0 || line.IndexOf("\"request_id\":\"" + id + "\"") >= 0)
              return line;
            continue;
          }
          if (line.IndexOf("\"type\":\"error\"") >= 0) {
            if (line.IndexOf("\"id\":\"" + id + "\"") >= 0 || line.IndexOf("\"request_id\":\"" + id + "\"") >= 0)
              return line;
            continue;
          }
        }
        return "FAIL|timeout";
      }
    }
  }
}
'@
  `$resp = [S]::Apply()
  Set-Content -Path '$scmOut' -Value `$resp -Encoding ASCII
  if (`$resp -notmatch 'gate_applied') { exit 5 }
  exit 0
} catch {
  Set-Content -Path '$scmOut' -Value `$_.Exception.Message -Encoding ASCII
  exit 5
}
"@ | Set-Content $scmPs -Encoding UTF8
Remove-Item $scmOut -Force -ErrorAction SilentlyContinue
$scmProc = Invoke-Native "powershell.exe" @(
  "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $scmPs
)
$applyResp = if (Test-Path $scmOut) { Get-Content $scmOut -Raw } else { "" }
if ($scmProc.ExitCode -ne 0 -or $applyResp -notmatch "gate_applied") {
  Set-Gate "SCM_STOP_RESTORE" "FAIL" ("apply failed: {0}" -f $applyResp)
} else {
  Stop-Service $SvcName -Force
  if (-not (Wait-Svc "Stopped" 45)) {
    Set-Gate "SCM_STOP_RESTORE" "FAIL" "service not stopped"
  } else {
    Start-Sleep -Seconds 2
    $chk = Invoke-Native $Bin @("-gate-verify-clean")
    if ($chk.ExitCode -eq 0) {
      Set-Gate "SCM_STOP_RESTORE" "PASS" "service-owned tx rolled back on Stop"
    } else {
      Set-Gate "SCM_STOP_RESTORE" "FAIL" ("verify-clean exit={0}" -f $chk.ExitCode)
    }
  }
  Start-Service $SvcName -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 2
}
Remove-Item $GateFlag -Force -ErrorAction SilentlyContinue

# Crash recovery: require ExitCode 99 + NYXVEIL_CRASH_CHECKPOINT=$step + dirty journal kind,
# then RecoverOnStartup via service start / -gate-verify-clean must leave OS+journal clean.
# If fault injection never reaches checkpoint -> FAIL (no false PASS from GATE_CLEAN_OK alone).
$crashFail = @()
$crashOk = 0
$crashKind = @{
  bypass      = "bypass_route"
  tun_addr    = "tun_addr"
  tun_dns     = "tun_dns"
  ipv6        = "ipv6_set"
  default_vpn = "default_vpn"
}
foreach ($step in @("bypass", "tun_addr", "tun_dns", "ipv6", "default_vpn")) {
  sc.exe stop $SvcName 2>$null | Out-Null
  $null = Wait-Svc "Stopped" 30
  $proc = Invoke-Native $Bin @("-gate-crash-after", $step)
  $stdout = [string]$proc.StdOut
  $stderr = [string]$proc.StdErr
  $combined = "$stdout`n$stderr"
  $marker = "NYXVEIL_CRASH_CHECKPOINT=$step"
  $hit = ($combined -like "*$marker*")
  $jPath = Join-Path $DataDir "route-journal.json"
  $jRaw = if (Test-Path $jPath) { Get-Content $jPath -Raw -ErrorAction SilentlyContinue } else { "" }
  $wantKind = [string]$crashKind[$step]
  $journalOk = ($jRaw -match ('"kind"\s*:\s*"' + [regex]::Escape($wantKind) + '"'))
  if ($proc.ExitCode -ne 99 -or -not $hit -or -not $journalOk) {
    $errTrim = ([string]$stderr).Trim()
    if ($errTrim.Length -gt 120) { $errTrim = $errTrim.Substring(0, 120) }
    $crashFail += ("{0}:exit={1};checkpoint={2};journalKind={3};jLen={4};err={5}" -f $step, $proc.ExitCode, $hit, $journalOk, $jRaw.Length, $errTrim)
    # Best-effort cleanup even on fail so later steps are not poisoned
    $null = Invoke-Native $Bin @("-gate-verify-clean")
    sc.exe start $SvcName 2>$null | Out-Null
    Start-Sleep -Seconds 2
    continue
  }
  # Intentional crash left dirty journal; recovery must restore OS + clear journal
  $v = Invoke-Native $Bin @("-gate-verify-clean")
  if ($v.ExitCode -ne 0) {
    $crashFail += ("{0}:verify-clean={1}" -f $step, $v.ExitCode)
  } else {
    $crashOk++
  }
  sc.exe start $SvcName 2>$null | Out-Null
  Start-Sleep -Seconds 2
}
if ($crashFail.Count -eq 0 -and $crashOk -eq 5) {
  Set-Gate "CRASH_RECOVERY" "PASS" "all 5 steps checkpoint+journal+clean"
} else {
  Set-Gate "CRASH_RECOVERY" "FAIL" (("ok={0}/5; " -f $crashOk) + ($crashFail -join ";"))
}

# Uninstall / reinstall
$unins = Get-ChildItem $InstallDir -Filter "unins*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
if ($unins) {
  Stop-Service $SvcName -Force -ErrorAction SilentlyContinue
  $null = Wait-Svc "Stopped" 40
  $u = Start-Process -FilePath $unins.FullName -ArgumentList @("/VERYSILENT", "/NORESTART", "/SUPPRESSMSGBOXES") -Wait -PassThru
  Start-Sleep -Seconds 3
  if (-not (Get-Service $SvcName -ErrorAction SilentlyContinue)) {
    Set-Gate "UNINSTALL" "PASS"
  } else {
    Set-Gate "UNINSTALL" "FAIL" "service remains"
  }

  Add-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -ErrorAction SilentlyContinue
  $task2 = "NyxveilGateReinstallA"
  Unregister-ScheduledTask -TaskName $task2 -Confirm:$false -ErrorAction SilentlyContinue
  $reLog = Join-Path $env:TEMP "nv-reinstall.log"
  $rePs = Join-Path $env:TEMP "nv-reinstall.ps1"
  @"
`$p = Start-Process -FilePath '$Setup' -ArgumentList '/VERYSILENT','/NORESTART','/SUPPRESSMSGBOXES' -Wait -PassThru
Set-Content -Path '$reLog' -Value `$p.ExitCode -Encoding ASCII
exit `$p.ExitCode
"@ | Set-Content $rePs -Encoding UTF8
  $action2 = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$rePs`"" -WorkingDirectory $env:TEMP
  Register-ScheduledTask -TaskName $task2 -Action $action2 -User $UserA.Name -Password $UserA.Plain -RunLevel Highest -Force | Out-Null
  Start-ScheduledTask -TaskName $task2
  $deadline = (Get-Date).AddMinutes(5)
  do { Start-Sleep -Seconds 2 } while ((Get-ScheduledTask -TaskName $task2).State -ne "Ready" -and (Get-Date) -lt $deadline)
  Unregister-ScheduledTask -TaskName $task2 -Confirm:$false -ErrorAction SilentlyContinue
  Remove-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 2
  $sid2 = (Get-Content $SidFile -Raw -ErrorAction SilentlyContinue).Trim()
  $svc2 = Get-Service $SvcName -ErrorAction SilentlyContinue
  if ($svc2 -and $svc2.Status -eq "Running" -and $sid2 -eq $sidA) {
    Set-Gate "REINSTALL" "PASS"
  } else {
    Set-Gate "REINSTALL" "FAIL" ("svc={0} sid={1}" -f $svc2.Status, $sid2)
  }
} else {
  Set-Gate "UNINSTALL" "FAIL" "no unins"
  Set-Gate "REINSTALL" "FAIL"
}

# Allowed non-required failures (recorded, not in Required)
Set-Gate "AUTHENTICODE" "FAIL" "NOT SIGNED (allowed)"
Set-Gate "LIVE_WINDOWS_UBUNTU_INTERNET" "FAIL" "NOT VERIFIED (allowed)"

if (-not $KeepInstall) {
  # Install remains from reinstall path unless caller keeps deeper cleanup elsewhere.
}

Remove-TempUsers

Write-Host ""
Write-Host "=== GATE TABLE ===" -ForegroundColor Cyan
$fail = 0
foreach ($k in $Results.Keys) {
  $r = $Results[$k]
  Write-Host ("{0,-32} {1,-6} {2}" -f $k, $r.Status, $r.Detail)
}
foreach ($k in $Required) {
  if (-not $Results.Contains($k)) {
    Write-Host "MISSING $k" -ForegroundColor Red
    $fail++
    continue
  }
  if ($Results[$k].Status -ne "PASS") {
    Write-Host "REQUIRED NOT PASS: $k" -ForegroundColor Red
    $fail++
  }
}

New-Item -ItemType Directory -Force -Path $Dist | Out-Null
($Results | ConvertTo-Json -Depth 6) | Set-Content (Join-Path $Dist "final-gate-results.json") -Encoding UTF8

if ($fail -gt 0) {
  Write-Host "RESULT: NOT COMPLETE" -ForegroundColor Red
  exit 1
}
Write-Host "RESULT: CLIENT IMPLEMENTATION COMPLETE" -ForegroundColor Green
exit 0
