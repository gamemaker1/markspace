package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already normal", "user@example.com", "user@example.com"},
		{"uppercase", "User@Example.COM", "user@example.com"},
		{"surrounding space", "  user@example.com\t", "user@example.com"},
		{"newline", "user@example.com\n", "user@example.com"},
		{"empty", "", ""},
		{"only space", "   ", ""},
		{"unicode local part", "ÜSER@example.com", "üser@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeEmail(tc.in); got != tc.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	long := strings.Repeat("a", 244) + "@example.com" // 256 bytes
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "user@example.com", false},
		{"subdomain", "user@mail.example.co.uk", false},
		{"plus tag", "user+tag@example.com", false},
		{"dotted local part", "first.last@example.com", false},
		{"empty", "", true},
		{"no at", "userexample.com", true},
		{"two at", "user@host@example.com", true},
		{"empty local part", "@example.com", true},
		{"empty domain", "user@", true},
		{"domain without a dot", "user@localhost", true},
		{"domain starting with a dot", "user@.example.com", true},
		{"domain ending with a dot", "user@example.com.", true},
		{"empty domain label", "user@example..com", true},
		{"inner space", "user name@example.com", true},
		{"leading space", " user@example.com", true},
		{"tab", "user@exa\tmple.com", true},
		{"too long", long, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEmail(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateEmail(%q) = nil, want an error", tc.in)
				}
				if !errors.Is(err, core.ErrInvalid) {
					t.Errorf("error does not wrap core.ErrInvalid: %v", err)
				}
				var ve *core.ValidationError
				if !errors.As(err, &ve) || ve.Field != "email" {
					t.Errorf("want a ValidationError on the email field, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateEmail(%q) = %v, want nil", tc.in, err)
			}
		})
	}
}

func TestValidateEmailAcceptsNormalizedInput(t *testing.T) {
	if err := ValidateEmail(NormalizeEmail("  User@Example.COM ")); err != nil {
		t.Fatalf("normalized address rejected: %v", err)
	}
}
