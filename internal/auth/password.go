package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	PasswordEnvelopeVersion = 1
	Argon2MemoryKiB         = 19 * 1024
	Argon2Passes            = 2
	Argon2Lanes             = 1
	Argon2SaltBytes         = 16
	Argon2KeyBytes          = 32
	MinimumPasswordRunes    = 12
	MaximumPasswordBytes    = 256
)

var ErrInvalidPassword = errors.New("password does not meet requirements")

type PasswordEnvelope struct {
	Version   int    `json:"version"`
	Algorithm string `json:"algorithm"`
	MemoryKiB uint32 `json:"memory_kib"`
	Passes    uint32 `json:"passes"`
	Lanes     uint8  `json:"lanes"`
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) > MaximumPasswordBytes || utf8.RuneCountInString(password) < MinimumPasswordRunes {
		return fmt.Errorf("%w: use at least %d Unicode characters and at most %d encoded bytes", ErrInvalidPassword, MinimumPasswordRunes, MaximumPasswordBytes)
	}
	return nil
}

func HashPassword(password string) (PasswordEnvelope, error) {
	return hashPassword(password, rand.Reader)
}

func hashPassword(password string, random io.Reader) (PasswordEnvelope, error) {
	if err := ValidatePassword(password); err != nil {
		return PasswordEnvelope{}, err
	}
	salt := make([]byte, Argon2SaltBytes)
	if _, err := io.ReadFull(random, salt); err != nil {
		return PasswordEnvelope{}, fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, Argon2Passes, Argon2MemoryKiB, Argon2Lanes, Argon2KeyBytes)
	return PasswordEnvelope{
		Version: PasswordEnvelopeVersion, Algorithm: "argon2id",
		MemoryKiB: Argon2MemoryKiB, Passes: Argon2Passes, Lanes: Argon2Lanes,
		Salt: base64.RawStdEncoding.EncodeToString(salt), Hash: base64.RawStdEncoding.EncodeToString(hash),
	}, nil
}

func VerifyPassword(envelope PasswordEnvelope, password string) (bool, error) {
	if err := validateEnvelope(envelope); err != nil {
		return false, err
	}
	validCandidate := ValidatePassword(password) == nil
	if !validCandidate {
		password = "invalid password placeholder"
	}
	salt, _ := base64.RawStdEncoding.DecodeString(envelope.Salt)
	want, _ := base64.RawStdEncoding.DecodeString(envelope.Hash)
	got := argon2.IDKey([]byte(password), salt, envelope.Passes, envelope.MemoryKiB, envelope.Lanes, uint32(len(want)))
	return validCandidate && subtle.ConstantTimeCompare(got, want) == 1, nil
}

func validateEnvelope(envelope PasswordEnvelope) error {
	if envelope.Version != PasswordEnvelopeVersion || envelope.Algorithm != "argon2id" || envelope.MemoryKiB != Argon2MemoryKiB || envelope.Passes != Argon2Passes || envelope.Lanes != Argon2Lanes {
		return fmt.Errorf("unsupported password envelope parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) != Argon2SaltBytes {
		return fmt.Errorf("invalid password salt")
	}
	hash, err := base64.RawStdEncoding.DecodeString(envelope.Hash)
	if err != nil || len(hash) != Argon2KeyBytes {
		return fmt.Errorf("invalid password hash")
	}
	return nil
}
