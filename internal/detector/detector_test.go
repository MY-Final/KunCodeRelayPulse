package detector

import (
	"testing"

	"kuncode-relay-pulse/internal/store"
)

func TestRestorePreservesDownState(t *testing.T) {
	d := New(2)
	d.Restore([]store.DetectorState{{
		ChannelID:           "ch_test",
		Model:               "model-a",
		ConsecutiveFailures: 4,
		Down:                true,
	}})

	events := d.Observe("ch_test", "model-a", 1, 123)
	if len(events) != 1 || events[0].Type != "up" {
		t.Fatalf("restored down state should emit up on success, got %#v", events)
	}
}

func TestResetChannelStartsFreshForAllModels(t *testing.T) {
	d := New(2)
	d.Observe("ch_reset", "model-a", 0, 100)
	d.Observe("ch_reset", "model-b", 0, 100)
	d.Observe("ch_other", "model-a", 0, 100)

	d.ResetChannel("ch_reset")
	if events := d.Observe("ch_reset", "model-a", 0, 101); len(events) != 0 {
		t.Fatalf("first failure after reset should not emit down, got %#v", events)
	}
	if events := d.Observe("ch_other", "model-a", 0, 101); len(events) != 1 || events[0].Type != "down" {
		t.Fatalf("other channel state should remain intact, got %#v", events)
	}
}

func TestDegradedAndFailedStreaksEscalateAndRecover(t *testing.T) {
	d := New(3, 2)

	for i := int64(1); i <= 2; i++ {
		if events := d.Observe("ch_test", "model-a", 2, i); len(events) != 0 {
			t.Fatalf("degraded probe %d emitted events: %#v", i, events)
		}
	}
	if events := d.Observe("ch_test", "model-a", 0, 3); len(events) != 1 || events[0].Type != "down" {
		t.Fatalf("third anomaly should emit down, got %#v", events)
	}
	state := d.Snapshot("ch_test", "model-a", 3)
	if state.ConsecutiveDegraded != 0 || state.ConsecutiveFailures != 1 || state.ConsecutiveAnomalies != 3 || !state.Down || state.RecoveryThreshold != 2 {
		t.Fatalf("unexpected escalated state: %#v", state)
	}
	if events := d.Observe("ch_test", "model-a", 1, 4); len(events) != 0 {
		t.Fatalf("first success should wait for recovery threshold, got %#v", events)
	}
	if events := d.Observe("ch_test", "model-a", 1, 5); len(events) != 1 || events[0].Type != "up" {
		t.Fatalf("second success should emit up, got %#v", events)
	}
	state = d.Snapshot("ch_test", "model-a", 5)
	if state.Down || state.ConsecutiveSuccesses != 2 || state.ConsecutiveAnomalies != 0 {
		t.Fatalf("unexpected recovered state: %#v", state)
	}
}
