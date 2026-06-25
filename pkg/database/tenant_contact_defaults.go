package database

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// Valores por defecto cuando el cliente no tiene dirección/ubigeo (SUNAT, XML, impresión).
// Ubigeo 040101 = distrito Arequipa (provincia y departamento Arequipa), catálogo INEI.
const (
	DefaultTenantContactAddress = "Arequipa"
	DefaultTenantContactUbigeo  = "040101"
)

// NormalizeTenantContactAddressUbigeo rellena dirección y ubigeo vacíos con los valores por defecto del tenant.
func NormalizeTenantContactAddressUbigeo(addr, ubigeo string) (string, string) {
	a := strings.TrimSpace(addr)
	u := strings.TrimSpace(ubigeo)
	if a == "" {
		a = DefaultTenantContactAddress
	}
	if u == "" {
		u = DefaultTenantContactUbigeo
	}
	return a, u
}

// EnsureDefaultSaleContact garantiza el cliente genérico del POS (SUNAT doc_type 0, doc_number 99999999)
// con dirección y ubigeo por defecto (Arequipa / 040101). Idempotente: crea la fila o corrige campos vacíos.
func EnsureDefaultSaleContact(db *gorm.DB) error {
	var c TenantContact
	err := db.Where("doc_type = ? AND doc_number = ?", "0", "99999999").First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		addr, ubi := NormalizeTenantContactAddressUbigeo("", "")
		def := TenantContact{
			Type:            "customer",
			DocType:         "0",
			DocNumber:       "99999999",
			BusinessName:    "Clientes Varios",
			TradeName:       "Público en general",
			Address:         addr,
			Ubigeo:          ubi,
			IsDefaultWalkIn: true,
			Active:          true,
		}
		return db.Create(&def).Error
	}
	if err != nil {
		return err
	}
	addr, ubi := NormalizeTenantContactAddressUbigeo(c.Address, c.Ubigeo)
	if strings.TrimSpace(c.Address) != addr || strings.TrimSpace(c.Ubigeo) != ubi {
		return db.Model(&c).Updates(map[string]interface{}{"address": addr, "ubigeo": ubi}).Error
	}
	return nil
}

// EnsureCompanyFiscalDomicile rellena domicilio fiscal vacío (ubigeo/dirección) para emisión SUNAT.
func EnsureCompanyFiscalDomicile(db *gorm.DB) error {
	var cfg TenantCompanyConfig
	if err := db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	addr, ubi := NormalizeTenantContactAddressUbigeo(cfg.Address, cfg.Ubigeo)
	if strings.TrimSpace(cfg.Address) == addr && strings.TrimSpace(cfg.Ubigeo) == ubi {
		return nil
	}
	return db.Model(&cfg).Updates(map[string]interface{}{
		"address": addr,
		"ubigeo":  ubi,
	}).Error
}
