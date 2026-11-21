package service

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"tukifac/pkg/database"
)

func setupSnapshotDB(t *testing.T) (db *gorm.DB, contact database.TenantContact, branch database.TenantBranch) {
	t.Helper()
	db = setupPrintDataTestDB(t)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}, &database.TenantBranch{}, &database.TenantContact{}, &database.TenantUser{},
		&database.TenantDocumentSnapshot{}, &database.TenantSnapshotLogo{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{
		RUC: "20000000001", BusinessName: "ACME SAC", TradeName: "Acme", Address: "Av. Central 100", Phone: "111",
		LogoURL: "data:image/png;base64,LOGO-ORIGINAL",
	}).Error; err != nil {
		t.Fatal(err)
	}
	branch = database.TenantBranch{Name: "Sucursal Norte", Address: "Jr. Norte 45", Phone: "222", Active: true}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}
	contact = database.TenantContact{Type: "customer", DocType: "6", DocNumber: "20565959027", BusinessName: "CLIENTE ORIGINAL SAC", Address: "Calle 1", Active: true}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	return db, contact, branch
}

func snapshotTestSale(id, branchID, contactID uint) *database.TenantSale {
	return &database.TenantSale{
		ID: id, BranchID: branchID, ContactID: &contactID, DocType: "01", Series: "F001", Number: "F001-0000000" + string(rune('0'+id%10)),
		IssueDate: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC), Currency: "PEN", Total: 118,
	}
}

// Lo pedido: editar el cliente, la empresa o la sucursal DESPUÉS de emitir no cambia el PDF del comprobante ya emitido.
func TestFreezePrintData_editingPartiesDoesNotAlterIssuedDocument(t *testing.T) {
	db, contact, branch := setupSnapshotDB(t)
	sale := snapshotTestSale(1, branch.ID, contact.ID)

	first, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Client.BusinessName != "CLIENTE ORIGINAL SAC" || first.Company.BusinessName != "ACME SAC" || first.Company.Address != "Jr. Norte 45" {
		t.Fatalf("emisión: datos inesperados %+v / %+v", first.Client, first.Company)
	}

	// Ediciones posteriores: cliente, razón social, dirección y teléfono de sucursal, logo de la empresa, RUC.
	db.Model(&database.TenantContact{}).Where("id = ?", contact.ID).Updates(map[string]interface{}{"business_name": "CLIENTE EDITADO", "address": "Calle 99"})
	db.Model(&database.TenantCompanyConfig{}).Where("1 = 1").Updates(map[string]interface{}{"business_name": "OTRA RAZON SA", "ruc": "20999999999", "logo_url": "data:image/png;base64,LOGO-NUEVO"})
	db.Model(&database.TenantBranch{}).Where("id = ?", branch.ID).Updates(map[string]interface{}{"address": "Av. Nueva 1", "phone": "999"})

	again, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Client.BusinessName != "CLIENTE ORIGINAL SAC" || again.Client.Address != "Calle 1" {
		t.Fatalf("el cliente cambió en un comprobante emitido: %+v", again.Client)
	}
	if again.Company.BusinessName != "ACME SAC" || again.Company.RUC != "20000000001" {
		t.Fatalf("la empresa cambió en un comprobante emitido: %+v", again.Company)
	}
	if again.Company.Address != "Jr. Norte 45" || again.Company.Phone != "222" {
		t.Fatalf("la sucursal cambió en un comprobante emitido: %q %q", again.Company.Address, again.Company.Phone)
	}
	if again.Company.LogoURL == "data:image/png;base64,LOGO-NUEVO" {
		t.Fatal("el logo cambió en un comprobante emitido")
	}
	if again.QRData != first.QRData {
		t.Fatalf("el QR cambió: %q vs %q", again.QRData, first.QRData)
	}

	// Un comprobante NUEVO sí toma los datos actuales.
	other := snapshotTestSale(2, branch.ID, contact.ID)
	fresh, err := BuildPrintData(db, other, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Client.BusinessName != "CLIENTE EDITADO" || fresh.Company.BusinessName != "OTRA RAZON SA" || fresh.Company.Address != "Av. Nueva 1" {
		t.Fatalf("un comprobante nuevo debe usar los datos vigentes: %+v / %+v", fresh.Client, fresh.Company)
	}
}

// Una sola foto por documento y el logo se guarda una vez por contenido.
func TestFreezePrintData_oneSnapshotPerDocumentAndLogoDeduplicated(t *testing.T) {
	db, contact, branch := setupSnapshotDB(t)
	for i := 0; i < 3; i++ {
		if _, err := BuildPrintData(db, snapshotTestSale(1, branch.ID, contact.ID), nil, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := BuildPrintData(db, snapshotTestSale(2, branch.ID, contact.ID), nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	var snaps, logos int64
	db.Model(&database.TenantDocumentSnapshot{}).Count(&snaps)
	db.Model(&database.TenantSnapshotLogo{}).Count(&logos)
	if snaps != 2 {
		t.Fatalf("snapshots: got %d, want 2 (uno por documento)", snaps)
	}
	if logos != 1 {
		t.Fatalf("logos: got %d, want 1 (mismo contenido, una fila)", logos)
	}
}

// Compras: la foto guarda proveedor y emisor al registrar y no cambia al editar el proveedor.
func TestEnsurePurchaseSnapshot_freezesSupplierAndIssuer(t *testing.T) {
	db, contact, branch := setupSnapshotDB(t)
	cid := contact.ID
	p := &database.TenantPurchase{ID: 5, BranchID: branch.ID, ContactID: &cid}

	s1, logo1 := EnsurePurchaseSnapshot(db, p)
	if s1.Client == nil || s1.Client.BusinessName != "CLIENTE ORIGINAL SAC" || s1.Company.BusinessName != "ACME SAC" || logo1 == "" {
		t.Fatalf("foto inicial incompleta: %+v logo=%q", s1, logo1)
	}
	db.Model(&database.TenantContact{}).Where("id = ?", contact.ID).Update("business_name", "PROVEEDOR EDITADO")
	db.Model(&database.TenantCompanyConfig{}).Where("1 = 1").Update("business_name", "OTRA RAZON SA")

	s2, _ := EnsurePurchaseSnapshot(db, p)
	if s2.Client.BusinessName != "CLIENTE ORIGINAL SAC" || s2.Company.BusinessName != "ACME SAC" {
		t.Fatalf("la compra cambió al editar proveedor/empresa: %+v / %+v", s2.Client, s2.Company)
	}
}

// Cotización editada: su foto se descarta y la siguiente impresión toma los datos vigentes.
func TestDeletePrintSnapshot_nextBuildRefreshes(t *testing.T) {
	db, contact, branch := setupSnapshotDB(t)
	sale := snapshotTestSale(1, branch.ID, contact.ID)
	if _, err := BuildPrintData(db, sale, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	db.Model(&database.TenantContact{}).Where("id = ?", contact.ID).Update("business_name", "CLIENTE EDITADO")
	DeletePrintSnapshot(db, database.SnapshotKindSale, 1)
	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.Client.BusinessName != "CLIENTE EDITADO" {
		t.Fatalf("tras descartar la foto debe tomar los datos vigentes: %+v", pd.Client)
	}
}
