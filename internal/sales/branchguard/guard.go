// Package branchguard concentra las guardas de aislamiento por sucursal (Fase 3A) de ventas,
// facturación y cobranzas: dado el ID de un registro, cargan su sucursal (o la heredan de la
// venta/sesión/compra de la que cuelga) y aplican branch.RecordReadable / branch.RecordWritable.
//
// Todas devuelven ErrNotFound cuando el acceso no corresponde (y también cuando el registro no
// existe), para que el handler responda 404 sin revelar qué IDs existen en otras sucursales.
//
// Solo se usan desde handlers HTTP. Cron, workers y jobs del facturador trabajan con la conexión
// base y NO pasan por aquí.
package branchguard

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"tukifac/pkg/branch"
	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// inferDB devuelve db sin el scope de sucursal para INFERIR la sucursal de un documento relacionado. Estas guardas son quienes
// deciden el acceso (RecordReadable/RecordWritable): si la búsqueda del documento ajeno se ejecutara acotada no lo
// encontraría y lo trataría como "sin sucursal / documento externo", dejándolo pasar (fail-open). Nunca devuelve datos al
// usuario: solo se usa el branch_id para decidir.
func inferDB(db *gorm.DB, reason string) *gorm.DB {
	if db == nil {
		return nil
	}
	return branchscope.System(db, "branchguard: "+reason)
}

// ErrNotFound: el registro no existe o pertenece a una sucursal a la que el usuario no accede.
var ErrNotFound = errors.New("no encontrado")

// SaleBranch sucursal de una venta/comprobante (0 = sin sucursal, se trata como del tenant).
func SaleBranch(db *gorm.DB, saleID uint) (uint, error) {
	if db == nil || saleID == 0 {
		return 0, ErrNotFound
	}
	var s database.TenantSale
	if err := db.Select("id", "branch_id").First(&s, saleID).Error; err != nil {
		return 0, ErrNotFound
	}
	return s.BranchID, nil
}

// SaleReadable: lectura por ID de una venta/comprobante.
func SaleReadable(c fiber.Ctx, db *gorm.DB, saleID uint) error {
	b, err := SaleBranch(db, saleID)
	if err != nil || !branch.RecordReadable(c, b) {
		return ErrNotFound
	}
	return nil
}

// SaleWritable: anular, emitir, reenviar, cobrar… solo sobre ventas de la sucursal activa.
func SaleWritable(c fiber.Ctx, db *gorm.DB, saleID uint) error {
	b, err := SaleBranch(db, saleID)
	if err != nil || !branch.RecordWritable(c, b) {
		return ErrNotFound
	}
	return nil
}

// SessionBranch sucursal de una sesión de caja.
func SessionBranch(db *gorm.DB, sessionID uint) (uint, error) {
	if db == nil || sessionID == 0 {
		return 0, ErrNotFound
	}
	var s database.TenantCashSession
	if err := db.Select("id", "branch_id").First(&s, sessionID).Error; err != nil {
		return 0, ErrNotFound
	}
	return s.BranchID, nil
}

// SessionWritable: la caja destino de un egreso/cobro debe ser de la sucursal activa.
func SessionWritable(c fiber.Ctx, db *gorm.DB, sessionID uint) error {
	b, err := SessionBranch(db, sessionID)
	if err != nil || !branch.RecordWritable(c, b) {
		return ErrNotFound
	}
	return nil
}

// CashMovementWritable: un movimiento de caja hereda la sucursal de su sesión.
func CashMovementWritable(c fiber.Ctx, db *gorm.DB, movementID uint) error {
	if db == nil || movementID == 0 {
		return ErrNotFound
	}
	var m database.TenantCashMovement
	if err := db.Select("id", "cash_session_id").First(&m, movementID).Error; err != nil {
		return ErrNotFound
	}
	return SessionWritable(c, db, m.CashSessionID)
}

// QuotationBranchMatches: la cotización origen debe ser de la sucursal en la que se crea la venta.
func QuotationBranchMatches(c fiber.Ctx, db *gorm.DB, quotationID, saleBranchID uint) error {
	if db == nil || quotationID == 0 {
		return ErrNotFound
	}
	var q database.TenantQuotation
	if err := db.Select("id", "branch_id").First(&q, quotationID).Error; err != nil {
		return ErrNotFound
	}
	if !branch.RecordReadable(c, q.BranchID) || (q.BranchID != 0 && q.BranchID != saleBranchID) {
		return ErrNotFound
	}
	return nil
}

// PurchaseBranchMatches: la compra origen de una retención debe ser de la sucursal de emisión.
func PurchaseBranchMatches(c fiber.Ctx, db *gorm.DB, purchaseID, branchID uint) error {
	if db == nil || purchaseID == 0 {
		return ErrNotFound
	}
	var p database.TenantPurchase
	if err := db.Select("id", "branch_id").First(&p, purchaseID).Error; err != nil {
		return ErrNotFound
	}
	if !branch.RecordWritable(c, p.BranchID) || (p.BranchID != 0 && p.BranchID != branchID) {
		return ErrNotFound
	}
	return nil
}

// SaleBranchMatches: la venta origen (de una guía/percepción) debe ser de la sucursal de emisión.
func SaleBranchMatches(c fiber.Ctx, db *gorm.DB, saleID, branchID uint) error {
	b, err := SaleBranch(db, saleID)
	if err != nil || !branch.RecordWritable(c, b) || (b != 0 && b != branchID) {
		return ErrNotFound
	}
	return nil
}

// ReadableFunc captura (sin retener el fiber.Ctx, que Fiber reutiliza) la regla de lectura del
// usuario, para usarla dentro de streams SSE o goroutines.
func ReadableFunc(c fiber.Ctx) func(branchID uint) bool {
	active := branch.ActiveBranchID(c)
	admin := branch.IsBranchAdmin(c)
	allowed := append([]uint(nil), branch.AllowedBranchIDs(c)...)
	return func(id uint) bool {
		if id == 0 || admin || id == active {
			return true
		}
		for _, a := range allowed {
			if a == id {
				return true
			}
		}
		return false
	}
}

// --- documentos fiscales sin branch_id propio (heredan de la compra/venta/serie) ---

// seriesBranch sucursal de una serie por su nombre (las series de retención/percepción/NC/guía se
// crean por sucursal). Sin coincidencia devuelve 0. Si el nombre se repite en varias sucursales
// devuelve la primera: la serie ya la validó ValidateForBranch al emitir.
func seriesBranch(db *gorm.DB, series string) uint {
	db = inferDB(db, "inferir sucursal de una serie")
	series = strings.ToUpper(strings.TrimSpace(series))
	if series == "" {
		return 0
	}
	var s database.TenantDocumentSeries
	if err := db.Select("id", "branch_id").Where("UPPER(series) = ?", series).Order("id ASC").First(&s).Error; err != nil {
		return 0
	}
	return s.BranchID
}

// RetentionBranch: compra origen > serie.
func RetentionBranch(db *gorm.DB, r database.TenantRetention) uint {
	db = inferDB(db, "inferir sucursal de una retención")
	if r.PurchaseID != nil && *r.PurchaseID > 0 {
		var p database.TenantPurchase
		if err := db.Select("id", "branch_id").First(&p, *r.PurchaseID).Error; err == nil && p.BranchID > 0 {
			return p.BranchID
		}
	}
	return seriesBranch(db, r.Series)
}

// PerceptionBranch: venta origen > serie.
func PerceptionBranch(db *gorm.DB, p database.TenantPerception) uint {
	db = inferDB(db, "inferir sucursal de una percepción")
	if p.SourceSaleID != nil && *p.SourceSaleID > 0 {
		if b, err := SaleBranch(db, *p.SourceSaleID); err == nil && b > 0 {
			return b
		}
	}
	return seriesBranch(db, p.Series)
}

// RetentionReadable / PerceptionReadable por ID de registro.
func RetentionReadable(c fiber.Ctx, db *gorm.DB, id uint) error {
	var r database.TenantRetention
	if db == nil || db.First(&r, id).Error != nil || !branch.RecordReadable(c, RetentionBranch(db, r)) {
		return ErrNotFound
	}
	return nil
}

func PerceptionReadable(c fiber.Ctx, db *gorm.DB, id uint) error {
	var p database.TenantPerception
	if db == nil || db.First(&p, id).Error != nil || !branch.RecordReadable(c, PerceptionBranch(db, p)) {
		return ErrNotFound
	}
	return nil
}

// DespatchReadable por ID de guía (tiene branch_id propio).
func DespatchReadable(c fiber.Ctx, db *gorm.DB, id uint) error {
	var d database.TenantDespatch
	if db == nil || db.Select("id", "branch_id").First(&d, id).Error != nil || !branch.RecordReadable(c, d.BranchID) {
		return ErrNotFound
	}
	return nil
}

// DocLine un comprobante referenciado dentro del payload de una comunicación de baja/reversión.
type DocLine struct {
	TipoDoc     string
	Serie       string
	Correlativo string
}

// PayloadLines extrae las líneas (tipo/serie/correlativo) del payload guardado de una
// comunicación de baja o reversión. ok=false si el payload no se puede interpretar.
func PayloadLines(payloadJSON string) (lines []DocLine, ok bool) {
	payloadJSON = strings.TrimSpace(payloadJSON)
	if payloadJSON == "" {
		return nil, false
	}
	var raw struct {
		Details []struct {
			TipoDoc     string `json:"tipoDoc"`
			Serie       string `json:"serie"`
			Correlativo string `json:"correlativo"`
		} `json:"details"`
	}
	if json.Unmarshal([]byte(payloadJSON), &raw) != nil {
		return nil, false
	}
	for _, d := range raw.Details {
		lines = append(lines, DocLine{TipoDoc: d.TipoDoc, Serie: d.Serie, Correlativo: d.Correlativo})
	}
	return lines, true
}

// DocBranch sucursal del comprobante referenciado por tipo/serie/correlativo. found=false si no
// existe localmente (documento de otro sistema): se trata como del tenant (0).
func DocBranch(db *gorm.DB, tipoDoc, serie, correlativo string) (branchID uint, found bool) {
	db = inferDB(db, "inferir sucursal de un comprobante por serie y correlativo")
	serie = strings.ToUpper(strings.TrimSpace(serie))
	correlativo = strings.TrimSpace(correlativo)
	if db == nil || serie == "" || correlativo == "" {
		return 0, false
	}
	switch strings.TrimSpace(tipoDoc) {
	case "20":
		var r database.TenantRetention
		if db.Where("UPPER(series) = ? AND correlative = ?", serie, correlativo).First(&r).Error == nil {
			return RetentionBranch(db, r), true
		}
		return 0, false
	case "40":
		var p database.TenantPerception
		if db.Where("UPPER(series) = ? AND correlative = ?", serie, correlativo).First(&p).Error == nil {
			return PerceptionBranch(db, p), true
		}
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimLeft(correlativo, "0"), 10, 32)
	if err != nil {
		return 0, false
	}
	var s database.TenantSale
	if db.Select("id", "branch_id").Where("UPPER(series) = ? AND correlative = ?", serie, uint(n)).First(&s).Error == nil {
		return s.BranchID, true
	}
	return 0, false
}

// LinesReadable: todas las líneas referenciadas son legibles. Un payload ilegible solo lo ve
// quien ve todas las sucursales (fail-closed).
func LinesReadable(c fiber.Ctx, db *gorm.DB, payloadJSON string) bool {
	lines, ok := PayloadLines(payloadJSON)
	if !ok {
		return branch.IsBranchAdmin(c)
	}
	for _, l := range lines {
		if b, found := DocBranch(db, l.TipoDoc, l.Serie, l.Correlativo); found && !branch.RecordReadable(c, b) {
			return false
		}
	}
	return true
}

// LineWritable: el comprobante referenciado (si existe localmente) es de la sucursal activa.
func LineWritable(c fiber.Ctx, db *gorm.DB, tipoDoc, serie, correlativo string) bool {
	b, found := DocBranch(db, tipoDoc, serie, correlativo)
	return !found || branch.RecordWritable(c, b)
}

// CoversAllBranches: el usuario ve todas las sucursales activas del tenant (administrador, o
// único con acceso a todas). Los resúmenes diarios son un documento del RUC completo.
func CoversAllBranches(c fiber.Ctx, db *gorm.DB) bool {
	if branch.IsBranchAdmin(c) {
		return true
	}
	if db == nil {
		return false
	}
	var ids []uint
	if db.Model(&database.TenantBranch{}).Where("active = ?", true).Pluck("id", &ids).Error != nil {
		return false
	}
	readable := ReadableFunc(c)
	for _, id := range ids {
		if !readable(id) {
			return false
		}
	}
	return len(ids) > 0
}

// InScope: un registro de la sucursal recBranch entra en un listado filtrado por filter
// (0 = todas, solo para quien ve todo). Los registros sin sucursal (0) son del tenant.
func InScope(filter, recBranch uint) bool {
	return filter == 0 || recBranch == 0 || recBranch == filter
}

// LinesInScope igual que InScope para comunicaciones de baja/reversión: todos los comprobantes
// que referencian deben caer en el filtro. Un payload ilegible solo entra con filter=0.
func LinesInScope(db *gorm.DB, payloadJSON string, filter uint) bool {
	if filter == 0 {
		return true
	}
	lines, ok := PayloadLines(payloadJSON)
	if !ok {
		return false
	}
	for _, l := range lines {
		if b, found := DocBranch(db, l.TipoDoc, l.Serie, l.Correlativo); found && !InScope(filter, b) {
			return false
		}
	}
	return true
}

// DespatchNumberReadable: la guía "SERIE-N" (si existe) debe ser legible. Si no existe, true (el
// servicio responde su propio "no encontrada").
func DespatchNumberReadable(c fiber.Ctx, db *gorm.DB, number string) bool {
	parts := strings.SplitN(strings.TrimSpace(number), "-", 2)
	if db == nil || len(parts) != 2 {
		return true
	}
	n, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return true
	}
	var d database.TenantDespatch
	if db.Select("id", "branch_id").Where("UPPER(series) = ? AND correlative = ?", strings.ToUpper(strings.TrimSpace(parts[0])), uint(n)).First(&d).Error != nil {
		return true
	}
	return branch.RecordReadable(c, d.BranchID)
}
