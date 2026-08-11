package exchangerate

import (
	"errors"
	"fmt"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type repository struct {
	db *gorm.DB
}

func newRepository(db *gorm.DB) *repository {
	if db == nil {
		return &repository{}
	}
	return &repository{db: db}
}

func (r *repository) findByDate(date string) (*database.SaasExchangeRate, error) {
	if r.db == nil {
		return nil, gorm.ErrRecordNotFound
	}
	var row database.SaasExchangeRate
	err := r.db.Where("rate_date = ?", date).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *repository) findLastConfirmedBefore(date string) (*database.SaasExchangeRate, error) {
	if r.db == nil {
		return nil, gorm.ErrRecordNotFound
	}
	var row database.SaasExchangeRate
	err := r.db.Where("status = ? AND rate_date < ?", StatusConfirmed, date).
		Order("rate_date DESC").
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *repository) save(row *database.SaasExchangeRate) error {
	if r.db == nil {
		return errors.New("central database not available")
	}
	return r.db.Save(row).Error
}

func (r *repository) upsertConfirmed(date string, venta, compra float64, effectiveDate string, fetchedAt time.Time) error {
	row, err := r.findByDate(date)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = &database.SaasExchangeRate{RateDate: date}
	} else if err != nil {
		return err
	}
	row.SaleRate = venta
	row.BuyRate = compra
	row.Status = StatusConfirmed
	row.Source = SourceApiPeru
	row.EffectiveDate = effectiveDate
	row.FetchedAt = &fetchedAt
	row.LastAttemptAt = &fetchedAt
	row.NextRetryAt = nil
	row.ErrorMessage = ""
	return r.save(row)
}

func (r *repository) upsertPendingFallback(in pendingFallbackInput) error {
	row, err := r.findByDate(in.RateDate)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = &database.SaasExchangeRate{RateDate: in.RateDate}
	} else if err != nil {
		return err
	}
	// No sobrescribir un confirmed con pending.
	if row.Status == StatusConfirmed && row.EffectiveDate == in.RateDate {
		return nil
	}
	row.SaleRate = in.SaleRate
	row.BuyRate = in.BuyRate
	row.Status = in.Status
	row.Source = in.Source
	row.EffectiveDate = in.EffectiveDate
	row.LastAttemptAt = &in.AttemptAt
	row.NextRetryAt = in.NextRetryAt
	row.AttemptCount = in.AttemptCount
	row.ErrorMessage = in.ErrorMessage
	if in.FetchedAt != nil {
		row.FetchedAt = in.FetchedAt
	}
	return r.save(row)
}

type pendingFallbackInput struct {
	RateDate      string
	SaleRate      float64
	BuyRate       float64
	Status        string
	Source        string
	EffectiveDate string
	AttemptAt     time.Time
	NextRetryAt   *time.Time
	AttemptCount  int
	ErrorMessage  string
	FetchedAt     *time.Time
}

func rowToQueryResult(requestedDate string, row *database.SaasExchangeRate) *QueryResult {
	if row == nil {
		return nil
	}
	esFallback := row.Status == StatusFallback || row.Status == StatusPending ||
		(row.EffectiveDate != "" && row.EffectiveDate != requestedDate)
	out := &QueryResult{
		Success:       row.SaleRate > 0,
		Fecha:         requestedDate,
		FechaEfectiva: row.EffectiveDate,
		Moneda:        "USD",
		Venta:         row.SaleRate,
		Compra:        row.BuyRate,
		Fuente:        SourceCacheCentral,
		Status:        row.Status,
		EsFallback:    esFallback,
	}
	if row.Status == StatusConfirmed && row.EffectiveDate == requestedDate {
		out.EsFallback = false
		out.Fuente = SourceApiPeru
	}
	if row.NextRetryAt != nil && row.Status == StatusPending {
		s := row.NextRetryAt.Format(time.RFC3339)
		out.ProximoReintento = &s
	}
	if esFallback && row.EffectiveDate != "" {
		out.Mensaje = fmt.Sprintf(
			"Se utiliza el tipo de cambio del %s porque SUNAT aún no publica el correspondiente al %s.",
			formatDateDMY(row.EffectiveDate), formatDateDMY(requestedDate),
		)
	}
	if row.Status == StatusUnavailable {
		out.Success = false
		out.ErrorMessage = row.ErrorMessage
		if out.ErrorMessage == "" {
			out.ErrorMessage = "tipo de cambio no disponible"
		}
	}
	return out
}

func formatDateDMY(ymd string) string {
	t, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		return ymd
	}
	return t.Format("02/01/2006")
}
