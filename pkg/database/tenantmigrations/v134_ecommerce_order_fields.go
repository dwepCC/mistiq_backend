package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// v134EcommerceOrder espejo local de TenantEcommerceOrder solo con las columnas nuevas — mismo
// patrón que v127EcommerceSettings: migrator.AddColumn lee el struct tag, no toca el resto.
type v134EcommerceOrder struct {
	ID                uint    `gorm:"primaryKey"`
	CustomerAccountID *uint   `gorm:"index"`
	ContactID         *uint   `gorm:"index"`
	BranchID          *uint   `gorm:"index"`
	Subtotal          float64 `gorm:"type:decimal(15,2);default:0"`
	DeliveryMethod    string  `gorm:"size:30"`
	DeliveryAddressID *uint   `gorm:"index"`
	GuestAddressLine  string  `gorm:"size:255"`
	GuestReference    string  `gorm:"size:255"`
	GuestUbigeo       string  `gorm:"size:6"`
	PaymentStatus     string  `gorm:"size:20;default:'NO_APLICA'"`
}

func (v134EcommerceOrder) TableName() string { return "tenant_ecommerce_orders" }

// V134EcommerceOrderFields agrega a tenant_ecommerce_orders las columnas del Contrato ecommerce v2
// (docs/ECOMMERCE-EVOLUTION-CONTRACT.md §1.1/§2, Fase 1): sucursal de preparación, cuenta/contacto
// del cliente, dirección de entrega (FK o snapshot de invitado), subtotal y payment_status.
// Ninguna es NOT NULL sin default: pedidos existentes quedan con valores neutros (nil/0/cadena
// vacía/NO_APLICA), sin backfill destructivo. payment_status queda fijo en NO_APLICA para todo
// pedido existente y nuevo mientras no exista pasarela de pago — no es un campo editable por el
// cliente público, es solo el marcador arquitectónico aprobado para una fase futura.
type V134EcommerceOrderFields struct{}

func (V134EcommerceOrderFields) Version() int { return 134 }
func (V134EcommerceOrderFields) Name() string { return "ecommerce_order_fields" }

func (V134EcommerceOrderFields) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v134EcommerceOrder{}) {
		return nil // tenant sin tabla de ecommerce todavía (provisioning en curso)
	}
	row := &v134EcommerceOrder{}
	columns := []string{
		"CustomerAccountID", "ContactID", "BranchID", "Subtotal", "DeliveryMethod",
		"DeliveryAddressID", "GuestAddressLine", "GuestReference", "GuestUbigeo", "PaymentStatus",
	}
	for _, col := range columns {
		if !mig.HasColumn(row, col) {
			if err := mig.AddColumn(row, col); err != nil {
				return fmt.Errorf("add tenant_ecommerce_orders.%s: %w", col, err)
			}
		}
	}
	// Resguardo: subtotal debe reflejar el total ya cobrado en pedidos existentes (no dejarlo en
	// 0 mientras total>0, que confundiría cualquier reporte futuro que sume subtotal).
	if err := db.Table("tenant_ecommerce_orders").
		Where("subtotal = ? OR subtotal IS NULL", 0).
		Update("subtotal", gorm.Expr("total")).Error; err != nil {
		return fmt.Errorf("v134 backfill subtotal: %w", err)
	}
	// Resguardo: mismo motivo que v127 — AddColumn con default ya debería dejarlo, esto cubre
	// drivers que lo dejan NULL.
	if err := db.Table("tenant_ecommerce_orders").
		Where("payment_status IS NULL OR payment_status = ?", "").
		Update("payment_status", "NO_APLICA").Error; err != nil {
		return fmt.Errorf("v134 backfill payment_status: %w", err)
	}
	return nil
}
