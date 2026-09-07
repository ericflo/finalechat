package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("expected match, ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil || ok {
		t.Fatalf("expected mismatch, ok=%v err=%v", ok, err)
	}
	if _, err := VerifyPassword("garbage", "x"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}

func TestAPIToken(t *testing.T) {
	secret, prefix, err := NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeAPIToken(secret) {
		t.Fatalf("token %q does not look like an API token", secret)
	}
	if len(prefix) != 11 || secret[:11] != prefix {
		t.Fatalf("unexpected prefix %q for %q", prefix, secret)
	}
	other, _, _ := NewAPIToken()
	if other == secret {
		t.Fatal("tokens must be unique")
	}
	if string(HashToken(secret)) == string(HashToken(other)) {
		t.Fatal("hashes must differ")
	}
}
