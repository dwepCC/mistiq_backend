package tenantmigrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// La v178 solo agrega columnas NULL: una fila anterior (solo texto libre) sobrevive intacta y la
// migración es idempotente.
func TestV163_AgregaColumnasSinTocarFilasExistentes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(0)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_sale_detraccion (sale_id integer primary key, punto_origen text, punto_destino text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenant_sale_detraccion (sale_id, punto_origen, punto_destino) VALUES (2, 'CHAMACA', 'LIMA')`).Error; err != nil {
		t.Fatal(err)
	}

	mig := V178DetraccionTransportUbigeo{}
	for i := 0; i < 2; i++ { // 2ª pasada: idempotente
		if err := mig.Up(db); err != nil {
			t.Fatalf("Up #%d: %v", i+1, err)
		}
	}
	for _, col := range []string{"origin_ubigeo", "origin_address", "destination_ubigeo", "destination_address", "valor_ref_carga_efectiva_pen", "valor_ref_carga_util_pen"} {
		if !db.Migrator().HasColumn("tenant_sale_detraccion", col) {
			t.Errorf("falta la columna %s", col)
		}
	}
	var origin *string
	var ubigeo *string
	if err := db.Raw(`SELECT punto_origen, origin_ubigeo FROM tenant_sale_detraccion WHERE sale_id = 2`).Row().Scan(&origin, &ubigeo); err != nil {
		t.Fatal(err)
	}
	if origin == nil || *origin != "CHAMACA" || ubigeo != nil {
		t.Errorf("fila previa alterada: origen=%v ubigeo=%v", origin, ubigeo)
	}
	if mig.Version() != 178 {
		t.Errorf("version = %d", mig.Version())
	}
}
