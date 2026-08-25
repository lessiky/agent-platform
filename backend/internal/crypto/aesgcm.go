package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// AesGCM AES-256-GCM 加密器 (MCP 凭证加密存储, PRD 2.2.3 P0)
type AesGCM struct {
	aead cipher.AEAD
}

// NewAesGCM 从 64 位 hex 密钥 (32 字节) 创建加密器
func NewAesGCM(keyHex string) (*AesGCM, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid AES key (need 64 hex chars): %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid AES key length: %d bytes (need 32)", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	return &AesGCM{aead: aead}, nil
}

// Encrypt 明文 -> nonce || 密文 (含认证 tag)
func (c *AesGCM) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to read nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt Encrypt 的逆操作
func (c *AesGCM) Decrypt(data []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	plain, err := c.aead.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}
	return plain, nil
}
