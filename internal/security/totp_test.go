package security

import (
	"strings"
	"testing"
	"time"
)

// TestTOTPRFCVectors checks generation against the RFC 6238 (SHA-1) test
// vectors, truncated to the 6 digits authenticator apps use.
func TestTOTPRFCVectors(t *testing.T) {
	secret := base32NoPad.EncodeToString([]byte("12345678901234567890"))
	cases := []struct {
		unix int64
		want string // last 6 digits of the RFC's 8-digit value
	}{
		{59, "287082"},         // RFC: 94287082
		{1111111109, "081804"}, // RFC: 07081804
		{1234567890, "005924"}, // RFC: 89005924
	}
	for _, c := range cases {
		got, err := totpCode(secret, time.Unix(c.unix, 0))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("totpCode(@%d) = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTPAcceptsSkewRejectsWrong(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, _ := totpCode(secret, now)

	if !verifyTOTPAt(secret, code, now) {
		t.Fatal("current code should verify")
	}
	// One period of clock skew on the verifier side is tolerated.
	if !verifyTOTPAt(secret, code, now.Add(25*time.Second)) {
		t.Error("code should verify within ±1 window")
	}
	// Far out of window must fail.
	if verifyTOTPAt(secret, code, now.Add(5*time.Minute)) {
		t.Error("stale code should not verify")
	}
	// Wrong code, wrong length.
	if verifyTOTPAt(secret, "000000", now) && code != "000000" {
		t.Error("an arbitrary code should not verify")
	}
	if VerifyTOTP(secret, "12345") {
		t.Error("a non-6-digit code must be rejected")
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := TOTPProvisioningURI("Cubozoa", "alice", "ABCDEF")
	for _, want := range []string{"otpauth://totp/", "Cubozoa:alice", "secret=ABCDEF", "issuer=Cubozoa", "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("provisioning URI missing %q: %s", want, uri)
		}
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 8 || len(hashes) != 8 {
		t.Fatalf("got %d codes / %d hashes", len(codes), len(hashes))
	}
	// Each hash must correspond to its code, and codes are unique.
	seen := map[string]bool{}
	for i, c := range codes {
		if seen[c] {
			t.Fatal("duplicate recovery code")
		}
		seen[c] = true
		if HashToken(c) != hashes[i] {
			t.Errorf("hash mismatch for code %d", i)
		}
		if c == hashes[i] {
			t.Error("recovery code stored in clear")
		}
	}
}
