// Package secrets cifra valores sensibles que hay que poder leer de nuevo (API keys de terceros) antes
// de guardarlos en la BD. Usa AES-256-GCM con una clave maestra que vive solo en el entorno
// (SECRETS_ENCRYPTION_KEY): un volcado de la BD no expone los valores. Para lo que NO hace falta leer de
// vuelta (PIN, contraseñas) se sigue usando bcrypt.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// prefix versiona el formato para poder rotar de algoritmo sin ambigüedad.
const prefix = "v1:"

// ErrNoKey se devuelve cuando SECRETS_ENCRYPTION_KEY no está configurada o es inválida.
var ErrNoKey = errors.New("SECRETS_ENCRYPTION_KEY no está configurada (32 bytes en base64 o 64 caracteres hex)")

// keyFromEnv lee la clave maestra. Acepta 32 bytes en base64 (estándar o URL) o 64 caracteres hexadecimales.
func keyFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("SECRETS_ENCRYPTION_KEY"))
	if raw == "" {
		return nil, ErrNoKey
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(raw); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, ErrNoKey
}

func newGCM() (cipher.AEAD, error) {
	key, err := keyFromEnv()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt cifra un texto. El resultado es "v1:" + base64(nonce|ciphertext); cada llamada usa un nonce nuevo.
func Encrypt(plain string) (string, error) {
	gcm, err := newGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt revierte Encrypt. Falla si la clave maestra cambió o el valor fue alterado (GCM autentica).
func Decrypt(enc string) (string, error) {
	if !strings.HasPrefix(enc, prefix) {
		return "", errors.New("valor cifrado con formato desconocido")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, prefix))
	if err != nil {
		return "", fmt.Errorf("valor cifrado inválido: %w", err)
	}
	gcm, err := newGCM()
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("valor cifrado truncado")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("no se pudo descifrar (¿cambió SECRETS_ENCRYPTION_KEY?)")
	}
	return string(plain), nil
}

// Hint devuelve los últimos 4 caracteres para mostrar "••••1234" sin exponer el valor.
func Hint(plain string) string {
	plain = strings.TrimSpace(plain)
	if len(plain) <= 4 {
		return strings.Repeat("•", len(plain))
	}
	return plain[len(plain)-4:]
}
