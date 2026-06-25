package facturador

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"software.sslmate.com/src/go-pkcs12"
)

func decodeBase64Flexible(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("contenido base64 vacío")
	}
	if idx := strings.Index(s, ","); strings.HasPrefix(strings.ToLower(s), "data:") && idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		default:
			return r
		}
	}, s)
	raw, err := base64.StdEncoding.DecodeString(s)
	if err == nil {
		return raw, nil
	}
	if raw, err2 := base64.RawStdEncoding.DecodeString(s); err2 == nil {
		return raw, nil
	}
	return nil, fmt.Errorf("pfx_base64 inválido: %w", err)
}

func findOpenSSLExecutable() string {
	if p, err := exec.LookPath("openssl"); err == nil {
		return p
	}
	for _, candidate := range []string{
		`C:\Program Files\Git\usr\bin\openssl.exe`,
		`C:\Program Files\OpenSSL-Win64\bin\openssl.exe`,
		`C:\Program Files\OpenSSL-Win32\bin\openssl.exe`,
	} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func runOpenSSLPkcs12Extract(opensslBin string, raw []byte, password string, useLegacy bool) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "tukifac-pfx-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	pfxPath := filepath.Join(tmpDir, "input.p12")
	pemPath := filepath.Join(tmpDir, "output.pem")
	if err := os.WriteFile(pfxPath, raw, 0600); err != nil {
		return nil, err
	}

	args := []string{"pkcs12", "-in", pfxPath, "-nodes", "-out", pemPath}
	if useLegacy {
		args = []string{"pkcs12", "-legacy", "-in", pfxPath, "-nodes", "-out", pemPath}
	}
	if password != "" {
		args = append(args, "-passin", "env:PFX_PASSWORD")
	} else {
		args = append(args, "-passin", "pass:")
	}
	cmd := exec.Command(opensslBin, args...)
	if password != "" {
		cmd.Env = append(os.Environ(), "PFX_PASSWORD="+password)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "mac verify error") ||
			strings.Contains(lower, "invalid password") ||
			strings.Contains(lower, "bad decrypt") {
			return nil, fmt.Errorf("contraseña del PFX incorrecta")
		}
		if strings.Contains(lower, "unknown option") && useLegacy {
			return runOpenSSLPkcs12Extract(opensslBin, raw, password, false)
		}
		if msg != "" {
			return nil, fmt.Errorf("openssl pkcs12: %s", msg)
		}
		return nil, fmt.Errorf("openssl pkcs12: %w", err)
	}
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(pemBytes)) == 0 {
		return nil, fmt.Errorf("openssl no extrajo clave ni certificado del PFX")
	}
	return pemBytes, nil
}

func pfxToPEMViaOpenSSL(raw []byte, password string) ([]byte, error) {
	opensslBin := findOpenSSLExecutable()
	if opensslBin == "" {
		return nil, fmt.Errorf("openssl no está instalado o no está en PATH")
	}
	return runOpenSSLPkcs12Extract(opensslBin, raw, password, true)
}

func pfxToPEMBlocks(raw []byte, password string) ([]*pem.Block, error) {
	blocks, err := pkcs12.ToPEM(raw, password)
	if err == nil {
		return blocks, nil
	}
	if errors.Is(err, pkcs12.ErrIncorrectPassword) {
		return nil, fmt.Errorf("contraseña del PFX incorrecta")
	}
	pemBytes, oerr := pfxToPEMViaOpenSSL(raw, password)
	if oerr != nil {
		return nil, fmt.Errorf("no se pudo abrir el PFX: %w (parser Go: %v)", oerr, err)
	}
	return pemDecodeAll(pemBytes)
}

func pemDecodeAll(raw []byte) ([]*pem.Block, error) {
	var blocks []*pem.Block
	rest := raw
	for {
		block, rem := pem.Decode(rest)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		rest = rem
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no se encontraron bloques PEM en el PFX")
	}
	return blocks, nil
}
