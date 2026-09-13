package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
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

func TestLatencyTrendUsesAllStatusesAndNearestRankPercentiles(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().Unix()
	for _, row := range []ProbeRow{
		{ChannelID: "ch_test", Model: "model-a", Status: 1, LatencyMS: 100, TS: now - 10},
		{ChannelID: "ch_test", Model: "model-a", Status: 2, LatencyMS: 200, TS: now - 10},
		{ChannelID: "ch_test", Model: "model-a", Status: 0, LatencyMS: 500, TS: now - 10},
		{ChannelID: "ch_test", Model: "model-a", Status: 0, LatencyMS: 0, TS: now - 10},
	} {
		if err := st.InsertProbe(row); err != nil {
			t.Fatal(err)
		}
	}
	trend, err := st.LatencyTrend("ch_test", "model-a", "24h", now)
	if err != nil {
		t.Fatal(err)
	}
	var found *LatencyPoint
	for i := range trend.Points {
		if trend.Points[i].Samples == 3 {
			found = &trend.Points[i]
			break
		}
	}
	if found == nil || found.P50LatencyMS == nil || found.P95LatencyMS == nil || found.P99LatencyMS == nil || found.AvgLatencyMS == nil {
		t.Fatalf("trend point missing: %#v", found)
	}
	if *found.P50LatencyMS != 200 || *found.P95LatencyMS != 500 || *found.P99LatencyMS != 500 || *found.AvgLatencyMS != 800.0/3.0 {
		t.Fatalf("trend point = %#v", *found)
	}
	for _, point := range trend.Points {
		if point.Samples == 0 && (point.AvgLatencyMS != nil || point.P50LatencyMS != nil || point.P95LatencyMS != nil || point.P99LatencyMS != nil) {
			t.Fatalf("empty bucket should have null metrics: %#v", point)
		}
	}
}

func TestOpenMigratesLegacyDetectorState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE detector_state (
channel_id TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
consecutive_failures INTEGER NOT NULL DEFAULT 0,
down INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL,
PRIMARY KEY(channel_id, model));
INSERT INTO detector_state(channel_id, model, consecutive_failures, down, updated_at) VALUES('ch_legacy', 'model-a', 4, 1, 123);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	states, err := st.LoadDetectorStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].ConsecutiveAnomalies != 4 || states[0].ConsecutiveSuccesses != 0 || states[0].RecoveryThreshold != 0 || !states[0].Down {
		t.Fatalf("migrated states = %#v", states)
	}
}
