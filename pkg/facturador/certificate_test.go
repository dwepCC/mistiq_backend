package facturador

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeCertificatePayloadBase64_std(t *testing.T) {
	raw := []byte{0x30, 0x82, 0x01, 0x00, 0x02, 0x01}
	b64 := base64.StdEncoding.EncodeToString(raw)
	got, err := DecodeCertificatePayloadBase64(b64)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(raw) {
		t.Fatalf("len=%d want %d", len(got), len(raw))
	}
}

func TestDecodeCertificatePayloadBase64_dataURL(t *testing.T) {
	raw := []byte{0x30, 0x03, 0x01, 0x01, 0xff}
	b64 := "data:application/x-pkcs12;base64," + base64.StdEncoding.EncodeToString(raw)
	got, err := DecodeCertificatePayloadBase64(b64)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0x30 {
		t.Fatalf("got %x", got[0])
	}
}

func TestExtractPKCS12DER_rejectsPlainPEM(t *testing.T) {
	pem := []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
	_, err := extractPKCS12DER(pem)
	if err == nil {
		t.Fatal("expected error for PEM certificate without PKCS12 block")
	}
}

func TestIsLikelyCombinedPEM(t *testing.T) {
	pem := []byte("-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\n")
	if !isLikelyCombinedPEM(pem) {
		t.Fatal("expected true")
	}
	if isLikelyCombinedPEM([]byte{0x30, 0x01}) {
		t.Fatal("expected false for DER")
	}
}

func TestPrepareGreenterCertificateBase64_pemInPfxField(t *testing.T) {
	combined := strings.Join([]string{
		"-----BEGIN RSA PRIVATE KEY-----",
		base64.StdEncoding.EncodeToString([]byte("fake-key-bytes-not-real")),
		"-----END RSA PRIVATE KEY-----",
		"-----BEGIN CERTIFICATE-----",
		base64.StdEncoding.EncodeToString([]byte("fake-cert")),
		"-----END CERTIFICATE-----",
	}, "\n")
	b64 := base64.StdEncoding.EncodeToString([]byte(combined))
	_, err := PrepareGreenterCertificateBase64(b64, "", "", "")
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "not DER") || strings.Contains(err.Error(), "indefinite length") {
		t.Fatalf("PEM in pfx field should not hit PKCS12 DER parser: %v", err)
	}
}
