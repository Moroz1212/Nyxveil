module github.com/nyxveil/client-ios/bridge

go 1.26.0

require (
	github.com/nyxveil/nvp v0.0.0
	github.com/quic-go/quic-go v0.48.2
	golang.org/x/crypto v0.55.0
)

require (
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.1 // indirect
	github.com/google/pprof v0.0.0-20240409012703-83162a5b38cd // indirect
	github.com/onsi/ginkgo/v2 v2.9.5 // indirect
	github.com/quic-go/qpack v0.5.1 // indirect
	go.uber.org/mock v0.4.0 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/mobile v0.0.0-20260821190718-4776eadac327 // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

replace github.com/nyxveil/nvp => ../../windows/third_party/nvp

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)
