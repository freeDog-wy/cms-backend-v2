package pkg

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func TestTokenDigestMatchesGeneratedToken(t *testing.T) {
	token, storedDigest, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := TokenDigest(*token)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(*token))
	if !bytes.Equal(digest, storedDigest) || !bytes.Equal(digest, want[:]) {
		t.Fatal("token lookup digest must match the stored digest of the encoded string")
	}
}

func TestTokenDigestRejectsInvalidFormat(t *testing.T) {
	for _, token := range []string{"", "short", strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("!", 43)} {
		digest, err := TokenDigest(token)
		if !errors.Is(err, ErrInvalidToken) || digest != nil {
			t.Fatalf("token=%q digest=%x err=%v", token, digest, err)
		}
	}
}
