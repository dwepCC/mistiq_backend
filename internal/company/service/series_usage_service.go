package service

import (
	"errors"
	"fmt"
	"strings"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// SeriesUsageInfo describe por qué una serie está bloqueada (fuente única de verdad).
type SeriesUsageInfo struct {
	Category string `json:"category"`
	Table    string `json:"usage_table"`
	Count    int64  `json:"usage_count"`
	Reason   string `json:"usage_reason"`
}

// SeriesUsageService determina si una serie documental está en uso real.
type SeriesUsageService struct {
	db *gorm.DB
}

func NewSeriesUsageService(db *gorm.DB) *SeriesUsageService {
	return &SeriesUsageService{db: db}
}

// IsSeriesInUse indica si existen documentos que consumieron la numeración de la serie.
func (s *SeriesUsageService) IsSeriesInUse(seriesID uint) (bool, SeriesUsageInfo, error) {
	var row database.TenantDocumentSeries
	if err := s.db.First(&row, seriesID).Error; err != nil {
		return false, SeriesUsageInfo{}, err
	}
	info, err := s.usageForSeries(&row)
	if err != nil {
		return false, SeriesUsageInfo{}, err
	}
	return info.Count > 0, info, nil
}

func (s *SeriesUsageService) usageForSeries(row *database.TenantDocumentSeries) (SeriesUsageInfo, error) {
	cat := normalizeSeriesCategory(row.Category)
	count, table, reason, err := s.countDocuments(row, cat)
	if err != nil {
		return SeriesUsageInfo{}, err
	}
	return SeriesUsageInfo{
		Category: cat,
		Table:    table,
		Count:    count,
		Reason:   reason,
	}, nil
}

func normalizeSeriesCategory(category string) string {
	c := strings.TrimSpace(strings.ToLower(category))
	if c == "" {
		return "venta"
	}
	return c
}

func (s *SeriesUsageService) countDocuments(row *database.TenantDocumentSeries, category string) (count int64, table, reason string, err error) {
	switch category {
	case "venta":
		return s.countModel(
			&database.TenantSale{},
			"series_id = ?",
			[]interface{}{row.ID},
			"tenant_sales",
			"Esta serie ya fue utilizada por documentos de venta.",
		)
	case "nota_credito":
		return s.countModel(
			&database.TenantSale{},
			"series_id = ? AND UPPER(TRIM(doc_type)) = ?",
			[]interface{}{row.ID, "NOTA_CREDITO"},
			"tenant_sales",
			"Esta serie ya fue utilizada por notas de crédito.",
		)
	case "nota_debito":
		return s.countModel(
			&database.TenantSale{},
			"series_id = ? AND UPPER(TRIM(doc_type)) = ?",
			[]interface{}{row.ID, "NOTA_DEBITO"},
			"tenant_sales",
			"Esta serie ya fue utilizada por notas de débito.",
		)
	case "guia_remision":
		return s.countModel(
			&database.TenantDespatch{},
			"series_id = ?",
			[]interface{}{row.ID},
			"tenant_despatches",
			"Esta serie ya fue utilizada por guías de remisión.",
		)
	case "guia_transportista":
		return s.countModel(
			&database.TenantDespatch{},
			"series_id = ?",
			[]interface{}{row.ID},
			"tenant_despatches",
			"Esta serie ya fue utilizada por guías transportista.",
		)
	case "retencion":
		return s.countModel(
			&database.TenantRetention{},
			"UPPER(TRIM(series)) = ?",
			[]interface{}{strings.ToUpper(strings.TrimSpace(row.Series))},
			"tenant_retentions",
			"Esta serie ya fue utilizada por comprobantes de retención.",
		)
	case "percepcion":
		return s.countModel(
			&database.TenantPerception{},
			"UPPER(TRIM(series)) = ?",
			[]interface{}{strings.ToUpper(strings.TrimSpace(row.Series))},
			"tenant_perceptions",
			"Esta serie ya fue utilizada por comprobantes de percepción.",
		)
	case "cotizacion":
		return s.countModel(
			&database.TenantQuotation{},
			"series_id = ?",
			[]interface{}{row.ID},
			"tenant_quotations",
			"Esta serie ya fue utilizada por cotizaciones.",
		)
	case "almacen":
		return s.countModel(
			&database.TenantInventoryDocument{},
			"series_id = ? AND status = ?",
			[]interface{}{row.ID, invsvc.DocumentStatusConfirmed},
			"tenant_inventory_documents",
			"Esta serie ya fue utilizada por documentos de almacén confirmados.",
		)
	case "compra":
		return 0, "", "", nil
	default:
		return s.countModel(
			&database.TenantSale{},
			"series_id = ?",
			[]interface{}{row.ID},
			"tenant_sales",
			fmt.Sprintf("Esta serie ya fue utilizada (categoría %q).", category),
		)
	}
}

func (s *SeriesUsageService) countModel(model interface{}, where string, args []interface{}, table, reason string) (int64, string, string, error) {
	var n int64
	q := s.db.Model(model).Where(where, args...)
	if err := q.Count(&n).Error; err != nil {
		return 0, table, reason, err
	}
	if n > 0 {
		return n, table, reason, nil
	}
	return 0, table, "", nil
}

// LockMessageWhenInUse devuelve el mensaje de error para operaciones bloqueadas.
func LockMessageWhenInUse(info SeriesUsageInfo) string {
	if strings.TrimSpace(info.Reason) != "" {
		return info.Reason
	}
	return "Esta serie ya está en uso y no puede modificarse."
}

// ErrSeriesInUse error tipado cuando la serie tiene documentos asociados.
var ErrSeriesInUse = errors.New("serie en uso")
