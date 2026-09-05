#Requires -Version 5.1
<#
.SYNOPSIS
  Regression checks for exact key-file lookup and private-key Read ACLs.
  Creates uniquely named temporary crypto keys and deletes only those keys.
  Run elevated to include real machine CNG RSA and ECDSA checks.
#>
[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$ScratchDir)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$module = Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force -PassThru
$scratchRoot = (Resolve-Path -LiteralPath $ScratchDir).Path.TrimEnd('\')
$testRoot = Join-Path $scratchRoot ('nyxveil-cert-test-' + [Guid]::NewGuid().ToString('N'))
$accountSid = [Security.Principal.SecurityIdentifier]::new('S-1-5-19')
$account = $accountSid.Translate([Security.Principal.NTAccount]).Value
$elevated = ([Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)

try {
    # Synthetic files exercise lookup and rejection without changing OS key folders.
    $cngDir = Join-Path $testRoot 'Microsoft\Crypto\Keys'
    $cspDir = Join-Path $testRoot 'Microsoft\Crypto\RSA\MachineKeys'
    $null = New-Item -ItemType Directory -Path $cngDir -Force
    $null = New-Item -ItemType Directory -Path $cspDir -Force
    Set-Content -LiteralPath (Join-Path $cngDir 'cng-probe') -Value 'fixture'
    Set-Content -LiteralPath (Join-Path $cspDir 'csp-probe') -Value 'fixture'
    & $module {
        param($root)
        foreach ($case in @(@('CngRsa', 'csp-probe'), @('CspRsa', 'csp-probe'),
                @('CngRsa', 'cng-probe'), @('CngEcdsa', 'cng-probe'))) {
            $path = Resolve-NyxveilMachineKeyPath -UniqueName $case[1] -IsMachineKey $true -Storage $case[0] -KeyRoot $root
            if ([IO.Path]::GetFileName($path) -ne $case[1]) { throw 'Wrong key file selected.' }
        }
        foreach ($case in @(@('missing', $true), @('..\cng-probe', $true), @('cng-probe', $false))) {
            $rejected = $false
            try { $null = Resolve-NyxveilMachineKeyPath -UniqueName $case[0] -IsMachineKey $case[1] -Storage CngRsa -KeyRoot $root }
            catch { $rejected = $true }
            if (-not $rejected) { throw 'Unsafe or missing key was accepted.' }
        }
        Set-Content -LiteralPath (Join-Path $root 'Microsoft\Crypto\RSA\MachineKeys\cng-probe') -Value 'ambiguous fixture'
        $rejected = $false
        try { $null = Resolve-NyxveilMachineKeyPath -UniqueName 'cng-probe' -IsMachineKey $true -Storage CngRsa -KeyRoot $root }
        catch { $rejected = $true }
        if (-not $rejected) { throw 'Ambiguous key was accepted.' }
    } $testRoot
    Write-Host '[PASS] Key lookup: CAPI bridge, CNG RSA/ECDSA, missing/ambiguous/user keys rejected.'

    foreach ($kind in @('CspRsa', 'CngRsa', 'CngEcdsa')) {
        if (-not $elevated -and $kind -ne 'CspRsa') {
            Write-Warning "[SKIP] Real $kind machine key requires elevation."
            continue
        }
        $label = 'Nyxveil-CertAcl-Test-' + [Guid]::NewGuid().ToString('N')
        $algorithm = $null
        $cngKey = $null
        $cert = $null
        $opened = $null
        try {
            if ($kind -eq 'CspRsa') {
                $p = New-Object Security.Cryptography.CspParameters(24, 'Microsoft Enhanced RSA and AES Cryptographic Provider', $label)
                $p.Flags = [Security.Cryptography.CspProviderFlags]::UseMachineKeyStore
                $algorithm = New-Object Security.Cryptography.RSACryptoServiceProvider(2048, $p)
            }
            else {
                $p = New-Object Security.Cryptography.CngKeyCreationParameters
                $p.KeyCreationOptions = [Security.Cryptography.CngKeyCreationOptions]::MachineKey
                $p.Provider = [Security.Cryptography.CngProvider]::MicrosoftSoftwareKeyStorageProvider
                $cngAlgorithm = if ($kind -eq 'CngRsa') { [Security.Cryptography.CngAlgorithm]::Rsa } else { [Security.Cryptography.CngAlgorithm]::ECDsaP256 }
                $cngKey = [Security.Cryptography.CngKey]::Create($cngAlgorithm, $label, $p)
                $algorithm = if ($kind -eq 'CngRsa') { [Security.Cryptography.RSACng]::new($cngKey) } else { [Security.Cryptography.ECDsaCng]::new($cngKey) }
            }
            if ($kind -eq 'CngEcdsa') {
                $request = [Security.Cryptography.X509Certificates.CertificateRequest]::new(
                    'CN=nyxveil-acl-test.invalid', $algorithm, [Security.Cryptography.HashAlgorithmName]::SHA256)
            }
            else {
                $request = [Security.Cryptography.X509Certificates.CertificateRequest]::new(
                    'CN=nyxveil-acl-test.invalid', $algorithm, [Security.Cryptography.HashAlgorithmName]::SHA256,
                    [Security.Cryptography.RSASignaturePadding]::Pkcs1)
            }
            $cert = $request.CreateSelfSigned([DateTimeOffset]::Now.AddMinutes(-1), [DateTimeOffset]::Now.AddHours(1))
            foreach ($run in 1..2) { Grant-CertificatePrivateKeyAccess -Certificate $cert -Account $account }

            # Reopen and sign after ACL updates to ensure the original key remains usable.
            $data = [Text.Encoding]::UTF8.GetBytes('Nyxveil certificate ACL regression')
            if ($kind -eq 'CngEcdsa') {
                $opened = [Security.Cryptography.X509Certificates.ECDsaCertificateExtensions]::GetECDsaPrivateKey($cert)
                $signature = $opened.SignData($data, [Security.Cryptography.HashAlgorithmName]::SHA256)
                $valid = $opened.VerifyData($data, $signature, [Security.Cryptography.HashAlgorithmName]::SHA256)
            }
            else {
                $opened = [Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($cert)
                $signature = $opened.SignData($data, [Security.Cryptography.HashAlgorithmName]::SHA256, [Security.Cryptography.RSASignaturePadding]::Pkcs1)
                $valid = $opened.VerifyData($data, $signature, [Security.Cryptography.HashAlgorithmName]::SHA256, [Security.Cryptography.RSASignaturePadding]::Pkcs1)
            }
            if (-not $valid) { throw 'Private key signing failed after ACL grant.' }
            Write-Host "[PASS] Real $kind ($($opened.GetType().Name)): repeated Read ACL grant and signing."
        }
        finally {
            if ($opened) { $opened.Dispose() }
            if ($cert) { $cert.Dispose() }
            if ($algorithm -is [Security.Cryptography.RSACryptoServiceProvider]) { $algorithm.PersistKeyInCsp = $false }
            if ($algorithm) { $algorithm.Dispose() }
            if ($cngKey) { $cngKey.Delete(); $cngKey.Dispose() }
        }
    }
}
finally {
    if (Test-Path -LiteralPath $testRoot) {
        $resolvedTest = (Resolve-Path -LiteralPath $testRoot).Path
        if ((Split-Path -Parent $resolvedTest) -ne $scratchRoot -or
            (Split-Path -Leaf $resolvedTest) -notlike 'nyxveil-cert-test-*') {
            throw "Refusing cleanup outside scratch directory: $resolvedTest"
        }
        Remove-Item -LiteralPath $resolvedTest -Recurse -Force
    }
}
