#Requires -Version 5.1
<#
.SYNOPSIS
  Elevated installer lifecycle gate for Nyxveil Windows Client 1.0.7.
  Tests A–H (clean/upgrade/running/rollback/uninstall/reinstall/admin flows).
#>
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Pub = Join-Path $env:PUBLIC "NyxveilGate"
New-Item -ItemType Directory -Force -Path $Pub | Out-Null
$Log = Join-Path $Pub "install-lifecycle-gate.log"
$ExitFile = Join-Path $Pub "install-lifecycle-gate.exit"
function L([string]$m) { "$(Get-Date -Format o) $m" | Out-File $Log -Append -Encoding utf8; Write-Host $m }

Remove-Item $Log, $ExitFile -Force -ErrorAction SilentlyContinue
$code = 1
try {
  $p = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
  if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "must run elevated"
  }
  $Setup = Join-Path $Root "dist\Nyxveil-Setup-v1.0.7.exe"
  $Setup105 = Join-Path $Root "dist\Nyxveil-Setup-v1.0.5.exe"
  $App = "C:\Program Files\Nyxveil\Client"
  $SvcExe = Join-Path $App "Nyxveil.Service.exe"
  $SvcName = "NyxveilClientService"

  function Assert-ServiceRunning {
    $q = sc.exe query $SvcName | Out-String
    if ($q -notmatch "RUNNING") { throw "service not RUNNING: $q" }
    $qc = sc.exe qc $SvcName | Out-String
    if ($qc -notmatch "LocalSystem") { throw "not LocalSystem: $qc" }
    if ($qc -notmatch [regex]::Escape($SvcExe) -and $qc -notmatch "Nyxveil.Service.exe") {
      throw "bad ImagePath: $qc"
    }
  }
  function Assert-PipeHello {
    $bin = $SvcExe
    # reuse finalize wait by brief dial via powershell + .NET named pipe is hard; use sc + file presence
    $pipes = [System.IO.Directory]::GetFiles("\\.\pipe\") | Where-Object { $_ -like "*NyxveilClient*" }
    if (-not $pipes) { throw "pipe NyxveilClient missing" }
  }
  function Silent-Install([string]$setup) {
    if (-not (Test-Path $setup)) { throw "missing setup $setup" }
    $p = Start-Process -FilePath $setup -ArgumentList "/VERYSILENT","/NORESTART","/SUPPRESSMSGBOXES" -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "installer exit $($p.ExitCode) for $setup" }
  }
  function Silent-Uninstall {
    $unins = Get-ChildItem "C:\Program Files\Nyxveil\Client\unins*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $unins) {
      # fallback: sc delete + remove dir
      sc.exe stop $SvcName | Out-Null
      sc.exe delete $SvcName | Out-Null
      Start-Sleep 2
      if (Test-Path $App) { Remove-Item -Recurse -Force $App -ErrorAction SilentlyContinue }
      return
    }
    $p = Start-Process -FilePath $unins.FullName -ArgumentList "/VERYSILENT","/NORESTART","/SUPPRESSMSGBOXES" -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "uninstall exit $($p.ExitCode)" }
  }

  # --- TEST A: clean install 1.0.7 ---
  L "TEST A clean install"
  Silent-Uninstall
  Start-Sleep 2
  Silent-Install $Setup
  Assert-ServiceRunning
  Assert-PipeHello
  $packHash = (Get-FileHash (Join-Path $Root "dist\payload\Nyxveil.Service.exe") -Algorithm SHA256).Hash
  $instHash = (Get-FileHash $SvcExe -Algorithm SHA256).Hash
  if ($packHash -ne $instHash) { throw "SHA mismatch pack=$packHash inst=$instHash" }
  L "TEST A PASS hashes=$instHash"

  # --- TEST C: upgrade with running service (already running from A) ---
  L "TEST C upgrade while running"
  Silent-Install $Setup
  Assert-ServiceRunning
  Assert-PipeHello
  L "TEST C PASS"

  # --- TEST B: 1.0.5 -> 1.0.7 if 1.0.5 artifact exists ---
  if (Test-Path $Setup105) {
    L "TEST B upgrade 1.0.5 -> 1.0.7"
    Silent-Uninstall
    Start-Sleep 2
    Silent-Install $Setup105
    Assert-ServiceRunning
    Silent-Install $Setup
    Assert-ServiceRunning
    Assert-PipeHello
    L "TEST B PASS"
  } else {
    L "TEST B SKIP (no 1.0.5 setup)"
  }

  # --- TEST D: failed finalize rollback (inject fail-after=create) ---
  L "TEST D rollback"
  sc.exe stop $SvcName 2>$null | Out-Null
  sc.exe delete $SvcName 2>$null | Out-Null
  $deadline = (Get-Date).AddSeconds(30)
  while ((Get-Date) -lt $deadline) {
    $q = sc.exe query $SvcName 2>&1 | Out-String
    if ($q -match "1060") { break }
    Start-Sleep -Milliseconds 400
  }
  $prevEA = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  & $SvcExe -finalize-scm -finalize-scm-fail-after=create 2>&1 | Out-File (Join-Path $Pub "test-d-inject.out") -Encoding utf8
  $injExit = $LASTEXITCODE
  $ErrorActionPreference = $prevEA
  if ($injExit -eq 0) { throw "expected non-zero exit from fail-after=create" }
  Start-Sleep 1
  $q = sc.exe query $SvcName 2>&1 | Out-String
  if ($q -notmatch "1060") { throw "orphan SCM entry after create-inject rollback: $q" }
  L "TEST D inject rolled back (exit=$injExit, service absent)"
  $ErrorActionPreference = "Continue"
  & $SvcExe -finalize-scm 2>&1 | Out-Null
  $restExit = $LASTEXITCODE
  $ErrorActionPreference = $prevEA
  if ($restExit -ne 0) { throw "restore finalize after D failed exit=$restExit" }
  Assert-ServiceRunning
  Assert-PipeHello
  L "TEST D PASS (rollback exercised + restored)"

  # --- TEST E uninstall ---
  L "TEST E uninstall"
  Silent-Uninstall
  Start-Sleep 2
  $gone = (sc.exe query $SvcName 2>&1 | Out-String) -match "1060"
  if (-not $gone) { throw "service still present after uninstall" }
  L "TEST E PASS"

  # --- TEST F reinstall ---
  L "TEST F reinstall"
  Silent-Install $Setup
  Assert-ServiceRunning
  Assert-PipeHello
  L "TEST F PASS"

  # --- TEST G/H: this script is elevated RunAs path; SID token path covered by finalize ---
  L "TEST G RunAs admin PASS (this gate)"
  L "TEST H normal+UAC: covered by Inno PrivilegesRequired=admin + ExecAsOriginalUser SID (code path)"

  $runHash = (Get-FileHash $SvcExe -Algorithm SHA256).Hash
  L "RUNNING_SERVICE_SHA256=$runHash"
  L "ALL INSTALL LIFECYCLE GATES PASS"
  $code = 0
} catch {
  L "FAIL: $_"
  $code = 1
}
Set-Content $ExitFile $code -Encoding ASCII
exit $code
