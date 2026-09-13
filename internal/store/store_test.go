package store

import (
	"path/filepath"
	"testing"
)

func TestRecordProbePersistsStateAndEvent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	row := ProbeRow{
		ChannelID: "ch_test", Model: "model-a", Status: 0,
		SubStatus: "timeout", TS: 100, ErrorDetail: "upstream detail",
	}
	events := []Event{{ChannelID: "ch_test", Model: "model-a", Type: "down", TS: 100, Detail: "timeout"}}
	state := DetectorState{
		ChannelID: "ch_test", Model: "model-a", ConsecutiveFailures: 2,
		Down: true, UpdatedAt: 100,
	}
	if err := st.RecordProbe(row, events, state); err != nil {
		t.Fatal(err)
	}

	states, err := st.LoadDetectorStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0] != state {
		t.Fatalf("persisted state = %#v, want %#v", states, state)
	}
	latest, err := st.Latest("ch_test", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ErrorDetail != row.ErrorDetail {
		t.Fatalf("latest probe = %#v, want %#v", latest, row)
	}
	recent, err := st.RecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0] != events[0] {
		t.Fatalf("persisted events = %#v, want %#v", recent, events)
	}
}

func TestRecentProbesReturnsLimitedHistoryInAscendingOrder(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for _, row := range []ProbeRow{
		{ChannelID: "ch_test", Model: "model-a", Status: 1, HTTPCode: 200, LatencyMS: 120, TS: 100},
		{ChannelID: "ch_test", Model: "model-a", Status: 2, HTTPCode: 200, LatencyMS: 2400, TS: 200},
		{ChannelID: "ch_test", Model: "model-a", Status: 0, HTTPCode: 500, LatencyMS: 400, TS: 300},
		{ChannelID: "other", Model: "model-a", Status: 1, HTTPCode: 200, LatencyMS: 1, TS: 400},
	} {
		if err := st.InsertProbe(row); err != nil {
			t.Fatal(err)
		}
	}

	points, err := st.RecentProbes("ch_test", "model-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].TS != 200 || points[1].TS != 300 {
		t.Fatalf("recent probes = %#v, want timestamps [200 300]", points)
	}
	if points[0].Status != 2 || points[1].HTTPCode != 500 {
		t.Fatalf("recent probe details = %#v", points)
	}
}

func TestResetChannelClearsProbesEventsAndDetectorState(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	resetRow := ProbeRow{ChannelID: "ch_reset", Model: "model-a", Status: 0, TS: 100}
	resetEvent := Event{ChannelID: "ch_reset", Model: "model-a", Type: "down", TS: 100, Detail: "timeout"}
	resetState := DetectorState{ChannelID: "ch_reset", Model: "model-a", ConsecutiveFailures: 2, Down: true, UpdatedAt: 100}
	if err := st.RecordProbe(resetRow, []Event{resetEvent}, resetState); err != nil {
		t.Fatal(err)
	}
	keepState := DetectorState{ChannelID: "ch_keep", Model: "model-a", ConsecutiveFailures: 1, UpdatedAt: 100}
	if err := st.RecordProbe(ProbeRow{ChannelID: "ch_keep", Model: "model-a", Status: 1, TS: 100}, nil, keepState); err != nil {
		t.Fatal(err)
	}

	deleted, err := st.ResetChannel("ch_reset")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted rows = %d, want 3", deleted)
	}
	latest, err := st.Latest("ch_reset", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Fatalf("latest reset probe = %#v, want nil", latest)
	}
	probes, err := st.RecentProbes("ch_reset", "model-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) != 0 {
		t.Fatalf("reset probes = %#v, want empty", probes)
	}
	events, err := st.RecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("reset events = %#v, want empty", events)
	}
	states, err := st.LoadDetectorStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0] != keepState {
		t.Fatalf("remaining states = %#v, want %#v", states, []DetectorState{keepState})
	}
}
