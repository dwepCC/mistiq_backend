package service

import (
	"testing"
	"time"

	detraccionsvc "tukifac/internal/detraccion"
	"tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"
	"tukifac/pkg/taxpayment"
)

// Autodetracción: el cliente paga el TOTAL (neto + detracción) al proveedor. Internamente las cuotas y el saldo
// van por el total y no hay pago "Detracción BN"; al SUNAT/PDF siguen yendo las cuotas netas de siempre.

func autodetraccionInput(contactID, seriesID uint, self bool) CreateSaleInput {
	issue := time.Now()
	due := func(days int) string { return issue.AddDate(0, 0, days).Format("2006-01-02") }
	lastDue := issue.AddDate(0, 0, 90)
	return CreateSaleInput{
		DueDate: &lastDue,
		BranchID: 1, UserID: 1, ContactID: &contactID, SeriesID: seriesID, DocType: "01",
		IssueDate: issue, Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		OperationTypeCode:    "1001",
		PaymentConditionCode: "credit",
		Items: []SaleItemInput{{
			Description: "Servicio administrativo", Unit: "ZZ", Quantity: 1, UnitPrice: 2000,
			IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
		Detraccion: &detraccionsvc.SaleInput{GoodCode: "014", SelfDetraction: self},
		// Con autodetracción las cuotas de cobro suman el TOTAL; sin ella, solo el neto.
		CreditInstallments: func() []CreditInstallmentInput {
			if self {
				return []CreditInstallmentInput{{DueDate: due(30), Amount: 666.67}, {DueDate: due(60), Amount: 666.67}, {DueDate: due(90), Amount: 666.66}}
			}
			return []CreditInstallmentInput{{DueDate: due(30), Amount: 640}, {DueDate: due(60), Amount: 640}, {DueDate: due(90), Amount: 640}}
		}(),
	}
}

func installmentSum(t *testing.T, svc *SaleService, saleID uint) float64 {
	t.Helper()
	rows, err := svc.GetCreditInstallments(saleID)
	if err != nil {
		t.Fatal(err)
	}
	var s float64
	for _, r := range rows {
		s += r.Amount
	}
	return s
}

func TestCreateAutodetraccion_cuotasPorElTotalSinPagoBNYConPDFNeto(t *testing.T) {
	db := setup1004TestDB(t)
	if err := db.AutoMigrate(&database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSaleService(db)
	contactID := seedContactConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	sale, err := svc.Create(autodetraccionInput(contactID, seriesID, true))
	if err != nil {
		t.Fatalf("Create con autodetracción: %v", err)
	}
	if sale.Status != "credit" {
		t.Errorf("status = %q, want credit", sale.Status)
	}

	det, err := detraccionsvc.NewService(db).LoadBySaleID(sale.ID)
	if err != nil || det == nil {
		t.Fatalf("no se persistió la detracción: %v", err)
	}
	if det.BnConfirmationStatus != taxpayment.BnStatusSelfPending {
		t.Errorf("bn_confirmation_status = %q, want %q", det.BnConfirmationStatus, taxpayment.BnStatusSelfPending)
	}
	if det.DetractionAmountPen != 80 || det.NetPayablePen != 1920 {
		t.Errorf("la detracción fiscal no debe cambiar: detracción %v neto %v (want 80 / 1920)", det.DetractionAmountPen, det.NetPayablePen)
	}

	// Sin línea "Detracción BN" (el cliente no deposita en el BN).
	var bn int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ? AND method = ?", sale.ID, taxpayment.CodeDetraccionBN).Count(&bn)
	if bn != 0 {
		t.Errorf("no debe existir pago detraccion_bn con autodetracción, hay %d", bn)
	}

	// Las cuotas de cobro suman el TOTAL.
	if got := installmentSum(t, svc, sale.ID); got != 2000 {
		t.Errorf("cuotas de cobro = %v, want 2000 (neto + detracción)", got)
	}

	// El comprobante impreso muestra las cuotas FISCALES (netas): 1920, igual que una venta normal.
	items, _ := svc.GetItems(sale.ID)
	pd, err := BuildPrintData(db, sale, items, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var printed float64
	for _, c := range pd.CreditInstallments {
		printed += c.Amount
	}
	if printed < 1919.99 || printed > 1920.01 {
		t.Errorf("cuotas impresas suman %v, want 1920 (netas, como las que viajan a SUNAT)", printed)
	}
	if pd.Fiscal == nil || pd.Fiscal.DetraccionNetPayable != 1920 {
		t.Errorf("el neto fiscal impreso no debe cambiar: %+v", pd.Fiscal)
	}

	// Listados: lo que se cobra al cliente es el total.
	list := []database.TenantSale{*sale}
	svc.enrichSalesWithDetraccion(list)
	if list[0].NetPayable != 2000 {
		t.Errorf("net_payable en listados = %v, want 2000 con autodetracción", list[0].NetPayable)
	}
}

// Control: sin la casilla todo sigue como antes (pago BN automático, cuotas por el neto, estado pending).
func TestCreateDetraccionNormal_sigueIgualSinAutodetraccion(t *testing.T) {
	db := setup1004TestDB(t)
	if err := db.AutoMigrate(&database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSaleService(db)
	contactID := seedContactConRUC(t, db)
	seriesID := seedSeries(t, db, "Factura", "01", "F001")

	sale, err := svc.Create(autodetraccionInput(contactID, seriesID, false))
	if err != nil {
		t.Fatalf("Create normal: %v", err)
	}
	det, _ := detraccionsvc.NewService(db).LoadBySaleID(sale.ID)
	if det == nil || det.BnConfirmationStatus != "pending" {
		t.Fatalf("estado BN = %+v, want pending", det)
	}
	var bn int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ? AND method = ?", sale.ID, taxpayment.CodeDetraccionBN).Count(&bn)
	if bn != 1 {
		t.Errorf("la venta normal sí lleva el pago detraccion_bn, hay %d", bn)
	}
	if got := installmentSum(t, svc, sale.ID); got != 1920 {
		t.Errorf("cuotas normales = %v, want 1920 (neto)", got)
	}
}

// Con autodetracción el backend sigue validando todo lo de una detracción normal.
func TestCreateAutodetraccion_siguenLasValidaciones(t *testing.T) {
	setup := func(t *testing.T) (*SaleService, uint, uint) {
		db := setup1004TestDB(t)
		if err := db.AutoMigrate(&database.TenantSaleCreditInstallment{}); err != nil {
			t.Fatal(err)
		}
		return NewSaleService(db), seedContactConRUC(t, db), seedSeries(t, db, "Factura", "01", "F001")
	}

	t.Run("bajo el mínimo de detracción no aplica y se rechaza", func(t *testing.T) {
		svc, contactID, seriesID := setup(t)
		in := autodetraccionInput(contactID, seriesID, true)
		in.Items[0].UnitPrice = 500 // <= S/ 700
		if _, err := svc.Create(in); err == nil {
			t.Fatal("debe rechazar: la detracción no supera el mínimo")
		}
	})

	t.Run("sin bien/servicio de detracción se rechaza", func(t *testing.T) {
		svc, contactID, seriesID := setup(t)
		in := autodetraccionInput(contactID, seriesID, true)
		in.Detraccion.GoodCode = ""
		if _, err := svc.Create(in); err == nil {
			t.Fatal("debe exigir el bien o servicio")
		}
	})

	t.Run("solo factura: boleta con detracción se rechaza", func(t *testing.T) {
		svc, contactID, _ := setup(t)
		in := autodetraccionInput(contactID, seedSeries(t, svc.db, "Boleta", "03", "B001"), true)
		in.DocType = "03"
		if _, err := svc.Create(in); err == nil {
			t.Fatal("la detracción solo aplica a facturas")
		}
	})

	t.Run("las cuotas deben sumar el total (no el neto)", func(t *testing.T) {
		svc, contactID, seriesID := setup(t)
		in := autodetraccionInput(contactID, seriesID, true)
		in.CreditInstallments = []CreditInstallmentInput{ // suman el neto 1920, no el total 2000
			{DueDate: in.CreditInstallments[0].DueDate, Amount: 960}, {DueDate: in.CreditInstallments[2].DueDate, Amount: 960},
		}
		if _, err := svc.Create(in); err == nil {
			t.Fatal("con autodetracción las cuotas deben sumar el total 2000")
		}
	})

	t.Run("al contado con autodetracción los pagos deben cubrir el total", func(t *testing.T) {
		svc, contactID, seriesID := setup(t)
		in := autodetraccionInput(contactID, seriesID, true)
		in.PaymentConditionCode, in.CreditInstallments, in.DueDate = "cash", nil, nil
		in.Payments = []PaymentInput{{Method: "cash", Amount: 1920}} // solo el neto
		if _, err := svc.Create(in); err == nil {
			t.Fatal("al contado con autodetracción se debe pagar el total, no solo el neto")
		}
		in.Payments = []PaymentInput{{Method: "cash", Amount: 2000}}
		sale, err := svc.Create(in)
		if err != nil {
			t.Fatalf("al contado pagando el total debe guardar: %v", err)
		}
		if sale.Status != "paid" {
			t.Errorf("status = %q, want paid", sale.Status)
		}
	})

	t.Run("no se puede combinar con retención de IGV", func(t *testing.T) {
		svc, contactID, seriesID := setup(t)
		in := autodetraccionInput(contactID, seriesID, true)
		yes := true
		in.FiscalContext = &salecontext.FiscalContextInput{HasIgvRetention: &yes}
		if _, err := svc.Create(in); err == nil {
			t.Fatal("no se puede combinar detracción con retención de IGV")
		}
	})
}
