package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

func newMusicServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mediaDir := t.TempDir()
	files := []string{
		"Music/Daft Punk/Discovery/01 - One More Time.mp3",
		"Music/Daft Punk/Discovery/02 - Aerodynamic.mp3",
		"Music/Daft Punk/Homework/01 - Da Funk.mp3",
	}
	for _, f := range files {
		full := filepath.Join(mediaDir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "music-pass-1")
	mediaSvc := media.NewService(st, log)
	mediaSvc.SyncLibrariesFromMediaDir(mediaDir)
	mediaSvc.ScanAll()

	ts := httptest.NewServer(New(&config.Config{ServerName: "Test"}, st, authSvc, mediaSvc, userdata.New(st), log).Handler())
	t.Cleanup(ts.Close)
	token, _ := login(t, ts.URL, "admin", "music-pass-1")
	return ts, token
}

func TestMusicHierarchy(t *testing.T) {
	ts, token := newMusicServer(t)
	uid := meID(t, ts.URL, token)

	// Library is a music collection.
	var views jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Users/"+uid+"/Views", token, &views)
	if views.Items[0].CollectionType != "music" {
		t.Fatalf("collection type = %q, want music", views.Items[0].CollectionType)
	}
	libID := views.Items[0].ID

	// Library -> one artist.
	var artists jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+libID, token, &artists)
	if artists.TotalRecordCount != 1 {
		t.Fatalf("expected 1 artist, got %d", artists.TotalRecordCount)
	}
	artist := artists.Items[0]
	if artist.Type != "MusicArtist" || !artist.IsFolder || artist.Name != "Daft Punk" || artist.ChildCount != 2 {
		t.Fatalf("unexpected artist: %+v", artist)
	}

	// Artist -> two albums (via generic browse and via /Artists).
	var albums jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+artist.ID, token, &albums)
	if albums.TotalRecordCount != 2 {
		t.Fatalf("expected 2 albums, got %d", albums.TotalRecordCount)
	}
	for _, al := range albums.Items {
		if al.Type != "MusicAlbum" || al.AlbumArtist != "Daft Punk" {
			t.Fatalf("unexpected album: %+v", al)
		}
	}

	// Album -> tracks, ordered by track number.
	var discovery *jellyfin.BaseItemDto
	for i := range albums.Items {
		if albums.Items[i].Name == "Discovery" {
			discovery = &albums.Items[i]
		}
	}
	if discovery == nil {
		t.Fatal("Discovery album not found")
	}
	var tracks jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+discovery.ID, token, &tracks)
	if tracks.TotalRecordCount != 2 {
		t.Fatalf("expected 2 tracks, got %d", tracks.TotalRecordCount)
	}
	first := tracks.Items[0]
	if first.Type != "Audio" || first.IndexNumber != 1 || first.Name != "One More Time" {
		t.Fatalf("unexpected first track: %+v", first)
	}
	if first.Album != "Discovery" || first.AlbumArtist != "Daft Punk" || len(first.Artists) != 1 {
		t.Fatalf("track not linked to album/artist: %+v", first)
	}

	// /Artists endpoint returns the artist.
	var viaArtists jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Artists", token, &viaArtists)
	if viaArtists.TotalRecordCount != 1 || viaArtists.Items[0].Name != "Daft Punk" {
		t.Fatalf("/Artists unexpected: %+v", viaArtists)
	}
}
