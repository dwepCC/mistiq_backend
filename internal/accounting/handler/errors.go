package handler

import "errors"

var (
	errBadJSON  = errors.New("JSON inválido")
	errBadDates = errors.New("fechas inválidas (use AAAA-MM-DD)")
	errBadRange = errors.New("la fecha final no puede ser menor que la inicial")
	errConfirm  = errors.New("aceptar la propuesta en SUNAT es irreversible: confirma la operación")
)
