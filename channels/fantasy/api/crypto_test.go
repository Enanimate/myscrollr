package main

import (
	"os"
	"strings"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")

	tests := []struct {
		name      string
		plaintext string
	}{
		{"empty string", ""},
		{"simple text", "hello world"},
		{"unicode", "日本語テスト"},
		{"json-like", `{"access_token":"abc123","refresh_token":"xyz789"}`},
		{"long string", strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 10)},
		{"special chars", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
		{"newlines", "line1\nline2\nline3"},
		{"mixed", "Token: abc123\nExpires: 3600\nScope: openid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encrypted, err := Encrypt(tc.plaintext)
			if err != nil {
				t.Fatalf("Encrypt(%q) error = %v", tc.plaintext, err)
			}

			// Encrypted output should differ from plaintext
			if tc.plaintext != "" && encrypted == tc.plaintext {
				t.Errorf("Encrypt produced no change for %q", tc.plaintext)
			}

			// Encrypted should be base64
			if len(encrypted) == 0 {
				t.Errorf("Encrypt returned empty string for %q", tc.plaintext)
			}

			decrypted, err := Decrypt(encrypted)
			if err != nil {
				t.Fatalf("Decrypt error = %v", err)
			}
			if decrypted != tc.plaintext {
				t.Errorf("Decrypt(Encrypt(%q)) = %q, want %q", tc.plaintext, decrypted, tc.plaintext)
			}
		})
	}
}

func TestEncryptDecryptIdempotent(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")

	plaintext := "my secret token"
	encrypted1, _ := Encrypt(plaintext)
	encrypted2, _ := Encrypt(plaintext)

	// Two encryptions of the same plaintext should produce DIFFERENT ciphertexts
	// (due to random nonce), but both should decrypt to the same value
	if encrypted1 == encrypted2 {
		t.Errorf("Encrypt produced identical output twice — nonce may not be random")
	}

	decrypted1, err := Decrypt(encrypted1)
	if err != nil {
		t.Fatalf("Decrypt(encrypted1) error = %v", err)
	}
	decrypted2, err := Decrypt(encrypted2)
	if err != nil {
		t.Fatalf("Decrypt(encrypted2) error = %v", err)
	}

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Errorf("Decrypted values don't match original: %q, %q", decrypted1, decrypted2)
	}
}

func TestDecryptInvalid(t *testing.T) {
	os.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	defer os.Unsetenv("ENCRYPTION_KEY")

	tests := []struct {
		name      string
		encrypted string
	}{
		{"invalid base64", "not-valid-base64!!!"},
		{"too short", "YWJj"}, // "abc" in base64 — too short for nonce+tag
		{"tampered ciphertext", "tamperedvalue12345678901234567890123456789012345678901234567890123456"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decrypt(tc.encrypted)
			if err == nil {
				t.Errorf("Decrypt(%q) expected error, got nil", tc.encrypted)
			}
		})
	}
}

func TestEncryptMissingKey(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "")
	_, err := Encrypt("test")
	if err == nil {
		t.Error("Encrypt without ENCRYPTION_KEY expected error, got nil")
	}
}

func TestDecryptMissingKey(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "")
	_, err := Decrypt("abc")
	if err == nil {
		t.Error("Decrypt without ENCRYPTION_KEY expected error, got nil")
	}
}

func TestEncryptInvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"too short", "shortkey"},
		{"too long", "this-key-is-way-too-long-for-aes-256!!"},
		{"not base64", "not-base64-characters!!!"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ENCRYPTION_KEY", tc.key)
			_, err := Encrypt("test")
			if err == nil {
				t.Errorf("Encrypt with invalid key %q expected error, got nil", tc.key)
			}
		})
	}
}
