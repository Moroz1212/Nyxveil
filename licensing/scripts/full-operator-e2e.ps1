#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('lab', 'release')][string]$GateMode = 'lab',
    [string]$Published138ZipPath = '',
    [string]$Candidate139ZipPath = '',
    [string]$InstallDir = 'C:\Program Files\Nyxveil\ControlPlane',
    [int]$Port = 8443,
    [string]$PublicHostname = 'localhost',
    [string]$DatabaseServer = '(localdb)\MSSQLLocalDB',
    [string]$Database = 'NyxveilControlPlane_OperatorE2E',
    [string]$AdminUser = 'operator-e2e@example.test',
    [securestring]$AdminPassword
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = Split-Path -Parent $PSScriptRoot
$failures = [Collections.Generic.List[string]]::new()
$partials = [Collections.Generic.List[string]]::new()

function Write-Step([string]$Name, [string]$Detail) {
    Write-Host ("FULL_OPERATOR_STEP={0} {1}" -f $Name, $Detail)
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

function Assert-ReleaseZip {
    param([string]$Path, [string]$ExpectedVersion)
    if (-not $Path) { throw "ZIP path for $ExpectedVersion is required." }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "ZIP not found: $Path"
    }
    $resolved = (Resolve-Path -LiteralPath $Path).Path
    $temp = Join-Path ([IO.Path]::GetTempPath()) ("nyxveil-operator-manifest-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temp | Out-Null
    try {
        Expand-Archive -LiteralPath $resolved -DestinationPath $temp -Force
        $manifestPath = Join-Path $temp 'release-manifest.json'
        if (-not (Test-Path -LiteralPath $manifestPath)) {
            throw "$resolved has no release-manifest.json"
        }
        $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
        if ([string]$manifest.version -ne $ExpectedVersion) {
            throw "$resolved version is '$($manifest.version)', expected '$ExpectedVersion'"
        }
    }
    finally {
        Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    }
    return $resolved
}

function Invoke-ChildPowerShell {
    param(
        [Parameter(Mandatory = $true)][string]$Script,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $Script @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$(Split-Path -Leaf $Script) failed: exit=$LASTEXITCODE"
    }
}

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
        --logger 'trx;LogFileName=browser-e2e.trx' 2>&1 | Tee-Object -Variable browserLines)
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
    Write-Step 'release_warning' 'DESTRUCTIVE: requires a disposable elevated Windows lab host.'
    try {
        if (-not (Test-IsAdministrator)) {
            throw 'Release mode requires an elevated administrator PowerShell.'
        }
        if ($null -eq $AdminPassword) {
            throw 'Release mode requires -AdminPassword as SecureString.'
        }
        $stableZip = Assert-ReleaseZip -Path $Published138ZipPath -ExpectedVersion '1.3.8'
        $candidateZip = Assert-ReleaseZip -Path $Candidate139ZipPath -ExpectedVersion '1.3.9'
        if (Get-Service -Name 'NyxveilControlPlane' -ErrorAction SilentlyContinue) {
            throw 'NyxveilControlPlane already exists; release mode requires a disposable clean host.'
        }

        $work = Join-Path ([IO.Path]::GetTempPath()) ("nyxveil-full-operator-" + [guid]::NewGuid().ToString('N'))
        $stable = Join-Path $work '1.3.8'
        $candidate = Join-Path $work '1.3.9'
        New-Item -ItemType Directory -Path $stable, $candidate -Force | Out-Null
        try {
            Write-Step 'extract_1.3.8' $stableZip
            Expand-Archive -LiteralPath $stableZip -DestinationPath $stable -Force
            Write-Step 'extract_1.3.9' $candidateZip
            Expand-Archive -LiteralPath $candidateZip -DestinationPath $candidate -Force

            Write-Step 'install_1.3.8' 'Fresh install, real SCM and LocalDB path'
            $passwordPath = Join-Path $work 'admin-password.clixml'
            $installWrapper = Join-Path $work 'invoke-install.ps1'
            $AdminPassword | Export-Clixml -LiteralPath $passwordPath
            @'
param(
    [string]$InstallScript, [string]$PublishDir, [string]$InstallDir,
    [int]$Port, [string]$PublicHostname, [string]$DatabaseServer,
    [string]$Database, [string]$AdminUser, [string]$PasswordPath
)
$ErrorActionPreference = 'Stop'
$password = Import-Clixml -LiteralPath $PasswordPath
& $InstallScript -InstallMode Fresh -PublishDir $PublishDir -InstallDir $InstallDir `
    -Port $Port -PublicHostname $PublicHostname -GenerateSelfSignedCertificate `
    -DatabaseServer $DatabaseServer -Database $Database -DatabaseAuth Windows `
    -TrustSqlServerCertificate -AdminUser $AdminUser -AdminPassword $password `
    -NonInteractive -SkipFirewall
'@ | Set-Content -LiteralPath $installWrapper -Encoding UTF8
            Invoke-ChildPowerShell -Script $installWrapper -Arguments @(
                '-InstallScript', (Join-Path $stable 'scripts\install-windows.ps1'),
                '-PublishDir', (Join-Path $stable 'publish'),
                '-InstallDir', $InstallDir,
                '-Port', [string]$Port,
                '-PublicHostname', $PublicHostname,
                '-DatabaseServer', $DatabaseServer,
                '-Database', $Database,
                '-AdminUser', $AdminUser,
                '-PasswordPath', $passwordPath
            )

            Write-Step 'update_1.3.8_to_1.3.9' 'production-deploy backup, rehearsal, update and health'
            Invoke-ChildPowerShell `
                -Script (Join-Path $candidate 'scripts\production-deploy.ps1') `
                -Arguments @(
                    '-PublishDir', (Join-Path $candidate 'publish'),
                    '-InstallDir', $InstallDir,
                    '-ReleaseZip', $candidateZip
                )

            Write-Step 'post_update_gate' 'production installed-state and schema validation'
            Invoke-ChildPowerShell `
                -Script (Join-Path $candidate 'scripts\production-gate.ps1') `
                -Arguments @(
                    '-GateMode', 'production',
                    '-InstallDir', $InstallDir,
                    '-CheckDatabase'
                )
        }
        finally {
            Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    catch {
        Add-Failure $_.Exception.Message
    }
}

if ($failures.Count -gt 0) {
    Write-Output ("FULL_OPERATOR_E2E=FAIL reasons={0}" -f ($failures -join ' | '))
    exit 1
}
if ($partials.Count -gt 0) {
    Write-Output ("FULL_OPERATOR_E2E=PARTIAL reasons={0}" -f ($partials -join ' | '))
    if ($GateMode -eq 'release') { exit 1 }
    exit 0
}
Write-Output 'FULL_OPERATOR_E2E=PASS'
exit 0
