package summary

import (
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"tukifac/pkg/database"
)

// StaleAfterDays: un pendiente/error sin enviar con esta antigüedad (o más) se marca como atrasado.
const StaleAfterDays = 3

// Filter filtros de la tabla del panel central.
type Filter struct {
	From, To    string   // YYYY-MM-DD (día de emisión), vacíos = sin límite
	DocTypes    []string // 01,03,07,08,09,31; vacío = todos
	RUC         string   // coincidencia parcial
	Q           string   // nombre o slug, parcial
	OnlyPending bool     // solo tenants con algo por enviar (pending + error)
	StaleOnly   bool     // solo tenants con un pendiente de + días (sin importar el rango)
	Sort        string   // to_send (def), emitted, accepted, sent, rejected, name, ruc, oldest, last_issue, scanned
	Dir         string   // asc | desc; vacío = la natural de la columna
	Page        int
	PerPage     int
}

// TypeCount desglose por tipo de comprobante dentro del filtro.
type TypeCount struct {
	Emitted int `json:"emitted"`
	ToSend  int `json:"to_send"`
}

// Row una fila de la tabla (un tenant).
type Row struct {
	TenantID     uint   `json:"tenant_id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	RUC          string `json:"ruc"`
	TenantStatus string `json:"tenant_status"`

	Emitted  int `json:"emitted"`
	Accepted int `json:"accepted"`
	Pending  int `json:"pending"`
	Sent     int `json:"sent"`
	Error    int `json:"error"`
	Rejected int `json:"rejected"`
	// ToSend = lo que falta enviar a SUNAT: pending + error.
	ToSend int `json:"to_send"`

	ByType map[string]TypeCount `json:"by_type"`

	// Estado actual del tenant (no depende del rango de fechas).
	OpenToSend int `json:"open_to_send"`
	// Stale: su pendiente más antiguo tiene StaleAfterDays o más.
	Stale        bool       `json:"stale"`
	OldestOpenAt *time.Time `json:"oldest_open_at"`
	LastIssueAt  *time.Time `json:"last_issue_at"`
	ScannedAt    *time.Time `json:"scanned_at"`
	ScanError    string     `json:"scan_error,omitempty"`
}

// Totals suma de las filas filtradas (todas las páginas).
type Totals struct {
	Tenants  int `json:"tenants"`
	Emitted  int `json:"emitted"`
	Accepted int `json:"accepted"`
	Pending  int `json:"pending"`
	Sent     int `json:"sent"`
	Error    int `json:"error"`
	Rejected int `json:"rejected"`
	ToSend   int `json:"to_send"`
	// WithToSend: tenants con algo por enviar. ScanErrors: tenants cuyo último escaneo falló.
	WithToSend int `json:"with_to_send"`
	// Stale: tenants con un pendiente de StaleAfterDays o más.
	Stale      int `json:"stale"`
	ScanErrors int `json:"scan_errors"`
}

// Result respuesta paginada.
type Result struct {
	Items   []Row  `json:"items"`
	Total   int    `json:"total"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	Totals  Totals `json:"totals"`
	// OldestScan: el escaneo más viejo entre los tenants (indica cuán atrasado puede estar el dato).
	OldestScan *time.Time `json:"oldest_scan"`
}

type aggRow struct {
	TenantID uint
	DocCode  string
	St       string
	Cnt      int
}

// Summarize lee SOLO la BD central: tenants + resumen diario + salud.
func Summarize(central *gorm.DB, f Filter) (Result, error) {
	if f.PerPage <= 0 || f.PerPage > 200 {
		f.PerPage = 25
	}
	if f.Page <= 0 {
		f.Page = 1
	}

	tq := central.Model(&database.Tenant{}).Select("id, name, slug, ruc, status")
	if r := strings.TrimSpace(f.RUC); r != "" {
		tq = tq.Where("ruc LIKE ?", "%"+r+"%")
	}
	if q := strings.ToLower(strings.TrimSpace(f.Q)); q != "" {
		tq = tq.Where("(LOWER(name) LIKE ? OR LOWER(slug) LIKE ?)", "%"+q+"%", "%"+q+"%")
	}
	var tenants []database.Tenant
	if err := tq.Find(&tenants).Error; err != nil {
		return Result{}, err
	}

	aq := central.Table("tenant_fiscal_daily").
		Select("tenant_id, doc_code, billing_status AS st, SUM(cnt) AS cnt").
		Group("tenant_id, doc_code, billing_status")
	if f.From != "" {
		aq = aq.Where("day >= ?", f.From)
	}
	if f.To != "" {
		aq = aq.Where("day <= ?", f.To)
	}
	if len(f.DocTypes) > 0 {
		aq = aq.Where("doc_code IN ?", f.DocTypes)
	}
	var aggs []aggRow
	if err := aq.Scan(&aggs).Error; err != nil {
		return Result{}, err
	}

	var healths []database.TenantFiscalHealth
	if err := central.Find(&healths).Error; err != nil {
		return Result{}, err
	}
	healthBy := make(map[uint]database.TenantFiscalHealth, len(healths))
	for _, h := range healths {
		healthBy[h.TenantID] = h
	}

	rowBy := make(map[uint]*Row, len(tenants))
	rows := make([]*Row, 0, len(tenants))
	for _, t := range tenants {
		r := &Row{
			TenantID: t.ID, Name: t.Name, Slug: t.Slug, RUC: t.RUC, TenantStatus: t.Status,
			ByType: map[string]TypeCount{},
		}
		if h, ok := healthBy[t.ID]; ok {
			sc := h.ScannedAt
			r.ScannedAt, r.ScanError = &sc, h.ScanError
			r.OldestOpenAt, r.LastIssueAt = h.OldestOpenAt, h.LastIssueAt
			r.OpenToSend = h.OpenPending + h.OpenError
			r.Stale = h.OldestOpenAt != nil && r.OpenToSend > 0 && time.Since(*h.OldestOpenAt) >= StaleAfterDays*24*time.Hour
		}
		rowBy[t.ID] = r
		rows = append(rows, r)
	}
	for _, a := range aggs {
		r, ok := rowBy[a.TenantID]
		if !ok {
			continue
		}
		r.Emitted += a.Cnt
		tc := r.ByType[a.DocCode]
		tc.Emitted += a.Cnt
		switch a.St {
		case "accepted":
			r.Accepted += a.Cnt
		case "pending":
			r.Pending += a.Cnt
			tc.ToSend += a.Cnt
		case "error":
			r.Error += a.Cnt
			tc.ToSend += a.Cnt
		case "sent":
			r.Sent += a.Cnt
		case "rejected":
			r.Rejected += a.Cnt
		}
		r.ByType[a.DocCode] = tc
	}
	for _, r := range rows {
		r.ToSend = r.Pending + r.Error
	}

	if f.StaleOnly {
		kept := rows[:0]
		for _, r := range rows {
			if r.Stale {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if f.OnlyPending {
		kept := rows[:0]
		for _, r := range rows {
			if r.ToSend > 0 {
				kept = append(kept, r)
			}
		}
		rows = kept
	}

	totals := Totals{Tenants: len(rows)}
	var oldestScan *time.Time
	for _, r := range rows {
		totals.Emitted += r.Emitted
		totals.Accepted += r.Accepted
		totals.Pending += r.Pending
		totals.Sent += r.Sent
		totals.Error += r.Error
		totals.Rejected += r.Rejected
		totals.ToSend += r.ToSend
		if r.ToSend > 0 {
			totals.WithToSend++
		}
		if r.Stale {
			totals.Stale++
		}
		if r.ScanError != "" {
			totals.ScanErrors++
		}
		if r.ScannedAt != nil && (oldestScan == nil || r.ScannedAt.Before(*oldestScan)) {
			oldestScan = r.ScannedAt
		}
	}

	sortRows(rows, f.Sort, f.Dir)

	total := len(rows)
	start := (f.Page - 1) * f.PerPage
	if start > total {
		start = total
	}
	end := start + f.PerPage
	if end > total {
		end = total
	}
	items := make([]Row, 0, end-start)
	for _, r := range rows[start:end] {
		items = append(items, *r)
	}
	return Result{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage, Totals: totals, OldestScan: oldestScan}, nil
}

// Claves de orden aceptadas por Filter.Sort y su dirección por defecto. `desc` = de mayor a menor.
var sortDefaultDesc = map[string]bool{
	"to_send": true, "emitted": true, "accepted": true, "sent": true, "rejected": true,
	"last_issue": true,
	// Texto y fechas: "name"/"ruc" de A a Z; "oldest" el pendiente más viejo primero; "scanned" el
	// verificado hace más tiempo primero.
	"name": false, "ruc": false, "oldest": false, "scanned": false,
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpTimePtr compara fechas opcionales de menor a mayor. nilFirst decide dónde cae "sin dato".
func cmpTimePtr(a, b *time.Time, nilFirst bool) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		if nilFirst {
			return -1
		}
		return 1
	case b == nil:
		if nilFirst {
			return 1
		}
		return -1
	case a.Before(*b):
		return -1
	case b.Before(*a):
		return 1
	}
	return 0
}

// sortRows ordena la tabla por `key` (to_send por defecto) en la dirección `dir` ("asc" | "desc"; vacío o
// inválido = la natural de la columna, ver sortDefaultDesc). Los valores numéricos van de menor a mayor en
// "asc". En las columnas de fecha un tenant sin dato (sin pendientes, sin comprobantes) queda siempre al
// final, salvo "scanned", donde "nunca verificado" es lo más atrasado y va primero en su orden natural.
func sortRows(rows []*Row, key, dir string) {
	if _, ok := sortDefaultDesc[key]; !ok {
		key = "to_send"
	}
	desc := sortDefaultDesc[key]
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "asc":
		desc = false
	case "desc":
		desc = true
	}
	mul := 1
	if desc {
		mul = -1
	}

	// primary devuelve <0, 0, >0 con el orden ascendente natural de la columna.
	primary := func(a, b *Row) int {
		switch key {
		case "emitted":
			return mul * cmpInt(a.Emitted, b.Emitted)
		case "accepted":
			return mul * cmpInt(a.Accepted, b.Accepted)
		case "sent":
			return mul * cmpInt(a.Sent, b.Sent)
		case "rejected":
			return mul * cmpInt(a.Rejected, b.Rejected)
		case "name":
			return mul * strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "ruc":
			return mul * strings.Compare(a.RUC, b.RUC)
		case "oldest", "last_issue":
			ta, tb := a.OldestOpenAt, b.OldestOpenAt
			if key == "last_issue" {
				ta, tb = a.LastIssueAt, b.LastIssueAt
			}
			// Sin dato siempre al final, sea cual sea la dirección.
			if ta == nil || tb == nil {
				return cmpTimePtr(ta, tb, false)
			}
			return mul * cmpTimePtr(ta, tb, false)
		case "scanned":
			return mul * cmpTimePtr(a.ScannedAt, b.ScannedAt, true)
		default: // to_send
			if c := cmpInt(a.ToSend, b.ToSend); c != 0 {
				return mul * c
			}
			return mul * cmpInt(a.OpenToSend, b.OpenToSend)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if c := primary(rows[i], rows[j]); c != 0 {
			return c < 0
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
}
