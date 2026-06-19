package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// --- small request helpers ---

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func authReq(t *testing.T, method, url, token string, v any) int {
	t.Helper()
	r, _ := http.NewRequest(method, url, nil)
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil && resp.StatusCode == http.StatusOK {
		decodeJSON(t, resp, v)
	}
	return resp.StatusCode
}

func postJSON(t *testing.T, url, token string, v any) int {
	return authReq(t, http.MethodPost, url, token, v)
}

// meID returns the authenticated user's own ID, as a client would obtain it
// from /Users/Me before building per-user request paths.
func meID(t *testing.T, baseURL, token string) string {
	t.Helper()
	var me jellyfin.UserDto
	if code := authReq(t, http.MethodGet, baseURL+"/Users/Me", token, &me); code != http.StatusOK {
		t.Fatalf("/Users/Me status = %d", code)
	}
	return me.ID
}

func reportProgress(t *testing.T, baseURL, token, itemID string, ticks int64) {
	t.Helper()
	body := strings.NewReader(`{"ItemId":"` + itemID + `","PositionTicks":` + itoa(ticks) + `}`)
	r, _ := http.NewRequest(http.MethodPost, baseURL+"/Sessions/Playing/Progress", body)
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("progress report status = %d, want 204", resp.StatusCode)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestMarkPlayedAndUnplayedReflectInDto(t *testing.T) {
	ts, token, itemID := newPlaybackServer(t, []byte("data"))
	uid := meID(t, ts.URL, token)

	var ud jellyfin.UserItemDataDto
	if code := postJSON(t, ts.URL+"/Users/"+uid+"/PlayedItems/"+itemID, token, &ud); code != http.StatusOK {
		t.Fatalf("mark played status = %d", code)
	}
	if !ud.Played || ud.PlayCount != 1 {
		t.Fatalf("played dto = %+v", ud)
	}

	var detail jellyfin.BaseItemDto
	authReq(t, http.MethodGet, ts.URL+"/Items/"+itemID, token, &detail)
	if detail.UserData == nil || !detail.UserData.Played {
		t.Fatalf("item detail should show Played: %+v", detail.UserData)
	}

	if code := authReq(t, http.MethodDelete, ts.URL+"/Users/"+uid+"/PlayedItems/"+itemID, token, nil); code != http.StatusOK {
		t.Fatalf("mark unplayed status = %d", code)
	}
	authReq(t, http.MethodGet, ts.URL+"/Items/"+itemID, token, &detail)
	if detail.UserData.Played {
		t.Fatal("item should no longer be Played")
	}
}

func TestFavoriteToggle(t *testing.T) {
	ts, token, itemID := newPlaybackServer(t, []byte("data"))
	uid := meID(t, ts.URL, token)

	var ud jellyfin.UserItemDataDto
	postJSON(t, ts.URL+"/Users/"+uid+"/FavoriteItems/"+itemID, token, &ud)
	if !ud.IsFavorite {
		t.Fatalf("expected favorite, got %+v", ud)
	}
	authReq(t, http.MethodDelete, ts.URL+"/Users/"+uid+"/FavoriteItems/"+itemID, token, nil)

	var detail jellyfin.BaseItemDto
	authReq(t, http.MethodGet, ts.URL+"/Items/"+itemID, token, &detail)
	if detail.UserData.IsFavorite {
		t.Fatal("favorite should be cleared")
	}
}

func TestResumeReflectsReportedProgress(t *testing.T) {
	ts, token, itemID := newPlaybackServer(t, []byte("data"))
	uid := meID(t, ts.URL, token)

	var resume jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Users/"+uid+"/Items/Resume", token, &resume)
	if resume.TotalRecordCount != 0 {
		t.Fatalf("expected empty resume, got %d", resume.TotalRecordCount)
	}

	reportProgress(t, ts.URL, token, itemID, 123456)

	authReq(t, http.MethodGet, ts.URL+"/Users/"+uid+"/Items/Resume", token, &resume)
	if resume.TotalRecordCount != 1 {
		t.Fatalf("expected 1 resumable item, got %d", resume.TotalRecordCount)
	}
	if resume.Items[0].ID != itemID || resume.Items[0].UserData.PlaybackPositionTicks != 123456 {
		t.Fatalf("unexpected resume item: %+v", resume.Items[0])
	}

	postJSON(t, ts.URL+"/Users/"+uid+"/PlayedItems/"+itemID, token, nil)
	authReq(t, http.MethodGet, ts.URL+"/Users/"+uid+"/Items/Resume", token, &resume)
	if resume.TotalRecordCount != 0 {
		t.Fatalf("played item should leave resume row, got %d", resume.TotalRecordCount)
	}
}

// TestNonAdminCannotActOnAnotherUser verifies the per-user authorization
// boundary: a regular user may only touch their own item data.
func TestNonAdminCannotActOnAnotherUser(t *testing.T) {
	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	if _, _, err := authSvc.SeedAdmin("admin", "admin-password-1"); err != nil {
		t.Fatal(err)
	}
	if err := authSvc.CreateUser("bob", "bob-password-1", false); err != nil {
		t.Fatal(err)
	}

	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	os.MkdirAll(movies, 0o755)
	os.WriteFile(filepath.Join(movies, "Film (2020).mp4"), []byte("x"), 0o644)
	mediaSvc := media.NewService(st, log)
	mediaSvc.SyncLibrariesFromMediaDir(mediaDir)
	mediaSvc.ScanAll()

	ts := httptest.NewServer(New(&config.Config{ServerName: "Test"}, st, authSvc, mediaSvc, userdata.New(st), log).Handler())
	t.Cleanup(ts.Close)

	bobToken, code := login(t, ts.URL, "bob", "bob-password-1")
	if code != http.StatusOK {
		t.Fatalf("bob login status = %d", code)
	}

	// Bob targets a different user id -> 403, regardless of item.
	got := postJSON(t, ts.URL+"/Users/somebody-else/PlayedItems/whatever", bobToken, nil)
	if got != http.StatusForbidden {
		t.Fatalf("non-admin acting on another user = %d, want 403", got)
	}
}
