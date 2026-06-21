package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/security"
)

// post sends a JSON body with an optional token and returns status + decodes the
// response into v (when 2xx and v != nil).
func post(t *testing.T, url, token, body string, v any) int {
	t.Helper()
	r, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("X-Emby-Token", token)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil && resp.StatusCode == http.StatusOK {
		json.NewDecoder(resp.Body).Decode(v)
	}
	return resp.StatusCode
}

func TestTwoFactorEnrollLoginAndRecover(t *testing.T) {
	ts, password := newTestServer(t) // user "admin"
	token, _ := login(t, ts.URL, "admin", password)

	// Status starts disabled.
	var status struct{ Enabled bool }
	authReq(t, http.MethodGet, ts.URL+"/Cubozoa/2FA/Status", token, &status)
	if status.Enabled {
		t.Fatal("2FA should start disabled")
	}

	// Begin setup -> secret + provisioning URI.
	var setup struct {
		Secret          string
		ProvisioningUri string
	}
	if code := post(t, ts.URL+"/Cubozoa/2FA/Setup", token, "{}", &setup); code != http.StatusOK {
		t.Fatalf("setup status = %d", code)
	}
	if setup.Secret == "" || !strings.HasPrefix(setup.ProvisioningUri, "otpauth://") {
		t.Fatalf("bad setup payload: %+v", setup)
	}

	// Activate with a current code -> recovery codes.
	otp, _ := security.CurrentTOTP(setup.Secret)
	var act struct{ RecoveryCodes []string }
	if code := post(t, ts.URL+"/Cubozoa/2FA/Activate", token, `{"Code":"`+otp+`"}`, &act); code != http.StatusOK {
		t.Fatalf("activate status = %d", code)
	}
	if len(act.RecoveryCodes) == 0 {
		t.Fatal("expected recovery codes")
	}

	// Status now enabled.
	authReq(t, http.MethodGet, ts.URL+"/Cubozoa/2FA/Status", token, &status)
	if !status.Enabled {
		t.Fatal("2FA should be enabled after activation")
	}

	// Bare-password login now fails; password+code succeeds.
	if _, code := login(t, ts.URL, "admin", password); code != http.StatusUnauthorized {
		t.Fatalf("bare login after 2FA: code = %d, want 401", code)
	}
	otp2, _ := security.CurrentTOTP(setup.Secret)
	if tok, code := login(t, ts.URL, "admin", password+otp2); code != http.StatusOK || tok == "" {
		t.Fatalf("password+code login: code = %d", code)
	}

	// Recover with password + a recovery code (unauthenticated) -> 204.
	body := `{"Username":"admin","Pw":"` + password + `","RecoveryCode":"` + act.RecoveryCodes[0] + `"}`
	if code := post(t, ts.URL+"/Cubozoa/2FA/Recover", "", body, nil); code != http.StatusNoContent {
		t.Fatalf("recover status = %d, want 204", code)
	}
	// After recovery, bare login works again.
	if _, code := login(t, ts.URL, "admin", password); code != http.StatusOK {
		t.Fatalf("login after recovery: code = %d, want 200", code)
	}
}

func TestTwoFactorEndpointsRequireAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/Cubozoa/2FA/Status", "/Cubozoa/2FA/Setup"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "Setup") {
			method = http.MethodPost
		}
		r, _ := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without auth = %d, want 401", path, resp.StatusCode)
		}
	}
}
