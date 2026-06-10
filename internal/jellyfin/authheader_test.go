package jellyfin

import (
	"net/http"
	"testing"
)

func req(t *testing.T, mutate func(*http.Request)) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "http://localhost/Users/Me", nil)
	if err != nil {
		t.Fatal(err)
	}
	mutate(r)
	return r
}

func TestParseMediaBrowserHeader(t *testing.T) {
	r := req(t, func(r *http.Request) {
		r.Header.Set("Authorization", `MediaBrowser Client="Jellyfin Web", Device="Firefox", DeviceId="abc123", Version="10.10.0", Token="tok"`)
	})
	ca := ParseClientAuth(r)
	if ca.Client != "Jellyfin Web" || ca.Device != "Firefox" || ca.DeviceID != "abc123" || ca.Version != "10.10.0" || ca.Token != "tok" {
		t.Fatalf("unexpected parse: %+v", ca)
	}
}

func TestParseLegacyEmbyHeader(t *testing.T) {
	r := req(t, func(r *http.Request) {
		r.Header.Set("X-Emby-Authorization", `Emby Client="Kodi", Device="HTPC", DeviceId="d", Version="20"`)
	})
	ca := ParseClientAuth(r)
	if ca.Client != "Kodi" || ca.Device != "HTPC" {
		t.Fatalf("legacy header not parsed: %+v", ca)
	}
}

func TestDeviceNameWithCommaIsPreserved(t *testing.T) {
	r := req(t, func(r *http.Request) {
		r.Header.Set("Authorization", `MediaBrowser Client="App", Device="Living Room, TV", DeviceId="x", Version="1"`)
	})
	ca := ParseClientAuth(r)
	if ca.Device != "Living Room, TV" {
		t.Fatalf("comma in quoted value not preserved: %q", ca.Device)
	}
}

func TestTokenFallbacks(t *testing.T) {
	t.Run("X-Emby-Token", func(t *testing.T) {
		r := req(t, func(r *http.Request) { r.Header.Set("X-Emby-Token", "hdr") })
		if ParseClientAuth(r).Token != "hdr" {
			t.Fatal("X-Emby-Token not honored")
		}
	})
	t.Run("api_key query", func(t *testing.T) {
		r := req(t, func(r *http.Request) { r.URL.RawQuery = "api_key=qp" })
		if ParseClientAuth(r).Token != "qp" {
			t.Fatal("api_key query param not honored")
		}
	})
	t.Run("structured token wins over fallback", func(t *testing.T) {
		r := req(t, func(r *http.Request) {
			r.Header.Set("Authorization", `MediaBrowser Token="primary"`)
			r.Header.Set("X-Emby-Token", "secondary")
		})
		if ParseClientAuth(r).Token != "primary" {
			t.Fatal("structured token should take precedence")
		}
	})
}
