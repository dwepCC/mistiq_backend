package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v108TransferLog struct {
	ID             uint  `gorm:"primaryKey"`
	PresentationID *uint `gorm:"column:presentation_id;index"`
}

func (v108TransferLog) TableName() string { return "tenant_transfer_logs" }

type v108StockMovement struct {
	ID         uint  `gorm:"primaryKey"`
	TransferID *uint `gorm:"column:transfer_id;index"`
	SaleItemID *uint `gorm:"column:sale_item_id;index"`
}

func (v108StockMovement) TableName() string { return "tenant_stock_movements" }

// V108TransferKardexLinks agrega: (1) presentation_id a las líneas de transferencia, para que
// transferir "5 rojo" mueva el stock de la variante correcta entre sucursales; (2) transfer_id y
// sale_item_id al kardex, como enlace directo (sin inferir por fecha/referencia) para poder
// mostrar qué números de serie participaron en cada movimiento.
type V108TransferKardexLinks struct{}

func (V108TransferKardexLinks) Version() int { return 108 }
func (V108TransferKardexLinks) Name() string { return "transfer_kardex_links" }

func (V108TransferKardexLinks) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if mig.HasTable(&v108TransferLog{}) && !mig.HasColumn(&v108TransferLog{}, "PresentationID") {
		if err := mig.AddColumn(&v108TransferLog{}, "PresentationID"); err != nil {
			return fmt.Errorf("add tenant_transfer_logs.presentation_id: %w", err)
		}
	}
	if mig.HasTable(&v108StockMovement{}) {
		if !mig.HasColumn(&v108StockMovement{}, "TransferID") {
			if err := mig.AddColumn(&v108StockMovement{}, "TransferID"); err != nil {
				return fmt.Errorf("add tenant_stock_movements.transfer_id: %w", err)
			}
		}
		if !mig.HasColumn(&v108StockMovement{}, "SaleItemID") {
			if err := mig.AddColumn(&v108StockMovement{}, "SaleItemID"); err != nil {
				return fmt.Errorf("add tenant_stock_movements.sale_item_id: %w", err)
			}
		}
	}
	return nil
}
