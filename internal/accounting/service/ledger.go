package service

import (
	"tukifac/pkg/salecurrency"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// LedgerRow fila del Libro Mayor: saldo anterior, movimientos del mes y saldo final de una cuenta.
type LedgerRow struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Level    int     `json:"level"`
	Kind     string  `json:"kind"`
	Previous float64 `json:"previous"`
	Debit    float64 `json:"debit"`
	Credit   float64 `json:"credit"`
	Final    float64 `json:"final"`
}

type Ledger struct {
	Month    string      `json:"month"`
	Company  string      `json:"company"`
	RUC      string      `json:"ruc"`
	From     string      `json:"from"`
	To       string      `json:"to"`
	Rows     []LedgerRow `json:"rows"`
	Notes    []string    `json:"notes"`
	Warnings []string    `json:"warnings,omitempty"`
}

type dc struct{ d, c float64 }

// ledgerEpoch inicio de los tiempos para el saldo anterior.
var ledgerEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// activity movimientos (débito/crédito) por código de cuenta en [from,to), calculados desde los
// datos de Tukifac. Las cuentas de activo/gasto aumentan por el débito; las de pasivo/ingreso, por el
// crédito. NO hay asientos persistidos: es un cálculo de saldos (opción A del módulo).
func (s *Service) activity(from, to time.Time) (map[string]dc, map[string]string, []string, error) {
	acts := map[string]dc{}
	names := map[string]string{}
	add := func(code string, d, c float64) {
		a := acts[code]
		a.d += d
		a.c += c
		acts[code] = a
	}

	// Caja: ingresos = débito, egresos = crédito.
	var cash []struct {
		Type string
		Sum  float64
	}
	if err := s.db.Table("tenant_cash_movements").Select("type, SUM(COALESCE(amount_pen, amount)) AS sum").
		Where("created_at >= ? AND created_at < ?", from, to).Group("type").Scan(&cash).Error; err != nil {
		return nil, nil, nil, err
	}
	for _, r := range cash {
		if r.Type == "income" {
			add("1.1.1.1", r.Sum, 0)
		} else if r.Type == "expense" {
			add("1.1.1.1", 0, r.Sum)
		}
	}

	// Bancos y billeteras, una cuenta por cuenta bancaria.
	var banks []database.TenantBankAccount
	s.db.Unscoped().Find(&banks)
	for _, b := range banks {
		names[fmt.Sprintf("1.1.1.2.%d", b.ID)] = strings.TrimSpace(b.Name)
	}
	var bm []struct {
		BankAccountID uint
		Type          string
		Sum           float64
	}
	if err := s.db.Table("tenant_bank_movements").Select("bank_account_id, type, SUM(COALESCE(amount_pen, amount)) AS sum").
		Where("date >= ? AND date < ?", from, to).Group("bank_account_id, type").Scan(&bm).Error; err != nil {
		return nil, nil, nil, err
	}
	for _, r := range bm {
		code := fmt.Sprintf("1.1.1.2.%d", r.BankAccountID)
		if r.Type == "credit" {
			add(code, r.Sum, 0)
		} else {
			add(code, 0, r.Sum)
		}
	}

	// Ventas e IGV de ventas (comprobantes fiscales; las notas de crédito restan).
	sales, warns, err := s.SalesDocs(from, to)
	if err != nil {
		return nil, nil, nil, err
	}
	var creditSales float64
	for _, d := range sales {
		if d.Voided {
			continue
		}
		net := d.PEN.Taxed + d.PEN.Exonerated + d.PEN.Unaffected + d.PEN.Exportation
		if d.IsCreditNote() {
			add("4.1.1", net, 0)
			add("2.1.2.1", d.PEN.Igv, 0)
		} else {
			add("4.1.1", 0, net)
			add("2.1.2.1", 0, d.PEN.Igv)
			if d.PaymentCondition == "credit" {
				creditSales += d.PEN.Total
			}
		}
	}
	// Cuentas por cobrar: ventas al crédito (débito) y cobros posteriores (crédito).
	add("1.1.2", creditSales, 0)
	var collected float64
	s.db.Table("tenant_sale_payments").
		Joins("JOIN tenant_sales ON tenant_sales.id = tenant_sale_payments.sale_id").
		Where("tenant_sales.payment_condition_code = ? AND tenant_sales.deleted_at IS NULL AND tenant_sale_payments.created_at >= ? AND tenant_sale_payments.created_at < ?", "credit", from, to).
		Select("COALESCE(SUM(" + salecurrency.TotalPENSQL("tenant_sales", "tenant_sale_payments.amount") + "),0)").Scan(&collected)
	add("1.1.2", 0, collected)

	// Compras: IGV crédito fiscal y compras netas (las notas de crédito restan).
	purchases, pw, err := s.PurchaseDocs(from, to)
	if err != nil {
		return nil, nil, nil, err
	}
	warns = append(warns, pw...)
	for _, d := range purchases {
		net := d.PEN.Taxed + d.PEN.Exonerated + d.PEN.Unaffected + d.PEN.Exportation
		if d.IsCreditNote() {
			add("5.1.1", 0, net)
			add("1.1.4.1", 0, d.PEN.Igv)
		} else {
			add("5.1.1", net, 0)
			add("1.1.4.1", d.PEN.Igv, 0)
		}
	}
	// Cuentas por pagar: compras al crédito (crédito) y pagos posteriores (débito).
	var creditPurch, paid float64
	s.db.Table("tenant_purchase_payables").
		Joins("JOIN tenant_purchases ON tenant_purchases.id = tenant_purchase_payables.purchase_id").
		Where("tenant_purchases.deleted_at IS NULL AND tenant_purchases.issue_date >= ? AND tenant_purchases.issue_date < ?", from, to).
		Select("COALESCE(SUM(" + salecurrency.TotalPENSQL("tenant_purchases", "tenant_purchase_payables.original_amount") + "),0)").Scan(&creditPurch)
	s.db.Table("tenant_purchase_payments").
		Joins("JOIN tenant_purchase_payables ON tenant_purchase_payables.purchase_id = tenant_purchase_payments.purchase_id").
		Joins("JOIN tenant_purchases ON tenant_purchases.id = tenant_purchase_payments.purchase_id").
		Where("tenant_purchase_payments.created_at >= ? AND tenant_purchase_payments.created_at < ?", from, to).
		Select("COALESCE(SUM(" + salecurrency.TotalPENSQL("tenant_purchases", "tenant_purchase_payments.amount") + "),0)").Scan(&paid)
	add("2.1.1", 0, creditPurch)
	add("2.1.1", paid, 0)
	return acts, names, warns, nil
}

func codeKey(code string) []int {
	parts := strings.Split(code, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func codeLess(a, b string) bool {
	ka, kb := codeKey(a), codeKey(b)
	for i := 0; i < len(ka) && i < len(kb); i++ {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	return len(ka) < len(kb)
}

// Ledger Libro Mayor de un mes: saldo anterior (todo lo anterior al mes), débito y crédito del mes y
// saldo final, por cuenta del plan, con el signo correcto y los padres sumando a sus hijos DEL MISMO MES.
func (s *Service) Ledger(month string) (*Ledger, error) {
	from, to, err := ParseMonth(month)
	if err != nil {
		return nil, err
	}
	prior, names, warns, err := s.activity(ledgerEpoch, from)
	if err != nil {
		return nil, err
	}
	cur, names2, w2, err := s.activity(from, to)
	if err != nil {
		return nil, err
	}
	warns = append(warns, w2...)
	for k, v := range names2 {
		names[k] = v
	}

	var chart []database.TenantAccountingAccount
	if err := s.db.Where("active = ?", true).Find(&chart).Error; err != nil {
		return nil, err
	}
	kind := map[string]string{}
	label := map[string]string{}
	codes := map[string]bool{}
	for _, a := range chart {
		kind[a.Code], label[a.Code], codes[a.Code] = a.Kind, a.Name, true
	}
	// Cuentas dinámicas (bancos): heredan el tipo del padre 1.1.1.2.
	for c, n := range names {
		kind[c], label[c], codes[c] = "asset", n, true
	}
	signed := func(code string, a dc) float64 {
		if kind[code] == "liability" || kind[code] == "income" || kind[code] == "equity" {
			return a.c - a.d
		}
		return a.d - a.c
	}

	// Hojas = cuentas con movimiento propio; los padres suman a sus hijos (mismo mes y mismo periodo previo).
	type agg struct{ prev, d, c float64 }
	res := map[string]*agg{}
	for code := range codes {
		res[code] = &agg{}
	}
	leafAdd := func(code string, p, c dc) {
		// sube por los prefijos
		parts := strings.Split(code, ".")
		for i := len(parts); i >= 1; i-- {
			pc := strings.Join(parts[:i], ".")
			a, ok := res[pc]
			if !ok {
				continue
			}
			a.prev += signed(code, p)
			a.d += c.d
			a.c += c.c
		}
	}
	all := map[string]bool{}
	for k := range prior {
		all[k] = true
	}
	for k := range cur {
		all[k] = true
	}
	for code := range all {
		if !codes[code] {
			continue
		}
		leafAdd(code, prior[code], cur[code])
	}

	co := s.Company()
	led := &Ledger{
		Month: month, Company: co.Name, RUC: co.RUC, From: from.Format("02/01/2006"), To: to.AddDate(0, 0, -1).Format("02/01/2006"),
		Warnings: warns,
		Notes: []string{
			"Saldos calculados desde ventas, compras, caja, bancos y cuentas por cobrar/pagar de Tukifac (no hay asientos persistidos).",
			"Los inventarios no se valorizan en esta versión.",
		},
	}
	keys := make([]string, 0, len(res))
	for k := range res {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return codeLess(keys[i], keys[j]) })
	for _, code := range keys {
		a := res[code]
		if a.prev == 0 && a.d == 0 && a.c == 0 && strings.Count(code, ".") >= 1 && !isTopLevelOrKnown(code) {
			continue
		}
		var fin float64
		if k := kind[code]; k == "liability" || k == "income" || k == "equity" {
			fin = a.prev + (a.c - a.d)
		} else {
			fin = a.prev + (a.d - a.c)
		}
		led.Rows = append(led.Rows, LedgerRow{
			Code: code, Name: label[code], Level: strings.Count(code, ".") + 1, Kind: kind[code],
			Previous: round2(a.prev), Debit: round2(a.d), Credit: round2(a.c), Final: round2(fin),
		})
	}
	return led, nil
}

// isTopLevelOrKnown mantiene siempre visibles las cuentas del plan sembrado, incluso en cero; solo se
// ocultan las cuentas dinámicas (bancos) sin movimientos.
func isTopLevelOrKnown(code string) bool { return strings.Count(code, ".") < 4 }
