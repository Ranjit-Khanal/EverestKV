package store

// SyncMode controls when the WAL is fsynced.
type SyncMode int

const (
	// SyncEveryWrite fsyncs every write. The default.
	SyncEveryWrite SyncMode = iota
	// GroupCommit fsyncs once per batch of concurrent writes. Faster under load.
	GroupCommit
)

// Options configures a DB.
type Options struct {
	// SyncMode defaults to SyncEveryWrite.
	SyncMode SyncMode

	// MemtableSizeThreshold is the memtable size that triggers a flush. Default 4 MiB.
	MemtableSizeThreshold int64

	// MaxImmutableMemtables is how many memtables can wait to flush before writes block. Default 2.
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
