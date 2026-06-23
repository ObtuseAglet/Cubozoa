package transcode

import "testing"

func names(rs []Rendition) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func TestSelectRenditionsLadder(t *testing.T) {
	// A 1080p source yields the full ladder, top-first, without upscaling.
	rs := SelectRenditions(1920, 1080, 8_000_000)
	if got := names(rs); len(got) != 4 || got[0] != "1080" || got[3] != "360" {
		t.Fatalf("1080p ladder = %v", got)
	}
	// Widths are even and scale with the source aspect ratio (16:9).
	for _, r := range rs {
		if r.Width%2 != 0 {
			t.Errorf("odd width for %s: %d", r.Name, r.Width)
		}
	}
	if rs[1].Name == "720" && rs[1].Width != 1280 {
		t.Errorf("720p width = %d, want 1280", rs[1].Width)
	}
}

func TestSelectRenditionsNoUpscale(t *testing.T) {
	// A 480p source must not offer 720p or 1080p.
	rs := SelectRenditions(854, 480, 0)
	got := names(rs)
	if len(got) != 2 || got[0] != "480" || got[1] != "360" {
		t.Fatalf("480p ladder = %v", got)
	}
}

func TestSelectRenditionsUnknownSource(t *testing.T) {
	rs := SelectRenditions(0, 0, 0)
	if len(rs) != 1 || rs[0].Name != "auto" {
		t.Fatalf("unknown source should yield a single auto rendition, got %v", names(rs))
	}
}

func TestTopRungBitrateCappedToSource(t *testing.T) {
	// A 1080p source encoded at only 1.5 Mbps should not have its top rung
	// inflated to the ladder's 5 Mbps.
	rs := SelectRenditions(1920, 1080, 1_500_000)
	if rs[0].VideoKbps > 1500 {
		t.Fatalf("top rung video kbps = %d, should be capped near source (1500)", rs[0].VideoKbps)
	}
}
