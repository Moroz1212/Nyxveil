# Wintun

The Windows client service loads `wintun.dll` (official WireGuard Wintun 0.14.1)
from the same directory as `Nyxveil.Service.exe`. The redistributable is vendored
under `third_party/wintun/` with the official prebuilt binary license.

Zip SHA2-256: `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`

Frozen Core is unchanged; the client injects `Factory.OpenFunc` only.
