package store

import (
	"fmt"
	"testing"
)

func benchmarkPutParallel(b *testing.B, mode SyncMode) {
	dir := b.TempDir()
	db, err := Open(dir, Options{SyncMode: mode})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer db.Close()

	val := []byte("benchmark-value-0123456789")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Appendf(nil, "key-%d-%d", i, b.N)
			if err := db.Put(key, val); err != nil {
				b.Fatalf("Put: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSyncEveryWrite fsyncs the WAL on every single write.
func BenchmarkSyncEveryWrite(b *testing.B) {
	benchmarkPutParallel(b, SyncEveryWrite)
}

// BenchmarkGroupCommit batches concurrent writers into shared fsyncs. Run
// with -cpu to vary concurrency; the gap versus BenchmarkSyncEveryWrite
// should widen as more goroutines contend (go test -bench . -cpu 1,4,16).
func BenchmarkGroupCommit(b *testing.B) {
	benchmarkPutParallel(b, GroupCommit)
}
