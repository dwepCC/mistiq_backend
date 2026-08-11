package tenantbackfills

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Modelos mínimos para el test (evita depender del paquete database completo).
type tos struct {
	ID        uint `gorm:"primaryKey"`
	SessionID uint
	Status    string
	UpdatedAt string // la tabla real tiene updated_at; el backfill lo toca
}

func (tos) TableName() string { return "tenant_table_orders" }

type tss struct {
	ID     uint `gorm:"primaryKey"`
	Status string
}

func (tss) TableName() string { return "tenant_table_sessions" }

func setupBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	// DSN único por test: cache=shared compartiría la misma BD entre tests y colisionaría.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&tos{}, &tss{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV033_finalizesActiveOrdersOnTerminatedSessions(t *testing.T) {
	db := setupBackfillDB(t)
	// Sesiones: 1=billed (terminada), 2=closed (terminada), 3=open (en curso).
	db.Create(&tss{ID: 1, Status: "billed"})
	db.Create(&tss{ID: 2, Status: "closed"})
	db.Create(&tss{ID: 3, Status: "open"})
	db.Create(&tos{ID: 10, SessionID: 1, Status: "active"}) // huérfano → cerrar
	db.Create(&tos{ID: 11, SessionID: 2, Status: "active"}) // huérfano → cerrar
	db.Create(&tos{ID: 12, SessionID: 3, Status: "active"}) // en curso → intacto
	db.Create(&tos{ID: 13, SessionID: 1, Status: "cancelled"})

	if err := (V033FinalizeOrphanTableOrders{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	get := func(id uint) string {
		var o tos
		db.First(&o, id)
		return o.Status
	}
	if get(10) != "closed" || get(11) != "closed" {
		t.Fatalf("pedidos de sesiones terminadas deben quedar 'closed': 10=%s 11=%s", get(10), get(11))
	}
	if get(12) != "active" {
		t.Fatalf("el pedido de la sesión abierta NO debe tocarse: 12=%s", get(12))
	}
	if get(13) != "cancelled" {
		t.Fatalf("un pedido cancelado no cambia: 13=%s", get(13))
	}
}

// Idempotente: correrlo dos veces no cambia nada la segunda vez.
func TestV033_idempotent(t *testing.T) {
	db := setupBackfillDB(t)
	db.Create(&tss{ID: 1, Status: "billed"})
	db.Create(&tos{ID: 10, SessionID: 1, Status: "active"})

	bf := V033FinalizeOrphanTableOrders{}
	if err := bf.Run(db); err != nil {
		t.Fatal(err)
	}
	var affected int64
	db.Model(&tos{}).Where("status = ?", "active").Count(&affected)
	if affected != 0 {
		t.Fatalf("tras la 1ra corrida no debe quedar ningún 'active': %d", affected)
	}
	if err := bf.Run(db); err != nil {
		t.Fatalf("segunda corrida no debe fallar: %v", err)
	}
}
