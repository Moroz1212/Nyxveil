#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Real Windows SCM integration test for Nyxveil service BinaryPathName creation.

.DESCRIPTION
  Exercises the SAME production helpers (Get-NyxveilServiceBinaryPathName /
  New-NyxveilWindowsService / Assert-NyxveilWindowsServiceConfig / Remove-NyxveilWindowsService)
  against a uniquely named temporary service whose executable path contains spaces and
  includes an extra --service argument. This is not a static source string check.

  Also proves the LIVE 1.3.7 failure mechanism: PowerShell 5.1 + sc.exe create with
  embedded-quote BinaryPathName produces a malformed CreateProcess command line.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = $PSScriptRoot
Import-Module (Join-Path $scriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

function Fail([string]$Msg) {
    Write-Output "WINDOWS_SERVICE_CREATE_TEST=FAIL"
    throw $Msg
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Fail 'Administrator required for SCM CreateService integration test.'
}

$stamp = [guid]::NewGuid().ToString('N').Substring(0, 12)
$serviceName = "NyxveilCpSvcTest$stamp"
$displayName = "Nyxveil CP Service Test $stamp"
$work = Join-Path $env:TEMP ("nyxveil-svc-test-" + $stamp)
$spaceDir = Join-Path $work 'Program Files\Nyxveil\ControlPlane\updater'
New-Item -ItemType Directory -Force -Path $spaceDir | Out-Null

# Tiny console host used as service ImagePath target (does not need to implement ServiceMain for create/config asserts).
$exePath = Join-Path $spaceDir 'Nyxveil.ControlPlane.Updater.exe'
$csc = @(
    "${env:WINDIR}\Microsoft.NET\Framework64\v4.0.30319\csc.exe",
    "${env:WINDIR}\Microsoft.NET\Framework\v4.0.30319\csc.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $csc) { Fail 'csc.exe not found' }
$cs = Join-Path $work 'host.cs'
@'
using System;
using System.Threading;
static class P {
  static int Main(string[] args) {
    // Stay alive briefly if SCM ever starts us; test primarily validates create/config.
    Thread.Sleep(TimeSpan.FromSeconds(30));
    return 0;
  }
}
'@ | Set-Content -LiteralPath $cs -Encoding ASCII
& $csc /nologo /out:$exePath $cs | Out-Null
if (-not (Test-Path -LiteralPath $exePath)) { Fail 'failed to compile test host exe' }

# --- Prove LIVE 1.3.7 sc.exe quoting defect (diagnostic only; must not be used for create) ---
$trampCs = Join-Path $work 'DumpCmd.cs'
$trampExe = Join-Path $work 'DumpCmd.exe'
@'
using System;
using System.Runtime.InteropServices;
class Dump {
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode)]
  static extern IntPtr GetCommandLineW();
  static int Main() {
    Console.WriteLine(Marshal.PtrToStringUni(GetCommandLineW()));
    return 0;
  }
}
'@ | Set-Content -LiteralPath $trampCs -Encoding ASCII
& $csc /nologo /out:$trampExe $trampCs | Out-Null
$legacyBinPath = "`"$exePath`" --service"
$legacyCmd = & $trampExe create $serviceName binPath= $legacyBinPath DisplayName= $displayName start= auto obj= LocalSystem
Write-Host "LEGACY_PS51_SC_CMDLINE=$legacyCmd"
if ($legacyCmd -notmatch 'binPath= ""') {
    Fail 'Expected PowerShell 5.1 legacy sc.exe cmdline to contain malformed binPath= ""... pattern'
}
Write-Host 'LEGACY_SC_QUOTING_DEFECT=CONFIRMED'

$expectedPathName = Get-NyxveilServiceBinaryPathName -ExePath $exePath -BinPathArguments '--service'
Write-Host "EXPECTED_PATHNAME=$expectedPathName"
if ($expectedPathName -notmatch '(?i)--service') { Fail 'BinaryPathName missing --service' }
if ($expectedPathName -notmatch ' ') { Fail 'BinaryPathName should include spaced path' }

try {
    # demand start so we can assert config without requiring a durable ServiceMain implementation.
    New-NyxveilWindowsService -ServiceName $serviceName -ExePath $exePath `
        -ServiceAccount 'LocalSystem' `
        -DisplayName $displayName `
        -BinPathArguments '--service' `
        -StartType demand

    $snap = Assert-NyxveilWindowsServiceConfig -ServiceName $serviceName `
        -ExpectedBinaryPathName $expectedPathName `
        -ExpectedDisplayName $displayName `
        -ExpectedStartName 'LocalSystem' `
        -ExpectedStartMode Manual

    Write-Host "CREATED_PATHNAME=$($snap.PathName)"
    Write-Host "CREATED_STARTNAME=$($snap.StartName)"
    Write-Host "CREATED_STARTMODE=$($snap.StartMode)"
    Write-Host "CREATED_STATE=$($snap.State)"

    if ($snap.PathName -cne $expectedPathName) {
        Fail "PathName exact mismatch have=$($snap.PathName) want=$expectedPathName"
    }
    if ($snap.PathName -notlike '*--service*') {
        Fail '--service missing from PathName'
    }

    # Idempotent reconfigure
    New-NyxveilWindowsService -ServiceName $serviceName -ExePath $exePath `
        -ServiceAccount 'LocalSystem' `
        -DisplayName $displayName `
        -BinPathArguments '--service' `
        -StartType demand

    $snap2 = Assert-NyxveilWindowsServiceConfig -ServiceName $serviceName `
        -ExpectedBinaryPathName $expectedPathName `
        -ExpectedDisplayName $displayName `
        -ExpectedStartName 'LocalSystem' `
        -ExpectedStartMode Manual
    if ($snap2.PathName -cne $expectedPathName) {
        Fail 'PathName changed after idempotent reconfigure'
    }

    Write-Output 'WINDOWS_SERVICE_CREATE_TEST=PASS'
}
finally {
    try { Remove-NyxveilWindowsService -ServiceName $serviceName } catch { Write-Warning $_ }
    try { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue } catch { }
}
