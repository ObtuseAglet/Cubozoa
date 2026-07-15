package livetv

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(s))
	zw.Close()
	return buf.Bytes()
}

func TestMaybeGunzip(t *testing.T) {
	plain := []byte("#EXTM3U\nnot gzipped")
	if got := maybeGunzip(plain); string(got) != string(plain) {
		t.Fatal("plain data should pass through unchanged")
	}
	gz := gzipBytes(t, "hello gzipped world")
	if got := string(maybeGunzip(gz)); got != "hello gzipped world" {
		t.Fatalf("gunzip failed: %q", got)
	}
}

func TestM3UGuideURL(t *testing.T) {
	m3u := `#EXTM3U url-tvg="http://epg.example/guide.xml.gz" x-tvg-url="http://other"
#EXTINF:-1,Chan
http://s/1.ts`
	if got := M3UGuideURL([]byte(m3u)); got != "http://epg.example/guide.xml.gz" {
		t.Fatalf("guide URL = %q", got)
	}
	if got := M3UGuideURL([]byte("#EXTM3U\n#EXTINF:-1,Chan\nhttp://s/1.ts")); got != "" {
		t.Fatalf("no guide URL expected, got %q", got)
	}
}

// TestGuideAutoDiscoveryAndGzip serves a gzipped XMLTV whose URL is advertised
// only in the playlist header, and checks the service loads it end to end.
func TestGuideAutoDiscoveryAndGzip(t *testing.T) {
	const guide = `<tv><programme start="20240115180000 +0000" stop="20240115190000 +0000" channel="bbc1.uk"><title>News</title></programme></tv>`
	epg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(gzipBytes(t, guide))
	}))
	defer epg.Close()

	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U url-tvg=\"" + epg.URL + "/g.xml.gz\"\n#EXTINF:-1 tvg-id=\"bbc1.uk\",BBC One\nhttp://s/bbc.ts\n"))
	}))
	defer playlist.Close()

	svc, ok := NewService(playlist.URL+"/list.m3u", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Fatal("service should be enabled")
	}
	if err := svc.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	if svc.Count() != 1 {
		t.Fatalf("expected 1 channel, got %d", svc.Count())
	}
	chID := svc.Channels()[0].ID
	progs := svc.Programs([]string{chID}, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC))
	if len(progs) != 1 || progs[0].Title != "News" {
		t.Fatalf("auto-discovered gzipped guide not loaded: %+v", progs)
	}
}

func TestMultipleGuideSourcesMerged(t *testing.T) {
	guideFor := func(ch, title string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<tv><programme start="20240115180000 +0000" stop="20240115190000 +0000" channel="` + ch + `"><title>` + title + `</title></programme></tv>`))
		}
	}
	epg1 := httptest.NewServer(guideFor("a.tv", "Show A"))
	defer epg1.Close()
	epg2 := httptest.NewServer(guideFor("b.tv", "Show B"))
	defer epg2.Close()

	pl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"a.tv\",A\nhttp://s/a.ts\n#EXTINF:-1 tvg-id=\"b.tv\",B\nhttp://s/b.ts\n"))
	}))
	defer pl.Close()

	svc, _ := NewService(pl.URL+"/l.m3u", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetGuide(epg1.URL + "/1.xml, " + epg2.URL + "/2.xml") // two comma-separated sources
	if err := svc.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	all := svc.Programs(nil, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC))
	if len(all) != 2 {
		t.Fatalf("expected 2 merged programs, got %d: %+v", len(all), all)
	}
}
