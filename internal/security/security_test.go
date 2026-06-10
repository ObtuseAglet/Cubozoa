package security

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("password stored in plaintext")
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword on correct password: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword returned error on mismatch: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("identical passwords produced identical hashes; salt missing")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := []string{"", "plaintext", "$argon2id$bad", "$bcrypt$v=19$..."}
	for _, c := range cases {
		if _, err := VerifyPassword("x", c); err != ErrInvalidHash {
			t.Errorf("VerifyPassword(%q): expected ErrInvalidHash, got %v", c, err)
		}
	}
}

func TestTokenAndIDAreRandomAndSized(t *testing.T) {
	t1, _ := NewToken()
	t2, _ := NewToken()
	if t1 == t2 {
		t.Fatal("NewToken produced duplicate tokens")
	}
	if len(t1) != 64 { // 32 bytes hex
		t.Fatalf("token length = %d, want 64", len(t1))
	}
	id, _ := NewID()
	if len(id) != 32 { // 16 bytes hex
		t.Fatalf("id length = %d, want 32", len(id))
	}
}

func TestHashTokenIsStableAndOneWay(t *testing.T) {
	h1 := HashToken("secret-token")
	h2 := HashToken("secret-token")
	if h1 != h2 {
		t.Fatal("HashToken not deterministic")
	}
	if h1 == "secret-token" {
		t.Fatal("HashToken returned the plaintext")
	}
}
