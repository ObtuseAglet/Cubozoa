package auth

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

func newAuthService(t *testing.T) *Service {
	t.Helper()
	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.CreateUser("alice", "alice-password-1", false); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestAccountLocksAfterThreshold(t *testing.T) {
	svc := newAuthService(t)
	svc.SetLockoutPolicy(3, 15*time.Minute)

	// Two wrong attempts: still just invalid credentials.
	for i := 0; i < 2; i++ {
		if _, err := svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v, want ErrInvalidCredentials", i, err)
		}
	}
	// Third wrong attempt crosses the threshold and locks the account.
	if _, err := svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("third attempt: err = %v, want ErrAccountLocked", err)
	}
	// Even the correct password is now refused while locked.
	if _, err := svc.Authenticate("alice", "alice-password-1"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("correct password while locked: err = %v, want ErrAccountLocked", err)
	}
}

func TestLockClearsAfterDuration(t *testing.T) {
	svc := newAuthService(t)
	// Lock immediately (threshold 1) with a brief window we can wait out.
	svc.SetLockoutPolicy(1, 40*time.Millisecond)

	if _, err := svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("err = %v, want ErrAccountLocked", err)
	}
	time.Sleep(80 * time.Millisecond)
	// The window has passed; the correct password works again.
	if _, err := svc.Authenticate("alice", "alice-password-1"); err != nil {
		t.Fatalf("after lock window: err = %v, want success", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	svc := newAuthService(t)
	svc.SetLockoutPolicy(3, time.Minute)

	svc.Authenticate("alice", "wrong")
	svc.Authenticate("alice", "wrong")
	// A success resets the counter...
	if _, err := svc.Authenticate("alice", "alice-password-1"); err != nil {
		t.Fatal(err)
	}
	// ...so two more failures do not lock (would need three consecutive).
	svc.Authenticate("alice", "wrong")
	if _, err := svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials (counter should have reset)", err)
	}
}

func TestLockoutDisabledWithZeroThreshold(t *testing.T) {
	svc := newAuthService(t)
	svc.SetLockoutPolicy(0, time.Minute)
	for i := 0; i < 10; i++ {
		if _, err := svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v, want ErrInvalidCredentials (no lockout)", i, err)
		}
	}
}
