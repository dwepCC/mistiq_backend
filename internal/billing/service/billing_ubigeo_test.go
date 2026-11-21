package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func ubigeoTestService(t *testing.T) *BillingService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.UbiRegion{}, &database.UbiProvincia{}, &database.UbiDistrito{}); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedUbigeoRegionesProvincias(db); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertUbigeoDistritos(db); err != nil {
		t.Fatal(err)
	}
	return &BillingService{db: db}
}

// Regresión: el RUC 20614927781 tiene su domicilio fiscal en San Antonio (Mariscal Nieto, Moquegua, 180107, Ley 31216). Antes el catálogo
// no lo tenía y el emisor fallaba con «ubigeo … no encontrado».
func TestResolveUbigeoToAddress_distritoNuevoSanAntonio(t *testing.T) {
	s := ubigeoTestService(t)
	dep, prov, dist, err := s.resolveUbigeoToAddress("180107")
	if err != nil {
		t.Fatal(err)
	}
	if dep != "Moquegua" || prov != "Mariscal Nieto" || dist != "San Antonio" {
		t.Fatalf("got %q / %q / %q", dep, prov, dist)
	}
	if _, err := s.buildInvoiceAddressFromUbigeo("180107", "MZ. F LT. 21"); err != nil {
		t.Fatalf("la dirección del emisor debe armarse: %v", err)
	}
}

// Los distritos de Calca (Cusco) no se podían resolver porque faltaba la provincia.
func TestResolveUbigeoToAddress_calcaYDemasDistritosNuevos(t *testing.T) {
	s := ubigeoTestService(t)
	for _, ub := range []string{"080401", "080408", "030612", "130112", "250307"} {
		if _, _, _, err := s.resolveUbigeoToAddress(ub); err != nil {
			t.Errorf("%s: %v", ub, err)
		}
	}
}

// Un código que no existe sigue rechazándose, con un mensaje que dice cómo resolverlo.
func TestResolveUbigeoToAddress_inexistenteIndicaComoResolver(t *testing.T) {
	s := ubigeoTestService(t)
	_, _, _, err := s.resolveUbigeoToAddress("189999")
	if err == nil || !strings.Contains(err.Error(), "Agregar distrito") {
		t.Fatalf("err = %v, want mensaje con la opción «Agregar distrito»", err)
	}
}
