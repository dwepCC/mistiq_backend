package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/taxregime"

	"gorm.io/gorm"
)

// FiscalEmitter emite comprobantes vía facturador (único camino).
type FiscalEmitter interface {
	SendToSUNAT(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error)
}

// InvoiceOrchestrator delega emisión al facturador.
type InvoiceOrchestrator struct {
	db      *gorm.DB
	emitter FiscalEmitter
}

func NewInvoiceOrchestrator(db *gorm.DB, emitter FiscalEmitter) *InvoiceOrchestrator {
	return &InvoiceOrchestrator{db: db, emitter: emitter}
}

func (o *InvoiceOrchestrator) SendToSUNAT(saleID uint) (*database.TenantInvoice, error) {
	var saleRow database.TenantSale
	if err := o.db.First(&saleRow, saleID).Error; err != nil {
		return nil, errors.New("venta no encontrada")
	}
	var serRow database.TenantDocumentSeries
	sunatCode := ""
	if err := o.db.First(&serRow, saleRow.SeriesID).Error; err == nil {
		sunatCode = strings.TrimSpace(serRow.SunatCode)
		if sunatCode == "00" {
			return nil, errors.New("las notas de venta (SUNAT 00) no se envían a SUNAT")
		}
	}

	var cfg database.TenantCompanyConfig
	if err := o.db.First(&cfg).Error; err != nil || !cfg.SunatEnabled {
		return nil, errors.New("facturación electrónica no habilitada — configúrela en el panel central")
	}
	// Gate por régimen tributario del contribuyente (autoridad): p. ej. el Nuevo
	// RUS no puede emitir factura (01). El régimen se resuelve en pkg/taxregime.
	if sunatCode != "" && !taxregime.For(cfg.TaxpayerRegime).CanEmit(sunatCode) {
		return nil, errors.New("el régimen tributario de la empresa no permite emitir este tipo de comprobante")
	}
	if o.emitter == nil {
		return nil, errors.New("emisor fiscal no disponible")
	}
	return o.emitter.SendToSUNAT(saleID, &cfg)
}

func (o *InvoiceOrchestrator) CheckStatus(_ uint) (*database.TenantInvoice, error) {
	return nil, errors.New("estado fiscal vía webhook del facturador; consulte tenant_invoices o panel fiscal")
}
