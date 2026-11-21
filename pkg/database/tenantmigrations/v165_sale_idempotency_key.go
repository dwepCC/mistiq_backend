package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v165Sale struct {
	ID             uint    `gorm:"primaryKey"`
	IdempotencyKey *string `gorm:"column:idempotency_key;size:64"`
}

func (v165Sale) TableName() string { return "tenant_sales" }

const idxSaleUserIdempotency = "uk_tenant_sales_user_idempotency"

// V165SaleIdempotencyKey agrega tenant_sales.idempotency_key y UNIQUE(user_id, idempotency_key).
//
// Causa (incidente SNJ, 04-oct-2026): el POS manda el cobro, el servidor lo guarda, pero la
// respuesta no llega al navegador (red lenta, timeout, WebView). La pantalla muestra un error,
// conserva el carrito y el cajero vuelve a cobrar: el backend no distingue ese reintento de una
// venta nueva y crea una segunda con otro correlativo (BB08-00046899 y BB08-00046900).
//
// El cliente genera una clave por intento de cobro y la reenvía igual en cada reintento; con el
// índice único el servidor devuelve la venta ya creada en vez de insertar otra.
//
// Puramente aditiva: columna nullable. MySQL permite varias filas con NULL en un índice único, así
// que las ventas existentes (y los flujos que no mandan clave) no se ven afectadas ni requieren
// backfill.
type V165SaleIdempotencyKey struct{}

func (V165SaleIdempotencyKey) Version() int { return 165 }
func (V165SaleIdempotencyKey) Name() string { return "sale_idempotency_key" }

func (V165SaleIdempotencyKey) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable("tenant_sales") {
		return nil
	}
	if !mig.HasColumn(&v165Sale{}, "IdempotencyKey") {
		if err := mig.AddColumn(&v165Sale{}, "IdempotencyKey"); err != nil {
			return fmt.Errorf("add tenant_sales.idempotency_key: %w", err)
		}
	}
	if !mig.HasIndex("tenant_sales", idxSaleUserIdempotency) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON tenant_sales (user_id, idempotency_key)`,
			idxSaleUserIdempotency,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxSaleUserIdempotency, err)
		}
	}
	return nil
}
