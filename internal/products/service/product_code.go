package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// productCodeAlphabet mismo alfabeto que usa el formulario del catálogo.
const productCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

const productCodeLength = 6

// NextProductCode código alfanumérico libre (ej. «UKVE8N»).
//
// SUNAT exige código de producto en cada línea del comprobante: sin él la emisión falla al
// final del proceso, con el cliente esperando. Como el alta rápida no siempre tiene un código
// propio a mano, se genera uno y el usuario puede reemplazarlo por el suyo.
//
// No es correlativo a propósito: un número de secuencia sugiere un orden del catálogo que no
// existe y choca con los códigos que el propio negocio ya usa.
func (s *ProductService) NextProductCode(branchID uint, scopeBranch bool) (string, error) {
	// Varios intentos por si el azar cae sobre uno ya usado; con 36^6 combinaciones la
	// colisión es rarísima, pero el código debe ser único sí o sí.
	for attempt := 0; attempt < 20; attempt++ {
		code, err := randomProductCode()
		if err != nil {
			return "", err
		}
		existing, err := s.findProductByCodeUnscoped(code, branchID, scopeBranch)
		if err != nil {
			return "", err
		}
		if existing == nil {
			return code, nil
		}
	}
	return "", fmt.Errorf("no se pudo generar un código de producto libre")
}

func randomProductCode() (string, error) {
	max := big.NewInt(int64(len(productCodeAlphabet)))
	var sb strings.Builder
	for i := 0; i < productCodeLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(productCodeAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

// ensureProductCode completa el código cuando llega vacío.
//
// Es la red de seguridad del backend: el formulario ya genera uno, pero la API también se usa
// desde importaciones y otras integraciones, y un producto sin código no se puede facturar.
func (s *ProductService) ensureProductCode(input *ProductInput) error {
	if strings.TrimSpace(input.Code) != "" {
		input.Code = strings.TrimSpace(input.Code)
		return nil
	}
	scopeBranch := input.IsRestaurant && input.BranchID > 0
	code, err := s.NextProductCode(input.BranchID, scopeBranch)
	if err != nil {
		return err
	}
	input.Code = code
	return nil
}
