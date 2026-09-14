package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"

	"gorm.io/gorm"
)

// CreditInstallmentInput cuota al registrar venta a crédito.
type CreditInstallmentInput struct {
	DueDate string  `json:"due_date"`
	Amount  float64 `json:"amount"`
}

func normalizePaymentConditionCode(raw string) string {
	c := strings.TrimSpace(strings.ToLower(raw))
	if paymentcondition.IsCreditCode(c) {
		return paymentcondition.CodeCredit
	}
	return paymentcondition.CodeCash
}

func sumDirectPaymentsExclSpecial(payments []PaymentInput) float64 {
	var sum float64
	for _, p := range payments {
		if p.Amount <= 0 || p.Method == "" {
			continue
		}
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		sum += p.Amount
	}
	return money.RoundDisplay(sum)
}

func sumDirectPayments(payments []PaymentInput) float64 {
	var sum float64
	for _, p := range payments {
		if p.Amount <= 0 {
			continue
		}
		sum += p.Amount
	}
	return money.RoundDisplay(sum)
}

func parseInstallmentDueDate(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("fecha de cuota requerida")
	}
	if loc == nil {
		loc = time.Local
	}
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return time.Time{}, errors.New("fecha de cuota inválida")
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc), nil
}

// issueDate se usa para exigir que cada cuota venza estrictamente después de la fecha de
// emisión — SUNAT rechaza el comprobante (código 3267 "Fecha del pago único o de las cuotas
// no puede ser anterior o igual a la fecha de emisión") si alguna cuota queda igual o antes.
// Bug reportado: se pudo emitir F001-75 con la única cuota en la misma fecha que la emisión
// (ambas 2026-08-31) porque nada validaba esta relación — ni el frontend ni el backend.
func validateCreditInstallments(installments []CreditInstallmentInput, creditTarget float64, currency string, loc *time.Location, issueDate time.Time) ([]database.TenantSaleCreditInstallment, *time.Time, error) {
	if creditTarget <= 0.009 {
		return nil, nil, errors.New("el monto a crédito debe ser mayor a cero")
	}
	if len(installments) == 0 {
		return nil, nil, errors.New("indique al menos una cuota para la venta a crédito")
	}
	if strings.TrimSpace(currency) == "" {
		currency = "PEN"
	}
	var rows []database.TenantSaleCreditInstallment
	var sum float64
	var lastDue *time.Time
	for i, inst := range installments {
		due, err := parseInstallmentDueDate(inst.DueDate, loc)
		if err != nil {
			return nil, nil, fmt.Errorf("cuota %d: %w", i+1, err)
		}
		if !issueDate.IsZero() && !due.After(issueDate) {
			return nil, nil, fmt.Errorf(
				"cuota %d: la fecha de pago (%s) debe ser posterior a la fecha de emisión (%s)",
				i+1, due.Format("02/01/2006"), issueDate.Format("02/01/2006"),
			)
		}
		amt := money.RoundDisplay(inst.Amount)
		if amt <= 0 {
			return nil, nil, fmt.Errorf("cuota %d: monto inválido", i+1)
		}
		sum += amt
		lastDue = &due
		rows = append(rows, database.TenantSaleCreditInstallment{
			InstallmentNo: i + 1,
			DueDate:       due,
			Amount:        amt,
			Currency:      currency,
			Status:        "pending",
		})
	}
	if !money.PaidCoversTotal(sum, creditTarget) || sum > creditTarget+0.02 {
		return nil, nil, fmt.Errorf(
			"las cuotas (%.2f) deben igualar el saldo a crédito (%.2f)",
			money.RoundDisplay(sum),
			money.RoundDisplay(creditTarget),
		)
	}
	return rows, lastDue, nil
}

func (s *SaleService) GetCreditInstallments(saleID uint) ([]database.TenantSaleCreditInstallment, error) {
	var rows []database.TenantSaleCreditInstallment
	err := s.db.Where("sale_id = ?", saleID).Order("installment_no ASC").Find(&rows).Error
	return rows, err
}

func (s *SaleService) persistCreditInstallmentsTx(tx *gorm.DB, saleID uint, rows []database.TenantSaleCreditInstallment) error {
	for i := range rows {
		rows[i].SaleID = saleID
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}
