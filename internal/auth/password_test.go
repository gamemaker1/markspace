package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const plain = "correct horse battery staple"
	encoded, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("unexpected encoding %q", encoded)
	}
	if got := strings.Count(encoded, "$"); got != 5 {
		t.Fatalf("want 5 separators, got %d in %q", got, encoded)
	}
	ok, err := VerifyPassword(encoded, plain)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("correct password did not verify")
	}
	if NeedsRehash(encoded) {
		t.Fatal("a fresh hash should not need a rehash")
	}
}

func TestHashPasswordSaltIsRandom(t *testing.T) {
	a, err := HashPassword("same input")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := HashPassword("same input")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are identical, salt is not random")
	}
}

func TestVerifyPasswordWrong(t *testing.T) {
	encoded, err := HashPassword("right")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	for _, plain := range []string{"wrong", "", "Right", "right "} {
		ok, err := VerifyPassword(encoded, plain)
		if err != nil {
			t.Fatalf("VerifyPassword(%q): unexpected error %v", plain, err)
		}
		if ok {
			t.Fatalf("VerifyPassword(%q) = true, want false", plain)
		}
	}
}

func TestVerifyPasswordMalformed(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"plaintext", "hunter2"},
		{"too few fields", "$argon2id$v=19$m=65536,t=1,p=4$c2FsdA"},
		{"wrong algorithm", "$argon2i$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"bad version", "$argon2id$v=16$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"unparsable version", "$argon2id$vee$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"missing cost field", "$argon2id$v=19$m=65536,t=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"unknown cost field", "$argon2id$v=19$m=65536,t=1,p=4,q=9$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"non numeric cost", "$argon2id$v=19$m=lots,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"zero memory", "$argon2id$v=19$m=0,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"zero parallelism", "$argon2id$v=19$m=65536,t=1,p=0$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"bad base64 salt", "$argon2id$v=19$m=65536,t=1,p=4$!!!!$aGFzaA"},
		{"bad base64 key", "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$!!!!"},
		{"empty key", "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$"},
		// Found by FuzzVerifyPassword: without a cap these allocate 4 TiB or
		// run 2^32 passes before returning.
		{"memory above the cap", "$argon2id$v=19$m=4294967295,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"time above the cap", "$argon2id$v=19$m=65536,t=4294967295,p=4$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA"},
		{"key above the cap", "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$" + strings.Repeat("A", 400)},
		{"salt above the cap", "$argon2id$v=19$m=65536,t=1,p=4$" + strings.Repeat("A", 400) + "$aGFzaA"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := VerifyPassword(tc.encoded, "whatever")
			if err == nil {
				t.Fatal("want an error for a malformed hash, got nil")
			}
			if ok {
				t.Fatal("want false alongside the error")
			}
			if !errors.Is(err, ErrMalformedHash) {
				t.Errorf("error does not wrap ErrMalformedHash: %v", err)
			}
			if !errors.Is(err, core.ErrInvalid) {
				t.Errorf("error does not wrap core.ErrInvalid: %v", err)
			}
			if !NeedsRehash(tc.encoded) {
				t.Error("a malformed hash should report NeedsRehash")
			}
		})
	}
}

func TestNeedsRehashOnParameterChange(t *testing.T) {
	salt := make([]byte, ArgonSaltLength)
	key := make([]byte, ArgonKeyLength)
	current := encodeHash(ArgonMemory, ArgonTime, ArgonThreads, salt, key)
	if NeedsRehash(current) {
		t.Fatal("current parameters should not need a rehash")
	}

	tests := []struct {
		name    string
		encoded string
	}{
		{"less memory", encodeHash(32*1024, ArgonTime, ArgonThreads, salt, key)},
		{"more time", encodeHash(ArgonMemory, ArgonTime+1, ArgonThreads, salt, key)},
		{"fewer threads", encodeHash(ArgonMemory, ArgonTime, 1, salt, key)},
		{"short salt", encodeHash(ArgonMemory, ArgonTime, ArgonThreads, salt[:8], key)},
		{"short key", encodeHash(ArgonMemory, ArgonTime, ArgonThreads, salt, key[:16])},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !NeedsRehash(tc.encoded) {
				t.Errorf("NeedsRehash(%q) = false, want true", tc.encoded)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		plain   string
		wantErr bool
	}{
		{"empty", "", true},
		{"seven bytes", "1234567", true},
		{"eight bytes", "12345678", false},
		{"two hundred bytes", strings.Repeat("a", 200), false},
		{"two hundred and one bytes", strings.Repeat("a", 201), true},
		{"multibyte under the byte limit", strings.Repeat("é", 8), false},
		{"multibyte over the byte limit", strings.Repeat("é", 101), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.plain)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error, got nil")
				}
				if !errors.Is(err, core.ErrInvalid) {
					t.Errorf("error does not wrap core.ErrInvalid: %v", err)
				}
				var ve *core.ValidationError
				if !errors.As(err, &ve) || ve.Field != "password" {
					t.Errorf("want a ValidationError on the password field, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
