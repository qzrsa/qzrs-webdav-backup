package cryptoutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrDecrypt indicates the ciphertext was tampered with or the key is wrong.
var ErrDecrypt = errors.New("cryptoutil: decryption failed (wrong key or corrupted data)")

// DeriveKey turns an arbitrary-length secret into a 32-byte AES-256 key.
//
// NOTE: the domain-separation string below predates the project rename to
// qzrs-webdav-backup. It MUST stay unchanged: config.json files written by
// older releases encrypt their WebDAV credentials with a key derived from
// this exact string, and changing it would make those passwords undecryptable.
func DeriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte("webdav-backup/v1/secret\x00" + secret))
	return sum[:]
}

// Encrypt seals plaintext with AES-256-GCM and returns base64(nonce||ciphertext).
//
// This is "encryption at rest" for stored WebDAV credentials: it stops casual
// disclosure if the config file leaks, but the key lives beside the data, so it
// is not a defence against an attacker who can already read the whole disk.
func Encrypt(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	nonce := RandomBytes(gcm.NonceSize())
	out := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: bad base64", ErrDecrypt)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}
