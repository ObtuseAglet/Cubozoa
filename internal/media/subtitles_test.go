package media

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFindSubtitles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir,
		"Movie (2020).mkv",
		"Movie (2020).srt",           // no language
		"Movie (2020).en.srt",        // english
		"Movie (2020).es.forced.srt", // spanish, forced
		"Movie (2020).fr.vtt",        // french vtt
		"Unrelated.en.srt",           // different base: ignored
		"Movie (2020).nfo",           // not a subtitle: ignored
	)
	d := newDirLister()
	subs := findSubtitles(d, filepath.Join(dir, "Movie (2020).mkv"))
	if len(subs) != 4 {
		t.Fatalf("expected 4 subtitles, got %d: %+v", len(subs), subs)
	}

	byLang := map[string]bool{}
	for _, s := range subs {
		byLang[s.Language] = true
		if s.Language == "es" && !s.Forced {
			t.Error("es subtitle should be forced")
		}
		if s.Language == "fr" && s.Codec != "webvtt" {
			t.Errorf("fr subtitle codec = %q, want webvtt", s.Codec)
		}
	}
	for _, want := range []string{"", "en", "es", "fr"} {
		if !byLang[want] {
			t.Errorf("missing subtitle language %q", want)
		}
	}
}

func TestSRTToVTT(t *testing.T) {
	srt := "1\r\n00:00:01,000 --> 00:00:04,000\r\nHello world\r\n\r\n2\n00:00:05,500 --> 00:00:08,200\nSecond line\n"
	vtt := string(SRTToVTT([]byte(srt)))

	if !strings.HasPrefix(vtt, "WEBVTT\n\n") {
		t.Fatalf("missing WEBVTT header:\n%q", vtt)
	}
	if !strings.Contains(vtt, "00:00:01.000 --> 00:00:04.000") {
		t.Errorf("timestamp commas not converted to periods:\n%s", vtt)
	}
	if strings.Contains(vtt, ",000") {
		t.Errorf("comma decimals remain:\n%s", vtt)
	}
	if !strings.Contains(vtt, "Hello world") || !strings.Contains(vtt, "Second line") {
		t.Errorf("subtitle text lost:\n%s", vtt)
	}
}

func TestParseSubtitleTokens(t *testing.T) {
	lang, forced := parseSubtitleTokens(".en.forced")
	if lang != "en" || !forced {
		t.Fatalf("got (%q, %v)", lang, forced)
	}
	lang, forced = parseSubtitleTokens("")
	if lang != "" || forced {
		t.Fatalf("empty rest: got (%q, %v)", lang, forced)
	}
}
