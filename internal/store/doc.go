// Package store is the storage layer. It has two engines:
//
//   - Store: in-memory map with TTLs. The server uses this today.
//   - DB: LSM engine with WAL, memtables and SSTables. Not wired in yet.
//
// DB only acknowledges a write after its WAL record is fsynced, so it
// survives a crash. See docs/storage-engine.md.
package store
