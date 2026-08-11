// Package datespe centraliza el manejo de fecha y hora de Perú para los comprobantes.
package datespe

import "time"

// Location: zona horaria de Perú. Cae a la local si la máquina no tiene tzdata.
func Location() *time.Location {
	if loc, err := time.LoadLocation("America/Lima"); err == nil && loc != nil {
		return loc
	}
	return time.Local
}

// IssueTime: hora a imprimir en un comprobante, en hora de Perú.
//
// Ojo: NO usar la fecha de emisión para esto. Esa fecha se fija a las 12:00 a propósito
// (para que un cambio de zona horaria no mueva el documento de día), así que derivar de
// ella la hora hacía que todos los comprobantes salieran con «12:00:00». La hora real del
// acto es el timestamp de creación, que además hay que convertir porque el servidor puede
// correr en UTC.
//
// Devuelve "" si no hay timestamp: mejor sin hora que con una hora falsa.
func IssueTime(createdAt time.Time) string {
	if createdAt.IsZero() {
		return ""
	}
	return createdAt.In(Location()).Format("15:04:05")
}
