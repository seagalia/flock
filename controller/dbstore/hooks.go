package dbstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

type TransHook interface {
	Convert([]byte) ([]byte, error)
	Revert([]byte) ([]byte, error)
}

// EncryptTransHook encrypts the stored resources with AES-256-GCM with random nonces.
type EncryptTransHook struct {
	aead cipher.AEAD
}

func NewEncryptTransHook(secret string) (*EncryptTransHook, error) {
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, "flock dbstore encryption", 32)
	if err != nil {
		return nil, fmt.Errorf("failed to derive the encryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &EncryptTransHook{aead: aead}, nil
}

func (h *EncryptTransHook) Convert(content []byte) ([]byte, error) {
	nonce := make([]byte, h.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate a nonce: %w", err)
	}
	return h.aead.Seal(nonce, nonce, content, nil), nil
}

func (h *EncryptTransHook) Revert(content []byte) ([]byte, error) {
	nonceSize := h.aead.NonceSize()
	if len(content) < nonceSize {
		return nil, fmt.Errorf("encrypted content too short")
	}
	nonce, ciphertext := content[:nonceSize], content[nonceSize:]
	return h.aead.Open(nil, nonce, ciphertext, nil)
}
