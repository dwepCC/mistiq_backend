package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V120BranchDailyComandaCounter agrega el contador atómico de "Pedido #N" por sucursal+día,
// reemplazando el cálculo anterior (MAX(order_number)+1 por sesión de mesa, que se reiniciaba
// cada vez que una mesa se cerraba y volvía a abrirse).
type V120BranchDailyComandaCounter struct{}

func (V120BranchDailyComandaCounter) Version() int { return 120 }
func (V120BranchDailyComandaCounter) Name() string  { return "branch_daily_comanda_counter" }

func (V120BranchDailyComandaCounter) Up(db *gorm.DB) error {
	return db.AutoMigrate(&database.TenantBranchDailyComandaCounter{})
}
