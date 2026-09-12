#Requires -Version 5.1
<#
.SYNOPSIS
  Runs non-production Control Plane contract gates.

.DESCRIPTION
  This harness never emits FULL_OPERATOR_E2E. That production verdict is owned
  exclusively by aggregate-production-gates.ps1 after all mandatory real-world
  evidence is present. Release mode is reserved until it has a real button path.
#>
[CmdletBinding()]
param(
    [ValidateSet('lab', 'release')][string]$GateMode = 'lab'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$failures = [Collections.Generic.List[string]]::new()
$partials = [Collections.Generic.List[string]]::new()

function Write-Step([string]$Name, [string]$Detail) {
    Write-Host ("CONTRACT_OPERATOR_STEP={0} {1}" -f $Name, $Detail)
}

function Add-Failure([string]$Reason) {
    $script:failures.Add($Reason)
    Write-Warning $Reason
}

function Add-Partial([string]$Reason) {
    $script:partials.Add($Reason)
    Write-Warning $Reason
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-DotNetTest {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Project,
        [string]$Filter = ''
    )
    Write-Step $Name "project=$Project"
    $args = @('test', $Project, '-c', 'Release', '--logger', "trx;LogFileName=$Name.trx")
    if ($Filter) { $args += @('--filter', $Filter) }
    & dotnet @args
    if ($LASTEXITCODE -ne 0) {
        Add-Failure "$Name failed with exit code $LASTEXITCODE"
    }
}

try {
    if ($GateMode -eq 'lab') {
        Write-Step 'lab_begin' 'non-destructive test gates'

        Invoke-DotNetTest -Name 'lease-unit' `
            -Project 'tests\Nyxveil.ControlPlane.UnitTests\Nyxveil.ControlPlane.UnitTests.csproj' `
            -Filter 'FullyQualifiedName~NodeCommandLeaseTests'
        Invoke-DotNetTest -Name 'lease-integration' `
            -Project 'tests\Nyxveil.ControlPlane.IntegrationTests\Nyxveil.ControlPlane.IntegrationTests.csproj' `
            -Filter 'FullyQualifiedName~ManagementCommandConcurrencyTests'

        if (Test-IsAdministrator) {
            Write-Step 'windows_service_create' 'administrator detected'
            & (Join-Path $PSScriptRoot 'test-windows-service-create.ps1')
            if ($LASTEXITCODE -ne 0) {
                Add-Failure "Windows service CreateService test failed with exit code $LASTEXITCODE"
            }
        }
        else {
            Add-Partial 'Windows service CreateService test skipped: administrator token unavailable.'
        }

        $browserProject = 'tests\Nyxveil.ControlPlane.BrowserE2E\Nyxveil.ControlPlane.BrowserE2E.csproj'
        Write-Step 'browser_e2e' "project=$browserProject"
        $browserOutput = @(& dotnet test $browserProject -c Release `
            --logger 'trx;LogFileName=browser-e2e.trx' 2>&1)
        $browserExit = $LASTEXITCODE
        $browserOutput | ForEach-Object { Write-Host $_ }
        if ($browserExit -ne 0) {
            $text = ($browserOutput -join "`n")
            if ($text -match "(?i)Executable doesn't exist|playwright install|browser.*not.*installed") {
                Add-Partial 'Browser E2E skipped: Playwright Chromium is not installed. Run the generated playwright.ps1 install chromium command.'
            }
            else {
                Add-Failure "Browser E2E failed with exit code $browserExit"
            }
        }
    }
    else {
        # Reserved until this harness has a real browser-button production path.
        # FULL_OPERATOR_E2E is emitted only by aggregate-production-gates.ps1 after
        # every mandatory production evidence key has passed.
        Add-Failure 'Release mode is reserved and has no real button path. Run real-cp-button-update-e2e.ps1 and aggregate production evidence instead.'
    }
}
catch {
    Add-Failure $_.Exception.Message
}

if ($failures.Count -gt 0) {
    Write-Output ("CONTRACT_OPERATOR_GATES=FAIL reasons={0}" -f ($failures -join ' | '))
    exit 1
}
if ($partials.Count -gt 0) {
    Write-Output ("CONTRACT_OPERATOR_GATES=PARTIAL reasons={0}" -f ($partials -join ' | '))
    exit 1
}
Write-Output 'CONTRACT_OPERATOR_GATES=PASS'
exit 0
