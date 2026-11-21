package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"tukifac/pkg/database"
)

func setupPrintDataTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// Bug reportado (tenant RUC 20548414424, F001-86): en el PDF de una factura a crédito, la
// cabecera mostraba "Fecha Vencimiento: 2026-08-31" (igual a la fecha de emisión) mientras la
// tabla de CUOTAS, más abajo, mostraba la fecha real de la cuota (2026-09-20) — misma venta,
// dos fechas distintas. Causa: PrintData.ValidUntil nunca se asignaba en BuildPrintData (solo
// lo llena quotations/service/print_data.go para cotizaciones); el frontend
// (receiptPdfA4.ts resolveDueDate) cae a issue_date cuando valid_until viene vacío.
func TestBuildPrintData_validUntilFromDueDate(t *testing.T) {
	db := setupPrintDataTestDB(t)
	due := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	sale := &database.TenantSale{
		ID:                   339,
		DocType:              "01",
		Series:               "F001",
		Number:               "F001-00000086",
		IssueDate:            time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
		DueDate:              &due,
		Currency:             "PEN",
		Total:                5331.24,
		PaymentConditionCode: "credit",
		Status:               "credit",
	}

	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.ValidUntil != "20/09/2026" {
		t.Fatalf("valid_until: got %q, want %q (misma fecha que la cuota, no la de emisión)", pd.ValidUntil, "20/09/2026")
	}
	if pd.ValidUntil == pd.IssueDate {
		t.Fatalf("valid_until no debe coincidir con issue_date en una venta a crédito: %q", pd.ValidUntil)
	}
}

// Venta al contado (o sin fecha de vencimiento): sale.DueDate es nil, valid_until debe quedar
// vacío — el frontend cae a issue_date, que es el comportamiento correcto en ese caso.
func TestBuildPrintData_validUntilEmptyWhenNoDueDate(t *testing.T) {
	db := setupPrintDataTestDB(t)
	sale := &database.TenantSale{
		ID:        1,
		DocType:   "03",
		Series:    "B001",
		Number:    "B001-1",
		IssueDate: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Currency:  "PEN",
		Total:     100,
	}

	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.ValidUntil != "" {
		t.Fatalf("valid_until debería quedar vacío sin due_date, got %q", pd.ValidUntil)
	}
}

// El campo «Notas» de la nota de venta se guardaba pero nunca llegaba a la representación impresa.
func TestBuildPrintData_includesSaleNotes(t *testing.T) {
	db := setupPrintDataTestDB(t)
	sale := &database.TenantSale{
		ID: 7, DocType: "NOTA DE VENTA", Series: "NV001", Number: "NV001-00000007",
		IssueDate: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), Currency: "PEN", Total: 50,
		Notes: "Entregar en la puerta lateral",
	}
	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.Notes != "Entregar en la puerta lateral" {
		t.Fatalf("Notes = %q, want la nota escrita por el usuario", pd.Notes)
	}
}

// La dirección del emisor sigue la misma regla que el logo: la de la sucursal si la tiene; si no, la de la
// empresa (ajustes) como respaldo.
func TestBuildPrintData_issuerAddressBranchFirstThenCompany(t *testing.T) {
	db := setupPrintDataTestDB(t)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}, &database.TenantBranch{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{RUC: "20000000001", BusinessName: "ACME SAC", Address: "Av. Central 100 (empresa)"}).Error; err != nil {
		t.Fatal(err)
	}
	withAddr := database.TenantBranch{Name: "Sucursal Norte", Address: "Jr. Norte 45 (sucursal)", Active: true}
	noAddr := database.TenantBranch{Name: "Sucursal Sur", Address: "  ", Active: true}
	if err := db.Create(&withAddr).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&noAddr).Error; err != nil {
		t.Fatal(err)
	}

	build := func(branchID uint) string {
		sale := &database.TenantSale{
			ID: 1, BranchID: branchID, DocType: "03", Series: "B001", Number: "B001-1",
			IssueDate: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), Currency: "PEN", Total: 10,
		}
		pd, err := BuildPrintData(db, sale, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return pd.Company.Address
	}

	if got := build(withAddr.ID); got != "Jr. Norte 45 (sucursal)" {
		t.Fatalf("sucursal con dirección: got %q, want la de la sucursal", got)
	}
	if got := build(noAddr.ID); got != "Av. Central 100 (empresa)" {
		t.Fatalf("sucursal sin dirección: got %q, want la de la empresa como respaldo", got)
	}
}

// El teléfono del emisor sigue la misma regla: el de la sucursal si lo tiene; si no, el de la empresa.
func TestBuildPrintData_issuerPhoneBranchFirstThenCompany(t *testing.T) {
	db := setupPrintDataTestDB(t)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}, &database.TenantBranch{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{RUC: "20000000001", BusinessName: "ACME SAC", Phone: "111 111 111"}).Error; err != nil {
		t.Fatal(err)
	}
	withPhone := database.TenantBranch{Name: "Sucursal Norte", Phone: "983 545 104", Active: true}
	noPhone := database.TenantBranch{Name: "Sucursal Sur", Phone: "  ", Active: true}
	if err := db.Create(&withPhone).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&noPhone).Error; err != nil {
		t.Fatal(err)
	}
	build := func(branchID uint) string {
		sale := &database.TenantSale{
			ID: 1, BranchID: branchID, DocType: "03", Series: "B001", Number: "B001-1",
			IssueDate: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), Currency: "PEN", Total: 10,
		}
		pd, err := BuildPrintData(db, sale, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return pd.Company.Phone
	}
	if got := build(withPhone.ID); got != "983 545 104" {
		t.Fatalf("sucursal con teléfono: got %q, want el de la sucursal", got)
	}
	if got := build(noPhone.ID); got != "111 111 111" {
		t.Fatalf("sucursal sin teléfono: got %q, want el de la empresa como respaldo", got)
	}
}

func TestPrintableSaleNotes_quitaElRegistroDeAnulacion(t *testing.T) {
	cases := map[string]string{
		"Entregar mañana":                                 "Entregar mañana",
		"Entregar mañana | ANULADA: error de precio":      "Entregar mañana",
		"ANULADA: error de precio":                        "",
		"  ":                                              "",
		"(Desde cotización #12) | ANULADA: cliente desistió": "(Desde cotización #12)",
	}
	for in, want := range cases {
		if got := printableSaleNotes(in); got != want {
			t.Errorf("printableSaleNotes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrintsSaleNotes_excluyeNotasYGuias(t *testing.T) {
	for code, want := range map[string]bool{"00": true, "01": true, "03": true, "07": false, "08": false, "09": false, "31": false, "QT": false} {
		if got := printsSaleNotes(code, ""); got != want {
			t.Errorf("printsSaleNotes(%q) = %v, want %v", code, got, want)
		}
	}
	if printsSaleNotes("", "NOTA_CREDITO") {
		t.Error("una NC (por doc_type) no imprime notas de venta")
	}
}

// Bug reportado (RUC 20611759518, F002-18): el cliente se eliminó y se recreó después de emitir; la
// venta seguía apuntando al contacto eliminado y el PDF salía con "Cliente genérico". Un contacto
// eliminado sigue siendo el cliente del comprobante ya emitido.
func TestBuildPrintData_clientSurvivesContactDeletion(t *testing.T) {
	db := setupPrintDataTestDB(t)
	if err := db.AutoMigrate(&database.TenantContact{}); err != nil {
		t.Fatal(err)
	}
	contact := database.TenantContact{Type: "customer", DocType: "6", DocNumber: "20565959027", BusinessName: "DEPOSITO DE MINERALES CAJAMARQUILLA S.A.C.", Active: true}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&contact).Error; err != nil {
		t.Fatal(err)
	}
	cid := contact.ID
	sale := &database.TenantSale{
		ID: 10, DocType: "01", Series: "F002", Number: "F002-00000018",
		IssueDate: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), Currency: "PEN", Total: 100, ContactID: &cid,
	}

	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.Client == nil || pd.Client.DocNumber != "20565959027" || pd.Client.BusinessName == "Cliente genérico" {
		t.Fatalf("el cliente debe ser el del comprobante aunque su contacto se haya eliminado: %+v", pd.Client)
	}
}
