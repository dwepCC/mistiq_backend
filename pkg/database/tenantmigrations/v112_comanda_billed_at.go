package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v112Comanda struct {
	BilledAt *time.Time `gorm:"column:billed_at;index"`
}

func (v112Comanda) TableName() string { return "tenant_comandas" }

// V112ComandaBilledAt marcador de "ya facturada" independiente del status de cocina.
//
// Antes, BillTable reutilizaba comanda.status = 'entregada' para dos cosas a la vez: "servido en
// mesa" (cocina) y "ya incluido en un cobro parcial anterior" (facturación). Si el mozo marcaba un
// plato como entregado antes de cualquier cobro parcial, ese plato quedaba excluido de cobros
// futuros por error — y no había forma de facturar solo un subconjunto elegido a mano (dividir
// cuenta por comensal). billed_at desacopla ambos conceptos: status sigue siendo 100% de cocina,
// billed_at (NULL = pendiente de cobro) es lo único que BillTable consulta para decidir qué
// comandas ya se facturaron.
type V112ComandaBilledAt struct{}

func (V112ComandaBilledAt) Version() int { return 112 }
func (V112ComandaBilledAt) Name() string { return "comanda_billed_at" }

func (V112ComandaBilledAt) Up(db *gorm.DB) error {
	mig := db.Migrator()
	comanda := &v112Comanda{}

	if !mig.HasColumn(comanda, "BilledAt") {
		if err := mig.AddColumn(comanda, "BilledAt"); err != nil {
			return fmt.Errorf("add tenant_comandas.billed_at: %w", err)
		}
	}

	// Sin backfill a propósito: el frontend nunca ha usado el cobro parcial (close_session=false)
	// en producción — BillTable con cierre total (el único camino real hoy) BORRA las comandas al
	// facturar. Así que cualquier comanda 'entregada' que exista hoy en la BD es "servida, mesa
	// sigue abierta, aún no cobrada" — billed_at debe quedar NULL (pendiente), no se marca como
	// facturada. Si algún tenant llegó a usar el cobro parcial por API directa (nunca expuesto en
	// la UI), esas filas quedarán como pendientes tras esta migración y se re-facturarían en el
	// próximo cobro — el riesgo es cobrar de más, nunca perder venta, y es el único escenario
	// donde no hay forma de distinguir retroactivamente sin arriesgar lo contrario (dar por
	// cobrado algo que no lo está).
	return nil
}
