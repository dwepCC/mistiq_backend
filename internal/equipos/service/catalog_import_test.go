package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

func TestImportCatalog_previewCommitIdempotent(t *testing.T) {
	s := New(setupEquiposDB(t))
	mustProduct(t, s, "TK-OLD", 2, 5)
	if _, err := s.CreateCombo(ComboInput{Code: "COMBO1", Items: []ComboItemInput{{ProductID: 1, Quantity: 1}}}); err != nil {
		t.Fatal(err)
	}
	rows := []CatalogImportRow{
		{Row: 2, Code: "NEW-1", Name: "Impresora", Kind: "Equipo", ReferencePrice: "350,50", YellowThreshold: "2", GreenThreshold: "5"},
		{Row: 3, Code: "tk-old", Name: "Renombrado", Notes: "n"},
		{Row: 4, Code: "NEW-1", Name: "Dup"},
		{Row: 5, Code: "", Name: "Sin código"},
		{Row: 6, Code: "X1", Name: ""},
		{Row: 7, Code: "X2", Name: "a", Kind: "cosa"},
		{Row: 8, Code: "X3", Name: "a", YellowThreshold: "9", GreenThreshold: "3"},
		{Row: 9, Code: "X4", Name: "a", ReferencePrice: "-1"},
		{Row: 10, Code: "X5", Name: "a", GreenThreshold: "abc"},
		{Row: 11, Code: "COMBO1", Name: "a"},
		{Row: 12},
	}
	pv, err := s.ImportCatalog(rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Created != 1 || pv.Updated != 1 || pv.Errors != 8 || pv.Total != 10 {
		t.Fatalf("preview: %+v", pv)
	}
	var n int64
	s.db.Model(&database.EquipProduct{}).Count(&n)
	if n != 1 {
		t.Fatalf("la vista previa no debe escribir: %d productos", n)
	}
	for _, it := range pv.Items {
		if it.Row == 4 && !strings.Contains(it.Error, "fila 2") {
			t.Errorf("duplicado: %q", it.Error)
		}
	}
	if _, err := s.ImportCatalog(rows, true); err != nil {
		t.Fatal(err)
	}
	var p database.EquipProduct
	s.db.Where("code = ?", "NEW-1").First(&p)
	if p.ReferencePrice != 350.5 || p.GreenThreshold != 5 || !p.Active || p.Kind != database.EquipKindEquipo {
		t.Fatalf("nuevo: %+v", p)
	}
	var old database.EquipProduct
	s.db.Where("code = ?", "TK-OLD").First(&old)
	if old.Name != "Renombrado" || old.Notes != "n" || old.YellowThreshold != 2 || old.GreenThreshold != 5 {
		t.Fatalf("existente (celdas vacías conservan): %+v", old)
	}
	again, err := s.ImportCatalog(rows, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created != 0 || again.Updated != 0 || again.Unchanged != 2 {
		t.Fatalf("reimportar no debe duplicar: %+v", again)
	}
	s.db.Model(&database.EquipProduct{}).Count(&n)
	if n != 2 {
		t.Fatalf("productos: %d", n)
	}
}

func TestImportCatalog_emptyAndInactive(t *testing.T) {
	s := New(setupEquiposDB(t))
	if _, err := s.ImportCatalog(nil, false); err == nil || !IsValidation(err) {
		t.Errorf("vacío: %v", err)
	}
	if _, err := s.ImportCatalog([]CatalogImportRow{{Row: 2, Code: "A", Name: "A", Active: "NO"}, {Row: 3, Code: "B", Name: "B", Active: "tal vez"}}, true); err != nil {
		t.Fatal(err)
	}
	var a database.EquipProduct
	s.db.Where("code = ?", "A").First(&a)
	if a.Active {
		t.Error("A debía quedar inactivo")
	}
	var n int64
	s.db.Model(&database.EquipProduct{}).Where("code = ?", "B").Count(&n)
	if n != 0 {
		t.Error("B con «activo» inválido no se importa")
	}
}
