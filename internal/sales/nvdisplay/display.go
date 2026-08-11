package nvdisplay

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const (
	NvStatusRegistrado = "registrado"
	NvStatusConvertida = "convertida"
	NvStatusAnulada    = "anulada"
)

// ElectronicIssue venta hijo 01/03 emitida desde una NV.
type ElectronicIssue struct {
	ID       uint
	ParentID uint
	DocType  string
	Series   string
	Number   string
}

// IsNotaVentaDocType indica comprobante interno NV (SUNAT 00).
func IsNotaVentaDocType(docType string) bool {
	u := strings.ToUpper(strings.TrimSpace(docType))
	return strings.Contains(u, "NOTA") && strings.Contains(u, "VENTA")
}

// FormatDocumentNumber replica formatSaleDocumentNumber del frontend.
func FormatDocumentNumber(series, numberRaw string) string {
	s := strings.TrimSpace(series)
	n := strings.TrimSpace(numberRaw)
	if n == "" {
		return s
	}
	if strings.Contains(n, "-") {
		return n
	}
	if len(n) > 0 && isDigitsOnly(n) && s != "" {
		if len(n) < 8 {
			n = strings.Repeat("0", 8-len(n)) + n
		}
		return s + "-" + n
	}
	if s != "" {
		return s + "-" + n
	}
	return n
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// LoadIssuesForParents carga hijos electrónicos por ID de NV padre.
func LoadIssuesForParents(db *gorm.DB, parentIDs []uint) map[uint]ElectronicIssue {
	out := make(map[uint]ElectronicIssue)
	if db == nil || len(parentIDs) == 0 {
		return out
	}
	var children []database.TenantSale
	if err := db.Select("id", "issued_from_nota_sale_id", "doc_type", "series", "number").
		Where("issued_from_nota_sale_id IN ?", parentIDs).
		Find(&children).Error; err != nil {
		return out
	}
	for _, ch := range children {
		if ch.IssuedFromNotaSaleID == nil || *ch.IssuedFromNotaSaleID == 0 {
			continue
		}
		out[*ch.IssuedFromNotaSaleID] = ElectronicIssue{
			ID:       ch.ID,
			ParentID: *ch.IssuedFromNotaSaleID,
			DocType:  ch.DocType,
			Series:   ch.Series,
			Number:   ch.Number,
		}
	}
	return out
}

// LoadDisplayNumbersBySaleID devuelve número comercial vigente por venta (hijo FE si NV convertida).
func LoadDisplayNumbersBySaleID(db *gorm.DB, saleIDs []uint) map[uint]string {
	out := make(map[uint]string)
	if db == nil || len(saleIDs) == 0 {
		return out
	}
	issues := LoadIssuesForParents(db, saleIDs)
	var sales []database.TenantSale
	if err := db.Select("id", "series", "number").Where("id IN ?", saleIDs).Find(&sales).Error; err != nil {
		return out
	}
	for _, s := range sales {
		if issue, ok := issues[s.ID]; ok {
			out[s.ID] = FormatDocumentNumber(issue.Series, issue.Number)
		} else {
			out[s.ID] = FormatDocumentNumber(s.Series, s.Number)
		}
	}
	return out
}

func applyIssueToSale(sale *database.TenantSale, issue *ElectronicIssue) {
	if sale == nil {
		return
	}
	id := sale.ID
	sale.DisplaySaleID = &id
	sale.DisplayDocType = sale.DocType
	sale.DisplaySeries = sale.Series
	sale.DisplayNumber = sale.Number

	if issue != nil {
		sid := issue.ID
		sale.ElectronicIssueSaleID = &sid
		sale.ElectronicIssueDocType = issue.DocType
		sale.ElectronicIssueSeries = issue.Series
		sale.ElectronicIssueNumber = issue.Number
		sale.DisplaySaleID = &sid
		sale.DisplayDocType = issue.DocType
		sale.DisplaySeries = issue.Series
		sale.DisplayNumber = issue.Number
	}

	if !IsNotaVentaDocType(sale.DocType) {
		return
	}
	switch {
	case sale.Status == "cancelled":
		sale.NvStatus = NvStatusAnulada
	case issue != nil:
		sale.NvStatus = NvStatusConvertida
	default:
		sale.NvStatus = NvStatusRegistrado
	}
}

// EnrichSales enriquece listados con estado NV y documento comercial vigente.
func EnrichSales(db *gorm.DB, sales []database.TenantSale) {
	if len(sales) == 0 {
		return
	}
	parentIDs := make([]uint, 0, len(sales))
	for i := range sales {
		parentIDs = append(parentIDs, sales[i].ID)
	}
	issues := LoadIssuesForParents(db, parentIDs)
	for i := range sales {
		var issue *ElectronicIssue
		if iss, ok := issues[sales[i].ID]; ok {
			copyIss := iss
			issue = &copyIss
		}
		applyIssueToSale(&sales[i], issue)
	}
}
