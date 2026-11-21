package database

import (
	"errors"
	"log/slog"
	"strings"

	"tukifac/pkg/logger"

	"gorm.io/gorm"
)

// Hasta 2026-09, un cliente sin dirección/ubigeo se rellenaba con un default fijo
// ("Arequipa"/040101) — pensado para cumplir el requisito técnico de SUNAT de traer *algún*
// ubigeo/dirección en el XML. Es innecesario: SUNAT NO exige dirección del cliente ni en factura
// ni en boleta (solo la del emisor, y la del destinatario real en guías de remisión, por
// logística). Confirmado contra la fuente: Greenter\Model\Client\Client::$address es nullable
// (vendor/greenter/core/.../Client.php) y el propio template UBL 2.1 de Greenter
// (invoice2.1.xml.twig: `{% if client.address %}`) omite el nodo <cac:RegistrationAddress>
// completo cuando address es null — XML 100% válido, así lo hacen los ejemplos oficiales de
// Greenter (nunca llaman a Client::setAddress). El default fijo causaba que TODO tenant sin
// dirección de cliente cargada mostrara "Arequipa" en sus comprobantes, sin importar en qué
// departamento estuviera el tenant real — confuso y falso para el cliente que lo recibe.
//
// NormalizeTenantContactAddressUbigeo ahora solo recorta espacios — ya NO fabrica un valor. Los
// callers deben tratar "" como "sin dirección" y, para emisión SUNAT, omitir el objeto Address
// del cliente en vez de mandarlo con campos vacíos (ver billing_service.go/fiscal_document_emit.go).
func NormalizeTenantContactAddressUbigeo(addr, ubigeo string) (string, string) {
	return strings.TrimSpace(addr), strings.TrimSpace(ubigeo)
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

// EnsureCompanyFiscalDomicile rellena domicilio fiscal vacío (ubigeo/dirección) para emisión
// SUNAT. Corre en la migración v072 como backfill de tenants viejos (previos a exigir dirección/
// ubigeo en el alta) — si dispara para un tenant nuevo, es señal de que algo se coló sin pasar
// por la validación de TenantService.Create, por eso queda logueado en vez de aplicarse en
// silencio.
func EnsureCompanyFiscalDomicile(db *gorm.DB) error {
	var cfg TenantCompanyConfig
	if err := db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	wasEmpty := strings.TrimSpace(cfg.Address) == "" || strings.TrimSpace(cfg.Ubigeo) == ""
	addr, ubi := NormalizeTenantContactAddressUbigeo(cfg.Address, cfg.Ubigeo)
	if strings.TrimSpace(cfg.Address) == addr && strings.TrimSpace(cfg.Ubigeo) == ubi {
		return nil
	}
	if wasEmpty {
		logger.L.Warn("tenant_fiscal_domicile_default_applied",
			slog.Uint64("company_config_id", uint64(cfg.ID)),
		)
	}
	return db.Model(&cfg).Updates(map[string]interface{}{
		"address": addr,
		"ubigeo":  ubi,
	}).Error
}
