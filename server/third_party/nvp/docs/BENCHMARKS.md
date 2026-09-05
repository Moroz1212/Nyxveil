# Benchmarks

Measured on this host during acceptance packaging. Results are environment-dependent.

## Environment

- OS/Arch: windows/amd64
- Go: `go version go1.27.0 windows/amd64`
- CPU: AMD Ryzen 5 3600 6-Core Processor              
- Date: 2026-09-04
- Command:

```bash
go test -bench=. -benchmem ./core/keys/ ./core/packet/ ./core/replay/ ./core/auth/ticket/ ./core/session/ -count=1
```

## Measured results (raw)

```
goos: windows

goarch: amd64

pkg: github.com/nyxveil/nvp/core/keys

cpu: AMD Ryzen 5 3600 6-Core Processor              

BenchmarkDeriveSessionKeys-12    	  441135	      2548 ns/op	    2850 B/op	      39 allocs/op

BenchmarkAEADSealOpen-12         	  773973	      1570 ns/op	 652.37 MB/s	    2240 B/op	       6 allocs/op

PASS

ok  	github.com/nyxveil/nvp/core/keys	2.814s

PASS

ok  	github.com/nyxveil/nvp/core/packet	0.251s

PASS

ok  	github.com/nyxveil/nvp/core/replay	0.184s

goos: windows

goarch: amd64

pkg: github.com/nyxveil/nvp/core/auth/ticket

cpu: AMD Ryzen 5 3600 6-Core Processor              

BenchmarkTicketVerify-12    	   18918	     63832 ns/op	    3106 B/op	      48 allocs/op

PASS

ok  	github.com/nyxveil/nvp/core/auth/ticket	2.207s

PASS

ok  	github.com/nyxveil/nvp/core/session	0.882s
```

## Notes

- Packages that print only `PASS` have no `Benchmark*` functions; they were included for harness completeness.
- Numbers are not DPI/network throughput claims.