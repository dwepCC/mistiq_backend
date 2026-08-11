package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	billingsvc "tukifac/internal/billing/service"
	prepaymentsvc "tukifac/internal/prepayment"
	salessvc "tukifac/internal/sales/service"
	"tukifac/config"
	"tukifac/pkg/database"
	sunatpre "tukifac/pkg/sunat/prepayment"
	"tukifac/pkg/fiscalclient"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/tax"
)

type phase0DocResult struct {
	Kind             string `json:"kind"`
	SaleID           uint   `json:"sale_id"`
	Number           string `json:"number"`
	TipoOperacion    string `json:"tipo_operacion"`
	SunatStatus      string `json:"sunat_status"`
	SunatCDRCode     string `json:"sunat_cdr_code"`
	SunatMessage     string `json:"sunat_message"`
	BillingStatus    string `json:"billing_status"`
	XMLPath          string `json:"xml_path,omitempty"`
	CDRPath          string `json:"cdr_path,omitempty"`
	PDFPath          string `json:"pdf_path,omitempty"`
	XMLHasListID0104 bool   `json:"xml_has_listid_0104"`
	VoucherStatus    string `json:"voucher_status,omitempty"`
	Error            string `json:"error,omitempty"`
}

// RunValidatePrepaymentPhase0 emite boleta y factura de anticipo contra SUNAT Beta (tenant demo por defecto).
func RunValidatePrepaymentPhase0(args []string) int {
	slug := "demo"
	opCode := sunatpre.EmitOperationTypeCode()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--slug=") {
			slug = strings.TrimSpace(strings.TrimPrefix(arg, "--slug="))
			continue
		}
		if strings.HasPrefix(arg, "--operation=") {
			opCode = strings.TrimSpace(strings.TrimPrefix(arg, "--operation="))
			continue
		}
		switch arg {
		case "--slug":
			if i+1 < len(args) {
				slug = strings.TrimSpace(args[i+1])
				i++
			}
		case "--operation":
			if i+1 < len(args) {
				opCode = strings.TrimSpace(args[i+1])
				i++
			}
		}
	}

	if config.AppConfig == nil {
		fmt.Fprintln(os.Stderr, "config not loaded")
		return 1
	}
	fiscalclient.Init(config.AppConfig.FacturadorBaseURL, config.AppConfig.FacturadorToken)

	var tenant database.Tenant
	if err := database.CentralDB.Where("slug = ? AND status = ?", slug, "active").First(&tenant).Error; err != nil {
		fmt.Fprintf(os.Stderr, "tenant %q: %v\n", slug, err)
		return 1
	}
	tdb, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tenant db: %v\n", err)
		return 1
	}

	taxCfg := tax.LoadFromDB(tdb)
	now := time.Now()
	loc, _ := time.LoadLocation("America/Lima")
	if loc != nil {
		now = time.Now().In(loc)
	}
	issueDate := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc)

	var product database.TenantProduct
	if err := tdb.Where("active = ?", true).First(&product).Error; err != nil {
		fmt.Fprintf(os.Stderr, "producto: %v\n", err)
		return 1
	}

	var boletaSeries, facturaSeries database.TenantDocumentSeries
	if err := tdb.Where("sunat_code = ? AND active = ?", "03", true).First(&boletaSeries).Error; err != nil {
		fmt.Fprintf(os.Stderr, "serie boleta: %v\n", err)
		return 1
	}
	if err := tdb.Where("sunat_code = ? AND active = ?", "01", true).First(&facturaSeries).Error; err != nil {
		fmt.Fprintf(os.Stderr, "serie factura: %v\n", err)
		return 1
	}

	var contactBoleta, contactFactura database.TenantContact
	if err := tdb.Where("doc_type = ? AND active = ?", "0", true).First(&contactBoleta).Error; err != nil {
		fmt.Fprintf(os.Stderr, "cliente boleta: %v\n", err)
		return 1
	}
	if err := tdb.Where("doc_type = ? AND active = ?", "6", true).First(&contactFactura).Error; err != nil {
		fmt.Fprintf(os.Stderr, "cliente factura RUC: %v\n", err)
		return 1
	}

	cashSessionID := uint(2)
	userID := uint(1)
	branchID := uint(1)

	saleSvc := salessvc.NewSaleService(tdb)
	billingSvc := billingsvc.NewBillingService(tdb)
	billingSvc.SetCentralTenantID(tenant.ID)
	billingSvc.SetTenantSlug(tenant.Slug)

	results := make([]phase0DocResult, 0, 2)

	emitOne := func(kind string, series database.TenantDocumentSeries, docType string, contactID uint) phase0DocResult {
		out := phase0DocResult{Kind: kind, TipoOperacion: opCode}
		price := product.SalePrice
		if price <= 0 {
			price = 100
		}
		_, _, lineTotal := tax.CalcItem(price, 1, 0, product.IgvAffectationType, product.PriceIncludesIgv, taxCfg)
		cid := contactID
		createIn := salessvc.CreateSaleInput{
			BranchID:          branchID,
			ContactID:         &cid,
			UserID:            userID,
			CashSessionID:     &cashSessionID,
			SeriesID:          series.ID,
			DocType:           docType,
			IssueDate:         issueDate,
			Currency:          salecurrency.CurrencyPEN,
			OperationTypeCode: opCode,
			Payments: []salessvc.PaymentInput{
				{Method: "cash", Amount: lineTotal},
			},
			TaxConfig:       taxCfg,
			CentralTenantID: tenant.ID,
			Items: []salessvc.SaleItemInput{
				{
					ProductID:          &product.ID,
					Code:               product.Code,
					Description:        "Anticipo Fase0 " + kind + " (" + opCode + ")",
					Unit:               "NIU",
					Quantity:           1,
					UnitPrice:          price,
					IgvAffectationType: product.IgvAffectationType,
					PriceIncludesIgv:   product.PriceIncludesIgv,
				},
			},
		}
		if sunatpre.IsAllowedEmitOperationType(opCode) {
			createIn.Prepayment = &prepaymentsvc.SaleInput{
				Emit:             true,
				AffectationGroup: sunatpre.AffectationGravado,
			}
		}
		sale, err := saleSvc.Create(createIn)
		if err != nil {
			out.Error = err.Error()
			return out
		}

		out.SaleID = sale.ID
		out.Number = sale.Number

		manual, err := billingSvc.ManualSendToSUNAT(sale.ID, tenant.ID, tenant.Slug)
		if err != nil {
			out.Error = err.Error()
		}
		if manual != nil {
			out.BillingStatus = manual.BillingStatus
			out.SunatMessage = manual.SunatMessage
			if manual.Invoice != nil {
				inv := manual.Invoice
				out.SunatStatus = inv.SunatStatus
				out.SunatCDRCode = inv.SunatCDRCode
				out.SunatMessage = inv.SunatMessage
				out.XMLPath = inv.XMLURL
				out.CDRPath = inv.CDRURL
				out.PDFPath = inv.PDFURL
			}
		}

		if inv, _ := billingSvc.GetInvoice(sale.ID); inv != nil {
			out.SunatStatus = inv.SunatStatus
			out.SunatCDRCode = inv.SunatCDRCode
			out.SunatMessage = inv.SunatMessage
			out.XMLPath = inv.XMLURL
			out.CDRPath = inv.CDRURL
			out.PDFPath = inv.PDFURL
			out.XMLHasListID0104 = xmlContainsConfiguredEmitListID(inv, opCode)
		}

		if v, _ := prepaymentsvc.NewService(tdb).LoadBySaleID(sale.ID); v != nil {
			out.VoucherStatus = v.Status
		}

		if out.SunatCDRCode != "0" && out.Error == "" && out.SunatCDRCode != "" {
			out.Error = "SUNAT CDR code " + out.SunatCDRCode
		}
		return out
	}

	results = append(results, emitOne("boleta_anticipo", boletaSeries, "BOLETA", contactBoleta.ID))
	results = append(results, emitOne("factura_anticipo", facturaSeries, "FACTURA", contactFactura.ID))

	enc, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(enc))

	for _, r := range results {
		if r.Error != "" {
			return 1
		}
		if sunatpre.IsAllowedEmitOperationType(opCode) && r.SunatCDRCode != "0" {
			return 1
		}
		if opCode == sunatpre.OpVentaInterna && r.SunatCDRCode != "0" && r.SunatStatus != "accepted" {
			return 1
		}
	}
	return 0
}

func xmlContainsConfiguredEmitListID(inv *database.TenantInvoice, opCode string) bool {
	if inv == nil {
		return false
	}
	data := readInvoiceXMLBytes(inv)
	if len(data) == 0 {
		return false
	}
	return strings.Contains(string(data), `listID="`+strings.TrimSpace(opCode)+`"`)
}

func xmlContainsListID0104(inv *database.TenantInvoice) bool {
	return xmlContainsConfiguredEmitListID(inv, sunatpre.OpVentaInternaAnticipos)
}

func xmlContainsListID0101(inv *database.TenantInvoice) bool {
	if inv == nil {
		return false
	}
	data := readInvoiceXMLBytes(inv)
	return strings.Contains(string(data), `listID="0101"`)
}

func readInvoiceXMLBytes(inv *database.TenantInvoice) []byte {
	if inv == nil {
		return nil
	}
	url := strings.TrimSpace(inv.XMLURL)
	if strings.Contains(url, "/fiscal-files/") {
		const marker = "/fiscal-files/"
		if idx := strings.Index(url, marker); idx >= 0 {
			rel := strings.TrimPrefix(url[idx+len(marker):], "/")
			path := filepath.Join("..", "facturador_lycet", "var", "fiscal_storage", filepath.FromSlash(rel))
			if data, err := os.ReadFile(path); err == nil {
				return data
			}
		}
	}
	if strings.TrimSpace(inv.XMLURL) == "" {
		return nil
	}
	base := config.AppConfig.InvoiceStoragePath
	if base == "" {
		base = "./storage/invoices"
	}
	path := filepath.Join(base, filepath.FromSlash(inv.XMLURL))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
