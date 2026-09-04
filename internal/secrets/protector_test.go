package secrets

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestProtectorRoundTripAndContextBinding(t *testing.T) {
	t.Parallel()
	master := [32]byte{1, 2, 3, 4}
	protector, err := NewProtector(master, bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	document := []byte(`{"password":"TEST_ONLY_NOT_A_SECRET"}`)
	envelope, err := protector.Seal("outbound:vless_a", document)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(envelope.Ciphertext, document) || bytes.Contains(envelope.Ciphertext, []byte("TEST_ONLY_NOT_A_SECRET")) {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := protector.Open("outbound:vless_a", envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, document) {
		t.Fatalf("opened document = %q", opened)
	}
	if _, err := protector.Open("outbound:vless_b", envelope); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong context error = %v", err)
	}
	envelope.Ciphertext[0] ^= 0xff
	if _, err := protector.Open("outbound:vless_a", envelope); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tamper error = %v", err)
	}
}

func TestProtectorRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()
	if _, err := NewProtector([32]byte{}, bytes.NewReader(nil)); err == nil {
		t.Fatal("zero master key accepted")
	}
	protector, err := NewProtector([32]byte{1}, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protector.Seal("", []byte("value")); err == nil {
		t.Fatal("empty context accepted")
	}
	if _, err := protector.Seal("outbound:test", []byte{}); err == nil {
		t.Fatal("empty document accepted")
	}
	if _, err := protector.Seal("outbound:test", []byte(strings.Repeat("x", MaximumDocumentBytes+1))); err == nil {
		t.Fatal("oversized document accepted")
	}
}
