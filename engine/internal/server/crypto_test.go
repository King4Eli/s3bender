package server

import "testing"

func TestSignatureSameInputsMatch(t *testing.T) {
	sts := StringToSign("GET", "/buckets/demo/objects/a.txt", 1_700_000_000)
	if !SignatureMatches(Sign("top-secret", sts), Sign("top-secret", sts)) {
		t.Fatal("same inputs should produce matching signatures")
	}
}

func TestSignatureDifferentSecretMismatch(t *testing.T) {
	sts := StringToSign("GET", "/buckets/demo/objects/a.txt", 1_700_000_000)
	if SignatureMatches(Sign("secret-a", sts), Sign("secret-b", sts)) {
		t.Fatal("different secrets should not match")
	}
}

func TestSignatureDifferentPathMismatch(t *testing.T) {
	a := Sign("top-secret", StringToSign("GET", "/buckets/demo/objects/a.txt", 1_700_000_000))
	b := Sign("top-secret", StringToSign("GET", "/buckets/demo/objects/b.txt", 1_700_000_000))
	if SignatureMatches(a, b) {
		t.Fatal("different paths should not match")
	}
}

// Pins the exact byte layout of the protocol - a change here breaks every client and presigned URL.
func TestSignatureKnownVector(t *testing.T) {
	got := StringToSign("PUT", "/buckets/demo/objects/a b.txt", 1_700_000_000)
	if want := "PUT\n/buckets/demo/objects/a b.txt\n1700000000"; got != want {
		t.Fatalf("string-to-sign = %q, want %q", got, want)
	}
	if sig := Sign("key", "The quick brown fox jumps over the lazy dog"); sig != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8" {
		t.Fatalf("HMAC-SHA256 hex = %s", sig)
	}
}

func TestCryptoRoundTripAndKeyFormats(t *testing.T) {
	c, err := NewCrypto(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc := c.EncryptSecret("s3cr3t")
	if dec, err := c.DecryptSecret(enc); err != nil || dec != "s3cr3t" {
		t.Fatalf("round trip = %q, %v", dec, err)
	}
	if _, err := NewCrypto("dG9vLXNob3J0"); err == nil {
		t.Fatal("a master key that isn't 32 bytes must be rejected")
	}
	if _, err := NewCrypto(""); err == nil {
		t.Fatal("a missing master key must be rejected")
	}
	if ak := GenerateAccessKey(); len(ak) != 2+22 || ak[:2] != "AK" {
		t.Fatalf("unexpected access key format %q", ak)
	}
}
