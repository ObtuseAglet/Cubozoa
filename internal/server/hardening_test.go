package server

import (
	"bytes"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/audit"
	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// syncBuffer is a goroutine-safe buffer for capturing audit output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newHardenedServer builds a server with a configurable lockout threshold and a
// captured audit log.
func newHardenedServer(t *testing.T, lockoutThreshold int) (*httptest.Server, *syncBuffer) {
	t.Helper()
	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SetLockoutPolicy(lockoutThreshold, time.Minute)
	authSvc.SeedAdmin("admin", "hardening-pass-1")

	buf := &syncBuffer{}
	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)
	srv.SetAudit(audit.New(buf))

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, buf
}

func TestAuditLogsLoginEvents(t *testing.T) {
	ts, buf := newHardenedServer(t, 5)

	login(t, ts.URL, "admin", "wrong")            // failure
	login(t, ts.URL, "admin", "hardening-pass-1") // success

	out := buf.String()
	if !strings.Contains(out, audit.LoginFailure) {
		t.Errorf("audit missing login failure event:\n%s", out)
	}
	if !strings.Contains(out, audit.LoginSuccess) {
		t.Errorf("audit missing login success event:\n%s", out)
	}
	if !strings.Contains(out, `"category":"audit"`) {
		t.Errorf("audit events missing category marker:\n%s", out)
	}
	// A failed login must never record the password.
	if strings.Contains(out, "wrong") {
		t.Errorf("audit log leaked a credential:\n%s", out)
	}
}

func TestLockoutReturns401AndAudits(t *testing.T) {
	ts, buf := newHardenedServer(t, 2)

	// Two failures cross the threshold; the next attempt is locked.
	for i := 0; i < 2; i++ {
		if _, code := login(t, ts.URL, "admin", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, want 401", i, code)
		}
	}
	// Even with the correct password the account is now locked — still 401, so
	// the lock is not distinguishable to the client.
	if _, code := login(t, ts.URL, "admin", "hardening-pass-1"); code != http.StatusUnauthorized {
		t.Fatalf("locked correct-password code = %d, want 401", code)
	}
	if !strings.Contains(buf.String(), audit.AccountLocked) {
		t.Errorf("audit missing account-locked event:\n%s", buf.String())
	}
}

func TestHSTSHeaderOnlyOverTLS(t *testing.T) {
	// Plain HTTP: no HSTS.
	plain, _ := newHardenedServer(t, 5)
	resp, err := http.Get(plain.URL + "/System/Info/Public")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent over plaintext HTTP")
	}

	// TLS: HSTS present.
	st, _ := store.OpenJSON(t.TempDir())
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "x-pass-12345")
	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)
	tlsSrv := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(tlsSrv.Close)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	tresp, err := client.Get(tlsSrv.URL + "/System/Info/Public")
	if err != nil {
		t.Fatal(err)
	}
	tresp.Body.Close()
	if hsts := tresp.Header.Get("Strict-Transport-Security"); !strings.Contains(hsts, "max-age=") {
		t.Errorf("HSTS missing over TLS: %q", hsts)
	}
}
