package cryptoutil

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Vectors for PBKDF2-HMAC-SHA256 (the widely published RFC 7914 / draft-josefsson set).
var sha256Vectors = []struct {
	password string
	salt     string
	iter     int
	dkLen    int
	want     string
}{
	{"password", "salt", 1, 32, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
	{"password", "salt", 2, 32, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
	{"password", "salt", 4096, 32, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
	{"passwordPASSWORDpassword", "saltSALTsaltSALTsaltSALTsaltSALTsalt", 4096, 40,
		"348c89dbcbd32b2f32d814b8116e84cf2b17347ebc1800181c4e2a1fb8dd53e1c635518c7dac47e9"},
}

func TestPBKDF2SHA256Vectors(t *testing.T) {
	for _, v := range sha256Vectors {
		got := hex.EncodeToString(pbkdf2SHA256([]byte(v.password), []byte(v.salt), v.iter, v.dkLen))
		if got != v.want {
			t.Errorf("pbkdf2(%q,%q,%d,%d) = %s, want %s", v.password, v.salt, v.iter, v.dkLen, got, v.want)
		}
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	// 明显的测试口令，避免把任何真实凭据写进仓库。
	const pw = "correct-horse-battery-staple"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(h, pw) {
		t.Fatal("correct password rejected")
	}
	for _, bad := range []string{"", "wrong", "correct-horse-battery-stapl", " correct-horse-battery-staple"} {
		if VerifyPassword(h, bad) {
			t.Errorf("password %q wrongly accepted", bad)
		}
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "plain", "pbkdf2$sha256$0$aa$bb", "pbkdf2$md5$1$aa$bb",
		"pbkdf2$sha256$99999999$aa$bb", "pbkdf2$sha256$1$zz$bb"} {
		if VerifyPassword(s, "x") {
			t.Errorf("malformed hash %q accepted", s)
		}
	}
}

func TestEncryptDecrypt(t *testing.T) {
	key := DeriveKey("test-secret")
	plain := []byte("hunter2 with ünïcode and \x00 bytes")
	enc, err := Encrypt(key, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	dec, err := Decrypt(key, enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("round trip mismatch: got %q want %q", dec, plain)
	}
	if _, err := Decrypt(DeriveKey("other-secret"), enc); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
	// Tamper with the ciphertext: GCM must reject it.
	raw, _ := hex.DecodeString("00")
	_ = raw
	if _, err := Decrypt(key, enc[:len(enc)-2]+"AA"); err == nil {
		t.Fatal("tampered ciphertext should fail")
	}
}
