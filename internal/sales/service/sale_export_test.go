package service

import (
	"strings"
	"testing"

	"tukifac/internal/exportacion"
	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"

	"gorm.io/gorm"
)

func setupExportTestDB(t *testing.T, enabled bool) (*gorm.DB, uint, uint) {
	t.Helper()
	db := setupMaxMontoTestDB(t)
	if err := db.AutoMigrate(&database.TenantSaleExport{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TenantCompanyConfig{}).Where("id = 1").Update("exports_enabled", enabled).Error; err != nil {
		t.Fatal(err)
	}
	return db, seedClienteSinRUC(t, db), seedSeries(t, db, "Factura", "01", "F001")
}

func exportInput(contactID, seriesID uint, op string, ex *exportacion.SaleInput) CreateSaleInput {
	in := facturaOperationTypeInput(contactID, seriesID, op, 118)
	in.Export = ex
	return in
}

// Sin el interruptor de la empresa no se puede exportar.
func TestExport_apagadoPorDefecto(t *testing.T) {
	db, c, s := setupExportTestDB(t, false)
	_, err := NewSaleService(db).Create(exportInput(c, s, salecurrency.OpExportServicios, &exportacion.SaleInput{CountryUse: "US"}))
	if err == nil || !strings.Contains(err.Error(), "habilitada") {
		t.Fatalf("err = %v, want mensaje de exportación no habilitada", err)
	}
}

// 0201: factura a un cliente sin RUC, país de uso obligatorio; las líneas salen sin IGV aunque el cajero las haya dejado gravadas.
func TestExport_0201SinIGVYConDatos(t *testing.T) {
	db, c, s := setupExportTestDB(t, true)
	svc := NewSaleService(db)
	if _, err := svc.Create(exportInput(c, s, salecurrency.OpExportServicios, &exportacion.SaleInput{})); err != nil {
		t.Fatalf("0201 sin país de uso debe permitirse (es opcional): %v", err)
	}
	if _, err := svc.Create(exportInput(c, s, salecurrency.OpExportServicios, &exportacion.SaleInput{CountryUse: "PE"})); err == nil {
		t.Fatal("0201 con país Perú debió rechazarse")
	}
	sale, err := svc.Create(exportInput(c, s, salecurrency.OpExportServicios, &exportacion.SaleInput{CountryUse: "us"}))
	if err != nil {
		t.Fatal(err)
	}
	if sale.TaxAmount != 0 || sale.Total != 118 || sale.Subtotal != 118 {
		t.Fatalf("exportación sin IGV: subtotal=%v igv=%v total=%v, want 118/0/118", sale.Subtotal, sale.TaxAmount, sale.Total)
	}
	var it database.TenantSaleItem
	db.Where("sale_id = ?", sale.ID).First(&it)
	if it.IgvAffectationType != "40" {
		t.Fatalf("afectación = %q, want 40", it.IgvAffectationType)
	}
	row, _ := exportacion.LoadBySaleID(db, sale.ID)
	if row == nil || row.CountryUse != "US" || row.OperationTypeCode != "0201" {
		t.Fatalf("datos de exportación no guardados: %+v", row)
	}
}

// 0202: huésped completo; más de 60 días o residente en el Perú no califican.
func TestExport_0202Hospedaje(t *testing.T) {
	db, c, s := setupExportTestDB(t, true)
	svc := NewSaleService(db)
	ok := exportacion.SaleInput{
		GuestName: "John Smith", GuestDocNumber: "X1234567", PassportCountry: "US", ResidenceCountry: "US",
		EntryDate: "2026-11-01", CheckIn: "2026-11-02", CheckOut: "2026-11-05",
	}
	sale, err := svc.Create(exportInput(c, s, salecurrency.OpExportHospedaje, &ok))
	if err != nil {
		t.Fatal(err)
	}
	row, _ := exportacion.LoadBySaleID(db, sale.ID)
	if row == nil || row.StayDays != 3 || row.GuestDocType != "7" {
		t.Fatalf("estadía mal guardada: %+v", row)
	}
	long := ok
	long.CheckOut = "2027-02-01" // > 60 días desde el ingreso al país
	if _, err := svc.Create(exportInput(c, s, salecurrency.OpExportHospedaje, &long)); err == nil || !strings.Contains(err.Error(), "60") {
		t.Fatalf("más de 60 días debió rechazarse con el motivo: %v", err)
	}
	resident := ok
	resident.ResidenceCountry = "PE"
	if _, err := svc.Create(exportInput(c, s, salecurrency.OpExportHospedaje, &resident)); err == nil {
		t.Fatal("huésped residente en el Perú debió rechazarse")
	}
}

// La exportación también se puede emitir en boleta, pero no con otros tipos de documento, ni con datos de exportación en una venta normal.
func TestExport_soloFacturaOBoleta_yNoEnVentaNormal(t *testing.T) {
	db, c, s := setupExportTestDB(t, true)
	svc := NewSaleService(db)
	nv := seedSeries(t, db, "Nota de venta", "00", "NV01")
	if _, err := svc.Create(exportInput(c, nv, salecurrency.OpExportServicios, &exportacion.SaleInput{CountryUse: "US"})); err == nil {
		t.Fatal("exportación en nota de venta debió rechazarse")
	}
	if _, err := svc.Create(exportInput(c, s, salecurrency.OpVentaInterna, &exportacion.SaleInput{CountryUse: "US"})); err == nil {
		t.Fatal("datos de exportación en una venta interna debieron rechazarse")
	}
	// SUNAT (cat. 51): el hospedaje 0202 no admite boleta (error 3206); 0201 sí.
	bol := seedSeries(t, db, "Boleta", "03", "B001")
	hosp := exportacion.SaleInput{
		GuestName: "John Smith", GuestDocNumber: "X1234567", PassportCountry: "US", ResidenceCountry: "US",
		EntryDate: "2026-11-01", CheckIn: "2026-11-02", CheckOut: "2026-11-05",
	}
	in0202 := exportInput(c, bol, salecurrency.OpExportHospedaje, &hosp)
	in0202.DocType = "BOLETA"
	if _, err := svc.Create(in0202); err == nil || !strings.Contains(err.Error(), "solo se emite en factura") {
		t.Fatalf("0202 en boleta debió rechazarse: %v", err)
	}
	in0201 := exportInput(c, bol, salecurrency.OpExportServicios, &exportacion.SaleInput{CountryUse: "US"})
	in0201.DocType = "BOLETA"
	if _, err := svc.Create(in0201); err != nil {
		t.Fatalf("0201 en boleta debe permitirse: %v", err)
	}
}

// El cliente de la venta ES el huésped: su nombre y su pasaporte salen del contacto, sin volver a escribirlos. Con un cliente que no tiene
// pasaporte o carné (RUC, DNI, «Clientes varios») no se puede emitir el hospedaje.
func TestExport_0202TomaAlHuespedDelCliente(t *testing.T) {
	db, varios, f := setupExportTestDB(t, true)
	svc := NewSaleService(db)
	guest := database.TenantContact{DocType: "7", DocNumber: "AB123456", BusinessName: "  Ann   Lee  ", Type: "customer"}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatal(err)
	}
	dates := exportacion.SaleInput{PassportCountry: "us", EntryDate: "2026-11-01", CheckIn: "2026-11-02", CheckOut: "2026-11-05"}

	ok := exportInput(guest.ID, f, salecurrency.OpExportHospedaje, &dates)
	sale, err := svc.Create(ok)
	if err != nil {
		t.Fatalf("hospedaje con cliente pasaporte: %v", err)
	}
	row, _ := exportacion.LoadBySaleID(db, sale.ID)
	if row == nil || row.GuestName != "Ann Lee" || row.GuestDocType != "7" || row.GuestDocNumber != "AB123456" || row.PassportCountry != "US" || row.ResidenceCountry != "US" {
		t.Fatalf("el huésped debe salir del cliente y la residencia igualar al pasaporte: %+v", row)
	}

	if _, err := svc.Create(exportInput(varios, f, salecurrency.OpExportHospedaje, &dates)); err == nil || !strings.Contains(err.Error(), "pasaporte o carné") {
		t.Fatalf("«Clientes varios» no sirve de huésped: %v", err)
	}
	other := dates
	other.ResidenceCountry = "ca"
	sale2, err := svc.Create(exportInput(guest.ID, f, salecurrency.OpExportHospedaje, &other))
	if err != nil {
		t.Fatal(err)
	}
	if r2, _ := exportacion.LoadBySaleID(db, sale2.ID); r2 == nil || r2.ResidenceCountry != "CA" {
		t.Fatalf("residencia distinta al pasaporte: %+v", r2)
	}
}
