package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	LoginKeyLength         = 32
	loginKeyHashMemoryKiB  = 19 * 1024
	loginKeyHashPasses     = 2
	loginKeyHashThreads    = 1
	loginKeyHashSaltLength = 16
	loginKeyHashLength     = 32
)

var ErrWrongLoginKey = errors.New("wrong email or login key")

var loginKeyHashForUnknownEmails = hashLoginKey(make([]byte, LoginKeyLength))

func hashLoginKey(loginKey []byte) string {
	salt := make([]byte, loginKeyHashSaltLength)
	rand.Read(salt)
	hash := argon2.IDKey(loginKey, salt, loginKeyHashPasses, loginKeyHashMemoryKiB, loginKeyHashThreads, loginKeyHashLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, loginKeyHashMemoryKiB, loginKeyHashPasses, loginKeyHashThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}

func loginKeyMatchesHash(loginKey []byte, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errors.New("check login key: stored hash is not an argon2id version 19 hash")
	}
	var memoryKiB, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memoryKiB, &passes, &threads); err != nil {
		return false, fmt.Errorf("check login key: read argon2id settings: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("check login key: read salt: %w", err)
	}
	storedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("check login key: read hash: %w", err)
	}

	submittedHash := argon2.IDKey(loginKey, salt, passes, memoryKiB, threads, uint32(len(storedHash)))
	return subtle.ConstantTimeCompare(submittedHash, storedHash) == 1, nil
}
