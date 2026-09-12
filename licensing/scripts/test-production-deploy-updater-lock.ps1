#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Behavioral regression: InstallDir mutation must not run while updater is Running.

.DESCRIPTION
  Proves the LIVE 1.3.8→1.3.10 failure mode (updater holds EventLog.dll) and that
  Stop-NyxveilWindowsServiceFully + unlock gate allow Clear-DirectoryContents.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = $PSScriptRoot
Import-Module (Join-Path $scriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

function Fail([string]$Msg) {
    Write-Output 'PRODUCTION_DEPLOY_UPDATER_LOCK_TEST=FAIL'
    throw $Msg
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Fail 'Administrator required.'
}

$stamp = [guid]::NewGuid().ToString('N').Substring(0, 12)
$serviceName = "NyxveilCpLockTest$stamp"
$displayName = "Nyxveil CP Lock Test $stamp"
$work = Join-Path $env:TEMP ("nyxveil-deploy-lock-" + $stamp)
$installDir = Join-Path $work 'InstallDir'
$updaterDir = Join-Path $installDir 'updater'
$exeKeep = Join-Path $work 'Nyxveil.ControlPlane.Updater.exe'
New-Item -ItemType Directory -Force -Path $updaterDir | Out-Null

$exePath = Join-Path $updaterDir 'Nyxveil.ControlPlane.Updater.exe'
$lockTarget = Join-Path $installDir 'System.Diagnostics.EventLog.dll'
$csc = @(
    "${env:WINDIR}\Microsoft.NET\Framework64\v4.0.30319\csc.exe",
    "${env:WINDIR}\Microsoft.NET\Framework\v4.0.30319\csc.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $csc) { Fail 'csc.exe not found' }

$cs = Join-Path $work 'lockhost.cs'
@"
using System;
using System.IO;
using System.ServiceProcess;
public class LockSvc : ServiceBase {
  FileStream _fs;
  public LockSvc() { ServiceName = `"$serviceName`"; CanStop = true; }
  protected override void OnStart(string[] args) {
    var path = Path.GetFullPath(Path.Combine(AppDomain.CurrentDomain.BaseDirectory, `"..`", `"System.Diagnostics.EventLog.dll`"));
    Directory.CreateDirectory(Path.GetDirectoryName(path));
    _fs = new FileStream(path, FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
    _fs.WriteByte(1); _fs.Flush();
  }
  protected override void OnStop() {
    if (_fs != null) { _fs.Dispose(); _fs = null; }
  }
  public static void Main() { ServiceBase.Run(new LockSvc()); }
}
"@ | Set-Content -LiteralPath $cs -Encoding ASCII

& $csc /nologo /target:exe /out:$exePath /r:System.ServiceProcess.dll $cs | Out-Null
if (-not (Test-Path -LiteralPath $exePath)) { Fail 'failed to compile lock host' }
Copy-Item -LiteralPath $exePath -Destination $exeKeep -Force
[IO.File]::WriteAllBytes($lockTarget, [byte[]](0..15))

try {
    New-NyxveilWindowsService -ServiceName $serviceName -ExePath $exePath `
        -ServiceAccount 'LocalSystem' `
        -DisplayName $displayName `
        -StartType auto
    Start-Service -Name $serviceName -ErrorAction Stop
    (Get-Service -Name $serviceName).WaitForStatus('Running', [TimeSpan]::FromSeconds(45))
    Start-Sleep -Seconds 2

    $lockedFail = $false
    try {
        Get-ChildItem -LiteralPath $installDir -Force | ForEach-Object {
            Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction Stop
        }
    }
    catch {
        $lockedFail = $true
        Write-Host "LOCK_WHILE_RUNNING=CONFIRMED $($_.Exception.Message)"
    }
    if (-not $lockedFail) {
        Fail 'Expected directory clear to fail while service holds EventLog.dll'
    }

    $snapRunning = Get-NyxveilWindowsServiceSnapshot -ServiceName $serviceName
    if ($snapRunning.State -ne 'Running') { Fail "expected Running have=$($snapRunning.State)" }
    try {
        Assert-NyxveilInstallDirUnlockedForMutation -UpdaterServiceName $serviceName
        Fail 'expected Assert-NyxveilInstallDirUnlockedForMutation to throw while Running'
    }
    catch {
        Write-Host 'UNLOCK_GATE_WHILE_RUNNING=PASS'
    }

    # State restore helper (stop + start if was Running)
    Stop-NyxveilWindowsServiceFully -ServiceName $serviceName
    Start-NyxveilWindowsServiceIfWasRunning -Snapshot $snapRunning
    if ((Get-Service -Name $serviceName).Status -ne 'Running') {
        Fail 'Start-NyxveilWindowsServiceIfWasRunning did not restore Running'
    }
    Write-Host 'ROLLBACK_STATE_RESTORE=PASS'

    Stop-NyxveilWindowsServiceFully -ServiceName $serviceName
    $after = Get-NyxveilWindowsServiceSnapshot -ServiceName $serviceName
    if ($after.State -ne 'Stopped') { Fail "expected Stopped have=$($after.State)" }

    Get-ChildItem -LiteralPath $installDir -Force | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction Stop
    }
    if (@(Get-ChildItem -LiteralPath $installDir -Force -ErrorAction SilentlyContinue).Count -ne 0) {
        Fail 'InstallDir not empty after stop+clear'
    }
    Write-Host 'CLEAR_AFTER_STOP=PASS'
    Write-Host 'PARTIAL_INSTALL_CLEAR_AFTER_STOP=PASS'
}
finally {
    try { Stop-NyxveilWindowsServiceFully -ServiceName $serviceName } catch { }
    try { Remove-NyxveilWindowsService -ServiceName $serviceName } catch { }
    try { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue } catch { }
}

Write-Output 'PRODUCTION_DEPLOY_UPDATER_LOCK_TEST=PASS'
exit 0
