package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openUbigeoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&UbiRegion{}, &UbiProvincia{}, &UbiDistrito{}); err != nil {
		t.Fatal(err)
	}
	if err := SeedUbigeoRegionesProvincias(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// Un tenant con el catálogo viejo (sin San Antonio) recibe los distritos nuevos, y correrlo otra vez no duplica ni falla.
func TestUpsertUbigeoDistritos_agregaLosFaltantesYEsIdempotente(t *testing.T) {
	db := openUbigeoTestDB(t)
	old := []UbiDistrito{{ID: "180101", Nombre: "Moquegua", ProvinciaID: "180100", RegionID: "180000"}, {ID: "180105", Nombre: "San Cristóbal", ProvinciaID: "180100", RegionID: "180000"}}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	n, err := UpsertUbigeoDistritos(db)
	if err != nil || n == 0 {
		t.Fatalf("n=%d err=%v, want distritos agregados", n, err)
	}
	var sa UbiDistrito
	if err := db.First(&sa, "id = ?", "180107").Error; err != nil || sa.Nombre != "San Antonio" || sa.ProvinciaID != "180100" || sa.RegionID != "180000" {
		t.Fatalf("San Antonio (180107) no quedó bien: %+v err=%v", sa, err)
	}
	var kept UbiDistrito
	db.First(&kept, "id = ?", "180105")
	if kept.Nombre != "San Cristóbal" {
		t.Fatalf("un distrito existente no debe modificarse: %+v", kept)
	}
	if again, err := UpsertUbigeoDistritos(db); err != nil || again != 0 {
		t.Fatalf("segunda corrida: n=%d err=%v, want 0", again, err)
	}
}

// Todo distrito del dataset debe colgar de una provincia y un departamento existentes (si no, no se podría elegir en cascada).
func TestUbigeoDataset_cadaDistritoTieneProvinciaYRegion(t *testing.T) {
	db := openUbigeoTestDB(t)
	if err := UpsertUbigeoRegionesProvincias(db); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertUbigeoDistritos(db); err != nil {
		t.Fatal(err)
	}
	var huerfanos int64
	db.Raw(`SELECT COUNT(*) FROM ubi_distritos d LEFT JOIN ubi_provincias p ON p.id=d.provincia_id LEFT JOIN ubi_regiones r ON r.id=d.region_id WHERE p.id IS NULL OR r.id IS NULL`).Scan(&huerfanos)
	if huerfanos != 0 {
		t.Fatalf("%d distritos sin provincia/departamento", huerfanos)
	}
	var total int64
	db.Model(&UbiDistrito{}).Count(&total)
	if total < 1891 {
		t.Fatalf("catálogo con %d distritos, want al menos 1891 (INEI 2025)", total)
	}
}

func TestAddUbigeoDistrito(t *testing.T) {
	db := openUbigeoTestDB(t)
	d, created, err := AddUbigeoDistrito(db, "180199", "  Distrito   Nuevo ")
	if err != nil || !created || d.Nombre != "Distrito Nuevo" || d.ProvinciaID != "180100" || d.RegionID != "180000" {
		t.Fatalf("alta válida: %+v created=%v err=%v", d, created, err)
	}
	if _, created, err := AddUbigeoDistrito(db, "180199", "Otro nombre"); err != nil || created {
		t.Fatalf("repetido: created=%v err=%v, want existente sin cambiar", created, err)
	}
	var got UbiDistrito
	db.First(&got, "id = ?", "180199")
	if got.Nombre != "Distrito Nuevo" {
		t.Fatalf("no debe pisar el nombre del existente: %q", got.Nombre)
	}
	for _, bad := range []string{"1801", "18010a", "", "991199"} {
		if _, _, err := AddUbigeoDistrito(db, bad, "Algo válido"); err == nil {
			t.Errorf("id %q debió rechazarse", bad)
		}
	}
	if _, _, err := AddUbigeoDistrito(db, "180198", "ab"); err == nil {
		t.Error("nombre de 2 letras debió rechazarse")
	}
}
