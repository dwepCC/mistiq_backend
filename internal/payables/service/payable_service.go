// Package service implementa CxP (cuentas por pagar) — Fase 2, simétrico a
// internal/receivables/service (CxC), adaptado al flujo de compras/proveedores.
package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	cashbanksvc "tukifac/internal/cashbank/service"
	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// Estados de TenantPurchasePayable — misma convención que TenantSaleCreditInstallment
// (internal/receivables/service/installments.go): pending, partial, paid.
const (
	StatusPending = "pending"
	StatusPartial = "partial"
	StatusPaid    = "paid"
)

type PayableService struct {
	db *gorm.DB
}

func NewPayableService(db *gorm.DB) *PayableService {
	return &PayableService{db: db}
}

// PayInput un pago a proveedor contra una compra a crédito.
type PayInput struct {
	Amount        float64 `json:"amount"`
	Method        string  `json:"method"`
	Reference     string  `json:"reference,omitempty"`
	Notes         string  `json:"notes,omitempty"`
	CashSessionID *uint   `json:"cash_session_id,omitempty"`
	UserID        uint    `json:"-"`
}

// PayableRow fila de listado — compra a crédito con su saldo pendiente.
type PayableRow struct {
	PurchaseID       uint       `json:"purchase_id"`
	PurchaseNumber   string     `json:"purchase_number"`
	DocType          string     `json:"doc_type"`
	IssueDate        time.Time  `json:"issue_date"`
	DueDate          *time.Time `json:"due_date,omitempty"`
	ContactID        uint       `json:"contact_id"`
	ContactName      string     `json:"contact_name"`
	ContactDocNumber string     `json:"contact_doc_number"`
	OriginalAmount   float64    `json:"original_amount"`
	PaidAmount       float64    `json:"paid_amount"`
	Due              float64    `json:"due"`
	Status           string     `json:"status"`
	IsOverdue        bool       `json:"is_overdue"`
}

// Summary totales para el listado de CxP de una sucursal.
type Summary struct {
	TotalDue     float64 `json:"total_due"`
	CountOpen    int64   `json:"count_open"`
	CountOverdue int64   `json:"count_overdue"`
}

type ListFilter struct {
	BranchID  uint
	ContactID uint
	Status    string // open | overdue | all
	Page      int
	PageSize  int
}

// PayableBalance saldo corriente de una cuenta por pagar — sin cronograma de cuotas (a
// diferencia de CxC, una compra no tiene ese concepto hoy; ver comentario del struct en
// pkg/database/migrations.go), así que el "saldo" es simplemente original - pagado.
func PayableBalance(p database.TenantPurchasePayable) (paid, due float64) {
	paid = p.PaidAmount
	due = money.RoundDisplay(p.OriginalAmount - paid)
	if due < money.PaymentTolerance {
		due = 0
	}
	return
}

// listQuery compras a crédito (con payable) de una sucursal, uniendo el contacto/proveedor.
func (s *PayableService) listQuery(f ListFilter) *gorm.DB {
	q := s.db.Table("tenant_purchase_payables AS pay").
		Joins("JOIN tenant_purchases AS p ON p.id = pay.purchase_id").
		Where("p.status != ?", "cancelled")
	if f.BranchID > 0 {
		q = q.Where("p.branch_id = ?", f.BranchID)
	}
	if f.ContactID > 0 {
		q = q.Where("p.contact_id = ?", f.ContactID)
	}
	switch f.Status {
	case "open":
		q = q.Where("pay.status != ?", StatusPaid)
	case "overdue":
		q = q.Where("pay.status != ? AND p.due_date IS NOT NULL AND p.due_date < ?", StatusPaid, time.Now())
	}
	return q
}

// List compras a crédito con saldo, paginado.
func (s *PayableService) List(f ListFilter) ([]PayableRow, int64, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 || f.PageSize > 200 {
		f.PageSize = 50
	}

	var total int64
	if err := s.listQuery(f).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	type row struct {
		PurchaseID       uint
		PurchaseSeries   string
		PurchaseNumber   string
		DocType          string
		IssueDate        time.Time
		DueDate          *time.Time
		ContactID        uint
		ContactName      string
		ContactDocNumber string
		OriginalAmount   float64
		PaidAmount       float64
		Status           string
	}
	var rows []row
	// series/number se traen por separado y se concatenan en Go (no con `||`, que en MySQL es el
	// operador lógico OR, no concatenación — con SQLite en los tests sí concatenaba, por eso el
	// bug pasó inadvertido: el "comprobante" mostraba "1" en vez de "F999-4" en MySQL real).
	err := s.listQuery(f).
		Select(`p.id AS purchase_id, p.series AS purchase_series, p.number AS purchase_number, p.doc_type,
			p.issue_date, p.due_date, p.contact_id,
			COALESCE(c.trade_name, c.business_name, '') AS contact_name,
			COALESCE(c.doc_number, '') AS contact_doc_number,
			pay.original_amount, pay.paid_amount, pay.status`).
		Joins("LEFT JOIN tenant_contacts AS c ON c.id = p.contact_id").
		Order("p.due_date ASC, p.issue_date ASC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	now := time.Now()
	out := make([]PayableRow, 0, len(rows))
	for _, r := range rows {
		due := money.RoundDisplay(r.OriginalAmount - r.PaidAmount)
		if due < money.PaymentTolerance {
			due = 0
		}
		out = append(out, PayableRow{
			PurchaseID: r.PurchaseID, PurchaseNumber: r.PurchaseSeries + "-" + r.PurchaseNumber, DocType: r.DocType,
			IssueDate: r.IssueDate, DueDate: r.DueDate, ContactID: r.ContactID,
			ContactName: r.ContactName, ContactDocNumber: r.ContactDocNumber,
			OriginalAmount: r.OriginalAmount, PaidAmount: r.PaidAmount, Due: due,
			Status:    r.Status,
			IsOverdue: due > 0 && r.DueDate != nil && r.DueDate.Before(now),
		})
	}
	return out, total, nil
}

// Summary totales de CxP abierta para una sucursal.
func (s *PayableService) Summary(branchID uint) (*Summary, error) {
	rows, _, err := s.List(ListFilter{BranchID: branchID, Status: "open", Page: 1, PageSize: 100000})
	if err != nil {
		return nil, err
	}
	out := &Summary{}
	now := time.Now()
	for _, r := range rows {
		out.TotalDue += r.Due
		if r.Due > 0 {
			out.CountOpen++
			if r.DueDate != nil && r.DueDate.Before(now) {
				out.CountOverdue++
			}
		}
	}
	return out, nil
}

// Pay registra un pago a proveedor contra una compra a crédito — simétrico a
// ReceivableService.Collect, adaptado a compras: un único saldo corriente (no un arreglo de
// cuotas), y sin detracción/confirmación BN (no aplican a compras).
func (s *PayableService) Pay(purchaseID uint, in PayInput) error {
	if in.Amount <= 0 {
		return errors.New("el monto a pagar debe ser mayor a cero")
	}
	method := strings.TrimSpace(in.Method)
	if method == "" {
		return errors.New("debe indicar un método de pago")
	}

	var purchase database.TenantPurchase
	if err := s.db.First(&purchase, purchaseID).Error; err != nil {
		return errors.New("compra no encontrada")
	}
	if purchase.Status == "cancelled" {
		return errors.New("no se puede pagar una compra anulada")
	}

	var payable database.TenantPurchasePayable
	if err := s.db.Where("purchase_id = ?", purchaseID).First(&payable).Error; err != nil {
		return errors.New("esta compra no tiene una cuenta por pagar registrada (¿se registró a crédito?)")
	}

	_, due := PayableBalance(payable)
	if due < money.PaymentTolerance {
		return errors.New("esta compra no tiene saldo pendiente")
	}
	if money.RoundDisplay(in.Amount) > due+money.PaymentTolerance {
		return fmt.Errorf("el pago (S/ %.2f) supera el saldo pendiente (S/ %.2f)", money.RoundDisplay(in.Amount), due)
	}

	cbSvc := cashbanksvc.NewCashBankService(s.db)
	// ResolveCashSessionForPayable exige sesión de caja del usuario para CUALQUIER método —
	// mismo criterio que ResolveCashSessionForCollection en receivables (P0): un pago a
	// proveedor, efectivo o digital, siempre debe quedar vinculado a la Caja donde ocurrió.
	cashSessionID, err := cbSvc.ResolveCashSessionForPayable(purchase.BranchID, in.UserID, in.CashSessionID,
		[]cashbanksvc.PaymentLineInput{{Method: method, Amount: in.Amount}})
	if err != nil {
		return err
	}

	// Misma referencia que usa el pago inmediato de una compra al contado (docNumber) — así, si
	// la compra se anula después, PurchaseService.Void ya encuentra y revierte este pago con el
	// mismo mecanismo existente (por purchase_id para efectivo, por reference para banco), sin
	// necesitar un camino de reversión distinto para CxP.
	docNumber := purchase.Series + "-" + purchase.Number

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&database.TenantPurchasePayment{
			PurchaseID:    purchaseID,
			Method:        method,
			Amount:        in.Amount,
			Reference:     strings.TrimSpace(in.Reference),
			Notes:         in.Notes,
			CashSessionID: cashSessionID,
		}).Error; err != nil {
			return err
		}

		desc := "Pago proveedor " + docNumber
		if err := cbSvc.RecordExpensePayment(tx, method, in.Amount, cashSessionID, docNumber, desc, &purchaseID, in.UserID); err != nil {
			return err
		}

		newPaid := money.RoundDisplay(payable.PaidAmount + in.Amount)
		newStatus := StatusPartial
		newDue := money.RoundDisplay(payable.OriginalAmount - newPaid)
		if newDue < money.PaymentTolerance {
			newStatus = StatusPaid
			newPaid = payable.OriginalAmount // limpia redondeo acumulado
		}
		return tx.Model(&payable).Updates(map[string]interface{}{
			"paid_amount": newPaid,
			"status":      newStatus,
		}).Error
	})
}
