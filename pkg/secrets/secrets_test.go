package secrets

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func setKey(t *testing.T, b byte) {
	t.Helper()
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	t.Setenv("SECRETS_ENCRYPTION_KEY", hex.EncodeToString(k))
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	setKey(t, 7)
	a, err := Encrypt("sk_live_abc123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a, "abc123") || !strings.HasPrefix(a, "v1:") {
		t.Fatalf("el valor cifrado no debe contener el texto plano: %q", a)
	}
	b, _ := Encrypt("sk_live_abc123")
	if a == b {
		t.Error("cada cifrado debe usar un nonce distinto")
	}
	got, err := Decrypt(a)
	if err != nil || got != "sk_live_abc123" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
}

func TestDecryptFailsWithWrongKeyOrTampering(t *testing.T) {
	setKey(t, 1)
	enc, _ := Encrypt("secreto")
	setKey(t, 2)
	if _, err := Decrypt(enc); err == nil {
		t.Error("con otra clave maestra no debe descifrar")
	}
	setKey(t, 1)
	if _, err := Decrypt(enc[:len(enc)-4] + "AAAA"); err == nil {
		t.Error("un valor alterado no debe descifrar")
	}
	if _, err := Decrypt("texto-plano"); err == nil {
		t.Error("formato desconocido debe fallar")
	}
}

func TestMissingKey(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	if _, err := Encrypt("x"); !errors.Is(err, ErrNoKey) {
		t.Errorf("sin clave: %v", err)
	}
	t.Setenv("SECRETS_ENCRYPTION_KEY", "corta")
	if _, err := Encrypt("x"); !errors.Is(err, ErrNoKey) {
		t.Errorf("clave inválida: %v", err)
	}
}

func TestHint(t *testing.T) {
	if Hint("abcdefgh1234") != "1234" || Hint("ab") != "••" {
		t.Error("hint")
	}
}
