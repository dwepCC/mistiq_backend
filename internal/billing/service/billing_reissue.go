package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"gorm.io/gorm"
)

// maxFiscalObservationLen tope de caracteres de la observación que viaja como
// cbc:Note del comprobante. SUNAT rechaza el XML si se excede.
const maxFiscalObservationLen = 250

// ReissueInput datos de la corrección solicitada por soporte.
type ReissueInput struct {
	SaleID uint
	// IssueDate nueva fecha de emisión (día calendario America/Lima).
	IssueDate time.Time
	// Observation texto opcional que se envía a SUNAT como observación del
	// comprobante. Vacío deja la observación actual sin tocar.
	Observation string
	SetObservation bool
	// Reason motivo de la corrección; obligatorio, queda en la auditoría.
	Reason string
	// Actor identidad del soporte que ejecuta (superadmin del panel central).
	ActorID    uint
	ActorEmail string
	ClientIP   string
}

// ReissueSale reenvía un comprobante a SUNAT con otra fecha de emisión,
// conservando su numeración.
//
// Resuelve el caso de comprobantes emitidos contra SUNAT beta que después hay
// que llevar a producción: la aceptación previa no vale en el ambiente nuevo,
// pero el correlativo sí quedó comprometido con el cliente final. No se genera
// una venta nueva: se actualizan fecha y observación, y el payload se
// reconstruye desde la venta al reenviar.
//
// El llamador debe haber verificado que la sesión es de acceso maestro.
func (s *BillingService) ReissueSale(in ReissueInput) (*ManualBillingResult, error) {
	if in.SaleID == 0 {
		return nil, errors.New("venta requerida")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, errors.New("el motivo de la corrección es obligatorio")
	}
	if s.centralTenantID == 0 {
		return nil, errors.New("tenant requerido")
	}

	var sale database.TenantSale
	if err := s.db.First(&sale, in.SaleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("venta no encontrada")
		}
		return nil, err
	}

	// Solo comprobantes electrónicos: una nota de venta (00) no se envía a SUNAT.
	if !IsElectronicSunatCode(s.saleSunatCode(in.SaleID)) {
		return nil, errors.New("este documento no es un comprobante electrónico")
	}

	newDate, err := validateReissueDate(in.IssueDate)
	if err != nil {
		return nil, err
	}

	observation := strings.TrimSpace(in.Observation)
	if in.SetObservation && len([]rune(observation)) > maxFiscalObservationLen {
		return nil, fmt.Errorf("la observación no puede superar %d caracteres", maxFiscalObservationLen)
	}

	previousDate := sale.IssueDate

	// ReissuedFromDate conserva la fecha del envío original: en una segunda
	// corrección no debe sobrescribirse con la fecha ya corregida.
	updates := map[string]interface{}{
		"issue_date":        newDate,
		"reissued_at":       time.Now(),
		"reissue_reason":    strings.TrimSpace(in.Reason),
		"reissued_by_email": in.ActorEmail,
		"reissue_count":     sale.ReissueCount + 1,
	}
	if sale.ReissuedFromDate == nil {
		updates["reissued_from_date"] = previousDate
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.TenantSale{}).Where("id = ?", in.SaleID).Updates(updates).Error; err != nil {
			return err
		}
		if !in.SetObservation {
			return nil
		}
		return upsertFiscalObservation(tx, in.SaleID, observation)
	}); err != nil {
		return nil, fmt.Errorf("actualizando la venta: %w", err)
	}

	s.auditReissue(in, &sale, previousDate, newDate)

	// Autoriza al facturador a regenerar el XML de un documento ya aceptado. Se
	// limita a esta emisión para que un envío posterior con la misma instancia no
	// herede el permiso.
	s.reissueMode = true
	defer func() { s.reissueMode = false }()

	inv, sendErr := s.processSendToSUNAT(in.SaleID, s.centralTenantID, s.tenantSlug, FiscalSourceReissue, true)
	if sendErr != nil {
		return s.buildManualResultFromSend(in.SaleID, inv, sendErr)
	}
	inv, sendErr = s.waitForFacturadorOutcome(in.SaleID, manualFiscalWaitTimeout)
	return s.buildManualResultFromSend(in.SaleID, inv, sendErr)
}

// validateReissueDate acota la nueva fecha al plazo que SUNAT admite para el
// envío. El margen es configurable (SUNAT_MAX_BACKDATE_DAYS) porque el plazo lo
// fija SUNAT y ha cambiado con el tiempo.
func validateReissueDate(d time.Time) (time.Time, error) {
	if d.IsZero() {
		return time.Time{}, errors.New("la fecha de emisión es obligatoria")
	}

	maxBackdate := 3
	if config.AppConfig != nil && config.AppConfig.SunatMaxBackdateDays > 0 {
		maxBackdate = config.AppConfig.SunatMaxBackdateDays
	}

	today := saas.CalendarDateLima(saas.NowLima())
	target := saas.CalendarDateLima(d)

	if target.After(today) {
		return time.Time{}, errors.New("la fecha de emisión no puede ser futura")
	}
	earliest := today.AddDate(0, 0, -maxBackdate)
	if target.Before(earliest) {
		return time.Time{}, fmt.Errorf(
			"la fecha no puede ser anterior a %s: SUNAT admite hasta %d día(s) de atraso",
			earliest.Format("02/01/2006"), maxBackdate,
		)
	}

	// Se emite a mediodía por la misma razón que el envío normal: evita que un
	// desfase de zona horaria corra el comprobante al día anterior o siguiente.
	return time.Date(target.Year(), target.Month(), target.Day(), 12, 0, 0, 0, target.Location()), nil
}

func upsertFiscalObservation(tx *gorm.DB, saleID uint, observation string) error {
	var profile database.TenantSaleFiscalProfile
	err := tx.Where("sale_id = ?", saleID).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&database.TenantSaleFiscalProfile{
			SaleID:             saleID,
			FiscalObservations: observation,
		}).Error
	}
	if err != nil {
		return err
	}
	return tx.Model(&database.TenantSaleFiscalProfile{}).
		Where("sale_id = ?", saleID).
		Update("fiscal_observations", observation).Error
}

// auditReissue deja el rastro en la BD central, fuera del alcance del tenant.
// Se escribe antes de enviar: si el envío falla, igual queda registrado que
// soporte alteró la fecha de emisión del comprobante.
func (s *BillingService) auditReissue(in ReissueInput, sale *database.TenantSale, previousDate, newDate time.Time) {
	payload := map[string]interface{}{
		"sale_id":       in.SaleID,
		"document":      sale.Number,
		"doc_type":      sale.DocType,
		"previous_date": previousDate.Format(time.RFC3339),
		"new_date":      newDate.Format(time.RFC3339),
		"reason":        strings.TrimSpace(in.Reason),
		"reissue_count": sale.ReissueCount + 1,
		"tenant_slug":   s.tenantSlug,
	}
	if in.SetObservation {
		payload["observation"] = strings.TrimSpace(in.Observation)
	}

	_ = database.CentralDB.Create(&database.AuditLog{
		TenantID:  s.centralTenantID,
		UserID:    in.ActorID,
		Action:    "sale_fiscal_reissue",
		Entity:    "tenant_sale",
		EntityID:  in.SaleID,
		Payload:   saas.MetaJSON(payload),
		IPAddress: in.ClientIP,
	}).Error
}
