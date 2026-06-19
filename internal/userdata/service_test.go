package userdata

import (
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st)
}

func TestGetReturnsZeroValueWhenMissing(t *testing.T) {
	s := newService(t)
	d := s.Get("u1", "i1")
	if d == nil || d.UserID != "u1" || d.ItemID != "i1" || d.Played {
		t.Fatalf("expected zeroed record, got %+v", d)
	}
}

func TestReportPositionPersistsResume(t *testing.T) {
	s := newService(t)
	if err := s.ReportPosition("u1", "i1", 5000); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("u1", "i1").PlaybackPositionTicks; got != 5000 {
		t.Fatalf("position = %d, want 5000", got)
	}
	// A non-positive position clears the resume mark.
	if err := s.ReportPosition("u1", "i1", 0); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("u1", "i1").PlaybackPositionTicks; got != 0 {
		t.Fatalf("position = %d, want 0", got)
	}
}

func TestMarkPlayedClearsResumeAndCounts(t *testing.T) {
	s := newService(t)
	s.ReportPosition("u1", "i1", 9000)
	ud, err := s.MarkPlayed("u1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if !ud.Played || ud.PlayCount != 1 || ud.PlaybackPositionTicks != 0 {
		t.Fatalf("after MarkPlayed: %+v", ud)
	}
	// Unplayed reverts the watched flag.
	ud, _ = s.MarkUnplayed("u1", "i1")
	if ud.Played {
		t.Fatal("MarkUnplayed should clear Played")
	}
}

func TestResumableExcludesPlayedAndOrdersRecentFirst(t *testing.T) {
	s := newService(t)
	s.ReportPosition("u1", "older", 100)
	s.ReportPosition("u1", "newer", 200)
	s.ReportPosition("u1", "done", 300)
	s.MarkPlayed("u1", "done") // played items drop out of resume

	ids := s.Resumable("u1")
	if len(ids) != 2 {
		t.Fatalf("resumable count = %d, want 2 (%v)", len(ids), ids)
	}
	if ids[0] != "newer" {
		t.Fatalf("most recent should be first, got %v", ids)
	}
}

func TestUserDataIsPerUser(t *testing.T) {
	s := newService(t)
	s.ReportPosition("u1", "i1", 100)
	if s.Get("u2", "i1").PlaybackPositionTicks != 0 {
		t.Fatal("user u2 must not see u1's progress")
	}
	if len(s.Map("u2")) != 0 {
		t.Fatal("u2 map should be empty")
	}
}
