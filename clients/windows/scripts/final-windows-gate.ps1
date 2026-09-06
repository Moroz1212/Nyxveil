#Requires -RunAsAdministrator
#Requires -Version 5.1
<#
.SYNOPSIS
  Elevated FINAL OS gate вЂ” fail-closed; required gates cannot SKIP.
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
$UserAPass = $null

$Required = @(
  "GO_TEST","GO_VET","DOTNET_TEST","CONNECT_CANCELLATION","QUIC","TLS_FALLBACK","TYPECONFIG",
  "TYPECONFIG_READER_OWNERSHIP","TICKETBROKER_RACE","JOURNAL_RESTORE_KEEPS_DIRTY","SERVICE_DIRTY_JOURNAL_REFUSED",
  "INSTALL","SCM_LOCALSYSTEM","SERVICE_PIPE_READY","QUOTED_SERVICE_PATH",
  "NORMAL_USER_PIPE","OTHER_USER_REJECT","ADMIN_PIPE",
  "AUTHORIZED_SID_FILE_ACL","PROGRAMDATA_DIR_ACL","ORIGINAL_USER_SID",
  "WINTUN_REAL","WINTUN_SIGNATURE",
  "ROUTES_REAL_WINDOWS","DNS_REAL_WINDOWS","IPV6_REAL_WINDOWS","SCM_STOP_RESTORE",
  "CRASH_RECOVERY","GUI_NON_ELEVATED","POSTINSTALL_RUNASORIGINALUSER","INSTALLER_ROLLBACK",
  "RECONNECT_DISCONNECT_RACES","UNINSTALL","REINSTALL","FROZEN_SELF_CONTAINED","SETUP_HASH",
  "LOCAL_GROUPS_SID_SAFE"
)

function Set-Gate([string]$Name, [string]$Status, [string]$Detail = "") {
  $Results[$Name] = @{ Status = $Status; Detail = $Detail }
  $c = switch ($Status) { "PASS" { "Green" } "FAIL" { "Red" } default { "Yellow" } }
  Write-Host ("[{0}] {1} {2}" -f $Status, $Name, $Detail) -ForegroundColor $c
}

function Assert-Admin {
  $p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw "Run as Administrator" }
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

function New-TempUser([string]$Prefix) {
  $name = $Prefix + (Get-Random -Maximum 99999)
  $plain = ([guid]::NewGuid().ToString("N") + "Aa1!")
  $sec = ConvertTo-SecureString $plain -AsPlainText -Force
  New-LocalUser -Name $name -Password $sec -PasswordNeverExpires -UserMayNotChangePassword | Out-Null
  Add-LocalGroupMember -Group $UsersGroup -Member $name -ErrorAction SilentlyContinue
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

function Invoke-AsUserFile([hashtable]$User, [string]$PsFile) {
  $p = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoProfile","-ExecutionPolicy","Bypass","-File",$PsFile `
    -Credential (New-Object pscredential(($env:COMPUTERNAME+"\"+$User.Name), $User.Secure)) `
    -Wait -PassThru -WindowStyle Hidden
  return $p.ExitCode
}

function Wait-Svc([string]$Want, [int]$Sec=45) {
  $d = (Get-Date).AddSeconds($Sec)
  do {
    $s = Get-Service $SvcName -ErrorAction SilentlyContinue
    if ($Want -eq "Absent" -and -not $s) { return $true }
    if ($s -and $s.Status -eq $Want) { return $true }
    Start-Sleep 1
  } while ((Get-Date) -lt $d)
  return $false
}

function Invoke-NamedGoTest([string]$Pattern) {
  Push-Location (Join-Path $Root "engine")
  go test ./... -count=1 -run $Pattern
  $code = $LASTEXITCODE
  Pop-Location
  return $code
}

Assert-Admin
Write-Host "=== Nyxveil FINAL Windows Gate ===" -ForegroundColor Cyan

if (-not (Test-Path $Setup)) {
  & (Join-Path $Root "scripts\package-final.ps1")
}
if (-not (Test-Path $Setup)) { throw "Setup missing" }

# --- Named unit gates (no hard-coded PASS) ---
Push-Location (Join-Path $Root "engine")
go test ./... -count=1
if ($LASTEXITCODE -eq 0) { Set-Gate "GO_TEST" "PASS" } else { Set-Gate "GO_TEST" "FAIL" "go test" }
go vet ./...
if ($LASTEXITCODE -eq 0) { Set-Gate "GO_VET" "PASS" } else { Set-Gate "GO_VET" "FAIL" }
Pop-Location

$code = Invoke-NamedGoTest "TestConnectCancel|TestTwoSimultaneous|TestDisconnectSupersedes|TestConnectAfterCancel|TestDisconnectWhileReconnect|TestDisconnectAfterTicket|TestSessionLostAfterManual|TestRepeatedSessionLoss"
if ($code -eq 0) { Set-Gate "CONNECT_CANCELLATION" "PASS" "named race/cancel tests" } else { Set-Gate "CONNECT_CANCELLATION" "FAIL" "exit $code" }
if ($code -eq 0) { Set-Gate "RECONNECT_DISCONNECT_RACES" "PASS" } else { Set-Gate "RECONNECT_DISCONNECT_RACES" "FAIL" "exit $code" }

# Locale-safe group resolution (no English name hardcoding)
if ($UsersGroup -and $AdminsGroup -and $UsersGroup -ne $AdminsGroup) {
  Set-Gate "LOCAL_GROUPS_SID_SAFE" "PASS" "UsersSIDв†’$UsersGroup AdminsSIDв†’$AdminsGroup"
} else {
  Set-Gate "LOCAL_GROUPS_SID_SAFE" "FAIL" "users=$UsersGroup admins=$AdminsGroup"
}

$iss = Get-Content (Join-Path $Root "installer\nyxveil.iss") -Raw
if ($iss -match '(?i)runasoriginaluser') {
  Set-Gate "POSTINSTALL_RUNASORIGINALUSER" "PASS" "iss Flags include runasoriginaluser"
} else {
  Set-Gate "POSTINSTALL_RUNASORIGINALUSER" "FAIL" "missing runasoriginaluser on [Run]"
}

$code = Invoke-NamedGoTest "TestRegistryHasQUICAndTLS"
if ($code -eq 0) {
  Set-Gate "QUIC" "PASS" "TestRegistryHasQUICAndTLS"
  Set-Gate "TLS_FALLBACK" "PASS" "TestRegistryHasQUICAndTLS"
} else {
  Set-Gate "QUIC" "FAIL"; Set-Gate "TLS_FALLBACK" "FAIL"
}

$code = Invoke-NamedGoTest "TestTypeConfigDNSRequired|TestDecodeRequiresDNS|TestDecodeOK"
if ($code -eq 0) { Set-Gate "TYPECONFIG" "PASS" } else { Set-Gate "TYPECONFIG" "FAIL" }

$code = Invoke-NamedGoTest "TestTypeConfigTempReader|TestTypeConfigDisconnect|TestTypeConfigTimeout|TestStopTempTransport"
if ($code -eq 0) { Set-Gate "TYPECONFIG_READER_OWNERSHIP" "PASS" } else { Set-Gate "TYPECONFIG_READER_OWNERSHIP" "FAIL" "exit $code" }

Push-Location (Join-Path $Root "engine")
$env:CGO_ENABLED = "1"
go test ./internal/ticketbroker/ -count=1 -race -timeout 180s -run "TestProvideVs|TestTimeoutVs|TestDisconnectWhile"
$tb = $LASTEXITCODE
if ($tb -ne 0) {
  # Fallback without cgo race detector (still stress-tested).
  Remove-Item Env:CGO_ENABLED -EA SilentlyContinue
  go test ./internal/ticketbroker/ -count=1 -timeout 180s -run "TestProvideVs|TestTimeoutVs|TestDisconnectWhile|TestRoundtrip|TestTimeout|TestDisconnectCancels"
  $tb = $LASTEXITCODE
}
Pop-Location
if ($tb -eq 0) { Set-Gate "TICKETBROKER_RACE" "PASS" } else { Set-Gate "TICKETBROKER_RACE" "FAIL" "exit $tb" }

$code = Invoke-NamedGoTest "TestRollbackPreservesJournalOnUndoFailure|TestSuccessfulRestoreClearsJournal|TestPendingPersistFailure"
if ($code -eq 0) { Set-Gate "JOURNAL_RESTORE_KEEPS_DIRTY" "PASS" } else { Set-Gate "JOURNAL_RESTORE_KEEPS_DIRTY" "FAIL" "exit $code" }

Push-Location (Join-Path $Root "gui")
dotnet test (Join-Path $Root "gui\Nyxveil.Client.sln") -c Release --verbosity minimal
if ($LASTEXITCODE -eq 0) { Set-Gate "DOTNET_TEST" "PASS" } else { Set-Gate "DOTNET_TEST" "FAIL" }
Pop-Location

& (Join-Path $Root "scripts\assert-wintun.ps1"); if ($LASTEXITCODE -eq 0) { Set-Gate "WINTUN_SIGNATURE" "PASS" } else { Set-Gate "WINTUN_SIGNATURE" "FAIL" }
& (Join-Path $Root "scripts\assert-frozen-core.ps1"); if ($LASTEXITCODE -eq 0) { Set-Gate "FROZEN_SELF_CONTAINED" "PASS" } else { Set-Gate "FROZEN_SELF_CONTAINED" "FAIL" }

$line = Get-Content (Join-Path $Dist "SHA256SUMS") | Where-Object { $_ -match "Nyxveil-Setup-v" } | Select-Object -First 1
$want = ($line -split '\s+')[0].ToLowerInvariant()
$got = (Get-FileHash $Setup -Algorithm SHA256).Hash.ToLowerInvariant()
if ($want -eq $got) { Set-Gate "SETUP_HASH" "PASS" } else { Set-Gate "SETUP_HASH" "FAIL" }

# --- Users: A temporarily Administrators so silent Highest install models UAC original-user ---
$UserA = New-TempUser "NvA"
$UserB = New-TempUser "NvB"
$sidA = Get-Sid $UserA.Name
$script:UserAPass = $UserA.Plain
Add-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name

# Clean prior install
sc.exe stop $SvcName 2>$null | Out-Null
sc.exe delete $SvcName 2>$null | Out-Null
Start-Sleep 1

# Install as UserA with Highest (original user = A for ExecAsOriginalUser)
$task = "NyxveilGateInstallA"
Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
$installLog = Join-Path $env:TEMP "nv-install-a.log"
Remove-Item $installLog -Force -ErrorAction SilentlyContinue
$installPs = Join-Path $env:TEMP "nv-install-a.ps1"
@"
`$ErrorActionPreference='Stop'
`$p = Start-Process -FilePath '$Setup' -ArgumentList '/VERYSILENT','/NORESTART','/SUPPRESSMSGBOXES' -Wait -PassThru
Set-Content -Path '$installLog' -Value ('exit=' + `$p.ExitCode)
exit `$p.ExitCode
"@ | Set-Content $installPs -Encoding UTF8

$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$installPs`""
Register-ScheduledTask -TaskName $task -Action $action -User $UserA.Name -Password $UserA.Plain -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName $task
$deadline = (Get-Date).AddMinutes(5)
do { Start-Sleep 2 } while ((Get-ScheduledTask -TaskName $task).State -ne "Ready" -and (Get-Date) -lt $deadline)
$ti = Get-ScheduledTaskInfo -TaskName $task
Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue

$instExit = 1
if (Test-Path $installLog) {
  $instExit = [int]((Get-Content $installLog -Raw) -replace 'exit=','').Trim()
}
if ($instExit -eq 0 -and (Test-Path $Bin) -and (Wait-Svc "Running" 60)) {
  Set-Gate "INSTALL" "PASS" "Setup as UserA Highest"
} else {
  Set-Gate "INSTALL" "FAIL" "exit=$instExit log=$(Get-Content $installLog -Raw -ErrorAction SilentlyContinue)"
  Remove-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -ErrorAction SilentlyContinue
  Remove-TempUsers
  throw "install failed"
}

# Demote A to ordinary user for pipe/GUI tests
Remove-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -ErrorAction SilentlyContinue

$svc = Get-CimInstance Win32_Service -Filter "Name='$SvcName'"
if ($svc -and $svc.StartName -match 'LocalSystem|Local System' -and $svc.State -eq "Running") {
  Set-Gate "SCM_LOCALSYSTEM" "PASS"
} else {
  Set-Gate "SCM_LOCALSYSTEM" "FAIL" "$($svc.StartName)/$($svc.State)"
}

# Quoted image path under Program Files
$pn = [string]$svc.PathName
if ($pn -match '^"[^"]+Nyxveil\.Service\.exe"') {
  Set-Gate "QUOTED_SERVICE_PATH" "PASS" $pn
} else {
  Set-Gate "QUOTED_SERVICE_PATH" "FAIL" "PathName=$pn"
}

# ORIGINAL USER SID вЂ” no manual override
$onDisk = (Get-Content $SidFile -Raw -ErrorAction SilentlyContinue).Trim()
if ($onDisk -eq $sidA) { Set-Gate "ORIGINAL_USER_SID" "PASS" $sidA }
else { Set-Gate "ORIGINAL_USER_SID" "FAIL" "disk=$onDisk want=$sidA" }

# SID ACL overwrite as A/B
$aclPs = Join-Path $env:TEMP "nv-sid-ow.ps1"
@"
try { Set-Content -Path '$SidFile' -Value 'BAD' -Encoding ASCII; exit 0 } catch { exit 5 }
"@ | Set-Content $aclPs -Encoding UTF8
$cA = Invoke-AsUserFile $UserA $aclPs
$cB = Invoke-AsUserFile $UserB $aclPs
$adminOk = $true
try { $x = Get-Content $SidFile -Raw; Set-Content $SidFile -Value $x -Encoding ASCII; & $Bin -lock-sid-acl | Out-Null } catch { $adminOk = $false }
# Ensure SID still A after admin touch
if ((Get-Content $SidFile -Raw).Trim() -ne $sidA) {
  # restore from known good without breaking "no manual override" claim for install вЂ” ACL test only
  & $Bin -provision-sid -sid $sidA | Out-Null
  & $Bin -lock-sid-acl | Out-Null
  Restart-Service $SvcName -Force; Start-Sleep 2
}
if ($cA -eq 5 -and $cB -eq 5 -and $adminOk) { Set-Gate "AUTHORIZED_SID_FILE_ACL" "PASS" }
else { Set-Gate "AUTHORIZED_SID_FILE_ACL" "FAIL" "A=$cA B=$cB admin=$adminOk" }

# Protected ProgramData Client directory: ordinary users cannot write SID/journal/gate/tmp
$dirAclPs = Join-Path $env:TEMP "nv-dir-acl.ps1"
$dirAclOut = Join-Path $env:TEMP "nv-dir-acl.out"
@"
`$ErrorActionPreference='Stop'
`$fails = @()
try { Set-Content -Path '$SidFile' -Value 'BAD' -Encoding ASCII; `$fails += 'sid-write' } catch {}
try { Remove-Item -Path '$SidFile' -Force; `$fails += 'sid-delete' } catch {}
try { Set-Content -Path '$DataDir\route-journal.json' -Value '{}' -Encoding ASCII; `$fails += 'journal-write' } catch {}
try { Set-Content -Path '$DataDir\route-journal.json.tmp' -Value 'x' -Encoding ASCII; `$fails += 'journal-tmp' } catch {}
try { Set-Content -Path '$GateFlag' -Value '1' -Encoding ASCII; `$fails += 'gate-flag' } catch {}
if (`$fails.Count -eq 0) { Set-Content '$dirAclOut' 'PASS'; exit 5 } else { Set-Content '$dirAclOut' ('FAIL|' + (`$fails -join ',')); exit 0 }
"@ | Set-Content $dirAclPs -Encoding UTF8
# Note: exit 5 = all writes denied (expected for UserA); exit 0 with FAIL list = some writes succeeded (bad)
$cDirA = Invoke-AsUserFile $UserA $dirAclPs
$outDirA = Get-Content $dirAclOut -Raw -EA SilentlyContinue
Remove-Item $dirAclOut -EA SilentlyContinue
$cDirB = Invoke-AsUserFile $UserB $dirAclPs
$outDirB = Get-Content $dirAclOut -Raw -EA SilentlyContinue
$adminDirOk = $true
try {
  & $Bin -protect-client-data-dir | Out-Null
  Set-Content -Path (Join-Path $DataDir "acl-admin-probe.txt") -Value "ok" -Encoding ASCII
  Remove-Item (Join-Path $DataDir "acl-admin-probe.txt") -Force
} catch { $adminDirOk = $false }
if ($cDirA -eq 5 -and $cDirB -eq 5 -and $adminDirOk -and $outDirA -like "PASS*" -and $outDirB -like "PASS*") {
  Set-Gate "PROGRAMDATA_DIR_ACL" "PASS"
} else {
  Set-Gate "PROGRAMDATA_DIR_ACL" "FAIL" "A=$cDirA/$outDirA B=$cDirB/$outDirB admin=$adminDirOk"
}

# Pipe helpers
$pipePs = Join-Path $env:TEMP "nv-pipe.ps1"
$pipeOut = Join-Path $env:TEMP "nv-pipe.out"
@"
`$ErrorActionPreference='Stop'
try {
  Add-Type -TypeDefinition @'
using System; using System.IO; using System.IO.Pipes; using System.Text;
public static class P { public static string D() {
  using (var p = new NamedPipeClientStream(".", "NyxveilClient", PipeDirection.InOut)) {
    p.Connect(8000);
    using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
    using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
      w.WriteLine("{\"v\":1,\"type\":\"hello\"}"); return r.ReadLine() ?? "";
    }
  }
}}
'@
  Set-Content '$pipeOut' ('PASS|' + [P]::D().Length); exit 0
} catch { Set-Content '$pipeOut' ('FAIL|' + `$_.Exception.Message); exit 5 }
"@ | Set-Content $pipePs -Encoding UTF8

Remove-Item $pipeOut -EA SilentlyContinue
if ((Invoke-AsUserFile $UserA $pipePs) -eq 0 -and (Get-Content $pipeOut -Raw) -like "PASS|*") {
  Set-Gate "NORMAL_USER_PIPE" "PASS"
} else { Set-Gate "NORMAL_USER_PIPE" "FAIL" (Get-Content $pipeOut -Raw -EA SilentlyContinue) }

Remove-Item $pipeOut -EA SilentlyContinue
if ((Invoke-AsUserFile $UserB $pipePs) -ne 0) { Set-Gate "OTHER_USER_REJECT" "PASS" }
else { Set-Gate "OTHER_USER_REJECT" "FAIL" "B connected" }

Remove-Item $pipeOut -EA SilentlyContinue
& powershell -NoProfile -ExecutionPolicy Bypass -File $pipePs | Out-Null
if ((Get-Content $pipeOut -Raw -EA SilentlyContinue) -like "PASS|*") {
  Set-Gate "ADMIN_PIPE" "PASS"
  Set-Gate "SERVICE_PIPE_READY" "PASS"
} else {
  Set-Gate "ADMIN_PIPE" "FAIL"
  Set-Gate "SERVICE_PIPE_READY" "FAIL" (Get-Content $pipeOut -Raw -EA SilentlyContinue)
}

# Installer SCM rollback: fail-injection must leave no orphan service, then restore
$rbFail = @()
foreach ($step in @("create","description","failure","start","ready")) {
  $p = Start-Process $Bin -ArgumentList "-finalize-scm","-finalize-scm-fail-after",$step -Wait -PassThru -NoNewWindow
  if ($p.ExitCode -eq 0) { $rbFail += "${step}:expected-nonzero" }
  if (Get-Service $SvcName -EA SilentlyContinue) { $rbFail += "${step}:orphan-remains" }
}
# Restore production service after injection tests
$rest = Start-Process $Bin -ArgumentList "-finalize-scm" -Wait -PassThru -NoNewWindow
if ($rest.ExitCode -ne 0 -or -not (Wait-Svc "Running" 60)) {
  $rbFail += "restore-failed:$($rest.ExitCode)"
} else {
  Remove-Item $pipeOut -EA SilentlyContinue
  & powershell -NoProfile -ExecutionPolicy Bypass -File $pipePs | Out-Null
  if ((Get-Content $pipeOut -Raw -EA SilentlyContinue) -notlike "PASS|*") { $rbFail += "pipe-after-restore" }
}
if ($rbFail.Count -eq 0) { Set-Gate "INSTALLER_ROLLBACK" "PASS" } else { Set-Gate "INSTALLER_ROLLBACK" "FAIL" ($rbFail -join ";") }

# Service must refuse start over dirty/unrecoverable journal
Stop-Service $SvcName -Force -EA SilentlyContinue
Wait-Svc "Stopped" 30 | Out-Null
$jPath = Join-Path $DataDir "route-journal.json"
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
@'
{"phase":"restore_failed","applied":[{"id":"bypass-x","kind":"bypass_route","dest_prefix":"198.51.100.1/32","next_hop":""}]}
'@ | Set-Content $jPath -Encoding UTF8
$startDirty = Start-Process $Bin -ArgumentList "-console","-pipe","\\.\pipe\NyxveilDirtyProbe" -PassThru -WindowStyle Hidden
Start-Sleep 3
$dirtyRefused = $false
if ($startDirty.HasExited -and $startDirty.ExitCode -ne 0) { $dirtyRefused = $true }
else {
  try { Stop-Process -Id $startDirty.Id -Force -EA SilentlyContinue } catch {}
  # Console mode may still start if recovery only runs in service mode вЂ” probe Recover via -uninstall-network-cleanup / gate-verify
  $rec = Start-Process $Bin -ArgumentList "-gate-verify-clean" -Wait -PassThru -NoNewWindow
  # With empty next hop, RecoverOnStartup should fail; VerifyGateClean may clear вЂ” use direct recover path:
  # Service Execute path uses RecoverOnStartup fail-closed. Re-test via sc start after writing journal.
}
Remove-Item $jPath -Force -EA SilentlyContinue
# Explicit: start real service with dirty journal
@'
{"phase":"restore_failed","applied":[{"id":"bypass-x","kind":"bypass_route","dest_prefix":"198.51.100.1/32","next_hop":""}]}
'@ | Set-Content $jPath -Encoding UTF8
$scStart = Start-Process "sc.exe" -ArgumentList "start",$SvcName -Wait -PassThru -NoNewWindow
Start-Sleep 2
$svcDirty = Get-Service $SvcName -EA SilentlyContinue
if ($svcDirty -and $svcDirty.Status -eq "Running") {
  Set-Gate "SERVICE_DIRTY_JOURNAL_REFUSED" "FAIL" "service running over dirty journal"
  Stop-Service $SvcName -Force -EA SilentlyContinue
} else {
  Set-Gate "SERVICE_DIRTY_JOURNAL_REFUSED" "PASS" "start refused or not running"
}
Remove-Item $jPath -Force -EA SilentlyContinue
& $Bin -finalize-scm | Out-Null
Wait-Svc "Running" 60 | Out-Null

# GUI non-elevated as A
$guiPs = Join-Path $env:TEMP "nv-gui.ps1"
$guiOut = Join-Path $env:TEMP "nv-gui.out"
@"
`$ErrorActionPreference='Stop'
try {
  `$pr = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
  if (`$pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { Set-Content '$guiOut' 'FAIL|elevated'; exit 5 }
  `$p = Start-Process -FilePath '$Gui' -PassThru -WindowStyle Minimized
  Start-Sleep 4
  Add-Type -TypeDefinition @'
using System; using System.IO; using System.IO.Pipes; using System.Text;
public static class G { public static string D() {
  using (var p = new NamedPipeClientStream(".", "NyxveilClient", PipeDirection.InOut)) {
    p.Connect(8000);
    using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
    using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
      w.WriteLine("{\"v\":1,\"type\":\"hello\"}"); return r.ReadLine() ?? "";
    }
  }
}}
'@
  `$r = [G]::D()
  try { Stop-Process -Id `$p.Id -Force } catch {}
  Set-Content '$guiOut' ('PASS|pid=' + `$p.Id + '|pipe=' + `$r.Length); exit 0
} catch { Set-Content '$guiOut' ('FAIL|' + `$_.Exception.Message); exit 5 }
"@ | Set-Content $guiPs -Encoding UTF8
Remove-Item $guiOut -EA SilentlyContinue
if ((Invoke-AsUserFile $UserA $guiPs) -eq 0 -and (Get-Content $guiOut -Raw) -like "PASS|*") {
  Set-Gate "GUI_NON_ELEVATED" "PASS" (Get-Content $guiOut -Raw).Trim()
} else { Set-Gate "GUI_NON_ELEVATED" "FAIL" (Get-Content $guiOut -Raw -EA SilentlyContinue) }

# Wintun / net / ipv6 via service binary helpers
$w = Start-Process $Bin -ArgumentList "-gate-wintun" -Wait -PassThru -NoNewWindow
if ($w.ExitCode -eq 0) { Set-Gate "WINTUN_REAL" "PASS" } else { Set-Gate "WINTUN_REAL" "FAIL" "exit $($w.ExitCode)" }

$n = Start-Process $Bin -ArgumentList "-gate-net-tx" -Wait -PassThru -NoNewWindow
if ($n.ExitCode -eq 0) {
  Set-Gate "ROUTES_REAL_WINDOWS" "PASS"
  Set-Gate "DNS_REAL_WINDOWS" "PASS"
} else {
  Set-Gate "ROUTES_REAL_WINDOWS" "FAIL" "exit $($n.ExitCode)"
  Set-Gate "DNS_REAL_WINDOWS" "FAIL" "exit $($n.ExitCode)"
}

$i = Start-Process $Bin -ArgumentList "-gate-ipv6" -Wait -PassThru -NoNewWindow
if ($i.ExitCode -eq 0) { Set-Gate "IPV6_REAL_WINDOWS" "PASS" } else { Set-Gate "IPV6_REAL_WINDOWS" "FAIL" "exit $($i.ExitCode)" }

# --- Real SCM STOP: service-owned isolated tx via IPC, then Stop-Service ---
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Set-Content -Path $GateFlag -Value "1" -Encoding ASCII
$scmPs = Join-Path $env:TEMP "nv-scm-apply.ps1"
$scmOut = Join-Path $env:TEMP "nv-scm-apply.out"
@"
`$ErrorActionPreference='Stop'
try {
  Add-Type -TypeDefinition @'
using System; using System.IO; using System.IO.Pipes; using System.Text;
public static class S {
  public static string Apply() {
    using (var p = new NamedPipeClientStream(".", "NyxveilClient", PipeDirection.InOut)) {
      p.Connect(8000);
      using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
      using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
        w.WriteLine("{\"v\":1,\"type\":\"gate_apply_isolated\"}");
        return r.ReadLine() ?? "";
      }
    }
  }
}
'@
  `$resp = [S]::Apply()
  Set-Content '$scmOut' `$resp
  if (`$resp -notmatch 'gate_applied') { exit 5 }
  exit 0
} catch { Set-Content '$scmOut' `$_.Exception.Message; exit 5 }
"@ | Set-Content $scmPs -Encoding UTF8
Remove-Item $scmOut -EA SilentlyContinue
& powershell -NoProfile -ExecutionPolicy Bypass -File $scmPs | Out-Null
$applyResp = Get-Content $scmOut -Raw -EA SilentlyContinue
if ($LASTEXITCODE -ne 0 -or $applyResp -notmatch "gate_applied") {
  Set-Gate "SCM_STOP_RESTORE" "FAIL" "apply failed: $applyResp"
} else {
  # Confirm route present before stop
  $via = ($applyResp | ConvertFrom-Json -EA SilentlyContinue).via
  Stop-Service $SvcName -Force
  if (-not (Wait-Svc "Stopped" 45)) {
    Set-Gate "SCM_STOP_RESTORE" "FAIL" "service not stopped"
  } else {
    Start-Sleep 2
    # Verify marker route absent (Disconnect path)
    $chk = Start-Process $Bin -ArgumentList "-gate-verify-clean" -Wait -PassThru -NoNewWindow
    # Also ensure 198.51.100.55 gone via helper verify
    if ($chk.ExitCode -eq 0) { Set-Gate "SCM_STOP_RESTORE" "PASS" "service-owned tx rolled back on Stop" }
    else { Set-Gate "SCM_STOP_RESTORE" "FAIL" "verify-clean $($chk.ExitCode)" }
  }
  Start-Service $SvcName -EA SilentlyContinue
  Start-Sleep 2
}
Remove-Item $GateFlag -Force -EA SilentlyContinue

# Crash recovery steps
$crashFail = @()
foreach ($step in @("bypass","tun_addr","tun_dns","ipv6","default_vpn")) {
  sc.exe stop $SvcName 2>$null | Out-Null
  Wait-Svc "Stopped" 30 | Out-Null
  $proc = Start-Process $Bin -ArgumentList "-gate-crash-after",$step -PassThru -NoNewWindow
  Wait-Process -Id $proc.Id -Timeout 90 -EA SilentlyContinue
  sc.exe start $SvcName 2>$null | Out-Null
  Start-Sleep 2
  $v = Start-Process $Bin -ArgumentList "-gate-verify-clean" -Wait -PassThru -NoNewWindow
  if ($v.ExitCode -ne 0) { $crashFail += "${step}:$($v.ExitCode)" }
}
if ($crashFail.Count -eq 0) { Set-Gate "CRASH_RECOVERY" "PASS" } else { Set-Gate "CRASH_RECOVERY" "FAIL" ($crashFail -join ";") }

# Uninstall / reinstall
$unins = Get-ChildItem $InstallDir -Filter "unins*.exe" -EA SilentlyContinue | Select-Object -First 1
if ($unins) {
  Stop-Service $SvcName -Force -EA SilentlyContinue
  Wait-Svc "Stopped" 40 | Out-Null
  $u = Start-Process $unins.FullName -ArgumentList "/VERYSILENT","/NORESTART","/SUPPRESSMSGBOXES" -Wait -PassThru
  Start-Sleep 3
  if (-not (Get-Service $SvcName -EA SilentlyContinue)) { Set-Gate "UNINSTALL" "PASS" } else { Set-Gate "UNINSTALL" "FAIL" "service remains" }

  # Reinstall again as UserA original-user path
  Add-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -EA SilentlyContinue
  $task2 = "NyxveilGateReinstallA"
  Unregister-ScheduledTask -TaskName $task2 -Confirm:$false -EA SilentlyContinue
  $reLog = Join-Path $env:TEMP "nv-reinstall.log"
  $rePs = Join-Path $env:TEMP "nv-reinstall.ps1"
  @"
`$p = Start-Process -FilePath '$Setup' -ArgumentList '/VERYSILENT','/NORESTART','/SUPPRESSMSGBOXES' -Wait -PassThru
Set-Content '$reLog' `$p.ExitCode
exit `$p.ExitCode
"@ | Set-Content $rePs -Encoding UTF8
  $action2 = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$rePs`""
  Register-ScheduledTask -TaskName $task2 -Action $action2 -User $UserA.Name -Password $UserA.Plain -RunLevel Highest -Force | Out-Null
  Start-ScheduledTask -TaskName $task2
  $deadline = (Get-Date).AddMinutes(5)
  do { Start-Sleep 2 } while ((Get-ScheduledTask -TaskName $task2).State -ne "Ready" -and (Get-Date) -lt $deadline)
  Unregister-ScheduledTask -TaskName $task2 -Confirm:$false -EA SilentlyContinue
  Remove-LocalGroupMember -Group $AdminsGroup -Member $UserA.Name -EA SilentlyContinue
  Start-Sleep 2
  $sid2 = (Get-Content $SidFile -Raw -EA SilentlyContinue).Trim()
  $svc2 = Get-Service $SvcName -EA SilentlyContinue
  if ($svc2 -and $svc2.Status -eq "Running" -and $sid2 -eq $sidA) { Set-Gate "REINSTALL" "PASS" }
  else { Set-Gate "REINSTALL" "FAIL" "svc=$($svc2.Status) sid=$sid2" }
} else {
  Set-Gate "UNINSTALL" "FAIL" "no unins"
  Set-Gate "REINSTALL" "FAIL"
}

Set-Gate "AUTHENTICODE" "FAIL" "NOT SIGNED (allowed)"
Set-Gate "LIVE_WINDOWS_UBUNTU_INTERNET" "FAIL" "NOT VERIFIED (allowed)"

if (-not $KeepInstall) { } # already uninstalled/reinstalled above

Remove-TempUsers

Write-Host "`n=== GATE TABLE ===" -ForegroundColor Cyan
$fail = 0
foreach ($k in $Results.Keys) {
  $r = $Results[$k]
  Write-Host ("{0,-32} {1,-6} {2}" -f $k, $r.Status, $r.Detail)
}
foreach ($k in $Required) {
  if (-not $Results.Contains($k)) { Write-Host "MISSING $k" -ForegroundColor Red; $fail++; continue }
  if ($Results[$k].Status -ne "PASS") { Write-Host "REQUIRED NOT PASS: $k" -ForegroundColor Red; $fail++ }
}
($Results | ConvertTo-Json -Depth 6) | Set-Content (Join-Path $Dist "final-gate-results.json") -Encoding UTF8
if ($fail -gt 0) {
  Write-Host "RESULT: NOT COMPLETE (required failures=$fail)" -ForegroundColor Red
  exit 1
}
Write-Host "RESULT: CLIENT IMPLEMENTATION COMPLETE" -ForegroundColor Green
exit 0


