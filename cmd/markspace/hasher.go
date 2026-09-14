package main

import "github.com/gamemaker1/markspace/internal/auth"

// hasher adapts the auth package to the app.Hasher interface.
type hasher struct{}

// Hash returns an argon2id encoded hash of a password.
func (hasher) Hash(plain string) (string, error) { return auth.HashPassword(plain) }

// Verify reports whether plain matches the encoded hash.
func (hasher) Verify(encoded, plain string) (bool, error) { return auth.VerifyPassword(encoded, plain) }

// HashToken returns the hex sha256 of a token, which is what the database stores.
func (hasher) HashToken(plain string) string { return auth.HashToken(plain) }

// NewSessionToken returns a session token and its stored hash.
func (hasher) NewSessionToken() (string, string) { return auth.NewSessionToken() }

// NewAPIToken returns an API token, its stored hash and its display prefix.
func (hasher) NewAPIToken() (string, string, string) { return auth.NewAPIToken() }

// ValidatePassword rejects a password that is too short or too long.
func (hasher) ValidatePassword(plain string) error { return auth.ValidatePassword(plain) }

// NormalizeEmail trims and lowercases an address.
func (hasher) NormalizeEmail(s string) string { return auth.NormalizeEmail(s) }

// ValidateEmail rejects a structurally invalid address.
func (hasher) ValidateEmail(s string) error { return auth.ValidateEmail(s) }
