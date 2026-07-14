// Tests for the restart debouncer — the piece that turns "files are
// still being written" into "exactly one restart, once things settle".
package watch

import "testing"

func TestDebouncerFiresAfterQuietPeriod(t *testing.T) {
	d := NewDebouncer(2)
	if d.Observe(true) {
		t.Fatal("must not fire on the change itself")
	}
	if d.Observe(false) {
		t.Fatal("must not fire after only one quiet tick (need two)")
	}
	if !d.Observe(false) {
		t.Fatal("should fire after the second quiet tick")
	}
	// quietTicks < 1 is clamped to 1.
	clamped := NewDebouncer(0)
	clamped.Observe(true)
	if !clamped.Observe(false) {
		t.Fatal("quietTicks < 1 should behave like 1")
	}
}

func TestDebouncerCoalescesABurstIntoOneRestartThenReArms(t *testing.T) {
	d := NewDebouncer(1)
	fires := 0
	// A build touching files across five ticks, then quiet.
	for _, changed := range []bool{true, true, true, true, true, false, false, false} {
		if d.Observe(changed) {
			fires++
		}
	}
	if fires != 1 {
		t.Fatalf("a burst must produce exactly one restart, got %d", fires)
	}
	// A second, later burst must fire again.
	d.Observe(true)
	if !d.Observe(false) {
		t.Fatal("debouncer must re-arm after firing")
	}
}

func TestDebouncerCountdownResetsAndStaysIdleWithoutChanges(t *testing.T) {
	d := NewDebouncer(2)
	d.Observe(true)
	d.Observe(false) // 1 quiet tick
	d.Observe(true)  // new change: countdown restarts
	if d.Observe(false) {
		t.Fatal("countdown must restart after an interleaved change")
	}
	if !d.Observe(false) {
		t.Fatal("should fire once the fresh countdown completes")
	}
	// After settling, quiet ticks alone must never fire.
	for i := 0; i < 10; i++ {
		if d.Observe(false) {
			t.Fatal("must never fire without a change")
		}
	}
	if d.Pending() {
		t.Fatal("nothing should be pending")
	}
}
