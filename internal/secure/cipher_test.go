package secure

import (
	"bytes"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	cipher, err := NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"password":"secret"}`)
	encrypted, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(encrypted), []byte("secret")) {
		t.Fatal("ciphertext leaked plaintext")
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q", decrypted)
	}
}
