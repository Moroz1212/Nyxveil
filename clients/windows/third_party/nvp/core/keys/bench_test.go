package keys

import "testing"

func BenchmarkDeriveSessionKeys(b *testing.B) {
	var shared [32]byte
	shared[0] = 7
	transcript := []byte("bench-transcript")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DeriveSessionKeys(shared, transcript, uint32(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAEADSealOpen(b *testing.B) {
	var shared [32]byte
	shared[0] = 3
	sk, err := DeriveSessionKeys(shared, []byte("t"), 1)
	if err != nil {
		b.Fatal(err)
	}
	aead, err := NewClientAEAD(sk.ClientToServer)
	if err != nil {
		b.Fatal(err)
	}
	pt := make([]byte, 1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(pt)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ct, err := aead.Seal(1, uint64(i), pt)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := aead.Open(1, uint64(i), ct); err != nil {
			b.Fatal(err)
		}
	}
}
