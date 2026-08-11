package service

import (
	"encoding/json"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/sunatnote"

	"gorm.io/gorm"
)

// notePayloadRef lo que interesa del payload enviado al facturador.
type notePayloadRef struct {
	TipDocAfectado string `json:"tipDocAfectado"`
	NumDocfectado  string `json:"numDocfectado"`
	CodMotivo      string `json:"codMotivo"`
	DesMotivo      string `json:"desMotivo"`
}

// enrichNotesAffectedDoc completa el documento afectado y el tipo de nota en un listado.
//
// El listado mostraba «Venta #123», un identificador interno que no le dice nada a nadie y que
// no es lo que pide SUNAT: hace falta el tipo de comprobante, su serie y su correlativo.
//
// Se resuelve primero desde la venta original —es el dato vivo— y si no está (notas de otro
// sistema, ventas migradas) se cae al payload que se le envió al facturador, que es exactamente
// lo que se declaró.
func enrichNotesAffectedDoc(db *gorm.DB, sales []database.TenantSale) {
	if db == nil || len(sales) == 0 {
		return
	}

	noteIdx := make([]int, 0, len(sales))
	originalIDs := make([]uint, 0, len(sales))
	for i := range sales {
		if !isNoteDocType(sales[i].DocType) {
			continue
		}
		noteIdx = append(noteIdx, i)
		if id := sales[i].OriginalSaleID; id != nil && *id > 0 {
			originalIDs = append(originalIDs, *id)
		}
	}
	if len(noteIdx) == 0 {
		return
	}

	originals := loadOriginalsByID(db, originalIDs)
	payloads := loadNotePayloads(db, noteIdx, sales)

	for _, i := range noteIdx {
		sale := &sales[i]
		tipoDoc := noteSunatTipoDoc(sale.DocType)

		if id := sale.OriginalSaleID; id != nil && *id > 0 {
			if orig, ok := originals[*id]; ok {
				sale.AffectedDocSunatCode = printAffectedDocSunatType(orig.sale, orig.seriesSunatCode)
				sale.AffectedDocSeries = strings.TrimSpace(orig.sale.Series)
				sale.AffectedDocNumber = printAffectedDocNumber(orig.sale)
			}
		}

		if p, ok := payloads[sale.ID]; ok {
			if sale.AffectedDocNumber == "" {
				if v := strings.TrimSpace(p.TipDocAfectado); v != "" {
					sale.AffectedDocSunatCode = v
				}
				if v := strings.TrimSpace(p.NumDocfectado); v != "" {
					sale.AffectedDocNumber = v
					// El payload trae «B001-4»: la serie es lo que va antes del guion.
					if idx := strings.LastIndex(v, "-"); idx > 0 {
						sale.AffectedDocSeries = v[:idx]
					}
				}
			}
			sale.NoteTypeCode = strings.TrimSpace(p.CodMotivo)
			if reason := strings.TrimSpace(p.DesMotivo); reason != "" {
				sale.NoteTypeReason = reason
			}
		}

		// Sin motivo declarado se usa la descripción del catálogo, que es la que SUNAT espera.
		if sale.NoteTypeReason == "" {
			sale.NoteTypeReason = sunatnote.ReasonLabel(tipoDoc, sale.NoteTypeCode)
		}
		sale.AffectedDocType = sunatnote.AffectedDocLabel(sale.AffectedDocSunatCode)
	}
}

type originalDocRef struct {
	sale            *database.TenantSale
	seriesSunatCode string
}

func loadOriginalsByID(db *gorm.DB, ids []uint) map[uint]originalDocRef {
	out := make(map[uint]originalDocRef, len(ids))
	if len(ids) == 0 {
		return out
	}
	var originals []database.TenantSale
	if err := db.Where("id IN ?", ids).Find(&originals).Error; err != nil {
		return out
	}
	seriesIDs := make([]uint, 0, len(originals))
	for i := range originals {
		if originals[i].SeriesID > 0 {
			seriesIDs = append(seriesIDs, originals[i].SeriesID)
		}
	}
	sunatBySeries := make(map[uint]string, len(seriesIDs))
	if len(seriesIDs) > 0 {
		var series []database.TenantDocumentSeries
		if db.Where("id IN ?", seriesIDs).Find(&series).Error == nil {
			for i := range series {
				sunatBySeries[series[i].ID] = series[i].SunatCode
			}
		}
	}
	for i := range originals {
		out[originals[i].ID] = originalDocRef{
			sale:            &originals[i],
			seriesSunatCode: sunatBySeries[originals[i].SeriesID],
		}
	}
	return out
}

func loadNotePayloads(db *gorm.DB, noteIdx []int, sales []database.TenantSale) map[uint]notePayloadRef {
	out := make(map[uint]notePayloadRef, len(noteIdx))
	saleIDs := make([]uint, 0, len(noteIdx))
	for _, i := range noteIdx {
		saleIDs = append(saleIDs, sales[i].ID)
	}
	if len(saleIDs) == 0 {
		return out
	}
	var invoices []database.TenantInvoice
	if err := db.Select("sale_id", "note_payload_json").
		Where("sale_id IN ?", saleIDs).
		Where("note_payload_json IS NOT NULL AND note_payload_json <> ''").
		Find(&invoices).Error; err != nil {
		return out
	}
	for i := range invoices {
		var p notePayloadRef
		if json.Unmarshal([]byte(invoices[i].NotePayloadJSON), &p) == nil {
			out[invoices[i].SaleID] = p
		}
	}
	return out
}

func isNoteDocType(docType string) bool {
	dt := strings.ToUpper(strings.TrimSpace(docType))
	return dt == "NOTA_CREDITO" || dt == "NOTA_DEBITO"
}

// noteSunatTipoDoc "07" nota de crédito, "08" nota de débito.
func noteSunatTipoDoc(docType string) string {
	if strings.ToUpper(strings.TrimSpace(docType)) == "NOTA_DEBITO" {
		return "08"
	}
	return "07"
}
