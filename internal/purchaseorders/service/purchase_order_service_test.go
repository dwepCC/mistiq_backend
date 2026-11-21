package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantDocumentSeries{}, &database.TenantContact{}, &database.TenantProduct{},
		&database.TenantPurchaseOrder{}, &database.TenantPurchaseOrderItem{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

type fixture struct {
	db        *gorm.DB
	svc       *PurchaseOrderService
	seriesID  uint
	supplier  uint
	productID uint
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := setupDB(t)
	s := database.TenantDocumentSeries{BranchID: 1, DocType: "Orden de compra", SunatCode: "OC", Category: "orden_compra", Series: "OC01", Correlative: 1, Active: true}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	c := database.TenantContact{Type: "supplier", DocType: "RUC", DocNumber: "20123456789", BusinessName: "Proveedor SAC"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	p := database.TenantProduct{Code: "P1", Name: "Prod", Type: "product", Unit: "NIU", IgvAffectationType: "10", Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, svc: NewPurchaseOrderService(db), seriesID: s.ID, supplier: c.ID, productID: p.ID}
}

func (f fixture) input() SaveInput {
	sup := f.supplier
	pid := f.productID
	return SaveInput{
		BranchID: 1, UserID: 1, ContactID: &sup, SeriesID: f.seriesID, IssueDate: time.Now(),
		Currency: "PEN",
		Items: []ItemInput{{
			ProductID: &pid, Description: "Prod", Quantity: 2, UnitCost: 50, IgvAffectationType: "10",
		}},
		TaxConfig: tax.Config{TaxRate: 18},
	}
}

func TestCreate_NumberingTotalsAndStatus(t *testing.T) {
	f := newFixture(t)
	po, err := f.svc.Create(f.input())
	if err != nil {
		t.Fatal(err)
	}
	if po.Number != "OC01-00000001" || po.Series != "OC01" || po.Correlative != 1 {
		t.Errorf("numeración: %+v", po)
	}
	if po.Status != StatusDraft {
		t.Errorf("estado = %s, quiere draft", po.Status)
	}
	// 2 × 50 sin IGV incluido → subtotal 100, IGV 18, total 118.
	if po.Subtotal != 100 || po.TaxAmount != 18 || po.Total != 118 {
		t.Errorf("totales: sub=%v igv=%v total=%v", po.Subtotal, po.TaxAmount, po.Total)
	}
	po2, err := f.svc.Create(f.input())
	if err != nil {
		t.Fatal(err)
	}
	if po2.Number != "OC01-00000002" {
		t.Errorf("segundo correlativo = %s", po2.Number)
	}
}

func TestCreate_RejectsWrongSeriesCategoryAndBranch(t *testing.T) {
	f := newFixture(t)
	other := database.TenantDocumentSeries{BranchID: 1, DocType: "Cotización", SunatCode: "QT", Category: "cotizacion", Series: "COT01", Correlative: 1, Active: true}
	f.db.Create(&other)
	in := f.input()
	in.SeriesID = other.ID
	if _, err := f.svc.Create(in); err == nil || !strings.Contains(err.Error(), "orden de compra") {
		t.Errorf("serie de otra categoría debe rechazarse, err=%v", err)
	}
	in = f.input()
	in.BranchID = 2
	if _, err := f.svc.Create(in); err == nil {
		t.Error("serie de otra sucursal debe rechazarse")
	}
}

func TestCreate_RequiresSupplierAndItems(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.ContactID = nil
	if _, err := f.svc.Create(in); err != ErrSupplierNeeded {
		t.Errorf("sin proveedor: %v", err)
	}
	in = f.input()
	missing := uint(9999)
	in.ContactID = &missing
	if _, err := f.svc.Create(in); err == nil {
		t.Error("proveedor inexistente debe rechazarse")
	}
	in = f.input()
	in.Items = nil
	if _, err := f.svc.Create(in); err == nil {
		t.Error("sin ítems debe rechazarse")
	}
	in = f.input()
	in.Items[0].Quantity = 0
	if _, err := f.svc.Create(in); err == nil {
		t.Error("cantidad 0 debe rechazarse")
	}
}

func TestCreate_CurrencyAndExchangeRate(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Currency = "USD"
	if _, err := f.svc.Create(in); err == nil || !strings.Contains(err.Error(), "tipo de cambio") {
		t.Errorf("USD sin TC debe rechazarse, err=%v", err)
	}
	rate := 3.75
	in.ExchangeRate = &rate
	po, err := f.svc.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if po.Currency != "USD" || po.ExchangeRate == nil || *po.ExchangeRate != 3.75 {
		t.Errorf("USD con TC: %+v", po)
	}
	// PEN descarta el TC aunque venga.
	in = f.input()
	in.ExchangeRate = &rate
	po, err = f.svc.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if po.ExchangeRate != nil {
		t.Errorf("PEN no debe guardar TC, got %v", *po.ExchangeRate)
	}
	in = f.input()
	in.Currency = "EUR"
	if _, err := f.svc.Create(in); err == nil {
		t.Error("EUR no soportada debe rechazarse")
	}
}

func TestUpdate_ReplacesItemsKeepsNumberAndBlocksWhenClosed(t *testing.T) {
	f := newFixture(t)
	po, _ := f.svc.Create(f.input())
	in := f.input()
	in.Items[0].Quantity = 10
	upd, err := f.svc.Update(po.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if upd.Number != po.Number || upd.Total != 590 {
		t.Errorf("update: number=%s total=%v", upd.Number, upd.Total)
	}
	_, items, _ := f.svc.GetByID(po.ID)
	if len(items) != 1 || items[0].Quantity != 10 {
		t.Errorf("ítems tras update: %+v", items)
	}
	if err := f.svc.Cancel(po.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(po.ID, in); err != ErrNotEditable {
		t.Errorf("editar anulada: %v", err)
	}
}

func TestSendCancelAndReceivedGuards(t *testing.T) {
	f := newFixture(t)
	po, _ := f.svc.Create(f.input())
	if err := f.svc.MarkSent(po.ID); err != nil {
		t.Fatal(err)
	}
	got, _, _ := f.svc.GetByID(po.ID)
	if got.Status != StatusSent {
		t.Errorf("estado = %s", got.Status)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, po.ID, 77, "PEN") }); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Cancel(po.ID); err == nil {
		t.Error("no se puede anular una orden ya recibida")
	}
	if err := f.svc.MarkSent(po.ID); err != ErrAlreadyClosed {
		t.Errorf("enviar una recibida: %v", err)
	}
	if _, err := f.svc.EnsureConvertible(po.ID); err == nil {
		t.Error("una orden recibida no es convertible")
	}
}

func TestMarkReceived_OnlyOnceAndRelease(t *testing.T) {
	f := newFixture(t)
	po, _ := f.svc.Create(f.input())
	if err := f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, po.ID, 10, "PEN") }); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, po.ID, 11, "PEN") }); err == nil {
		t.Error("una segunda compra sobre la misma orden debe fallar")
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error { return ReleaseOnPurchaseVoidTx(tx, 10) }); err != nil {
		t.Fatal(err)
	}
	got, _, _ := f.svc.GetByID(po.ID)
	if got.Status != StatusSent || got.PurchaseID != nil {
		t.Errorf("tras anular la compra la orden vuelve a sent: %+v", got)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, po.ID, 12, "PEN") }); err != nil {
		t.Errorf("tras liberarla se puede volver a convertir: %v", err)
	}
}

func TestMarkReceived_PurchaseMustMatchOrderCurrency(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	rate := 3.7
	in.Currency, in.ExchangeRate = "USD", &rate
	usd, err := f.svc.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	// Orden USD con compra en soles (o sin moneda): se rechaza y la orden sigue abierta.
	for _, cur := range []string{"PEN", ""} {
		err = f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, usd.ID, 5, cur) })
		if err == nil || !strings.Contains(err.Error(), "moneda de la orden") {
			t.Errorf("compra %q sobre orden USD debe rechazarse, err=%v", cur, err)
		}
	}
	if got, _, _ := f.svc.GetByID(usd.ID); got.Status != StatusDraft {
		t.Errorf("la orden debe seguir abierta, estado=%s", got.Status)
	}
	// Misma moneda: se recibe.
	if err := f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, usd.ID, 5, "USD") }); err != nil {
		t.Errorf("orden USD con compra USD: %v", err)
	}
	// Orden en soles con compra en dólares: se rechaza.
	pen, _ := f.svc.Create(f.input())
	err = f.db.Transaction(func(tx *gorm.DB) error { return MarkReceivedTx(tx, pen.ID, 6, "USD") })
	if err == nil {
		t.Error("orden PEN con compra USD debe rechazarse")
	}
}

func TestList_FiltersByBranchStatusAndQuery(t *testing.T) {
	f := newFixture(t)
	a, _ := f.svc.Create(f.input())
	f.svc.Create(f.input())
	f.svc.Cancel(a.ID)
	rows, total, err := f.svc.List(ListParams{BranchID: 1, Status: StatusCancelled})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ContactName != "Proveedor SAC" {
		t.Errorf("cancelled: total=%d rows=%+v err=%v", total, rows, err)
	}
	if _, total, _ := f.svc.List(ListParams{BranchID: 2}); total != 0 {
		t.Errorf("otra sucursal debe verse vacía, total=%d", total)
	}
	if _, total, _ := f.svc.List(ListParams{BranchID: 1, Query: "Proveedor"}); total != 2 {
		t.Errorf("búsqueda por proveedor: total=%d", total)
	}
}
