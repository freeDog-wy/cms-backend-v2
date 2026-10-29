package pkg

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func GenerateToken() (*string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, nil, err
	}

	token := base64.RawURLEncoding.EncodeToString(raw)

	digest := sha256.Sum256([]byte(token))

	return &token, digest[:], nil
}
