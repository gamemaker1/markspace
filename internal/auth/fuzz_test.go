package auth

import (
	"strings"
	"testing"
)

// seedSecret is the only password the seed hash in FuzzVerifyPassword
// matches. The fuzzer is not expected to rediscover it.
const seedSecret = "fuzz seed secret \x00\x01\x02 not typeable"

// FuzzVerifyPassword checks that VerifyPassword never panics or hangs on an
// arbitrary encoded hash, and never reports a match for it.
func FuzzVerifyPassword(f *testing.F) {
	valid, err := HashPassword(seedSecret)
	if err != nil {
		f.Fatalf("HashPassword: %v", err)
	}
	for _, seed := range []string{
		valid,
		"",
		"$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHQ$a2V5a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=4294967295,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=64,t=4294967295,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=64,t=1,p=255$c2FsdA$a2V5",
		"$argon2i$v=19$m=64,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=64,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=64,t=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=64,t=1,p=1,x=2$c2FsdA$a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$$",
		"$argon2id$v=19$m=64,t=1,p=1$!!!$a2V5",
		"$$$$$",
	} {
		f.Add(seed, "password")
	}
	f.Fuzz(func(t *testing.T, encoded, plain string) {
		ok, err := VerifyPassword(encoded, plain)
		if ok && err != nil {
			t.Fatalf("VerifyPassword(%q) returned true with error %v", encoded, err)
		}
		if ok && !(encoded == valid && plain == seedSecret) {
			t.Fatalf("VerifyPassword(%q, %q) = true", encoded, plain)
		}
		_ = NeedsRehash(encoded)
	})
}

// FuzzHashVerifyRoundTrip checks that every password of 8 to 200 bytes
// verifies against its own hash and not against a changed password.
func FuzzHashVerifyRoundTrip(f *testing.F) {
	f.Add("correct horse battery")
	f.Add("\x00\x00\x00\x00\x00\x00\x00\x00")
	f.Add(strings.Repeat("é", 100))
	f.Fuzz(func(t *testing.T, plain string) {
		if len(plain) < MinPasswordBytes || len(plain) > MaxPasswordBytes {
			if ValidatePassword(plain) == nil {
				t.Fatalf("ValidatePassword accepted %d bytes", len(plain))
			}
			return
		}
		if err := ValidatePassword(plain); err != nil {
			t.Fatalf("ValidatePassword(%d bytes): %v", len(plain), err)
		}
		encoded, err := HashPassword(plain)
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		ok, err := VerifyPassword(encoded, plain)
		if err != nil || !ok {
			t.Fatalf("VerifyPassword of own hash = %v, %v", ok, err)
		}
		if NeedsRehash(encoded) {
			t.Fatalf("fresh hash %q needs a rehash", encoded)
		}
		if ok, _ := VerifyPassword(encoded, plain+"x"); ok {
			t.Fatal("VerifyPassword matched a longer password")
		}
	})
}

// FuzzEmail checks that NormalizeEmail is idempotent and that ValidateEmail
// never panics.
func FuzzEmail(f *testing.F) {
	for _, seed := range []string{
		"", " Ada@Example.COM ", "a@b.c", "@", "a@", "@b.c", "a@b", "a@.b", "a@b.",
		"a@@b.c", "a b@c.d", " x@y.z ", "İ@x.y", "K@x.y", "\xff@x.y",
		"ǅ@x.y", "a@b..c", strings.Repeat("a", 250) + "@b.c",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := NormalizeEmail(s)
		if again := NormalizeEmail(n); again != n {
			t.Fatalf("NormalizeEmail is not idempotent: %q -> %q -> %q", s, n, again)
		}
		_ = ValidateEmail(s)
		_ = ValidateEmail(n)
	})
}
