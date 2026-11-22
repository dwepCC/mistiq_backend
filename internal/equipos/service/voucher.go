package service

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// MaxVoucherBytes tamaño máximo del voucher de un cobro.
const MaxVoucherBytes = 5 << 20

var voucherTypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/webp":      ".webp",
	"application/pdf": ".pdf",
}

// VoucherDir carpeta (privada, fuera de /uploads) donde se guardan los vouchers de cobros de equipos.
// Se puede mover con EQUIPOS_VOUCHER_DIR; por defecto ./storage/equipos/vouchers.
func VoucherDir() string {
	if d := strings.TrimSpace(os.Getenv("EQUIPOS_VOUCHER_DIR")); d != "" {
		return d
	}
	return filepath.Join("storage", "equipos", "vouchers")
}

// SaveVoucher guarda (o reemplaza) el voucher de un cobro. El tipo se decide por el contenido, no por lo que declare el
// cliente: solo jpg, png, webp o pdf.
func (s *Service) SaveVoucher(paymentID uint, data []byte) (*PaymentView, error) {
	if len(data) == 0 {
		return nil, invalid("el archivo está vacío")
	}
	if len(data) > MaxVoucherBytes {
		return nil, invalid("el voucher no puede pesar más de 5 MB")
	}
	mime := http.DetectContentType(data)
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	ext, ok := voucherTypes[mime]
	if !ok {
		return nil, invalid("el voucher debe ser una imagen (jpg, png, webp) o un PDF")
	}
	var p database.EquipPayment
	if found, err := findOne(s.db, &p, "id = ?", paymentID); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("cobro no encontrado")
	}
	if err := os.MkdirAll(VoucherDir(), 0o750); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("pago-%d-%d%s", p.ID, time.Now().UnixNano(), ext)
	if err := os.WriteFile(filepath.Join(VoucherDir(), name), data, 0o640); err != nil {
		return nil, err
	}
	old := p.VoucherFile
	if err := s.db.Model(&database.EquipPayment{}).Where("id = ?", p.ID).Updates(map[string]any{"voucher_file": name, "voucher_mime": mime}).Error; err != nil {
		_ = os.Remove(filepath.Join(VoucherDir(), name))
		return nil, err
	}
	if old != "" && old != name {
		_ = os.Remove(filepath.Join(VoucherDir(), filepath.Base(old)))
	}
	return s.GetPayment(p.ID)
}

// ReadVoucher devuelve el contenido y el tipo del voucher de un cobro.
func (s *Service) ReadVoucher(paymentID uint) ([]byte, string, error) {
	var p database.EquipPayment
	if found, err := findOne(s.db, &p, "id = ?", paymentID); err != nil {
		return nil, "", err
	} else if !found || p.VoucherFile == "" {
		return nil, "", invalid("este cobro no tiene voucher")
	}
	// filepath.Base: el nombre viene de la BD, pero nunca debe poder salir de la carpeta de vouchers.
	b, err := os.ReadFile(filepath.Join(VoucherDir(), filepath.Base(p.VoucherFile)))
	if err != nil {
		return nil, "", invalid("no se encontró el archivo del voucher")
	}
	return b, p.VoucherMime, nil
}
