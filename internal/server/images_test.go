package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
)

// newServerWithArtwork builds a server whose Movies library contains one movie
// with a matching poster, and returns the server, a token, and the item ID.
func newServerWithArtwork(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ts, token, libID := newTestServerWithLibrary(t, []string{"Dune (2021).mkv", "Dune (2021).jpg"})

	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	if code := authGet(t, ts.URL+"/Items?ParentId="+libID, token, &res); code != http.StatusOK {
		t.Fatalf("browse status = %d", code)
	}
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(res.Items))
	}
	return ts, token, res.Items[0].ID
}

func TestItemDtoAdvertisesImageTag(t *testing.T) {
	ts, token, libID := newTestServerWithLibrary(t, []string{"Dune (2021).mkv", "Dune (2021).jpg"})
	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libID, token, &res)
	if res.Items[0].ImageTags["Primary"] == "" {
		t.Fatalf("expected a Primary image tag, got %+v", res.Items[0].ImageTags)
	}
}

func TestServeItemImage(t *testing.T) {
	ts, token, itemID := newServerWithArtwork(t)

	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Items/"+itemID+"/Images/Primary?api_key="+token, nil)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("image status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type = %q, want image/jpeg", ct)
	}
	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Fatal("expected an ETag for cache validation")
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("expected image bytes")
	}
}

func TestItemImageConditionalGet(t *testing.T) {
	ts, token, itemID := newServerWithArtwork(t)
	url := ts.URL + "/Items/" + itemID + "/Images/Primary?api_key=" + token

	first, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	r, _ := http.NewRequest(http.MethodGet, url, nil)
	r.Header.Set("If-None-Match", etag)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", resp.StatusCode)
	}
}

func TestItemImageMissingReturns404(t *testing.T) {
	// Movie with no artwork file.
	ts, token, libID := newTestServerWithLibrary(t, []string{"NoArt (2000).mkv"})
	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libID, token, &res)
	itemID := res.Items[0].ID

	code := authGet(t, ts.URL+"/Items/"+itemID+"/Images/Primary?api_key="+token, token, nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing image status = %d, want 404", code)
	}
}

func TestItemImageRequiresAuth(t *testing.T) {
	ts, _, itemID := newServerWithArtwork(t)
	resp, err := http.Get(ts.URL + "/Items/" + itemID + "/Images/Primary")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated image status = %d, want 401", resp.StatusCode)
	}
}
