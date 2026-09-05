#Requires -Version 5.1
#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Install Nyxveil Control Plane as a Windows Service.

.DESCRIPTION
  Interactive or non-interactive production installer:
  port selection, TLS certificate (runtime always Store+thumbprint), MSSQL bootstrap,
  DPAPI secrets, firewall rule, Windows Service, first admin CLI, health gate.

  Modes:
    Fresh   — default; fails if service already exists unless -Force
    Repair  — reconfigure/repair existing install; preserves license-kek.dpapi
    Upgrade — same secret preservation as Repair; for in-place binary refresh via install path

  Fresh install order (P0):
    validate → bootstrap DB → copy binaries/appsettings → create service STOPPED →
    service SID → service Environment=Production → directory ACLs → cert private-key ACL →
    SQL login for service → verify → admin create → Start-Service → health → commit

.NOTES
  Never drops an existing production database on failure.
  Never stops foreign services occupying a port.
  Never sets machine-wide ASPNETCORE_ENVIRONMENT / DOTNET_ENVIRONMENT.
  Default HTTPS port suggestion is 8443 (any free port is valid).
#>
[CmdletBinding()]
param(
    [ValidateSet('Fresh', 'Repair', 'Upgrade')]
    [string]$InstallMode = 'Fresh',
    [switch]$Force,
    [int]$Port = 0,
    [string]$BindAddress = '0.0.0.0',
    [string]$PublicHostname = '',
    [string]$CertificateThumbprint = '',
    [string]$CertificatePath = '',
    [securestring]$CertificatePassword,
    [switch]$GenerateSelfSignedCertificate,
    [string]$DatabaseServer = '',
    [string]$Database = '',
    [ValidateSet('', 'Windows', 'Sql')]$DatabaseAuth = '',
    [string]$DatabaseUser = '',
    [securestring]$DatabasePassword,
    [switch]$TrustSqlServerCertificate,
    [string]$ServiceAccount = 'NT SERVICE\NyxveilControlPlane',
    [string]$PublishDir = '',
    [string]$InstallDir = 'C:\Program Files\Nyxveil\ControlPlane',
    [string]$DataDir = '',
    [switch]$NonInteractive,
    [switch]$SkipFirewall,
    [string]$AdminUser = '',
    [securestring]$AdminPassword
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

$script:Rollback = @{
    ServiceCreated   = $false
    ServiceName      = 'NyxveilControlPlane'
    FirewallRuleName = ''
    BinariesCopied   = $false
    InstallDir       = $InstallDir
    BinaryBackup     = ''
}

function Invoke-InstallRollback {
    Write-Warning 'Install failed after mutations — attempting rollback (DB will NOT be dropped)...'
    try {
        # Fresh only: delete service created this run. Repair/Upgrade never delete existing service.
        if ($script:Rollback.ServiceCreated) {
            $svc = Get-Service -Name $script:Rollback.ServiceName -ErrorAction SilentlyContinue
            if ($svc) {
                Stop-Service -Name $script:Rollback.ServiceName -Force -ErrorAction SilentlyContinue
                Invoke-NativeChecked -Name "sc.exe delete $($script:Rollback.ServiceName)" -Script {
                    & sc.exe delete $script:Rollback.ServiceName | Out-Null
                }
                Write-Host "Removed service $($script:Rollback.ServiceName)."
            }
        }
    }
    catch { Write-Warning "Service rollback: $($_.Exception.Message)" }

    try {
        if ($script:Rollback.FirewallRuleName) {
            Remove-NyxveilFirewallRule -RuleName $script:Rollback.FirewallRuleName
            Write-Host "Removed firewall rule $($script:Rollback.FirewallRuleName)."
        }
    }
    catch { Write-Warning "Firewall rollback: $($_.Exception.Message)" }

    try {
        if ($script:Rollback.BinariesCopied -and $script:Rollback.BinaryBackup -and (Test-Path $script:Rollback.BinaryBackup)) {
            if (Test-Path $script:Rollback.InstallDir) {
                Get-ChildItem -LiteralPath $script:Rollback.InstallDir -Force -ErrorAction SilentlyContinue |
                    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
            }
            New-Item -ItemType Directory -Force -Path $script:Rollback.InstallDir | Out-Null
            Backup-DirectoryContents -SourceDir $script:Rollback.BinaryBackup -DestinationDir $script:Rollback.InstallDir
            Write-Host "Restored prior binaries from $($script:Rollback.BinaryBackup)."
        }
        elseif ($script:Rollback.BinariesCopied -and -not $script:Rollback.BinaryBackup) {
            Write-Warning "Binaries were copied this run; manual cleanup of $($script:Rollback.InstallDir) may be required."
        }
    }
    catch { Write-Warning "Binary rollback: $($_.Exception.Message)" }
}

function Read-Default([string]$Prompt, [string]$Default) {
    $v = Read-Host "$Prompt [$Default]"
    if ([string]::IsNullOrWhiteSpace($v)) { return $Default }
    return $v
}

try {
    Assert-Administrator
    Assert-DotNetAspNetRuntime -Major 10

    $paths = Get-DefaultPaths -InstallDir $InstallDir
    if ([string]::IsNullOrWhiteSpace($DataDir)) { $DataDir = $paths.DataDir }
    $LogsDir = $paths.LogsDir
    $SecretsDir = $paths.SecretsDir
    $KeysDir = $paths.KeysDir
    $ServiceName = 'NyxveilControlPlane'
    $script:Rollback.ServiceName = $ServiceName
    $script:Rollback.InstallDir = $InstallDir

    Write-Host "=== Nyxveil Control Plane install ($InstallMode) ==="
    Write-Host 'Default port suggestion is 8443 (any free TCP port is valid).'

    $existingSvc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    $serviceExistedBefore = [bool]$existingSvc
    if ($InstallMode -eq 'Fresh' -and $existingSvc) {
        if (-not $Force) {
            throw "Service '$ServiceName' already exists. Use -InstallMode Repair|Upgrade, or -Force for Fresh overwrite."
        }
        Write-Warning "Fresh + Force: existing service '$ServiceName' will be reconfigured."
    }
    if (($InstallMode -eq 'Repair' -or $InstallMode -eq 'Upgrade') -and -not $existingSvc) {
        Write-Warning "$InstallMode requested but service not found; continuing as a first-time install of binaries/config."
    }

    if ($NonInteractive) {
        Assert-SqlCmdAvailable -Context 'non-interactive install schema apply'
    }
    elseif (-not (Test-SqlCmdAvailable)) {
        Write-Warning 'sqlcmd not found. Schema apply will fail unless you install sqlcmd first.'
        $cont = Read-Host 'Continue anyway? (y/N)'
        if ($cont -notmatch '^[Yy]') { throw 'Aborted: sqlcmd required to apply database schema.' }
    }

    $PublishDir = Get-PublishedBuildDir -PublishDir $PublishDir -NonInteractive:$NonInteractive

    if (-not $NonInteractive) {
        if ([string]::IsNullOrWhiteSpace($InstallDir) -or $InstallDir -eq 'C:\Program Files\Nyxveil\ControlPlane') {
            $InstallDir = Read-Default 'Install path' $InstallDir
            $script:Rollback.InstallDir = $InstallDir
        }
    }

    # -------------------------------------------------------------------------
    # 1) Validate inputs / port / cert hostname / SQL
    # -------------------------------------------------------------------------
    if ($Port -le 0) {
        if ($NonInteractive) { throw '-Port is required in -NonInteractive mode.' }
        do {
            $portText = Read-Default 'HTTPS port' '8443'
            $Port = 0
            [void][int]::TryParse($portText, [ref]$Port)
            try {
                Assert-ValidPort -Port $Port
                if (-not (Test-TcpPortAvailable -Port $Port -BindAddress $BindAddress -AllowServiceName $ServiceName)) {
                    Write-Host 'Choose a different port.'
                    $Port = 0
                }
            }
            catch {
                Write-Host $_.Exception.Message
                $Port = 0
            }
        } while ($Port -le 0)
    }
    else {
        Assert-ValidPort -Port $Port
        if (-not (Test-TcpPortAvailable -Port $Port -BindAddress $BindAddress -AllowServiceName $ServiceName)) {
            if ($NonInteractive) {
                throw "Port $Port is occupied by a foreign process. Install aborted."
            }
            do {
                $portText = Read-Host 'Enter a different HTTPS port'
                $Port = 0
                [void][int]::TryParse($portText, [ref]$Port)
                try {
                    Assert-ValidPort -Port $Port
                    if (-not (Test-TcpPortAvailable -Port $Port -BindAddress $BindAddress -AllowServiceName $ServiceName)) { $Port = 0 }
                }
                catch {
                    Write-Host $_.Exception.Message
                    $Port = 0
                }
            } while ($Port -le 0)
        }
    }

    if ([string]::IsNullOrWhiteSpace($PublicHostname)) {
        if ($NonInteractive) { throw '-PublicHostname is required in -NonInteractive mode.' }
        $PublicHostname = Read-Default 'Public hostname (DNS)' 'localhost'
    }

    if ([string]::IsNullOrWhiteSpace($DatabaseServer)) {
        if ($NonInteractive) { $DatabaseServer = 'localhost' }
        else { $DatabaseServer = Read-Default 'SQL Server' 'localhost' }
    }
    if ([string]::IsNullOrWhiteSpace($Database)) {
        if ($NonInteractive) { $Database = 'NyxveilControlPlane' }
        else { $Database = Read-Default 'Database name' 'NyxveilControlPlane' }
    }
    Assert-ValidDatabaseName -DatabaseName $Database

    if ([string]::IsNullOrWhiteSpace($DatabaseAuth)) {
        if ($NonInteractive) { $DatabaseAuth = 'Windows' }
        else {
            $authIn = Read-Default 'Database auth (Windows|Sql)' 'Windows'
            if ($authIn -match '^[Ss]') { $DatabaseAuth = 'Sql' } else { $DatabaseAuth = 'Windows' }
        }
    }

    if ($DatabaseAuth -eq 'Sql') {
        if ([string]::IsNullOrWhiteSpace($DatabaseUser)) {
            if ($NonInteractive) { throw '-DatabaseUser required for SQL auth in -NonInteractive mode.' }
            $DatabaseUser = Read-Host 'SQL username'
        }
        if (-not $DatabasePassword) {
            if ($NonInteractive) { throw '-DatabasePassword required for SQL auth in -NonInteractive mode.' }
            $DatabasePassword = Read-Host 'SQL password' -AsSecureString
        }
    }

    Test-RemoteWindowsAuthSupported -DatabaseServer $DatabaseServer -DatabaseAuth $DatabaseAuth -ServiceAccount $ServiceAccount

    $trustSqlSpecified = $PSBoundParameters.ContainsKey('TrustSqlServerCertificate')
    $trustSql = Resolve-TrustSqlServerCertificatePolicy `
        -DatabaseServer $DatabaseServer `
        -TrustSqlServerCertificate:$TrustSqlServerCertificate `
        -TrustSqlServerCertificateSpecified:$trustSqlSpecified

    # Certificate: import/create then ALWAYS runtime Mode=Store + thumbprint
    $installCertSource = 'Store'
    $cert = $null
    if ($GenerateSelfSignedCertificate) {
        $installCertSource = 'SelfSigned'
        $cert = New-NyxveilSelfSignedCertificate -DnsName $PublicHostname
        $CertificateThumbprint = $cert.Thumbprint
    }
    elseif ($CertificatePath) {
        $installCertSource = 'Pfx'
        if (-not $CertificatePassword) {
            if ($NonInteractive) { throw '-CertificatePassword required with -CertificatePath in -NonInteractive mode.' }
            $CertificatePassword = Read-Host 'PFX password' -AsSecureString
        }
        $cert = Import-NyxveilPfxCertificate -CertificatePath $CertificatePath -CertificatePassword $CertificatePassword
        $CertificateThumbprint = $cert.Thumbprint
    }
    elseif ($CertificateThumbprint) {
        $installCertSource = 'Store'
        $cert = Get-CertificateFromStore -Thumbprint $CertificateThumbprint
        if (-not $cert) { throw "Certificate thumbprint not found in LocalMachine\My: $CertificateThumbprint" }
    }
    else {
        if ($NonInteractive) {
            throw 'Specify -CertificateThumbprint, -CertificatePath, or -GenerateSelfSignedCertificate.'
        }
        Write-Host 'Certificate source:'
        Write-Host '  1) Existing store thumbprint (recommended)'
        Write-Host '  2) Import PFX (runtime will use Store+thumbprint)'
        Write-Host '  3) Generate self-signed (NOT for public production; runtime Store+thumbprint)'
        $modeChoice = Read-Default 'Choose' '1'
        switch ($modeChoice) {
            '2' {
                $installCertSource = 'Pfx'
                $CertificatePath = Read-Host 'PFX path'
                $CertificatePassword = Read-Host 'PFX password' -AsSecureString
                $cert = Import-NyxveilPfxCertificate -CertificatePath $CertificatePath -CertificatePassword $CertificatePassword
                $CertificateThumbprint = $cert.Thumbprint
            }
            '3' {
                $installCertSource = 'SelfSigned'
                $cert = New-NyxveilSelfSignedCertificate -DnsName $PublicHostname
                $CertificateThumbprint = $cert.Thumbprint
            }
            default {
                $installCertSource = 'Store'
                $CertificateThumbprint = Read-Host 'Certificate thumbprint (LocalMachine\My)'
                $cert = Get-CertificateFromStore -Thumbprint $CertificateThumbprint
                if (-not $cert) { throw "Certificate not found: $CertificateThumbprint" }
            }
        }
    }

    # Runtime certificate mode is always Store after successful import/create.
    $certificateMode = 'Store'
    $certificateValidationMode = if ($installCertSource -eq 'SelfSigned' -or $GenerateSelfSignedCertificate) {
        'SelfSignedPinned'
    }
    else {
        'SystemTrust'
    }
    if ([string]::IsNullOrWhiteSpace($CertificateThumbprint)) {
        throw 'Certificate thumbprint missing after import/create.'
    }
    Write-Host "Certificate ready: source=$installCertSource runtime=Store validation=$certificateValidationMode thumbprint=$CertificateThumbprint"

    # Assert hostname BEFORE service install (P0)
    Assert-ValidServerCertificate -Certificate $cert -PublicHostname $PublicHostname `
        -CertificateValidationMode $certificateValidationMode

    if ([string]::IsNullOrWhiteSpace($AdminUser)) {
        if ($NonInteractive) { throw '-AdminUser is required in -NonInteractive mode.' }
        $AdminUser = Read-Host 'First SuperAdmin email (used for sign-in)'
    }
    try {
        $adminAddress = New-Object System.Net.Mail.MailAddress($AdminUser)
        if ($adminAddress.Address -cne $AdminUser -or -not $AdminUser.Contains('@')) {
            throw 'Use an email address without a display name.'
        }
    }
    catch { throw '-AdminUser must be an email address, for example admin@your-domain.com.' }
    if (-not $AdminPassword) {
        if ($NonInteractive) { throw '-AdminPassword is required in -NonInteractive mode.' }
        Write-Host 'Password: at least 12 characters, uppercase and lowercase Latin letters, a digit and a special character.'
        $AdminPassword = Read-Host 'First SuperAdmin password' -AsSecureString
        $confirm = Read-Host 'Confirm password' -AsSecureString
        $p1 = ConvertFrom-SecureStringPlain -SecureString $AdminPassword
        $p2 = ConvertFrom-SecureStringPlain -SecureString $confirm
        if ($p1 -cne $p2) { throw 'Admin passwords do not match.' }
        $p1 = $null; $p2 = $null
    }

    $PublicBaseUrl = Build-PublicBaseUrl -PublicHostname $PublicHostname -Port $Port

    $DataProtectionDir = Join-Path (Get-ProgramDataRoot) 'data-protection'

    # Non-sensitive dirs first
    foreach ($d in @($InstallDir, $DataDir, $LogsDir, (Join-Path $InstallDir 'config'))) {
        New-Item -ItemType Directory -Force -Path $d | Out-Null
    }

    # Restricted secret dirs BEFORE writing any secrets (SYSTEM+Administrators only).
    # Service account is granted later once the Windows service SID exists (directory ACL step).
    Initialize-NyxveilRestrictedDirectory -Path $SecretsDir
    Initialize-NyxveilRestrictedDirectory -Path $KeysDir
    Initialize-NyxveilRestrictedDirectory -Path $DataProtectionDir

    # License KEK: if license-kek.dpapi exists NEVER overwrite (Fresh/Repair/Upgrade).
    # Preserve or FAIL with guidance to use Repair/Recovery.
    $kekPath = Join-Path $SecretsDir 'license-kek.dpapi'
    if (Test-Path -LiteralPath $kekPath) {
        Write-Host "Preserving existing license-kek.dpapi ($InstallMode). Use restore-recovery.ps1 to replace KEK deliberately."
    }
    else {
        if ($InstallMode -ne 'Fresh') {
            throw @"
Missing license-kek.dpapi at $kekPath for InstallMode=$InstallMode.
Use -InstallMode Fresh only on a clean secrets dir, or restore via scripts\restore-recovery.ps1 / Repair after recovery.
"@
        }
        $kekHex = New-LicenseKekHex
        Write-ProtectedSecret -SecretsDir $SecretsDir -FileName 'license-kek.dpapi' -PlainText $kekHex | Out-Null
        $kekHex = $null
        Write-Host 'License KEK generated and stored via DPAPI (LocalMachine).'
    }

    if ($DatabaseAuth -eq 'Sql') {
        $sqlPlain = ConvertFrom-SecureStringPlain -SecureString $DatabasePassword
        Write-ProtectedSecret -SecretsDir $SecretsDir -FileName 'sql-password.dpapi' -PlainText $sqlPlain | Out-Null
        $sqlPlain = $null
        Write-Host 'SQL password stored via DPAPI (LocalMachine).'
    }

    # -------------------------------------------------------------------------
    # 2) Bootstrap DB (admin connection) — do NOT grant service login yet
    # -------------------------------------------------------------------------
    Assert-SqlCmdAvailable -Context 'applying create_database.sql'
    $repoRoot = Get-RepoRoot
    $sqlSource = Join-Path $repoRoot 'database\create_database.sql'
    if (-not (Test-Path $sqlSource)) {
        throw "create_database.sql not found at $sqlSource"
    }
    $sqlPrepared = New-CreateDatabaseScriptCopy -SourcePath $sqlSource -DatabaseName $Database
    try {
        Write-Host "Applying create_database.sql (DatabaseName=$Database)..."
        # Connect to master before CREATE DATABASE; prepared SQL binds and USEs the target.
        Invoke-NyxveilSql -Server $DatabaseServer -InputFile $sqlPrepared -DatabaseName 'master' `
            -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword
    }
    finally {
        Remove-Item -LiteralPath $sqlPrepared -Force -ErrorAction SilentlyContinue
    }

    # -------------------------------------------------------------------------
    # 3) Copy binaries + write appsettings (Store + ValidationMode + SQL trust policy)
    # -------------------------------------------------------------------------
    if (Test-Path $InstallDir) {
        $existingExe = Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'
        if (Test-Path $existingExe) {
            $bakRoot = Join-Path (Get-ProgramDataRoot) 'backups'
            $script:Rollback.BinaryBackup = Join-Path $bakRoot ("pre-install-{0}" -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
            New-Item -ItemType Directory -Force -Path $script:Rollback.BinaryBackup | Out-Null
            Backup-DirectoryContents -SourceDir $InstallDir -DestinationDir $script:Rollback.BinaryBackup
        }
    }
    Write-Host "Copying publish output to $InstallDir ..."
    Copy-Item -Path (Join-Path $PublishDir '*') -Destination $InstallDir -Recurse -Force
    $script:Rollback.BinariesCopied = $true

    $appsettingsPath = Write-AppsettingsProduction `
        -InstallDir $InstallDir `
        -Port $Port `
        -BindAddress $BindAddress `
        -PublicHostname $PublicHostname `
        -PublicBaseUrl $PublicBaseUrl `
        -CertificateMode $certificateMode `
        -CertificateThumbprint $CertificateThumbprint `
        -CertificateValidationMode $certificateValidationMode `
        -DatabaseServer $DatabaseServer `
        -DatabaseName $Database `
        -DatabaseAuth $DatabaseAuth `
        -DatabaseUser $DatabaseUser `
        -TrustSqlServerCertificate $trustSql `
        -SecretsDir $SecretsDir `
        -KeysDir $KeysDir

    $firewallRuleName = ''
    if (-not $SkipFirewall) {
        $firewallRuleName = New-NyxveilFirewallRule -Port $Port
        $script:Rollback.FirewallRuleName = $firewallRuleName
        Write-Host "Firewall rule: $firewallRuleName (TCP $Port only)"
    }

    $op = New-OperationalConfigObject
    $op.Port = $Port
    $op.BindAddress = $BindAddress
    $op.PublicHostname = $PublicHostname
    $op.PublicBaseUrl = $PublicBaseUrl
    $op.InstallDir = $InstallDir
    $op.DataDir = $DataDir
    $op.LogsDir = $LogsDir
    $op.SecretsDir = $SecretsDir
    $op.ServiceName = $ServiceName
    $op.ServiceAccount = $ServiceAccount
    $op.DatabaseServer = $DatabaseServer
    $op.DatabaseName = $Database
    $op.DatabaseAuth = $DatabaseAuth
    $op.DatabaseUser = $DatabaseUser
    $op.CertificateMode = $certificateMode
    $op.CertificateValidationMode = $certificateValidationMode
    $op.CertificateThumbprint = $CertificateThumbprint
    $op.TrustSqlServerCertificate = $trustSql
    $op.Encrypt = $true
    $op.FirewallRuleName = $firewallRuleName
    $op.ExpectedSchemaVersion = '1'
    $op.CreatedUtc = (Get-Date).ToUniversalTime().ToString('o')
    Write-OperationalConfig -Config $op -InstallDir $InstallDir

    $exe = Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'
    if (-not (Test-Path $exe)) { throw "Missing $exe" }
    if (-not (Test-Path $appsettingsPath)) { throw "Missing $appsettingsPath" }
    if (-not (Test-Path (Join-Path $SecretsDir 'license-kek.dpapi'))) {
        throw 'Missing license-kek.dpapi'
    }
    if (-not (Test-Path (Get-OperationalConfigPath -InstallDir $InstallDir))) {
        throw 'Missing operational.json'
    }

    # Re-assert with CLI available after binaries are copied
    Assert-ValidServerCertificate -Certificate $cert -PublicHostname $PublicHostname `
        -CertificateValidationMode $certificateValidationMode -InstallDir $InstallDir

    # -------------------------------------------------------------------------
    # 4) New-NyxveilWindowsService STOPPED (do not start)
    # -------------------------------------------------------------------------
    Write-Host "Creating Windows Service $ServiceName ($ServiceAccount) STOPPED..."
    New-NyxveilWindowsService -ServiceName $ServiceName -ExePath $exe -ServiceAccount $ServiceAccount `
        -DependOnLocalSql:(Test-IsLocalDatabaseServer -DatabaseServer $DatabaseServer)
    # Fresh rollback may delete only a service created this run (not pre-existing).
    if ($InstallMode -eq 'Fresh' -and -not $serviceExistedBefore) {
        $script:Rollback.ServiceCreated = $true
    }

    # -------------------------------------------------------------------------
    # 5) Ensure-NyxveilServiceSid (FAIL if cannot resolve)
    # -------------------------------------------------------------------------
    $null = Ensure-NyxveilServiceSid -ServiceName $ServiceName -ServiceAccount $ServiceAccount

    # -------------------------------------------------------------------------
    # 6) Set-NyxveilServiceEnvironment Production (service-specific, not machine-wide)
    # -------------------------------------------------------------------------
    Set-NyxveilServiceEnvironment -ServiceName $ServiceName -InstallDir $InstallDir

    # -------------------------------------------------------------------------
    # 7) Set-NyxveilDirectoryAcls (icacls exit checks via Invoke-NativeChecked)
    # -------------------------------------------------------------------------
    Set-NyxveilDirectoryAcls -InstallDir $InstallDir -DataDir $DataDir -LogsDir $LogsDir `
        -SecretsDir $SecretsDir -ServiceAccount $ServiceAccount -KeysDir $KeysDir `
        -DataProtectionDir $DataProtectionDir

    # -------------------------------------------------------------------------
    # 8) Grant-CertificatePrivateKeyAccess RSA+ECDSA (FAIL on error)
    # -------------------------------------------------------------------------
    Grant-CertificatePrivateKeyAccess -Certificate $cert -Account $ServiceAccount

    # -------------------------------------------------------------------------
    # 9) Grant-SqlLoginForServiceAccount AFTER SID exists
    # -------------------------------------------------------------------------
    if ($DatabaseAuth -eq 'Windows') {
        Write-Host "Granting SQL login/user for service account $ServiceAccount (db_owner on app DB only)..."
        Grant-SqlLoginForServiceAccount -Server $DatabaseServer -DatabaseName $Database `
            -ServiceAccount $ServiceAccount -DatabaseAuth $DatabaseAuth `
            -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword
    }

    # -------------------------------------------------------------------------
    # 10) Verify permissions where possible
    # -------------------------------------------------------------------------
    if (-not (Test-Path $exe)) { throw "Post-ACL verify: missing $exe" }
    if (-not (Test-Path $appsettingsPath)) { throw "Post-ACL verify: missing $appsettingsPath" }
    $svcStopped = Get-Service -Name $ServiceName -ErrorAction Stop
    if ($svcStopped.Status -eq 'Running') {
        Write-Warning 'Service was unexpectedly Running before Start-Service; stopping for ordered start.'
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    }

    # -------------------------------------------------------------------------
    # 11) Create first admin (CLI)
    # -------------------------------------------------------------------------
    Write-Host 'Creating first SuperAdmin via CLI (password on stdin)...'
    # Only the explicit already-exists result is acceptable during Repair/Upgrade.
    # Invalid passwords and all other failures must stop before starting the service.
    Invoke-AdminCreate -InstallDir $InstallDir -AdminUser $AdminUser -AdminPassword $AdminPassword `
        -AllowAlreadyExists:($InstallMode -ne 'Fresh')

    # -------------------------------------------------------------------------
    # 12) Start-Service
    # -------------------------------------------------------------------------
    Write-Host 'Starting service...'
    Start-Service -Name $ServiceName

    # -------------------------------------------------------------------------
    # 13) Health/self-test FAIL closed
    # -------------------------------------------------------------------------
    Write-Host "Health gate (CLI self-test + hostname-aware check) for $PublicHostname :$Port ..."
    if (-not (Wait-HttpsHealthy -Port $Port -PublicHostname $PublicHostname -InstallDir $InstallDir `
            -CertificateMode Store `
            -CertificateValidationMode $certificateValidationMode `
            -TimeoutSec 60)) {
        throw "Health checks failed for $PublicHostname :$Port. Install aborted."
    }

    # -------------------------------------------------------------------------
    # 14) Commit
    # -------------------------------------------------------------------------
    $script:Rollback.ServiceCreated = $false
    $script:Rollback.BinariesCopied = $false
    $script:Rollback.FirewallRuleName = ''

    Write-Host ''
    Write-Host 'Install complete.'
    Write-Host "Mode:           $InstallMode"
    Write-Host "Service:        $ServiceName"
    Write-Host "Listen:         https://${BindAddress}:${Port}"
    Write-Host "PublicBaseUrl:  $PublicBaseUrl"
    Write-Host "Certificate:    Store / $CertificateThumbprint ($certificateValidationMode)"
    Write-Host "SQL Trust:      TrustServerCertificate=$trustSql"
    Write-Host "Operational:    $(Get-OperationalConfigPath)"
}
catch {
    Write-Error $_.Exception.Message
    Invoke-InstallRollback
    exit 1
}
