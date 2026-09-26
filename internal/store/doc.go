// Package store holds EverestKV's storage layer. It currently contains two
// independent engines:
//
//   - Store, a thread-safe in-memory map[string]string. This is what the
//     server uses today; data does not survive a restart.
//
//   - DB, a log-structured merge-tree (LSM) engine with a write-ahead log,
//     skip-list memtables, SSTable flushes and a manifest for crash
//     recovery. It is not yet wired into the server, and its read path does
//     not yet consult SSTables (see DB.Get).
//
// Durability contract for DB: a Put or Delete returns only after its WAL
// record has been fsynced (per Options.SyncMode), so an acknowledged write
// survives a crash and is replayed by Open. See docs/storage-engine.md for
// the on-disk formats and the recovery protocol.
//
// Subpackages: wal (commit log), memtable (in-memory sorted buffer),
// sstable (on-disk sorted tables) and manifest (live-file bookkeeping).
package store
