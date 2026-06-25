package paymentmethod

import "tukifac/pkg/paymentcondition"

const (
	CodeCredito           = paymentcondition.CodeCredit
	NameCredito           = paymentcondition.NameCredit
	DestinationReceivable = "receivable"
)

func IsReceivableCode(code string) bool { return paymentcondition.IsCreditCode(code) }
