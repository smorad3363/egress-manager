// Package secrets protects outbound credential documents at rest.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

const MaximumDocumentBytes = 128 << 10

var ErrAuthentication = errors.New("secret document authentication failed")

type Envelope struct {
	Nonce      []byte
	Ciphertext []byte
}

type Protector struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewProtector(masterKey [32]byte, randomSource io.Reader) (*Protector, error) {
	if masterKey == [32]byte{} {
		return nil, fmt.Errorf("secret protection master key must not be zero")
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	mac := hmac.New(sha256.New, masterKey[:])
	_, _ = mac.Write([]byte("egress-manager/outbound-secrets/v1"))
	key := mac.Sum(nil)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize secret protection cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret protection mode: %w", err)
	}
	return &Protector{aead: aead, random: randomSource}, nil
}

func (protector *Protector) Seal(context string, plaintext []byte) (Envelope, error) {
	if protector == nil || protector.aead == nil {
		return Envelope{}, fmt.Errorf("secret protector is required")
	}
	if context == "" || len(context) > 256 {
		return Envelope{}, fmt.Errorf("secret context must be between 1 and 256 bytes")
	}
	if len(plaintext) == 0 || len(plaintext) > MaximumDocumentBytes {
		return Envelope{}, fmt.Errorf("secret document must be between 1 and %d bytes", MaximumDocumentBytes)
	}
	nonce := make([]byte, protector.aead.NonceSize())
	if _, err := io.ReadFull(protector.random, nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate secret nonce: %w", err)
	}
	ciphertext := protector.aead.Seal(nil, nonce, plaintext, []byte(context))
	return Envelope{Nonce: nonce, Ciphertext: ciphertext}, nil
}

func (protector *Protector) Open(context string, envelope Envelope) ([]byte, error) {
	if protector == nil || protector.aead == nil {
		return nil, fmt.Errorf("secret protector is required")
	}
	if context == "" || len(context) > 256 {
		return nil, fmt.Errorf("secret context must be between 1 and 256 bytes")
	}
	if len(envelope.Nonce) != protector.aead.NonceSize() || len(envelope.Ciphertext) <= protector.aead.Overhead() || len(envelope.Ciphertext) > MaximumDocumentBytes+protector.aead.Overhead() {
		return nil, fmt.Errorf("secret envelope is invalid")
	}
	plaintext, err := protector.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(context))
	if err != nil {
		return nil, ErrAuthentication
	}
	if len(plaintext) == 0 || len(plaintext) > MaximumDocumentBytes {
		return nil, fmt.Errorf("secret document is invalid")
	}
	return plaintext, nil
}
