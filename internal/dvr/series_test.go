package dvr

import (
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

func TestNormalizeTitle(t *testing.T) {
	cases := map[string]string{
		"The Show":        "the show",
		"  The   Show!  ": "the show",
		"NOVA: Season 2":  "nova season 2",
		"":                "",
		"---":             "",
	}
	for in, want := range cases {
		if got := normalizeTitle(in); got != want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleMatches(t *testing.T) {
	if !titleMatches("nova", "nova") {
		t.Error("exact match should succeed")
	}
	if !titleMatches("nova s02e01", "nova") {
		t.Error("prefix-with-space should match")
	}
	if titleMatches("nova special report", "novaspecial") {
		t.Error("unrelated titles must not match")
	}
	if titleMatches("nova", "") || titleMatches("", "nova") {
		t.Error("empty inputs must not match")
	}
	if titleMatches("supernova", "nova") {
		t.Error("substring that is not a leading word must not match")
	}
}

// TestExpandSeries checks that a series timer turns matching upcoming airings
// into scheduled recordings, deduped by program id, and honors channel scoping.
func TestExpandSeries(t *testing.T) {
	rec, _ := newRecorder(t, fakeChannels{})
	defer rec.Close()

	base := time.Now().Add(time.Hour) // future so nothing tries to capture
	rec.SetProgramSource(func(from, to time.Time) []Airing {
		return []Airing{
			{ProgramID: "p1", ChannelID: "ch1", ChannelName: "Chan One", Title: "NOVA", Start: base, Stop: base.Add(time.Hour)},
			{ProgramID: "p2", ChannelID: "ch1", ChannelName: "Chan One", Title: "NOVA: Deep Sea", Start: base.Add(2 * time.Hour), Stop: base.Add(3 * time.Hour)},
			{ProgramID: "p3", ChannelID: "ch2", ChannelName: "Chan Two", Title: "NOVA", Start: base, Stop: base.Add(time.Hour)},
			{ProgramID: "p4", ChannelID: "ch1", ChannelName: "Chan One", Title: "Frontline", Start: base, Stop: base.Add(time.Hour)},
		}
	})

	// Channel-scoped to ch1: matches p1 (exact) and p2 (prefix), not p3 (wrong
	// channel) or p4 (wrong title).
	if _, err := rec.ScheduleSeries("ch1", "Chan One", "NOVA", false); err != nil {
		t.Fatal(err)
	}

	timers := rec.Timers()
	if len(timers) != 2 {
		t.Fatalf("expected 2 scheduled recordings, got %d", len(timers))
	}
	progs := map[string]bool{}
	for _, tm := range timers {
		progs[tm.ProgramID] = true
		if tm.SeriesTimerID == "" {
			t.Errorf("recording %s missing SeriesTimerID", tm.ID)
		}
	}
	if !progs["p1"] || !progs["p2"] {
		t.Fatalf("expected p1 and p2 recorded, got %v", progs)
	}

	// Re-expanding must not duplicate (dedupe by program id).
	rec.expandSeries()
	if got := len(rec.Timers()); got != 2 {
		t.Fatalf("re-expand duplicated recordings: got %d, want 2", got)
	}

	// A series timer should be listed.
	if len(rec.SeriesTimers()) != 1 {
		t.Fatalf("expected 1 series timer, got %d", len(rec.SeriesTimers()))
	}
}

func TestExpandSeriesRecordAnyChannel(t *testing.T) {
	rec, _ := newRecorder(t, fakeChannels{})
	defer rec.Close()

	base := time.Now().Add(time.Hour)
	rec.SetProgramSource(func(from, to time.Time) []Airing {
		return []Airing{
			{ProgramID: "a", ChannelID: "ch1", Title: "NOVA", Start: base, Stop: base.Add(time.Hour)},
			{ProgramID: "b", ChannelID: "ch2", Title: "NOVA", Start: base, Stop: base.Add(time.Hour)},
		}
	})
	if _, err := rec.ScheduleSeries("", "", "NOVA", true); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.Timers()); got != 2 {
		t.Fatalf("record-any-channel should capture both airings, got %d", got)
	}
}

func TestCancelSeries(t *testing.T) {
	rec, _ := newRecorder(t, fakeChannels{})
	defer rec.Close()
	st, err := rec.ScheduleSeries("ch1", "Chan One", "NOVA", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.CancelSeries(st.ID); err != nil {
		t.Fatal(err)
	}
	if len(rec.SeriesTimers()) != 0 {
		t.Fatal("series timer should be gone after cancel")
	}
}

func TestScheduleSeriesRejectsEmptyName(t *testing.T) {
	rec, _ := newRecorder(t, fakeChannels{})
	defer rec.Close()
	if _, err := rec.ScheduleSeries("ch1", "Chan", "", false); err == nil {
		t.Fatal("empty series name must be rejected")
	}
}

// ensure store import is used even if a build tag ever changes the above.
var _ = store.RecScheduled
