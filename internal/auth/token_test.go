package auth

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestHashTokenDeterministic(t *testing.T) {
	const in = "msk_abcdefghijklmnop"
	first := HashToken(in)
	second := HashToken(in)
	if first != second {
		t.Fatalf("HashToken is not deterministic: %q then %q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("want a 64-character hex digest, got %d characters", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("digest is not hex: %v", err)
	}
	if first == HashToken(in+"x") {
		t.Fatal("different inputs produced the same digest")
	}
	// Known answer for the empty string, so an accidental change of hash
	// function or encoding fails here.
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := HashToken(""); got != emptySHA256 {
		t.Fatalf("HashToken(\"\") = %q, want %q", got, emptySHA256)
	}
}

func TestNewSessionToken(t *testing.T) {
	plain, hash := NewSessionToken()
	if plain == "" {
		t.Fatal("empty session token")
	}
	if hash != HashToken(plain) {
		t.Fatal("returned hash does not match HashToken of the plaintext")
	}
	if plain == hash {
		t.Fatal("plaintext and hash are the same value")
	}
	other, _ := NewSessionToken()
	if other == plain {
		t.Fatal("two session tokens are identical")
	}
	// 32 bytes in base32 without padding is 52 characters.
	if len(plain) != 52 {
		t.Fatalf("want 52 characters of base32 for 32 bytes, got %d", len(plain))
	}
}

func TestNewAPIToken(t *testing.T) {
	plain, hash, prefix := NewAPIToken()
	if !strings.HasPrefix(plain, APITokenPrefix) {
		t.Fatalf("token %q does not start with %q", plain, APITokenPrefix)
	}
	if hash != HashToken(plain) {
		t.Fatal("returned hash does not match HashToken of the plaintext")
	}
	if len(prefix) != APITokenPrefixLength {
		t.Fatalf("want a %d-character prefix, got %d", APITokenPrefixLength, len(prefix))
	}
	if prefix != plain[:APITokenPrefixLength] {
		t.Fatalf("prefix %q is not the head of %q", prefix, plain)
	}
	if !strings.HasPrefix(prefix, APITokenPrefix) {
		t.Fatalf("prefix %q does not keep the marker", prefix)
	}
	// 24 bytes in base32 without padding is 39 characters, plus the marker.
	if want := len(APITokenPrefix) + 39; len(plain) != want {
		t.Fatalf("want a %d-character token, got %d", want, len(plain))
	}
	otherPlain, otherHash, _ := NewAPIToken()
	if otherPlain == plain || otherHash == hash {
		t.Fatal("two API tokens are identical")
	}
}
