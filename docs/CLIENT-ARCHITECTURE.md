# Client Architecture

## Platform Separation

NVP/1 cryptographic core is platform-agnostic. Client applications add:

| Layer | Windows | Android |
|-------|---------|---------|
| TUN interface | Wintun (`client/windows`) | VpnService fd (`client/android`) |
| Routing | OS routing table | VpnService.Builder |
| Split tunnel | App-based routing | Per-app allow/deny |
| DNS | System/private DNS config | Protected DNS (DoH/DoT) |
| Bootstrap | Control Plane HTTPS | Control Plane HTTPS |

## Windows Client (`client/windows`)

- `Factory.Open()` creates Wintun adapter (integration point)
- Uses `tunnel.Device` interface
- Split tunneling: Windows Filtering Platform / app routes (platform layer)

Build: `go build -tags windows ./client/windows/...`

## Android Client (`client/android`)

- `SDK` — gomobile-compatible entry point
- `TUNDevice` — wraps ParcelFileDescriptor from VpnService
- `SetRouteMode()` — All / Selected Apps / Off
- `Connect()` — Control Plane ticket + NVP session (foundation)

Production integration:
1. Kotlin VpnService establishes TUN fd
2. Pass read/write/close callbacks to `NewTUNDevice`
3. Go SDK manages NVP session and packet tunneling

## Shared Flow

```
App UI
  → Control Plane (license, device, ticket)
  → Node selection / failover
  → Transport racing (QUIC → TLS)
  → NVP session (handshake, auth, data)
  → TUN read/write loop
```

Independent security/cryptographic audit: **NOT PERFORMED**
