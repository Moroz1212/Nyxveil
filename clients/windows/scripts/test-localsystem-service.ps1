#Requires -RunAsAdministrator
# Real LocalSystem deployment smoke: service as LocalSystem, pipe ACL for interactive SID.
$ErrorActionPreference = "Stop"
$Svc = "NyxveilClientServiceTest"
$Root = Split-Path $PSScriptRoot -Parent
$Bin = Join-Path $Root "engine\dist\Nyxveil.Service.exe"
$Pipe = "\\.\pipe\NyxveilLocalSystemTest"
if (-not (Test-Path $Bin)) {
  Push-Location (Join-Path $Root "engine")
  go build -o dist\Nyxveil.Service.exe .\cmd\nyxveil-service
  Pop-Location
}
if (-not (Test-Path $Bin)) { throw "build service first: $Bin" }

# Provision CURRENT interactive user (not LocalSystem) for pipe ACL.
& $Bin -provision-sid
if ($LASTEXITCODE -ne 0) { throw "provision-sid failed" }

sc.exe stop $Svc 2>$null | Out-Null
sc.exe delete $Svc 2>$null | Out-Null
sc.exe create $Svc binPath= "`"$Bin`" -pipe $Pipe" start= demand obj= LocalSystem | Out-Null
sc.exe start $Svc | Out-Null
Start-Sleep 2

$q = sc.exe query $Svc | Out-String
if ($q -notmatch "RUNNING") { throw "service not running: $q" }

# Dial pipe as current (non-LocalSystem) user — must succeed with provisioned SID ACL.
Add-Type -TypeDefinition @"
using System;
using System.IO;
using System.IO.Pipes;
using System.Text;
public static class NvPipeProbe {
  public static string Dial(string name) {
    using (var p = new NamedPipeClientStream(".", name.Replace(@"\\.\pipe\", ""), PipeDirection.InOut)) {
      p.Connect(5000);
      using (var w = new StreamWriter(p, new UTF8Encoding(false), 1024, true) { AutoFlush = true, NewLine = "\n" })
      using (var r = new StreamReader(p, Encoding.UTF8, false, 1024, true)) {
        w.WriteLine("{\"v\":1,\"type\":\"hello\"}");
        return r.ReadLine() ?? "";
      }
    }
  }
}
"@
$pipeLeaf = $Pipe -replace '^\\\\.\\pipe\\', ''
try {
  $resp = [NvPipeProbe]::Dial($pipeLeaf)
  Write-Host "PIPE_DIAL_OK response_len=$($resp.Length)"
} catch {
  sc.exe stop $Svc 2>$null | Out-Null
  sc.exe delete $Svc 2>$null | Out-Null
  throw "PIPE NORMAL USER dial FAILED under LocalSystem service: $_"
}

sc.exe stop $Svc | Out-Null
Start-Sleep 2
$q2 = sc.exe query $Svc | Out-String
if ($q2 -match "RUNNING") { throw "service still RUNNING after stop" }
sc.exe delete $Svc | Out-Null
Write-Host "LOCALSYSTEM_SERVICE_TEST PASS (pipe dial + SCM stop)"
