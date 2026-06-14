package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// saleSunatDocTypeForReference devuelve 01 (factura) o 03 (boleta/ticket) del comprobante referenciado.
func saleSunatDocTypeForReference(db *gorm.DB, sale *database.TenantSale) string {
	code := strings.TrimSpace(getSeriesSunatCode(db, sale.SeriesID))
	if code == "01" || code == "03" {
		return code
	}
	switch strings.ToUpper(strings.TrimSpace(sale.DocType)) {
	case "FACTURA":
		return "01"
	default:
		return "03"
	}
}

func allowedNoteSeriesPrefixes(noteSunatType, affectedType string) []string {
	switch strings.TrimSpace(noteSunatType) {
	case "08":
		if affectedType == "01" {
			return []string{"FD"}
		}
		if affectedType == "03" {
			return []string{"BD"}
		}
	case "07", "":
		if affectedType == "01" {
			return []string{"FC"}
		}
		if affectedType == "03" {
			return []string{"BC"}
		}
	}
	return nil
}

func seriesMatchesPrefixes(series string, prefixes []string) bool {
	u := strings.ToUpper(strings.TrimSpace(series))
	for _, p := range prefixes {
		if strings.HasPrefix(u, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}

func noteSeriesMatchesAffected(series, noteSunatType, affectedType string) bool {
	prefixes := allowedNoteSeriesPrefixes(noteSunatType, affectedType)
	if len(prefixes) == 0 {
		return false
	}
	return seriesMatchesPrefixes(series, prefixes)
}

func expectedNoteSeriesExample(noteSunatType, affectedType string) string {
	prefixes := allowedNoteSeriesPrefixes(noteSunatType, affectedType)
	if len(prefixes) == 0 {
		return "serie SUNAT"
	}
	return prefixes[0] + "01"
}

func affectedDocumentLabel(affectedType string) string {
	if affectedType == "01" {
		return "factura"
	}
	return "boleta"
}

func noteSeriesSelectionError(category, noteSunatType, affectedType string) error {
	doc := affectedDocumentLabel(affectedType)
	example := expectedNoteSeriesExample(noteSunatType, affectedType)
	prefixes := allowedNoteSeriesPrefixes(noteSunatType, affectedType)
	prefix := "FC/BC"
	if len(prefixes) > 0 {
		prefix = prefixes[0]
	}
	kind := "nota de crédito"
	if noteSunatType == "08" {
		kind = "nota de débito"
	}
	return fmt.Errorf(
		"no hay serie de %s para %s — configure una serie activa categoría %s con prefijo %s (ej. %s)",
		kind, doc, category, prefix, example,
	)
}

func findNoteSeriesForReferencedSale(
	db *gorm.DB,
	branchID uint,
	category string,
	noteSunatType string,
	orig database.TenantSale,
) (database.TenantDocumentSeries, error) {
	affected := saleSunatDocTypeForReference(db, &orig)
	prefixes := allowedNoteSeriesPrefixes(noteSunatType, affected)
	if len(prefixes) == 0 {
		return database.TenantDocumentSeries{}, fmt.Errorf("comprobante original no es factura ni boleta electrónica")
	}

	var candidates []database.TenantDocumentSeries
	if err := db.Where("branch_id = ? AND category = ? AND active = ?", branchID, category, true).
		Order("id ASC").
		Find(&candidates).Error; err != nil {
		return database.TenantDocumentSeries{}, err
	}
	for _, ser := range candidates {
		if seriesMatchesPrefixes(ser.Series, prefixes) {
			return ser, nil
		}
	}
	return database.TenantDocumentSeries{}, noteSeriesSelectionError(category, noteSunatType, affected)
}
