package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// Crypto encrypts per-bucket secret keys at rest (AES-256-GCM) under the server master key
// (S3BENDER_MASTER_KEY), and generates the random access/secret key pairs issued to buckets.
//
// Ciphertext layout is base64(nonce[12] || ciphertext || tag[16]) - the same bytes the previous
// C# engine wrote, so existing databases decrypt unchanged.
type Crypto struct {
	aead cipher.AEAD
}

func NewCrypto(masterKeyBase64 string) (*Crypto, error) {
	if masterKeyBase64 == "" {
		return nil, errors.New("S3BENDER_MASTER_KEY is not set. Generate one with: openssl rand -base64 32")
	}
	key, err := base64.StdEncoding.DecodeString(masterKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("S3BENDER_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("S3BENDER_MASTER_KEY must decode to exactly 32 bytes (AES-256)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypto{aead: aead}, nil
}

func (c *Crypto) EncryptSecret(plaintext string) string {
	nonce := make([]byte, c.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(c.aead.Seal(nonce, nonce, []byte(plaintext), nil))
}

func (c *Crypto) DecryptSecret(encoded string) (string, error) {
	combined, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	n := c.aead.NonceSize()
	if len(combined) < n+c.aead.Overhead() {
		return "", errors.New("encrypted secret is truncated")
	}
	plain, err := c.aead.Open(nil, combined[:n], combined[n:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func GenerateAccessKey() string { return "AK" + randomBase64URL(16) }

func GenerateSecretKey() string { return randomBase64URL(32) }

func randomBase64URL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---- S3BENDER-HMAC-SHA256 signing, see _docs/auth-and-signing.md ----

// StringToSign is METHOD "\n" decoded-PATH "\n" epoch-seconds. The header form signs the request
// timestamp and the presigned form signs the expiry; the byte layout is identical.
func StringToSign(method, path string, epochSeconds int64) string {
	return method + "\n" + path + "\n" + strconv.FormatInt(epochSeconds, 10)
}

// Sign returns lowercase hex HMAC-SHA256(secret, stringToSign).
func Sign(secretKey, stringToSign string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignatureMatches compares in constant time so signature material can't leak through timing.
func SignatureMatches(expected, provided string) bool {
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
