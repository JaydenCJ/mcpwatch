package watch

// Debouncer turns a stream of per-tick "did anything change?" booleans
// into restart decisions: it arms on the first change and fires only
// after quietTicks consecutive quiet ticks, so a burst of saves (or a
// build writing many files) triggers exactly one restart.
//
// It is a pure state machine — the caller owns the clock — which is why
// the restart policy can be tested without a single sleep.
type Debouncer struct {
	quietTicks int
	pending    bool
	quiet      int
}

// NewDebouncer returns a Debouncer that fires after quietTicks quiet
// polls following a change. quietTicks < 1 is clamped to 1.
func NewDebouncer(quietTicks int) *Debouncer {
	if quietTicks < 1 {
		quietTicks = 1
	}
	return &Debouncer{quietTicks: quietTicks}
}

// Observe records one poll result and reports whether the pending
// change burst has settled and a restart should fire now.
func (d *Debouncer) Observe(changed bool) bool {
	if changed {
		d.pending = true
		d.quiet = 0
		return false
	}
	if !d.pending {
		return false
	}
	d.quiet++
	if d.quiet < d.quietTicks {
		return false
	}
	d.pending = false
	d.quiet = 0
	return true
}

// Pending reports whether a change burst is waiting to settle.
func (d *Debouncer) Pending() bool { return d.pending }
