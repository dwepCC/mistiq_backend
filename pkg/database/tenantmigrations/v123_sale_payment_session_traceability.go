package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v123SalePayment struct {
	ID            uint  `gorm:"primaryKey"`
	CashSessionID *uint `gorm:"column:cash_session_id;index"`
}

func (v123SalePayment) TableName() string { return "tenant_sale_payments" }

// V123SalePaymentSessionTraceability agrega cash_session_id (nullable) a tenant_sale_payments —
// mismo patrón que V122FinancialSessionTraceability (una columna aditiva).
//
// Auditoría previa (checkpoint de pre-producción): tenant_sales.cash_session_id se estaba usando
// para representar DOS cosas distintas — dónde se registró la venta y dónde ocurrió el último
// cobro — porque ReceivableService.Collect sobrescribía ese campo en cada pago posterior de una
// venta a crédito. Eso destruía la sesión de registro original cada vez que se cobraba una cuota
// en una Caja distinta.
//
// Esta columna separa ambos conceptos sin tocar tenant_sales:
//   - tenant_sales.cash_session_id: sesión donde se REGISTRÓ el documento (venta al contado, a
//     crédito, con o sin adelanto) — inmutable desde esta fase en adelante.
//   - tenant_sale_payments.cash_session_id: sesión donde OCURRIÓ ese pago específico (el primero,
//     al crear la venta, coincide con la de la venta; uno posterior de una cuota puede diferir).
//
// Aditiva y compatible: columna nullable, sin DROP, sin tocar filas existentes (quedan NULL). No
// se ejecuta ningún backfill automático de datos históricos en esta migración — la auditoría
// determinó que no todos los pagos existentes pueden atribuirse a una sesión de forma inequívoca
// (varios pagos del mismo método/monto para una misma venta no se pueden distinguir sin más
// contexto); inventar esa asociación sería peor que dejarla en NULL. Un backfill best-effort,
// acotado a los casos SÍ inequívocos, se documenta por separado para ejecutarse deliberadamente
// más adelante (fuera de esta migración de esquema).
type V123SalePaymentSessionTraceability struct{}

func (V123SalePaymentSessionTraceability) Version() int { return 123 }
func (V123SalePaymentSessionTraceability) Name() string {
	return "sale_payment_session_traceability"
}

func (V123SalePaymentSessionTraceability) Up(db *gorm.DB) error {
	mig := db.Migrator()

	payment := &v123SalePayment{}
	if mig.HasTable(payment) && !mig.HasColumn(payment, "CashSessionID") {
		if err := mig.AddColumn(payment, "CashSessionID"); err != nil {
			return fmt.Errorf("add tenant_sale_payments.cash_session_id: %w", err)
		}
	}

	return nil
}
