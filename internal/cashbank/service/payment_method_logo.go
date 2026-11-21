package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
)

// MaxPaymentMethodLogoBytes tope del data URL del logo de un método de pago. El panel lo reduce a ~96 px (unos pocos KB);
// el tope solo evita que el API reciba imágenes pesadas que luego viajan en cada listado de métodos (POS, ventas).
const MaxPaymentMethodLogoBytes = 60 * 1024

// ValidatePaymentMethodLogo acepta vacío (sin logo propio) o un data URL png/webp/jpeg en base64 de tamaño razonable.
func ValidatePaymentMethodLogo(logo string) error {
	if logo == "" {
		return nil
	}
	if len(logo) > MaxPaymentMethodLogoBytes {
		return errors.New("el logo es demasiado pesado (máximo 60 KB); use una imagen más pequeña")
	}
	for _, p := range []string{"data:image/png;base64,", "data:image/webp;base64,", "data:image/jpeg;base64,"} {
		if strings.HasPrefix(logo, p) && len(logo) > len(p) {
			return nil
		}
	}
	return errors.New("el logo debe ser una imagen PNG, WEBP o JPG")
}

// SetPaymentMethodLogo guarda (o quita, con "") el logo propio de un método de pago.
func (s *CashBankService) SetPaymentMethodLogo(id uint, logo string) error {
	if err := ValidatePaymentMethodLogo(logo); err != nil {
		return err
	}
	res := s.db.Model(&database.TenantPaymentMethod{}).Where("id = ?", id).Update("logo_url", logo)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("método de pago no encontrado")
	}
	return nil
}
