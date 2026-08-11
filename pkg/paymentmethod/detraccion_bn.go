package paymentmethod

import "tukifac/pkg/taxpayment"

const (
	CodeDetraccionBN      = taxpayment.CodeDetraccionBN
	NameDetraccionBN      = taxpayment.NameDetraccionBN
	DestinationDetraction = "detraction"
)

func IsDetractionCode(code string) bool { return taxpayment.IsDetractionCode(code) }
