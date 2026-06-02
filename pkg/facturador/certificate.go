package facturador

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"software.sslmate.com/src/go-pkcs12"
)

// DecodeCertificatePayloadBase64 decodifica base64 estándar, URL-safe, sin padding o data-URL.
func DecodeCertificatePayloadBase64(b64 string) ([]byte, error) {
	s := strings.TrimSpace(b64)
	if s == "" {
		return nil, fmt.Errorf("contenido vacío")
	}
	if idx := strings.Index(s, "base64,"); idx >= 0 {
		s = s[idx+7:]
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		default:
			return r
		}
	}, s)
	if s == "" {
		return nil, fmt.Errorf("base64 vacío")
	}

	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	var lastErr error
	for _, decode := range decoders {
		raw, err := decode(s)
		if err == nil && len(raw) > 0 {
			return raw, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("base64 inválido: %w", lastErr)
	}
	return nil, fmt.Errorf("base64 inválido")
}

// extractPKCS12DER obtiene bytes DER PKCS#12 desde archivo binario o PEM envuelto.
func extractPKCS12DER(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("archivo vacío")
	}
	if bytes.Contains(raw, []byte("-----BEGIN")) {
		rest := raw
		for {
			block, rem := pem.Decode(rest)
			if block == nil {
				break
			}
			switch block.Type {
			case "PKCS12", "PKCS #12", "PFX", "PKCS7":
				if len(block.Bytes) > 0 {
					return block.Bytes, nil
				}
			}
			rest = rem
		}
		return nil, fmt.Errorf("el archivo es PEM pero no contiene bloque PKCS12; use modo PEM o suba el .pfx/.p12 binario original")
	}
	if raw[0] != 0x30 {
		return nil, fmt.Errorf("formato no reconocido: el PFX/P12 debe ser DER (bytes binarios); si exportó desde Windows, no renombre ni abra el archivo en un editor de texto")
	}
	return raw, nil
}

func normalizePFXPassword(password string) string {
	return strings.TrimSpace(password)
}

func pfxPasswordVariants(password string) []string {
	p := normalizePFXPassword(password)
	seen := map[string]struct{}{}
	var out []string
	for _, v := range []string{p, strings.TrimRight(p, "\r\n"), password} {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

func pkcs12BlocksToCombinedPEM(blocks []*pem.Block, password string) (string, error) {
	var keyParts []string
	var certParts []string
	for _, block := range blocks {
		if block == nil {
			continue
		}
		part := strings.TrimSpace(string(pem.EncodeToMemory(block)))
		if part == "" {
			continue
		}
		switch block.Type {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			keyParts = append(keyParts, part)
		case "ENCRYPTED PRIVATE KEY":
			dec, err := x509DecryptPEMBlock(block, password)
			if err != nil {
				return "", fmt.Errorf("no se pudo desencriptar la clave del PFX (revise la contraseña): %w", err)
			}
			keyParts = append(keyParts, strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: dec,
			}))))
		case "CERTIFICATE":
			certParts = append(certParts, part)
		}
	}
	if len(keyParts) == 0 {
		return "", fmt.Errorf("el PFX no contiene clave privada")
	}
	if len(certParts) == 0 {
		return "", fmt.Errorf("el PFX no contiene certificado")
	}
	combined := strings.Join(keyParts, "\n") + "\n" + strings.Join(certParts, "\n")
	return encodeGreenterCombinedPEM([]byte(combined))
}

// x509DecryptPEMBlock desencripta bloques ENCRYPTED PRIVATE KEY del PFX.
func x509DecryptPEMBlock(block *pem.Block, password string) ([]byte, error) {
	return x509.DecryptPEMBlock(block, []byte(password))
}

func pfxDERToPEMBlocks(der []byte, password string) ([]*pem.Block, error) {
	var lastErr error
	for _, pass := range pfxPasswordVariants(password) {
		blocks, err := pkcs12.ToPEM(der, pass)
		if err == nil {
			return blocks, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func pfxDERToPEMBlocksOpenSSL(der []byte, password string) ([]*pem.Block, error) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		return nil, fmt.Errorf("openssl no disponible en el servidor")
	}
	tmp, err := os.CreateTemp("", "bendey-pfx-*.p12")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(der); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	var lastErr error
	for _, pass := range pfxPasswordVariants(password) {
		for _, extra := range [][]string{{"-legacy"}, {"-legacy", "-nomacver"}, {}} {
			args := append([]string{"pkcs12", "-in", tmpPath, "-nodes", "-passin", "pass:" + pass}, extra...)
			cmd := exec.Command(openssl, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				lastErr = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
				continue
			}
			var blocks []*pem.Block
			rest := out
			for {
				block, rem := pem.Decode(rest)
				if block == nil {
					break
				}
				blocks = append(blocks, block)
				rest = rem
			}
			if len(blocks) > 0 {
				return blocks, nil
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("openssl no pudo convertir el PFX")
}

func isLikelyCombinedPEM(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "-----BEGIN") &&
		(strings.Contains(s, "PRIVATE KEY") || strings.Contains(s, "RSA PRIVATE KEY"))
}
