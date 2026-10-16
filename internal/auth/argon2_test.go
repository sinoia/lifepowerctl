package auth

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/argon2"
)

var benchmarkKey []byte

func TestArgon2IDParameters(t *testing.T) {
	password := []byte("correct horse battery staple")
	salt := []byte("0123456789abcdef")
	first := deriveTestKey(password, salt)
	second := deriveTestKey(password, salt)
	if len(first) != Argon2KeyBytes {
		t.Fatalf("key length = %d, want %d", len(first), Argon2KeyBytes)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same password, salt, and parameters produced different keys")
	}
	other := deriveTestKey(password, []byte("fedcba9876543210"))
	if bytes.Equal(first, other) {
		t.Fatal("different salts produced the same key")
	}
}

func BenchmarkArgon2IDTarget(b *testing.B) {
	password := []byte("correct horse battery staple")
	salt := []byte("0123456789abcdef")
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		benchmarkKey = deriveTestKey(password, salt)
	}
}

func deriveTestKey(password, salt []byte) []byte {
	return argon2.IDKey(
		password,
		salt,
		Argon2Passes,
		Argon2MemoryKiB,
		Argon2Lanes,
		Argon2KeyBytes,
	)
}
