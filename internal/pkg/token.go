package pkg

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const tokenRawBytes = 32

var ErrInvalidToken = errors.New("invalid token")

// TokenDigest 校验 GenerateToken 生成的 Token 格式，并计算其字符串的 SHA-256 摘要。
func TokenDigest(token string) ([]byte, error) {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenRawBytes) {
		return nil, ErrInvalidToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != tokenRawBytes {
		return nil, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], nil
}

func GenerateToken() (*string, []byte, error) {
	raw := make([]byte, tokenRawBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, nil, err
	}

	token := base64.RawURLEncoding.EncodeToString(raw)

	digest := sha256.Sum256([]byte(token))

	return &token, digest[:], nil
}
