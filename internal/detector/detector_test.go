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
