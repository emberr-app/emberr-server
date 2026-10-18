package auth

import "testing"

var (
	testEncryptionKey  = [EmailEncryptionKeyLength]byte{1}
	otherEncryptionKey = [EmailEncryptionKeyLength]byte{2}
	testHashKey        = [EmailHashKeyLength]byte{3}
	otherHashKey       = [EmailHashKeyLength]byte{4}
)

func TestEncryptedEmailDecryptsToTheSameEmail(t *testing.T) {
	protector := NewEmailProtector(testEncryptionKey, testHashKey)

	decryptedEmail, err := protector.Decrypt(protector.Encrypt("ana@example.com"))
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decryptedEmail != "ana@example.com" {
		t.Fatalf("decrypted email = %q, want %q", decryptedEmail, "ana@example.com")
	}
}

func TestDecryptingWithAWrongKeyFails(t *testing.T) {
	encryptedEmail := NewEmailProtector(testEncryptionKey, testHashKey).Encrypt("ana@example.com")

	_, err := NewEmailProtector(otherEncryptionKey, testHashKey).Decrypt(encryptedEmail)
	if err == nil {
		t.Fatal("decrypting with a wrong key succeeded, want an error")
	}
}

func TestEmailHashIgnoresCaseAndSurroundingSpaces(t *testing.T) {
	protector := NewEmailProtector(testEncryptionKey, testHashKey)

	if protector.Hash("  Ana@Example.COM ") != protector.Hash("ana@example.com") {
		t.Fatal("the same email typed differently gave different hashes")
	}
}

func TestDifferentHashKeyGivesDifferentHash(t *testing.T) {
	firstHash := NewEmailProtector(testEncryptionKey, testHashKey).Hash("ana@example.com")
	secondHash := NewEmailProtector(testEncryptionKey, otherHashKey).Hash("ana@example.com")

	if firstHash == secondHash {
		t.Fatal("two different hash keys gave the same hash")
	}
}
