package service

import (
	"encoding/base64"
	"strings"
	"testing"
)

// El adjunto del correo debe ser un PDF real: cualquier archivo con extensión .pdf no pasa.
func TestDecodeReceiptPdfBase64_RequiresRealPdf(t *testing.T) {
	enc := func(b string) string { return base64.StdEncoding.EncodeToString([]byte(b)) }
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"pdf válido", enc("%PDF-1.4\n...contenido..."), ""},
		{"pdf válido con prefijo data URI", "data:application/pdf;base64," + enc("%PDF-1.7 x"), ""},
		{"ejecutable disfrazado", enc("MZ\x90\x00 programa"), "no es un PDF"},
		{"html", enc("<html><script>alert(1)</script></html>"), "no es un PDF"},
		{"vacío", "", "no se recibió"},
		{"base64 inválido", "!!!no-es-base64!!!", "inválido"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeReceiptPdfBase64(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("debía aceptarse: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, debía contener %q", err, tc.wantErr)
			}
		})
	}
}
