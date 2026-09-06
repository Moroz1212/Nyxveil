# Control Plane TLS configure (1.0.5)

Transactional reconfiguration of HTTPS certificate / hostname / PublicBaseUrl on an
**existing** Windows Control Plane install. Does not change local listen port, NAT, or firewall.

## Config precedence

| Setting | Runtime SoT (service) | Ops metadata |
|---------|----------------------|--------------|
| Port, PublicHostname, PublicBaseUrl, Certificate.* | `InstallDir\appsettings.Production.json` | `C:\ProgramData\Nyxveil\ControlPlane\operational.json` (+ mirror `InstallDir\config\operational.json`) |
| FirewallRuleName | n/a | operational.json only |

`tls configure` updates **both** appsettings.Production.json and operational.json (dual-write).
ProgramData operational.json is preferred when reading ops metadata (avoids nested `config\config` drift).

## PublicBaseUrl semantics

`PublicBaseUrl` is the **externally advertised** HTTPS URL for operators.

- It is **not** read by C# for Kestrel bind, catalog URLs, redirects, admin links, or node callbacks.
- Local health / TLS probes use `PublicHostname` + **local** `Hosting:Port` (e.g. 8443).
- With NAT `Internet:18443 → Windows:8443`, set:

  `PublicBaseUrl = https://cp.nyxveil.ru:18443`

  while `Hosting:Port` remains `8443`.

## Commands

Run as Administrator from the install directory (or full path to the service exe):

```text
& "C:\Program Files\Nyxveil\ControlPlane\Nyxveil.ControlPlane.Web.exe" tls status

& "C:\Program Files\Nyxveil\ControlPlane\Nyxveil.ControlPlane.Web.exe" tls configure --check `
  --hostname cp.nyxveil.ru `
  --public-url https://cp.nyxveil.ru:18443 `
  --certificate-thumbprint <THUMBPRINT>

& "C:\Program Files\Nyxveil\ControlPlane\Nyxveil.ControlPlane.Web.exe" tls configure `
  --hostname cp.nyxveil.ru `
  --public-url https://cp.nyxveil.ru:18443 `
  --certificate-thumbprint <THUMBPRINT>
```

PFX mode (imports into LocalMachine\My, then Store + thumbprint):

```text
... tls configure --hostname cp.nyxveil.ru --public-url https://cp.nyxveil.ru:18443 `
  --certificate-pfx C:\secure\cp.nyxveil.ru.pfx --certificate-pfx-password <secret>
```

`--check` validates without writing config, stopping the service, importing (thumbprint mode), or changing ACL.

## Rollback

On start/health failure after commit attempt: restore previous appsettings.Production.json + operational.json,
restart only `NyxveilControlPlane`, require old health OK. Old certificate is never deleted.

## Update 1.0.4 → 1.0.5

```powershell
# From release package (publish\ folder):
powershell -ExecutionPolicy Bypass -File .\scripts\update-windows.ps1 -PublishDir .\publish
```

Preserves DB, DPAPI secrets, DataProtection keys, port/NAT/firewall, nodes/licenses.
No DB schema migration required for TLS configure.
