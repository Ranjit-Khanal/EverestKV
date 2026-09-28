# Storage engine

`internal/store.DB` is EverestKV's persistent engine: a small **log-structured merge-tree (LSM)**.
This document covers its write path, on-disk formats, crash-safety argument, and current
limitations.

> **Status:** the write path, flushing, crash recovery, and point reads (memtables and SSTables)
> are implemented and tested. Compaction and integration with the server are **not** implemented
> yet. The server still uses the in-memory `store.Store`. See [Limitations](#limitations).

## Overview

```
          Put / Delete
               │
               ▼
   ┌───────────────────────┐   1. append record          ┌──────────────────┐
   │        DB.write       │ ──────────────────────────▶ │  WAL segment N   │ wal-00000N.log
   │                       │   2. fsync (per SyncMode)   └──────────────────┘
   │                       │
   │                       │   3. insert                 ┌──────────────────┐
   │                       │ ──────────────────────────▶ │ active memtable  │ skip list
   └───────────────────────┘                             └────────┬─────────┘
               │ 4. ack                                           │ size ≥ threshold
               ▼                                                  ▼ rotate()
                                                          ┌──────────────────┐
                                                          │ immutable queue  │ ≤ MaxImmutableMemtables
                                                          └────────┬─────────┘
                                                                   │ flushLoop (1 goroutine)
                                                                   ▼
                                     ┌──────────────┐     ┌──────────────────┐
                                     │   MANIFEST   │ ◀── │  00000K.sst      │
                                     └──────────────┘     └──────────────────┘
                                             then delete obsolete WAL segments
```

## Using the engine

```go
db, err := store.Open("/var/lib/everestkv", store.Options{
    SyncMode:              store.GroupCommit, // default: store.SyncEveryWrite
    MemtableSizeThreshold: 4 << 20,           // default: 4 MiB
    MaxImmutableMemtables: 2,                 // default: 2
})
if err != nil { ... }
defer db.Close()

err = db.Put([]byte("k"), []byte("v"))
v, found, err := db.Get([]byte("k"))
err = db.Delete([]byte("k"))
```

| Option                  | Default          | Meaning                                                                 |
|-------------------------|------------------|-------------------------------------------------------------------------|
| `SyncMode`              | `SyncEveryWrite` | `SyncEveryWrite` fsyncs per write. `GroupCommit` shares one fsync across concurrent writers. |
| `MemtableSizeThreshold` | 4 MiB            | Approximate key+value bytes at which the active memtable is rotated.   |
| `MaxImmutableMemtables` | 2                | Frozen memtables allowed to wait for flush before writers block.       |

After `Close`, every method returns `store.ErrClosed`. If the background flush fails, the error is
*sticky*: all later writes return it, and so does `Close`.

## Write path

`DB.write` (`internal/store/db.go`) runs in four steps:

1. **Append** a `wal.Record{Seq, Type, Key, Value}` to the current WAL segment. `Seq` is a
   monotonically increasing sequence number from an atomic counter.
2. **Make it durable** according to `SyncMode` (see [Group commit](#group-commit)).
3. **Insert** into the active memtable. This happens only after step 2, so **a write is never
   visible or acknowledged before it is durable**.
4. **Return.** If the memtable is now over `MemtableSizeThreshold`, the writer calls `rotate()`
   before returning.

Steps 1–3 run under `db.mu.RLock()`. Many writers can be in this section at once, which is what
lets group commit batch them. `rotate()` needs the write lock, so it cannot swap out the memtable
or log while any writer is still in the middle of an append.

### Rotation and backpressure

`rotate()` takes `db.mu.Lock()` and then:

1. Re-checks the threshold, because another writer may already have rotated.
2. Blocks on `flushCond` while `len(immutables) >= MaxImmutableMemtables`. This is the
   **backpressure**: if disk flushes can't keep up, writers stall rather than using unbounded
   memory.
3. Creates WAL segment `N+1` and swaps in a fresh memtable.
4. Queues the frozen memtable together with the range of WAL segments its data came from
   (`firstWALSegment..lastWALSegment`).
5. Closes the old segment. Its contents were already fsynced before any write in it was
   acknowledged.

### Flushing

A single goroutine (`flushLoop`) takes the oldest immutable memtable and runs `flushOne`:

1. Stream the memtable in key order into a new SSTable (`sstable.Writer`). The file is written as
   `00000K.sst.tmp`, fsynced, renamed to `00000K.sst`, and the directory is fsynced.
2. Save a new **MANIFEST** listing the SSTable as live, with
   `WALSafeDeleteBelow = lastWALSegment + 1`.
3. Open an `sstable.Reader` for the new table, then delete WAL segments
   `firstWALSegment..lastWALSegment`.
4. Under `db.mu`, in one step, add the reader to `db.tables` and pop the memtable off the queue,
   then broadcast, which wakes any writers blocked on backpressure.

The memtable stays readable in the immutable queue until step 4, and the SSTable becomes readable
in the same critical section, so `Get` always finds a flushed key in exactly one of the two.

## Read path

`DB.Get` checks, newest data first, and returns the first entry it finds for the key:

1. the active memtable,
2. the immutable memtables, newest first,
3. the live SSTables (`db.tables`), newest first. SSTable ids are assigned in flush order, so a
   higher id always holds newer data.

Because the first entry wins, a tombstone shadows every older value, including values in older
SSTables. Each SSTable lookup binary-searches that table's in-memory sparse index and reads one
data block (see [SSTable](#sstable-nnnnnnsst)).

`Get` holds `db.mu.RLock()` for the whole lookup, including the disk reads. Writers also take the
read lock, so reads and writes don't block each other. It does mean `rotate()`, the flush
goroutine's final step, and `Close` wait for in-flight reads, which in exchange guarantees a table
is never closed while a read is using it.

`Open` opens a reader for every SSTable listed in the MANIFEST, and fails if any of them is
missing or corrupt. `Close` closes them all.

## Group commit

`fsync` is the dominant cost of a durable write. In `GroupCommit` mode (`groupcommit.go`),
concurrent writers share fsyncs:

- Every writer appends its record under `g.mu` and takes a ticket (`appended++`).
- If no fsync is in flight, that writer becomes the **leader**. It records
  `target = appended`, releases the lock, calls `Sync()`, then marks `synced = target` and wakes
  everyone.
- Writers whose ticket is `≤ synced` return success. Anyone who appended after the leader's
  snapshot waits for the next round, where one of them becomes leader.
- If a sync fails, `failUpTo = target` and every writer whose ticket is in that batch gets the
  error.

**Correctness:** appends happen under the same mutex the leader holds when it takes its snapshot.
So every append counted in `target` has completed before `Sync()` is called, and that fsync
covers it. No writer returns success unless an fsync covering its own record has completed.

Measure the difference yourself:

```bash
go test ./internal/store -run '^$' -bench . -cpu 1,4,16
```

## Memtable

`internal/store/memtable` is a skip list (max 16 levels, p = 0.25) ordered by `bytes.Compare`,
wrapped in a `sync.RWMutex`.

- It stores only the **latest** state per key (value or tombstone, plus its sequence number).
  Overwrites replace the entry in place.
- `Size()` is the approximate sum of `len(key) + len(value)` and drives rotation.
- Keys and values are copied on insert, so callers may reuse their buffers.
- `Iterator` walks the level-0 list without holding the lock. Use it **only on a frozen
  memtable**; iterating a memtable that is still taking writes is a data race.

## On-disk formats

All integers are **little-endian**. A data directory looks like:

```
data/
  MANIFEST            JSON, replaced atomically
  wal-000007.log      WAL segments still needed for recovery
  wal-000008.log      (current segment)
  000001.sst          SSTables listed in MANIFEST
  000002.sst
```

### WAL segment (`wal-NNNNNN.log`)

A segment is a sequence of records:

```
┌──────────┬──────────┬──────────┬─────────┬──────────┬───────┬──────────┬─────────┐
│ crc32    │ length   │ seq      │ type    │ keyLen   │ key   │ valLen   │ value   │
│ uint32   │ uint32   │ uint64   │ uint8   │ uint32   │ bytes │ uint32   │ bytes   │
└──────────┴──────────┴──────────┴─────────┴──────────┴───────┴──────────┴─────────┘
            └──────────────────────── covered by crc32 (Castagnoli) ─────────────────┘
                       └────────────── body: `length` bytes ────────────────────────┘
```

- `type`: `1` = put, `2` = delete (tombstone; `valLen` is 0).
- On read, a short read, a checksum mismatch, or inconsistent inner lengths is treated as a
  **torn tail**, meaning the last write of a crashed process. The reader returns `io.EOF` there.
  It does not return an error.

### SSTable (`NNNNNN.sst`)

```
data record 0
data record 1
...
sparse index
footer (16 bytes)
```

- **Data record:** `[keyLen uint32][key][type uint8][valLen uint32][value]`, in strictly
  increasing key order. Tombstones are kept (type `2`, empty value) so that they can shadow
  older values once multi-level reads exist.
- **Sparse index:** one `[keyLen uint32][key][offset uint64]` entry for the first record of each
  ~4 KiB block. `sstable.Reader` loads it into memory on open; a lookup binary-searches it for
  the last block whose first key is `<=` the target and scans only that block.
- **Footer:** `[indexOffset uint64][magic uint64]`, where `magic = 0x45766572657374`
  ("Everest").

### MANIFEST

```json
{"sstables":[1,2,3],"wal_safe_delete_below":9}
```

- `sstables`: ids of the **live** SSTables. Any `.sst` file not listed here is an orphan from a
  crash and is ignored.
- `wal_safe_delete_below`: every WAL segment with a lower number is fully captured by listed
  SSTables and does not need replaying.

The manifest is saved by writing `MANIFEST.tmp`, fsyncing it, renaming it over `MANIFEST`, and
fsyncing the directory.

## Recovery

`store.Open(dir, opts)`:

1. Loads `MANIFEST`. If there is no manifest, the directory is treated as a fresh database.
2. Lists the `wal-*.log` files and replays, in order, every segment `>= wal_safe_delete_below`
   into a new memtable. Replay stops at a torn tail.
3. Restores the sequence counter to the highest `seq` seen.
4. Opens a **new** segment (`last + 1`) for writes. Existing segments are never appended to.
5. The first memtable after recovery remembers that it depends on all the replayed segments.
   Its eventual flush therefore deletes all of them.

### Crash-safety invariants

| Crash point                                  | Why it's safe                                                         |
|----------------------------------------------|-----------------------------------------------------------------------|
| Mid WAL append                               | Torn record fails the CRC or is short, so replay stops before it. It was never acknowledged. |
| After fsync, before memtable insert          | Record is in the WAL and is replayed.                                 |
| Mid SSTable write                            | Only a `.sst.tmp` exists. It is not in the manifest and is ignored.   |
| After SSTable rename, before manifest save   | `.sst` is not listed and is ignored. The WAL segments still exist and are replayed. |
| After manifest save, before WAL delete       | Segments are below `wal_safe_delete_below` and are skipped on replay. |

These cases are covered by `TestCrashRecoveryReplaysUnflushedWrites`,
`TestCrashMidFlushIgnoresPartialSSTable`, `TestWriteOrderIsDurableBeforeAck`, and the WAL reader's
truncation and checksum tests.

## Concurrency

| State                                         | Protected by                                                         |
|-----------------------------------------------|----------------------------------------------------------------------|
| `active`, `log`, `activeWALFirst`             | `db.mu`: writers take RLock, rotate/Close take Lock                  |
| `immutables`, `closed`, `flushErr`            | `db.mu` (Lock); changes are signaled on `flushCond`                  |
| `nextSSTableID`, `liveSSTables`               | Touched only by the flush goroutine after `Open`                     |
| `seqCounter`                                  | `atomic.Uint64`                                                      |
| Memtable contents                             | The memtable's own `RWMutex`                                         |
| WAL appends/syncs                             | `commitLog.mu` (SyncEveryWrite) or `groupCommit.mu` (GroupCommit)    |

## Limitations

These are known and tracked. Contributions are welcome.

- **No compaction.** SSTables accumulate, and tombstones are never purged.
- **No iteration / range scans** across the engine (needed for `KEYS`).
- **Not used by the server yet.** The server still uses the in-memory `store.Store`.
- **Orphan files are ignored, not cleaned up.** Stray `.sst` / `.sst.tmp` files left by a crash are
  never deleted. Because SSTable ids restart from the highest *live* id, a later flush can pick an
  id whose `.sst.tmp` orphan still exists. `sstable.NewWriter` opens with `O_EXCL`, so that flush
  fails and the error becomes sticky. Until `Open` cleans up orphans, remove unlisted `*.sst.tmp`
  files manually after a crash.
- **Single process.** There is no lock file, so two processes opening the same directory will
  corrupt it.
- Record/key/value lengths are `uint32`, so each key and each value must be under 4 GiB.
