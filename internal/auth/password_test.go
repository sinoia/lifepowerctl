package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPasswordEnvelope(t *testing.T) {
	envelope, err := hashPassword("a correct long passphrase", bytes.NewReader(bytes.Repeat([]byte{0x42}, Argon2SaltBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Salt != "QkJCQkJCQkJCQkJCQkJCQg" || envelope.Hash == "" {
		t.Fatalf("envelope = %+v", envelope)
	}
	matched, err := VerifyPassword(envelope, "a correct long passphrase")
	if err != nil || !matched {
		t.Fatalf("correct VerifyPassword() = %v, %v", matched, err)
	}
	matched, err = VerifyPassword(envelope, "an incorrect passphrase")
	if err != nil || matched {
		t.Fatalf("incorrect VerifyPassword() = %v, %v", matched, err)
	}
}

func TestPasswordRequirements(t *testing.T) {
	tests := []string{"short", strings.Repeat("x", MaximumPasswordBytes+1), string([]byte{0xff, 0xfe})}
	for _, password := range tests {
		if err := ValidatePassword(password); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("ValidatePassword(%q) error = %v", password, err)
		}
	}
	if err := ValidatePassword("twelve chars!"); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordEnvelopeRejectsUnsafeParameters(t *testing.T) {
	envelope, err := hashPassword("a correct long passphrase", bytes.NewReader(bytes.Repeat([]byte{0x24}, Argon2SaltBytes)))
	if err != nil {
		t.Fatal(err)
	}
	envelope.MemoryKiB++
	if _, err := VerifyPassword(envelope, "a correct long passphrase"); err == nil {
		t.Fatal("VerifyPassword accepted unreviewed parameters")
	}
}
