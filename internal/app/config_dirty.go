package app

import "sync/atomic"

// AtomicDirty is the restart-to-apply flag: any adapter/settings mutation flips it,
// and GET /config/pending-restart reports it so the UI shows a "Restart to apply"
// banner. M4a applies adapter changes on the next process start (no hot-reload).
type AtomicDirty struct{ b atomic.Bool }

func (d *AtomicDirty) Set()        { d.b.Store(true) }
func (d *AtomicDirty) Dirty() bool { return d.b.Load() }
