// Package cryptoutil provides small, dependency-free cryptographic helpers.
//
// PBKDF2-HMAC-SHA256 is implemented here rather than pulled from an external
// module so that the whole project depends on nothing but the standard library
// (plus robfig/cron). The construction is simple and fully specified by RFC
// 8018; test vectors from RFC 6070 / RFC 7914 are asserted in pbkdf2_test.go.
package cryptoutil

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	// DefaultIterations is deliberately high; password verification happens
	// once per login on a router-class CPU, so ~100ms is an acceptable cost.
	DefaultIterations = 120000
	saltLen           = 16
	keyLen            = 32
)

// pbkdf2SHA256 derives a key of length dkLen using PBKDF2 with HMAC-SHA256.
func pbkdf2SHA256(password, salt []byte, iter, dkLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (dkLen + hashLen - 1) / hashLen

	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	t := make([]byte, hashLen)
	var blockBuf [4]byte

	for block := 1; block <= numBlocks; block++ {
		// U_1 = PRF(password, salt || INT_32_BE(block))
		prf.Reset()
		prf.Write(salt)
		blockBuf[0] = byte(block >> 24)
		blockBuf[1] = byte(block >> 16)
		blockBuf[2] = byte(block >> 8)
		blockBuf[3] = byte(block)
		prf.Write(blockBuf[:])
		u = prf.Sum(u[:0])
		copy(t, u)

		// U_n = PRF(password, U_{n-1}); T = U_1 xor U_2 xor ... xor U_iter
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:dkLen]
}

// HashPassword returns an encoded hash of the form
// "pbkdf2$sha256$<iter>$<saltHex>$<keyHex>".
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := pbkdf2SHA256([]byte(password), salt, DefaultIterations, keyLen)
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s",
		DefaultIterations, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash produced by
// HashPassword. Comparison is constant-time.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[2])
	if err != nil || iter <= 0 || iter > 10_000_000 {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[4])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomBytes returns n cryptographically secure random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing means the platform entropy source is broken;
		// there is no safe way to continue producing secrets.
		panic("cryptoutil: entropy source unavailable: " + err.Error())
	}
	return b
}

// RandomHex returns a hex string of n random bytes (2n characters).
func RandomHex(n int) string { return hex.EncodeToString(RandomBytes(n)) }

// RandomToken returns a URL-safe random string carrying n bytes of entropy.
func RandomToken(n int) string { return base64.RawURLEncoding.EncodeToString(RandomBytes(n)) }

// EqualBytes is a constant-time comparison helper for secrets.
func EqualBytes(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
