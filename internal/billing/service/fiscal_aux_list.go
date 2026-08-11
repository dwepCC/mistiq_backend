package service

import (
	"encoding/json"
	"strings"
	"time"

	"tukifac/internal/sales/nvdisplay"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// FiscalAuxListParams filtros operativos para listados CRE/CPE/RR (Fase 4A).
type FiscalAuxListParams struct {
	Q             string
	Status        string
	BillingStatus string
	Serie         string
	Correlativo   string
	PurchaseID    uint
	SourceSaleID  uint
	From          *time.Time
	To            *time.Time
}

// LinkedReversionSummary — ver retention_perception_link.go

// RevertedDocLine línea revertida dentro de una RR.
type RevertedDocLine struct {
	TipoDoc     string `json:"tipo_doc"`
	Serie       string `json:"serie"`
	Correlativo string `json:"correlativo"`
	Motivo      string `json:"motivo"`
}

// ReversionListItem fila RR con detalle parseado.
type ReversionListItem struct {
	database.TenantSunatReversion
	Details []RevertedDocLine `json:"details,omitempty"`
}

func reversionMatchKey(tipoDoc, serie, correlativo string) string {
	return strings.ToUpper(strings.TrimSpace(tipoDoc)) + "|" +
		strings.ToUpper(strings.TrimSpace(serie)) + "|" +
		strings.TrimSpace(correlativo)
}

func parseReversionPayloadDetails(payloadJSON string) []RevertedDocLine {
	payloadJSON = strings.TrimSpace(payloadJSON)
	if payloadJSON == "" {
		return nil
	}
	var raw struct {
		Details []struct {
			TipoDoc       string `json:"tipoDoc"`
			Serie         string `json:"serie"`
			Correlativo   string `json:"correlativo"`
			DesMotivoBaja string `json:"desMotivoBaja"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &raw); err != nil {
		return nil
	}
	out := make([]RevertedDocLine, 0, len(raw.Details))
	for _, d := range raw.Details {
		out = append(out, RevertedDocLine{
			TipoDoc:     strings.TrimSpace(d.TipoDoc),
			Serie:       strings.TrimSpace(d.Serie),
			Correlativo: strings.TrimSpace(d.Correlativo),
			Motivo:      strings.TrimSpace(d.DesMotivoBaja),
		})
	}
	return out
}

func (s *BillingService) buildReversionIndex() map[string]LinkedReversionSummary {
	var rows []database.TenantSunatReversion
	_ = s.db.Order("fec_comunicacion DESC").Find(&rows).Error
	idx := make(map[string]LinkedReversionSummary)
	for _, rev := range rows {
		for _, line := range parseReversionPayloadDetails(rev.PayloadJSON) {
			key := reversionMatchKey(line.TipoDoc, line.Serie, line.Correlativo)
			if key == "||" {
				continue
			}
			if _, exists := idx[key]; exists {
				continue
			}
			idx[key] = LinkedReversionSummary{
				ID:          rev.ID,
				Correlativo: rev.Correlativo,
				Status:      rev.Status,
				Ticket:      rev.Ticket,
				SunatCode:   rev.SunatCode,
				Motivo:      line.Motivo,
			}
		}
	}
	return idx
}

func (s *BillingService) findLinkedReversion(tipoDoc, serie, correlativo string) *LinkedReversionSummary {
	idx := s.buildReversionIndex()
	if rev, ok := idx[reversionMatchKey(tipoDoc, serie, correlativo)]; ok {
		revCopy := rev
		return &revCopy
	}
	return nil
}

func (s *BillingService) ListRetentionsFiltered(p FiscalAuxListParams) ([]RetentionListItem, error) {
	q := s.db.Model(&database.TenantRetention{})
	if p.PurchaseID > 0 {
		q = q.Where("purchase_id = ?", p.PurchaseID)
	}
	if strings.TrimSpace(p.Status) != "" {
		q = q.Where("status = ?", strings.TrimSpace(p.Status))
	}
	if strings.TrimSpace(p.Serie) != "" {
		q = q.Where("UPPER(series) = ?", strings.ToUpper(strings.TrimSpace(p.Serie)))
	}
	if strings.TrimSpace(p.Correlativo) != "" {
		q = q.Where("correlative = ?", strings.TrimSpace(p.Correlativo))
	}
	if p.From != nil {
		q = q.Where("fecha_emision >= ?", *p.From)
	}
	if p.To != nil {
		q = q.Where("fecha_emision <= ?", *p.To)
	}
	if qq := strings.TrimSpace(p.Q); qq != "" {
		like := "%" + qq + "%"
		q = q.Where("(series LIKE ? OR correlative LIKE ? OR proveedor_ruc LIKE ? OR proveedor_razon LIKE ?)", like, like, like, like)
	}
	var list []database.TenantRetention
	if err := q.Order("fecha_emision DESC, created_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	out := enrichRetentionListItemsExtended(s.db, list, s.buildReversionIndex())
	if bs := strings.TrimSpace(p.BillingStatus); bs != "" {
		filtered := make([]RetentionListItem, 0, len(out))
		for _, item := range out {
			if strings.EqualFold(item.BillingStatus, bs) {
				filtered = append(filtered, item)
			}
		}
		out = filtered
	}
	return out, nil
}

func (s *BillingService) ListPerceptionsFiltered(p FiscalAuxListParams) ([]PerceptionListItem, error) {
	q := s.db.Model(&database.TenantPerception{})
	if p.SourceSaleID > 0 {
		q = q.Where("source_sale_id = ?", p.SourceSaleID)
	}
	if strings.TrimSpace(p.Status) != "" {
		q = q.Where("status = ?", strings.TrimSpace(p.Status))
	}
	if strings.TrimSpace(p.Serie) != "" {
		q = q.Where("UPPER(series) = ?", strings.ToUpper(strings.TrimSpace(p.Serie)))
	}
	if strings.TrimSpace(p.Correlativo) != "" {
		q = q.Where("correlative = ?", strings.TrimSpace(p.Correlativo))
	}
	if p.From != nil {
		q = q.Where("fecha_emision >= ?", *p.From)
	}
	if p.To != nil {
		q = q.Where("fecha_emision <= ?", *p.To)
	}
	if qq := strings.TrimSpace(p.Q); qq != "" {
		like := "%" + qq + "%"
		q = q.Where("(series LIKE ? OR correlative LIKE ? OR proveedor_ruc LIKE ? OR proveedor_razon LIKE ?)", like, like, like, like)
	}
	var list []database.TenantPerception
	if err := q.Order("fecha_emision DESC, created_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	out := enrichPerceptionListItemsExtended(s.db, list, s.buildReversionIndex())
	if bs := strings.TrimSpace(p.BillingStatus); bs != "" {
		filtered := make([]PerceptionListItem, 0, len(out))
		for _, item := range out {
			if strings.EqualFold(item.BillingStatus, bs) {
				filtered = append(filtered, item)
			}
		}
		out = filtered
	}
	return out, nil
}

func (s *BillingService) ListReversionsFiltered(p FiscalAuxListParams) ([]ReversionListItem, error) {
	q := s.db.Model(&database.TenantSunatReversion{})
	if strings.TrimSpace(p.Status) != "" {
		q = q.Where("status = ?", strings.TrimSpace(p.Status))
	}
	if p.From != nil {
		q = q.Where("fec_comunicacion >= ?", *p.From)
	}
	if p.To != nil {
		q = q.Where("fec_comunicacion <= ?", *p.To)
	}
	if qq := strings.TrimSpace(p.Q); qq != "" {
		like := "%" + qq + "%"
		q = q.Where("(correlativo LIKE ? OR ticket LIKE ? OR payload_json LIKE ?)", like, like, like)
	}
	var list []database.TenantSunatReversion
	if err := q.Order("fec_comunicacion DESC, created_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	out := make([]ReversionListItem, len(list))
	for i, rev := range list {
		out[i] = ReversionListItem{TenantSunatReversion: rev, Details: parseReversionPayloadDetails(rev.PayloadJSON)}
	}
	if ss := strings.TrimSpace(p.Serie); ss != "" || strings.TrimSpace(p.Correlativo) != "" {
		filtered := make([]ReversionListItem, 0)
		for _, item := range out {
			for _, d := range item.Details {
				if ss != "" && !strings.EqualFold(d.Serie, ss) {
					continue
				}
				if cc := strings.TrimSpace(p.Correlativo); cc != "" && d.Correlativo != cc {
					continue
				}
				filtered = append(filtered, item)
				break
			}
		}
		out = filtered
	}
	return out, nil
}

func enrichRetentionListItemsExtended(db *gorm.DB, list []database.TenantRetention, revIdx map[string]LinkedReversionSummary) []RetentionListItem {
	base := enrichRetentionListItems(db, list)
	purchaseIDs := make([]uint, 0)
	for _, r := range list {
		if r.PurchaseID != nil && *r.PurchaseID > 0 {
			purchaseIDs = append(purchaseIDs, *r.PurchaseID)
		}
	}
	purchaseByID := map[uint]database.TenantPurchase{}
	if len(purchaseIDs) > 0 {
		var purchases []database.TenantPurchase
		_ = db.Where("id IN ?", purchaseIDs).Find(&purchases).Error
		for _, p := range purchases {
			purchaseByID[p.ID] = p
		}
	}
	for i := range base {
		r := &base[i]
		if rev, ok := revIdx[reversionMatchKey("20", r.Series, r.Correlative)]; ok {
			revCopy := rev
			r.LinkedReversion = &revCopy
		}
		if r.PurchaseID != nil {
			if p, ok := purchaseByID[*r.PurchaseID]; ok {
				r.OriginPurchaseLabel = nvdisplay.FormatDocumentNumber(p.Series, p.Number)
			}
		}
	}
	return base
}

func enrichPerceptionListItemsExtended(db *gorm.DB, list []database.TenantPerception, revIdx map[string]LinkedReversionSummary) []PerceptionListItem {
	base := enrichPerceptionListItems(db, list)
	saleIDs := make([]uint, 0)
	for _, p := range list {
		if p.SourceSaleID != nil && *p.SourceSaleID > 0 {
			saleIDs = append(saleIDs, *p.SourceSaleID)
		}
	}
	saleByID := map[uint]database.TenantSale{}
	if len(saleIDs) > 0 {
		var sales []database.TenantSale
		_ = db.Select("id, series, number, doc_type").Where("id IN ?", saleIDs).Find(&sales).Error
		for _, s := range sales {
			saleByID[s.ID] = s
		}
	}
	for i := range base {
		p := &base[i]
		if rev, ok := revIdx[reversionMatchKey("40", p.Series, p.Correlative)]; ok {
			revCopy := rev
			p.LinkedReversion = &revCopy
		}
		if p.SourceSaleID != nil {
			if s, ok := saleByID[*p.SourceSaleID]; ok {
				p.OriginSaleLabel = nvdisplay.FormatDocumentNumber(s.Series, s.Number)
			}
		}
	}
	return base
}

func (s *BillingService) BatchLinkedRetentionsByPurchaseIDs(ids []uint) map[uint]LinkedFiscalDocSummary {
	out := map[uint]LinkedFiscalDocSummary{}
	if len(ids) == 0 {
		return out
	}
	var rows []database.TenantRetention
	_ = s.db.Where("purchase_id IN ?", ids).Order("created_at DESC").Find(&rows).Error
	revIdx := s.buildReversionIndex()
	for _, r := range rows {
		if r.PurchaseID == nil {
			continue
		}
		pid := *r.PurchaseID
		if _, exists := out[pid]; exists {
			continue
		}
		bs := ""
		if r.SaleID != nil {
			bs = s.billingStatusForSaleID(*r.SaleID)
		}
		summary := linkedSummaryFromRetention(&r, bs)
		if rev, ok := revIdx[reversionMatchKey("20", r.Series, r.Correlative)]; ok {
			revCopy := rev
			summary.LinkedReversion = &revCopy
		}
		out[pid] = summary
	}
	return out
}

func (s *BillingService) BatchLinkedPerceptionsBySourceSaleIDs(ids []uint) map[uint]LinkedFiscalDocSummary {
	out := map[uint]LinkedFiscalDocSummary{}
	if len(ids) == 0 {
		return out
	}
	var rows []database.TenantPerception
	_ = s.db.Where("source_sale_id IN ?", ids).Order("created_at DESC").Find(&rows).Error
	revIdx := s.buildReversionIndex()
	for _, p := range rows {
		if p.SourceSaleID == nil {
			continue
		}
		sid := *p.SourceSaleID
		if _, exists := out[sid]; exists {
			continue
		}
		bs := ""
		if p.SaleID != nil {
			bs = s.billingStatusForSaleID(*p.SaleID)
		}
		summary := linkedSummaryFromPerception(&p, bs)
		if rev, ok := revIdx[reversionMatchKey("40", p.Series, p.Correlative)]; ok {
			revCopy := rev
			summary.LinkedReversion = &revCopy
		}
		out[sid] = summary
	}
	return out
}
