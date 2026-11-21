package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// V165 agrega la columna a una tabla tenant_sales previa, impone UNIQUE(user_id, idempotency_key)
// (dos ventas con la misma clave del mismo usuario no pueden coexistir) y es idempotente.
func TestV150SaleIdempotencyKey(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// tenant_sales como estaba antes de v165: sin idempotency_key.
	if err := db.Exec(`CREATE TABLE tenant_sales (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, number TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_sales (user_id, number) VALUES (1,'A-1'),(1,'A-2')`).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // dos pasadas: idempotente
		if err := (V165SaleIdempotencyKey{}).Up(db); err != nil {
			t.Fatalf("Up pasada %d: %v", i+1, err)
		}
	}
	if !db.Migrator().HasColumn(&v165Sale{}, "IdempotencyKey") {
		t.Fatal("falta la columna idempotency_key")
	}

	// Las ventas previas (NULL) no chocan entre sí.
	var nulls int64
	db.Raw(`SELECT COUNT(*) FROM tenant_sales WHERE idempotency_key IS NULL`).Scan(&nulls)
	if nulls != 2 {
		t.Fatalf("las 2 ventas previas deben quedar con NULL, hay %d", nulls)
	}

	if err := db.Exec(`INSERT INTO tenant_sales (user_id, number, idempotency_key) VALUES (1,'B-1','k1')`).Error; err != nil {
		t.Fatalf("primera venta con clave: %v", err)
	}
	if err := db.Exec(`INSERT INTO tenant_sales (user_id, number, idempotency_key) VALUES (1,'B-2','k1')`).Error; err == nil {
		t.Fatal("la misma clave del mismo usuario debe violar el índice único")
	}
	if err := db.Exec(`INSERT INTO tenant_sales (user_id, number, idempotency_key) VALUES (2,'B-3','k1')`).Error; err != nil {
		t.Fatalf("la misma clave de otro usuario debe permitirse: %v", err)
	}
}
