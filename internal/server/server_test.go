package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// newTestServer builds a fully wired server backed by a temp-dir store with a
// single seeded admin account.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	const password = "test-password-123"
	if _, _, err := authSvc.SeedAdmin("admin", password); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{ServerName: "Test", BindAddress: ":8096"}
	srv := New(cfg, st, authSvc, log)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, password
}

func TestPublicEndpointsAnonymous(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/System/Info/Public")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var info jellyfin.PublicSystemInfo
	json.NewDecoder(resp.Body).Decode(&info)
	if info.ProductName != jellyfin.ProductName {
		t.Fatalf("ProductName = %q", info.ProductName)
	}
	if !info.StartupWizardCompleted {
		t.Fatal("StartupWizardCompleted should be true so clients skip the wizard")
	}
}

func TestProtectedEndpointRequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/Users/Me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// login performs AuthenticateByName and returns the access token.
func login(t *testing.T, baseURL, user, pw string) (string, int) {
	t.Helper()
	body := strings.NewReader(`{"Username":"` + user + `","Pw":"` + pw + `"}`)
	r, _ := http.NewRequest(http.MethodPost, baseURL+"/Users/AuthenticateByName", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", `MediaBrowser Client="Test", Device="Go", DeviceId="dev", Version="1"`)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode
	}
	var ar jellyfin.AuthenticationResult
	json.NewDecoder(resp.Body).Decode(&ar)
	return ar.AccessToken, resp.StatusCode
}

func TestLoginFlow(t *testing.T) {
	ts, pw := newTestServer(t)

	if _, code := login(t, ts.URL, "admin", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want 401", code)
	}
	if _, code := login(t, ts.URL, "ghost", pw); code != http.StatusUnauthorized {
		t.Fatalf("unknown user: status = %d, want 401", code)
	}

	token, code := login(t, ts.URL, "admin", pw)
	if code != http.StatusOK || token == "" {
		t.Fatalf("valid login: status = %d token=%q", code, token)
	}

	// Token grants access to a protected endpoint.
	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Users/Me", nil)
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /Users/Me: status = %d", resp.StatusCode)
	}
	var u jellyfin.UserDto
	json.NewDecoder(resp.Body).Decode(&u)
	if u.Name != "admin" || !u.Policy.IsAdministrator {
		t.Fatalf("unexpected user dto: %+v", u)
	}
}

func TestLogoutRevokesToken(t *testing.T) {
	ts, pw := newTestServer(t)
	token, _ := login(t, ts.URL, "admin", pw)

	logout, _ := http.NewRequest(http.MethodPost, ts.URL+"/Sessions/Logout", nil)
	logout.Header.Set("X-Emby-Token", token)
	if resp, err := http.DefaultClient.Do(logout); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Users/Me", nil)
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: status = %d, want 401", resp.StatusCode)
	}
}

func TestAuthRateLimiting(t *testing.T) {
	ts, _ := newTestServer(t)
	// authBurst attempts are allowed, after which the limiter must kick in.
	var got429 bool
	for i := 0; i < authBurst+3; i++ {
		_, code := login(t, ts.URL, "admin", "wrong")
		if code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected rate limiter to return 429 after sustained failures")
	}
}

func TestEmbyPrefixIsStripped(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/emby/System/Info/Public")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/emby-prefixed route: status = %d, want 200", resp.StatusCode)
	}
}
