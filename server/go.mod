module github.com/nyxveil/server

go 1.24

require (
	github.com/nyxveil/nvp v0.0.0
	golang.org/x/sys v0.28.0
)

require (
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.1 // indirect
	github.com/google/pprof v0.0.0-20240409012703-83162a5b38cd // indirect
	github.com/onsi/ginkgo/v2 v2.9.5 // indirect
	github.com/quic-go/qpack v0.5.1 // indirect
	github.com/quic-go/quic-go v0.48.2 // indirect
	go.uber.org/mock v0.4.0 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	golang.org/x/tools v0.21.1-0.20240508182429-e35e4ccd0d2d // indirect
)

// Frozen Protocol Core 1.0.0 (NVP/1) — vendored from Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip.
// Do not edit third_party/nvp; replace with a fresh extract of the authoritative zip.
replace github.com/nyxveil/nvp => ./third_party/nvp
