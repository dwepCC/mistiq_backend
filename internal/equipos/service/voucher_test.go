package service

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// pngBytes: cabecera PNG válida (basta para que el tipo se detecte por contenido).
var pngBytes = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...)

func TestVoucher_saveReadReplaceAndValidate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EQUIPOS_VOUCHER_DIR", dir)
	f := newFixture(t)
	o := f.confirmed(t, 1, 100)
	cid := *o.CustomerID
	pay, err := f.s.CreatePayment(PaymentInput{CustomerID: &cid, Amount: 50, Method: "yape", Moment: "anticipado", Allocations: []PaymentAllocationInput{{OrderID: o.ID, Amount: 50}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pay.VoucherMime != "" {
		t.Fatal("un cobro nuevo no trae voucher")
	}

	v, err := f.s.SaveVoucher(pay.ID, pngBytes)
	if err != nil || v.VoucherMime != "image/png" {
		t.Fatalf("guardar voucher png: %v %+v", err, v)
	}
	data, mime, err := f.s.ReadVoucher(pay.ID)
	if err != nil || mime != "image/png" || !bytes.Equal(data, pngBytes) {
		t.Fatalf("leer voucher: %v %s", err, mime)
	}

	// Reemplazar borra el archivo anterior (no se acumulan huérfanos).
	pdf := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 64)...)
	if _, err := f.s.SaveVoucher(pay.ID, pdf); err != nil {
		t.Fatalf("reemplazar por pdf: %v", err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 || filepath.Ext(files[0].Name()) != ".pdf" {
		t.Fatalf("debe quedar un solo archivo (pdf): %v", files)
	}

	for name, bad := range map[string][]byte{
		"texto":   []byte("no soy una imagen"),
		"vacío":   {},
		"enorme":  append([]byte("%PDF-"), bytes.Repeat([]byte("a"), MaxVoucherBytes)...),
		"exe/zip": {'P', 'K', 3, 4, 0, 0, 0, 0},
	} {
		if _, err := f.s.SaveVoucher(pay.ID, bad); err == nil || !IsValidation(err) {
			t.Errorf("%s debe rechazarse: %v", name, err)
		}
	}
	if _, _, err := f.s.ReadVoucher(999999); err == nil {
		t.Error("un cobro inexistente no tiene voucher")
	}
}
