package service

import (
	"os"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func setupAgenciesDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupEquiposDB(t)
	if err := db.AutoMigrate(&database.EquipCarrierAgency{}, &database.UbiRegion{}, &database.UbiProvincia{}, &database.UbiDistrito{}); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedUbigeoRegionesProvincias(db); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedUbigeoDistritos(db, ""); err != nil {
		t.Fatal(err)
	}
	return db
}

func shalomRows(t *testing.T) []AgencyImportRow {
	t.Helper()
	raw, err := os.ReadFile("testdata/shalom_agencias.csv")
	if err != nil {
		t.Fatal(err)
	}
	var rows []AgencyImportRow
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		code, name, _ := strings.Cut(strings.TrimSpace(line), ";")
		rows = append(rows, AgencyImportRow{Code: code, Name: name})
	}
	return rows
}

func TestGuessKnownAgencies(t *testing.T) {
	db := setupAgenciesDB(t)
	ix, err := loadUbiIndex(db)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, dep, dist, status string
	}{
		{"ABANCAY", "Apurímac", "Abancay", database.AgencyLocConfirmed},
		{"JAEN CO", "Cajamarca", "Jaén", database.AgencyLocConfirmed},
		{"MIRAFLORES AREQUIPA", "Arequipa", "Miraflores", database.AgencyLocConfirmed},
		{"CUSCO CALCA", "Cusco", "Calca", ""},
		{"AV LIMA", "Lima", "", database.AgencyLocSuggested}, // una calle: nunca «confirmada»
	}
	for _, c := range cases {
		g := ix.Guess(c.name)
		if !strings.EqualFold(foldText(g.Department), foldText(c.dep)) {
			t.Errorf("%q: departamento %q, se esperaba %q", c.name, g.Department, c.dep)
		}
		if c.dist != "" && foldText(g.District) != foldText(c.dist) {
			t.Errorf("%q: distrito %q, se esperaba %q", c.name, g.District, c.dist)
		}
		if c.status != "" && g.Status != c.status {
			t.Errorf("%q: estado %q, se esperaba %q", c.name, g.Status, c.status)
		}
	}
}

func TestImportShalomAgencies(t *testing.T) {
	db := setupAgenciesDB(t)
	s := New(db)
	c := database.EquipCarrier{Code: "shalom", Name: "Shalom", Active: true}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	rows := shalomRows(t)

	pv, err := s.ImportAgencies(c.ID, rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Committed || pv.Total != 509 || pv.Created != 509 {
		t.Fatalf("vista previa inesperada: %+v", pv)
	}
	var n int64
	db.Model(&database.EquipCarrierAgency{}).Count(&n)
	if n != 0 {
		t.Fatalf("la vista previa no debe guardar nada (hay %d)", n)
	}
	t.Logf("confirmadas=%d sugeridas=%d pendientes=%d", pv.Confirmed, pv.Suggested, pv.Pending)
	if pv.Confirmed+pv.Suggested < 250 {
		t.Errorf("el cruce ubicó muy pocas agencias: %d de 509", pv.Confirmed+pv.Suggested)
	}

	res, err := s.ImportAgencies(c.ID, rows, true)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&database.EquipCarrierAgency{}).Count(&n)
	if !res.Committed || n != 509 {
		t.Fatalf("se esperaban 509 agencias guardadas, hay %d", n)
	}
	// Reimportar no duplica ni cambia nada.
	again, err := s.ImportAgencies(c.ID, rows, true)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&database.EquipCarrierAgency{}).Count(&n)
	if n != 509 || again.Created != 0 || again.Updated != 0 || again.Unchanged != 509 {
		t.Fatalf("reimportar debe ser idempotente: n=%d %+v", n, again)
	}
	// Lo confirmado a mano se respeta aunque se reimporte.
	var a database.EquipCarrierAgency
	db.Where("carrier_id = ? AND name = ?", c.ID, "AV BOLIVAR").First(&a)
	if _, err := s.UpdateAgency(a.ID, AgencyInput{Code: a.Code, Name: a.Name, Department: "Piura", Province: "Piura", District: "Piura"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportAgencies(c.ID, rows, true); err != nil {
		t.Fatal(err)
	}
	db.First(&a, a.ID)
	if a.Department != "Piura" || a.LocationStatus != database.AgencyLocConfirmed {
		t.Errorf("se pisó una ubicación confirmada a mano: %+v", a)
	}
}

func TestAgencyCRUDAndShipment(t *testing.T) {
	db := setupAgenciesDB(t)
	s := New(db)
	c := database.EquipCarrier{Code: "shalom", Name: "Shalom", Active: true}
	o := database.EquipCarrier{Code: "olva", Name: "Olva", Active: true}
	db.Create(&c)
	db.Create(&o)

	origin, err := s.CreateAgency(c.ID, AgencyInput{Code: "1", Name: "Zamacola"})
	if err != nil {
		t.Fatal(err)
	}
	if origin.LocationStatus != database.AgencyLocPending {
		t.Errorf("sin departamento debe quedar pendiente, quedó %q", origin.LocationStatus)
	}
	dest, err := s.CreateAgency(c.ID, AgencyInput{Name: "Jaén", Department: "cajamarca", Province: "Jaén", District: "Jaén", Ubigeo: "060801", Address: "Jr. Bolívar 100"})
	if err != nil {
		t.Fatal(err)
	}
	if dest.Department != "Cajamarca" || dest.LocationStatus != database.AgencyLocConfirmed {
		t.Errorf("agencia con ubicación mal guardada: %+v", dest)
	}
	if _, err := s.CreateAgency(c.ID, AgencyInput{Name: "ZAMACOLA"}); err == nil {
		t.Error("se esperaba error por nombre repetido en el mismo transportista")
	}
	if _, err := s.CreateAgency(o.ID, AgencyInput{Name: "Zamacola"}); err != nil {
		t.Errorf("otro transportista sí puede tener una agencia con el mismo nombre: %v", err)
	}

	// Origen por defecto: debe ser del mismo transportista.
	if err := s.checkOriginAgency(db, o.ID, &origin.ID); err == nil {
		t.Error("el origen de otro transportista debe rechazarse")
	}
	if err := s.checkOriginAgency(db, c.ID, &origin.ID); err != nil {
		t.Error(err)
	}

	// El envío toma el origen por defecto y copia la ubicación de la agencia de destino.
	c.DefaultOriginAgencyID = &origin.ID
	db.Save(&c)
	sh := database.EquipShipment{CarrierID: &c.ID}
	if err := s.applyShipmentInput(db, &sh, &ShipmentInput{CarrierID: &c.ID, DestinationAgencyID: &dest.ID}); err != nil {
		t.Fatal(err)
	}
	if sh.OriginAgencyID == nil || *sh.OriginAgencyID != origin.ID {
		t.Errorf("debió proponerse el origen por defecto: %v", sh.OriginAgencyID)
	}
	if sh.DestinationDepartment != "Cajamarca" || sh.DestinationDistrict != "Jaén" || sh.Ubigeo != "060801" || !strings.Contains(sh.DestinationAgency, "Jr. Bolívar 100") {
		t.Errorf("destino mal copiado: %+v", sh)
	}
	// Una agencia de otro transportista se rechaza.
	bad := database.EquipShipment{CarrierID: &o.ID}
	if err := s.applyShipmentInput(db, &bad, &ShipmentInput{CarrierID: &o.ID, DestinationAgencyID: &dest.ID}); err == nil {
		t.Error("la agencia de destino de otro transportista debe rechazarse")
	}
}
