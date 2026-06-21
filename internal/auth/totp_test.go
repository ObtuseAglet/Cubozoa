package auth

import (
	"errors"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/security"
)

func TestTOTPEnrollAndLogin(t *testing.T) {
	svc := newAuthService(t) // seeds user "alice" / "alice-password-1"
	u, _ := svc.store.GetUserByName("alice")

	// Enroll.
	setup, err := svc.BeginTOTPSetup(u.ID, "Cubozoa", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if setup.Secret == "" || setup.ProvisioningURI == "" {
		t.Fatal("incomplete setup data")
	}

	// Login still works without a code until activation completes.
	if _, err := svc.Authenticate("alice", "alice-password-1"); err != nil {
		t.Fatalf("pre-activation login should work: %v", err)
	}

	// Activate with a valid code.
	code, _ := security.CurrentTOTP(setup.Secret)
	recovery, err := svc.ActivateTOTP(u.ID, code)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if len(recovery) != recoveryCodeCount {
		t.Fatalf("got %d recovery codes", len(recovery))
	}

	// Now the bare password is rejected...
	if _, err := svc.Authenticate("alice", "alice-password-1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bare password after 2FA: err = %v, want ErrInvalidCredentials", err)
	}
	// ...but password + current code works.
	code2, _ := security.CurrentTOTP(setup.Secret)
	if _, err := svc.Authenticate("alice", "alice-password-1"+code2); err != nil {
		t.Fatalf("password+code login: %v", err)
	}
	// A wrong code with the right password fails.
	if _, err := svc.Authenticate("alice", "alice-password-1"+"000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password+wrong code: err = %v", err)
	}
}

func TestTOTPDisableRequiresCode(t *testing.T) {
	svc := newAuthService(t)
	u, _ := svc.store.GetUserByName("alice")
	setup, _ := svc.BeginTOTPSetup(u.ID, "Cubozoa", "alice")
	code, _ := security.CurrentTOTP(setup.Secret)
	svc.ActivateTOTP(u.ID, code)

	// Wrong code cannot disable.
	if err := svc.DisableTOTP(u.ID, "000000"); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("disable with wrong code: err = %v", err)
	}
	// Correct code disables; bare password works again.
	good, _ := security.CurrentTOTP(setup.Secret)
	if err := svc.DisableTOTP(u.ID, good); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := svc.Authenticate("alice", "alice-password-1"); err != nil {
		t.Fatalf("login after disable: %v", err)
	}
}

func TestTOTPRecoveryWithCode(t *testing.T) {
	svc := newAuthService(t)
	u, _ := svc.store.GetUserByName("alice")
	setup, _ := svc.BeginTOTPSetup(u.ID, "Cubozoa", "alice")
	code, _ := security.CurrentTOTP(setup.Secret)
	recovery, _ := svc.ActivateTOTP(u.ID, code)

	// Wrong password is rejected.
	if err := svc.RecoverTOTP("alice", "nope", recovery[0]); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("recover wrong password: err = %v", err)
	}
	// Correct password + a recovery code disables 2FA.
	if err := svc.RecoverTOTP("alice", "alice-password-1", recovery[0]); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if _, err := svc.Authenticate("alice", "alice-password-1"); err != nil {
		t.Fatalf("login after recovery: %v", err)
	}
	// Each recovery code is single-use: re-enroll and confirm the consumed code
	// no longer works.
	setup2, _ := svc.BeginTOTPSetup(u.ID, "Cubozoa", "alice")
	c, _ := security.CurrentTOTP(setup2.Secret)
	recovery2, _ := svc.ActivateTOTP(u.ID, c)
	// Use one code, then try it again.
	svc.RecoverTOTP("alice", "alice-password-1", recovery2[0])
	setup3, _ := svc.BeginTOTPSetup(u.ID, "Cubozoa", "alice")
	c3, _ := security.CurrentTOTP(setup3.Secret)
	r3, _ := svc.ActivateTOTP(u.ID, c3)
	_ = r3
	if err := svc.RecoverTOTP("alice", "alice-password-1", recovery2[0]); err == nil {
		t.Fatal("a consumed recovery code must not work again")
	}
}

func TestActivateRejectsBadCode(t *testing.T) {
	svc := newAuthService(t)
	u, _ := svc.store.GetUserByName("alice")
	svc.BeginTOTPSetup(u.ID, "Cubozoa", "alice")
	if _, err := svc.ActivateTOTP(u.ID, "000000"); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("activate with bad code: err = %v", err)
	}
	// Without a pending enrollment, activation has nothing to confirm.
	u2, _ := svc.store.GetUserByName("alice")
	u2.PendingTOTPSecret = ""
	svc.store.UpdateUser(u2)
	if _, err := svc.ActivateTOTP(u.ID, "000000"); !errors.Is(err, ErrNoPendingTOTP) {
		t.Fatalf("activate without pending: err = %v", err)
	}
}
