package service

import (
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

const (
	InstallmentPending = "pending"
	InstallmentPartial = "partial"
	InstallmentPaid    = "paid"
)

// InstallmentRow cuota de venta a crédito en CxC.
type InstallmentRow struct {
	ID            uint      `json:"id"`
	InstallmentNo int       `json:"installment_no"`
	DueDate       time.Time `json:"due_date"`
	Amount        float64   `json:"amount"`
	PaidAmount    float64   `json:"paid_amount"`
	DueAmount     float64   `json:"due_amount"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	IsOverdue     bool      `json:"is_overdue"`
}

func installmentDueAmount(row database.TenantSaleCreditInstallment) float64 {
	due := money.RoundDisplay(row.Amount - row.PaidAmount)
	if due < money.PaymentTolerance {
		return 0
	}
	return due
}

func toInstallmentRows(rows []database.TenantSaleCreditInstallment, now time.Time) []InstallmentRow {
	out := make([]InstallmentRow, 0, len(rows))
	for _, r := range rows {
		dueAmt := installmentDueAmount(r)
		status := r.Status
		if status == "" {
			status = InstallmentPending
		}
		if dueAmt <= 0 {
			status = InstallmentPaid
		} else if r.PaidAmount > money.PaymentTolerance {
			status = InstallmentPartial
		}
		out = append(out, InstallmentRow{
			ID:            r.ID,
			InstallmentNo: r.InstallmentNo,
			DueDate:       r.DueDate,
			Amount:        r.Amount,
			PaidAmount:    r.PaidAmount,
			DueAmount:     dueAmt,
			Currency:      r.Currency,
			Status:        status,
			IsOverdue:     dueAmt > 0 && r.DueDate.Before(now),
		})
	}
	return out
}

// applyPaymentToInstallmentsTx aplica un cobro a las cuotas (FIFO por número, o cuota preferida primero).
func applyPaymentToInstallmentsTx(tx *gorm.DB, saleID uint, amount float64, preferInstallmentID uint) error {
	amount = money.RoundDisplay(amount)
	if amount < money.PaymentTolerance {
		return nil
	}
	var rows []database.TenantSaleCreditInstallment
	if err := tx.Where("sale_id = ?", saleID).Order("installment_no ASC").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	remaining := amount
	applyTo := func(i int) {
		due := installmentDueAmount(rows[i])
		if due <= 0 || remaining < money.PaymentTolerance {
			return
		}
		pay := due
		if remaining < due {
			pay = remaining
		}
		rows[i].PaidAmount = money.RoundDisplay(rows[i].PaidAmount + pay)
		if installmentDueAmount(rows[i]) < money.PaymentTolerance {
			rows[i].PaidAmount = rows[i].Amount
			rows[i].Status = InstallmentPaid
		} else {
			rows[i].Status = InstallmentPartial
		}
		remaining = money.RoundDisplay(remaining - pay)
	}

	if preferInstallmentID > 0 {
		for i := range rows {
			if rows[i].ID == preferInstallmentID {
				applyTo(i)
				break
			}
		}
	}
	for i := range rows {
		if preferInstallmentID > 0 && rows[i].ID == preferInstallmentID {
			continue
		}
		applyTo(i)
		if remaining < money.PaymentTolerance {
			break
		}
	}

	for i := range rows {
		if err := tx.Model(&rows[i]).Updates(map[string]interface{}{
			"paid_amount": rows[i].PaidAmount,
			"status":      rows[i].Status,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func nextOpenInstallmentDue(rows []InstallmentRow) *time.Time {
	for _, r := range rows {
		if r.DueAmount > 0 {
			d := r.DueDate
			return &d
		}
	}
	return nil
}

func countPendingInstallments(rows []InstallmentRow) int {
	n := 0
	for _, r := range rows {
		if r.DueAmount > 0 {
			n++
		}
	}
	return n
}
