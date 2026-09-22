package store

// SyncMode controls when a write's WAL record is fsynced before the write
// is acknowledged.
type SyncMode int

const (
	// SyncEveryWrite fsyncs the WAL before acknowledging each write. This
	// is the safest mode and the default.
	SyncEveryWrite SyncMode = iota
	// GroupCommit batches concurrent writers and fsyncs once per batch;
	// every writer in a batch waits for that one fsync. Higher throughput
	// under concurrent load, at the cost of each write's fsync possibly
	// being shared (and therefore very slightly delayed) rather than
	// immediate.
	GroupCommit
)

// Options configures a DB.
type Options struct {
	// SyncMode selects the WAL durability mode. Zero value is
	// SyncEveryWrite.
	SyncMode SyncMode

	// MemtableSizeThreshold is the approximate size, in bytes, at which
	// the active memtable is frozen and rotated out. Zero means the
	// default (4 MiB).
	MemtableSizeThreshold int64

	// MaxImmutableMemtables is how many frozen memtables may be queued for
	// flushing before writes start blocking for backpressure. Zero means
	// the default (2).
	MaxImmutableMemtables int
}

const (
	defaultMemtableSizeThreshold = 4 << 20 // 4 MiB
	defaultMaxImmutableMemtables = 2
)

func (o Options) withDefaults() Options {
	if o.MemtableSizeThreshold <= 0 {
		o.MemtableSizeThreshold = defaultMemtableSizeThreshold
	}
	if o.MaxImmutableMemtables <= 0 {
		o.MaxImmutableMemtables = defaultMaxImmutableMemtables
	}
	return o
}
