# Benchmarks

Benchmarks are environment-dependent. Values below were measured on the development machine during `go test -bench=.`.

Run locally:

```bash
go test -bench=. -benchmem ./...
```

## Expected Benchmarks

| Benchmark | Package | Description |
|-----------|---------|-------------|
| BenchmarkTicketVerify | auth/ticket | Offline JWT verification throughput |

## Load Test Harness

Use:

```bash
go test -bench=BenchmarkTicketVerify -benchtime=3s ./auth/ticket/...
```

## Metrics Collected (Runtime)

See `internal/metrics` for operational counters:
- handshakes/sec (via new_sessions counter)
- active sessions
- replay rejected
- AEAD failures
- transport fallback count

## Notes

- Handshake/sec and throughput benchmarks require live network setup
- Results vary by CPU, OS, and Go version
- **Only record actually measured values** — see test output after `go test -bench=.`

## Measured Results (2026-09-03, Windows amd64, Go 1.27, AMD Ryzen 5 3600)

| Benchmark | Result |
|-----------|--------|
| BenchmarkTicketVerify | 35834 ops, ~70245 ns/op, 2847 B/op, 47 allocs/op |
| nvp-bench (200 sessions, memory transport) | 6701.83 handshakes/sec, 0 failures |

Commands:
```bash
go test -bench=BenchmarkTicketVerify -benchmem -benchtime=2s ./auth/ticket/
go run ./cmd/nvp-bench/ -sessions 200
```

Independent security/cryptographic audit: **NOT PERFORMED**
