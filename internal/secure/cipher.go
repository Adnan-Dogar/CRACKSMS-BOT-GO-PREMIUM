package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

type Cipher struct{ aead cipher.AEAD }

func NewCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	if c == nil || c.aead == nil {
		return "", errors.New("cipher is not configured")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(value string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errors.New("cipher is not configured")
	}
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	nonceSize := c.aead.NonceSize()
	if len(sealed) < nonceSize {
		return nil, errors.New("encrypted value is too short")
	}
	return c.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
}
