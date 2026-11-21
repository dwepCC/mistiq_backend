package exportacion

import (
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
)

func fmtDate(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Format("02/01/2006")
}

// PrintLines texto para el comprobante impreso (ticket/A4): leyenda de exportación y los datos de la operación. Sin datos → nil.
func PrintLines(r *database.TenantSaleExport) []string {
	if r == nil {
		return nil
	}
	if r.OperationTypeCode == salecurrency.OpExportHospedaje {
		return []string{
			LeyendaHospedaje,
			fmt.Sprintf("Huésped: %s · Doc. %s %s (emitido en %s) · Residencia: %s", r.GuestName, docTypeName(r.GuestDocType), r.GuestDocNumber, r.PassportCountry, r.ResidenceCountry),
			fmt.Sprintf("Ingreso al país: %s · Estadía: %s al %s (%d día(s))", fmtDate(r.EntryDate), fmtDate(r.CheckIn), fmtDate(r.CheckOut), r.StayDays),
		}
	}
	lines := []string{"EXPORTACIÓN DE SERVICIOS"}
	if r.CountryUse != "" {
		lines = append(lines, "País donde se usa el servicio: "+r.CountryUse)
	}
	return lines
}

// AppendToNotes antepone las líneas de exportación a las notas del comprobante.
func AppendToNotes(existing string, r *database.TenantSaleExport) string {
	lines := PrintLines(r)
	if len(lines) == 0 {
		return existing
	}
	block := strings.Join(lines, "\n")
	if strings.TrimSpace(existing) == "" {
		return block
	}
	return block + "\n" + existing
}

func docTypeName(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "7":
		return "Pasaporte"
	case "4":
		return "C. Extranjería"
	default:
		return "Doc."
	}
}
