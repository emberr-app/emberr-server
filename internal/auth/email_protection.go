package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/nacl/secretbox"
)

const (
	EmailEncryptionKeyLength = 32
	EmailHashKeyLength       = 64
	emailNonceLength         = 24
)

type EncryptedEmail struct {
	Ciphertext []byte
	Nonce      []byte
}

type EmailProtector struct {
	encryptionKey [EmailEncryptionKeyLength]byte
	hashKey       [EmailHashKeyLength]byte
}

func NewEmailProtector(encryptionKey [EmailEncryptionKeyLength]byte, hashKey [EmailHashKeyLength]byte) EmailProtector {
	return EmailProtector{encryptionKey: encryptionKey, hashKey: hashKey}
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (protector EmailProtector) Encrypt(email string) EncryptedEmail {
	var nonce [emailNonceLength]byte
	rand.Read(nonce[:])
	ciphertext := secretbox.Seal(nil, []byte(NormalizeEmail(email)), &nonce, &protector.encryptionKey)
	return EncryptedEmail{Ciphertext: ciphertext, Nonce: nonce[:]}
}

func (protector EmailProtector) Decrypt(encryptedEmail EncryptedEmail) (string, error) {
	if len(encryptedEmail.Nonce) != emailNonceLength {
		return "", fmt.Errorf("decrypt email: nonce is %d bytes, want %d", len(encryptedEmail.Nonce), emailNonceLength)
	}
	nonce := [emailNonceLength]byte(encryptedEmail.Nonce)
	email, opened := secretbox.Open(nil, encryptedEmail.Ciphertext, &nonce, &protector.encryptionKey)
	if !opened {
		return "", errors.New("decrypt email: wrong key or damaged data")
	}
	return string(email), nil
}

func (protector EmailProtector) Hash(email string) string {
	hasher, _ := blake2b.New256(protector.hashKey[:])
	hasher.Write([]byte(NormalizeEmail(email)))
	return base64.StdEncoding.EncodeToString(hasher.Sum(nil))
}
