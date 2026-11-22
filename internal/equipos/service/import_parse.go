package service

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// ── Celdas ─────────────────────────────────────────────────────────────────
// El frontend lee el .xlsx con hucre y manda las filas tal cual: texto, número, booleano, null y las fechas como
// texto ISO. Todo lo demás (normalizar, validar, conciliar) ocurre aquí.

func cellAt(row []any, i int) any {
	if i < 0 || i >= len(row) {
		return nil
	}
	return row[i]
}

// cellStr texto recortado; los números enteros se escriben sin decimales (DNI/RUC leídos como número).
func cellStr(row []any, i int) string {
	switch v := cellAt(row, i).(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(strings.ReplaceAll(v, " ", " "))
	case float64:
		if v == math.Trunc(v) {
			return strconv.FormatFloat(v, 'f', 0, 64)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// cellNum número de la celda; ok=false si está vacía o no es numérica («-», texto).
func cellNum(row []any, i int) (float64, bool) {
	switch v := cellAt(row, i).(type) {
	case float64:
		return v, true
	case string:
		t := strings.TrimSpace(strings.ReplaceAll(v, ",", "."))
		if t == "" || t == "-" {
			return 0, false
		}
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func cellInt(row []any, i int) (int, bool) {
	f, ok := cellNum(row, i)
	if !ok {
		return 0, false
	}
	return int(math.Round(f)), math.Abs(f-math.Round(f)) < 1e-9
}

// parsedTime fecha y/o hora leídas de una celda.
type parsedTime struct {
	Date    *time.Time // fecha de calendario (a mediodía de Lima)
	Hour    int
	Minute  int
	HasTime bool
}

// cellTime interpreta fechas ISO («2026-08-03T00:00:00.000Z» o «2026-08-03») y horas sueltas
// (Excel las guarda como fecha 1899-12-31 + hora). Los componentes se leen en UTC porque hucre
// devuelve las fechas de Excel como UTC sin zona.
func cellTime(row []any, i int) parsedTime {
	var out parsedTime
	switch v := cellAt(row, i).(type) {
	case string:
		t := strings.TrimSpace(v)
		if t == "" {
			return out
		}
		var parsed time.Time
		var err error
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
			if parsed, err = time.Parse(layout, t); err == nil {
				break
			}
		}
		if err != nil {
			return out
		}
		if parsed.Year() <= 1900 { // solo hora
			out.Hour, out.Minute, out.HasTime = parsed.Hour(), parsed.Minute(), true
			return out
		}
		d := noonLima(time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC))
		out.Date = &d
		if parsed.Hour() != 0 || parsed.Minute() != 0 {
			out.Hour, out.Minute, out.HasTime = parsed.Hour(), parsed.Minute(), true
		}
	case float64: // serie de Excel
		if v > 0 && v < 1 { // solo hora
			mins := int(math.Round(v * 24 * 60))
			out.Hour, out.Minute, out.HasTime = mins/60, mins%60, true
			return out
		}
		if v >= 25569 { // 1970-01-01
			base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(v))
			d := noonLima(base)
			out.Date = &d
			frac := v - math.Floor(v)
			if frac > 0 {
				mins := int(math.Round(frac * 24 * 60))
				out.Hour, out.Minute, out.HasTime = mins/60, mins%60, true
			}
		}
	}
	return out
}

// ── Texto ──────────────────────────────────────────────────────────────────

var spaceRe = regexp.MustCompile(`\s+`)

func collapse(s string) string { return strings.TrimSpace(spaceRe.ReplaceAllString(s, " ")) }

var accentStripper = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// foldText minúsculas y sin tildes, para comparar.
func foldText(s string) string {
	out, _, err := transform.String(accentStripper, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(collapse(out))
}

// key clave de comparación de un producto: mayúsculas y solo letras/dígitos.
func key(s string) string {
	var b strings.Builder
	for _, r := range foldText(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

func onlyDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ── Cliente / documento / teléfono ─────────────────────────────────────────

// docInfo documento normalizado del cliente de un pedido.
type docInfo struct {
	DocType    string // RUC | DNI | CE | SIN_DOC
	DocNumber  string
	ContactDNI string
	BillingDoc string // boleta | factura | ""
	Weird      string // documento que no es DNI/RUC (se informa)
}

// classifyDoc interpreta las columnas «Boleta o Factura» y «DNI / RUC» del Excel. En el libro la primera trae a
// veces el tipo («Boleta», «Factura») y a veces el RUC, y la segunda el DNI.
func classifyDoc(colBillingOrRUC, colDocument string) docInfo {
	var d docInfo
	a := strings.TrimSpace(colBillingOrRUC)
	b := strings.TrimSpace(colDocument)
	if b == "-" {
		b = ""
	}
	if a == "-" {
		a = ""
	}
	switch foldText(a) {
	case "boleta":
		d.BillingDoc, a = "boleta", ""
	case "factura":
		d.BillingDoc, a = "factura", ""
	}
	ruc, dni := "", ""
	for _, v := range []string{a, b} {
		switch {
		case onlyDigits(v) && len(v) == 11 && ruc == "":
			ruc = v
		case onlyDigits(v) && len(v) == 8 && dni == "":
			dni = v
		case v != "" && d.Weird == "":
			d.Weird = v
		}
	}
	switch {
	case ruc != "":
		d.DocType, d.DocNumber, d.ContactDNI = "RUC", ruc, dni
		if d.BillingDoc == "" {
			d.BillingDoc = "factura"
		}
	case dni != "":
		d.DocType, d.DocNumber = "DNI", dni
	case d.Weird != "":
		d.DocType, d.DocNumber = "CE", d.Weird
	default:
		d.DocType = "SIN_DOC"
	}
	return d
}

var phoneUserRe = regexp.MustCompile(`[A-Za-z]`)

// classifyPhone devuelve el teléfono limpio y su tipo (whatsapp | usuario) y si conviene advertir.
func classifyPhone(raw string) (phone, kind, warn string) {
	t := strings.TrimSpace(raw)
	if t == "" || t == "-" {
		return "", "whatsapp", ""
	}
	if phoneUserRe.MatchString(t) {
		return t, "usuario", ""
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, t)
	if len(digits) == 9 && strings.HasPrefix(digits, "9") {
		return digits, "whatsapp", ""
	}
	if len(digits) == 11 && strings.HasPrefix(digits, "519") {
		return digits[2:], "whatsapp", ""
	}
	return t, "whatsapp", "el teléfono no parece un celular de 9 dígitos"
}

// ── Comprobantes ───────────────────────────────────────────────────────────

var invoiceRe = regexp.MustCompile(`^([A-Za-z]\d{3})\s*-\s*0*(\d+)$`)

// parseInvoice separa «F002-59» en tipo, serie y número. Series con espacios o minúsculas se normalizan.
func parseInvoice(raw string) (docType, series, number string, ok bool) {
	t := strings.TrimSpace(raw)
	if t == "" || t == "-" {
		return "", "", "", true
	}
	m := invoiceRe.FindStringSubmatch(t)
	if m == nil {
		return "", "", t, false
	}
	series = strings.ToUpper(m[1])
	switch series[0] {
	case 'F':
		docType = "factura"
	case 'B':
		docType = "boleta"
	}
	return docType, series, m[2], true
}

// ── Planes ─────────────────────────────────────────────────────────────────

// planMonths detecta si la línea es un plan/promoción de Tukifac y de cuántos meses.
func planMonths(name string) (isPlan bool, months int) {
	f := foldText(name)
	if !strings.Contains(f, "plan") && !strings.Contains(f, "promocion") {
		return false, 0
	}
	switch {
	case strings.Contains(f, "semestral"):
		months = 6
	case strings.Contains(f, "trimestral"):
		months = 3
	case strings.Contains(f, "anual"):
		months = 12
	case strings.Contains(f, "mensual"):
		months = 1
	}
	return true, months
}

// canonicalPlanName unifica «Plan anual », «PLAN SEMESTRAL», «plan semestral emprendedor» a un solo formato.
func canonicalPlanName(name string) string {
	t := collapse(strings.ToLower(name))
	if t == "" {
		return t
	}
	r := []rune(t)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// ── Destinos ───────────────────────────────────────────────────────────────

var peruDepartments = map[string]string{}

func init() {
	for _, d := range []string{"Amazonas", "Áncash", "Apurímac", "Arequipa", "Ayacucho", "Cajamarca", "Callao", "Cusco",
		"Huancavelica", "Huánuco", "Ica", "Junín", "La Libertad", "Lambayeque", "Lima", "Loreto", "Madre de Dios",
		"Moquegua", "Pasco", "Piura", "Puno", "San Martín", "Tacna", "Tumbes", "Ucayali"} {
		peruDepartments[foldText(d)] = d
	}
}

// destination resultado de interpretar «Provincia / Depto.» del Excel.
type destination struct {
	Mode       string // agencia | oficina | pendiente_recojo
	Department string
	Province   string
	District   string
	Known      bool // el departamento es uno de los 25
}

func parseDestination(raw string) destination {
	t := collapse(raw)
	d := destination{Mode: "agencia"}
	f := foldText(t)
	switch {
	case strings.Contains(f, "entregado en oficina"):
		d.Mode = "oficina"
		return d
	case strings.Contains(f, "pendiente de recojo"):
		d.Mode = "pendiente_recojo"
		return d
	case t == "":
		return d
	}
	parts := strings.Split(t, "/")
	for i := range parts {
		parts[i] = collapse(parts[i])
	}
	if name, ok := peruDepartments[foldText(strings.TrimRight(parts[0], ". "))]; ok {
		d.Department, d.Known = name, true
	} else {
		d.Department = parts[0]
	}
	if len(parts) > 1 {
		d.Province = parts[1]
	}
	if len(parts) > 2 {
		d.District = parts[2]
	}
	return d
}

// ── Período ────────────────────────────────────────────────────────────────

var monthNames = map[string]int{
	"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7,
	"agosto": 8, "setiembre": 9, "septiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
}

var monthYearRe = regexp.MustCompile(`(?i)(enero|febrero|marzo|abril|mayo|junio|julio|agosto|setiembre|septiembre|octubre|noviembre|diciembre)\s+(\d{4})`)

// parsePeriod «AGOSTO 2026» → «2026-08».
func parsePeriod(s string) (string, bool) {
	m := monthYearRe.FindStringSubmatch(foldText(s))
	if m == nil {
		return "", false
	}
	return fmt.Sprintf("%s-%02d", m[2], monthNames[m[1]]), true
}

var monthOnlyRe = regexp.MustCompile(`^(enero|febrero|marzo|abril|mayo|junio|julio|agosto|setiembre|septiembre|octubre|noviembre|diciembre)\.?$`)

// parseMonthOnly «OCTUBRE» → 10 (celda con SOLO el mes, sin año).
func parseMonthOnly(s string) (int, bool) {
	m := monthOnlyRe.FindStringSubmatch(foldText(s))
	if m == nil {
		return 0, false
	}
	return monthNames[m[1]], true
}

var monthLabels = [...]string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "setiembre", "octubre", "noviembre", "diciembre"}

func monthLabel(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return monthLabels[m]
}

// inferPeriodYear año de un libro cuyo mes de control no trae año: el año más frecuente entre las fechas del
// libro (Control de Envíos y Retornos) que caen en ese mes; si no hay ninguna, el año en curso (Lima).
func inferPeriodYear(month int, sheets ...[][]any) int {
	count := map[int]int{}
	for _, rows := range sheets {
		for _, r := range rows {
			for c := range r {
				if t := cellTime(r, c); t.Date != nil {
					y := t.Date.Year()
					if y >= 2020 && y <= 2100 && int(t.Date.Month()) == month {
						count[y]++
					}
				}
			}
		}
	}
	best, bestN := 0, 0
	for y, n := range count {
		if n > bestN || (n == bestN && y > best) {
			best, bestN = y, n
		}
	}
	if best == 0 {
		return time.Now().In(limaLoc()).Year()
	}
	return best
}

// ── Productos ──────────────────────────────────────────────────────────────

// inferKind tipo de producto por su nombre (se corrige luego en pantalla).
func inferKind(code, name string) string {
	f := foldText(code + " " + name)
	switch {
	case strings.HasPrefix(f, "cont"):
		return "consumible"
	case strings.HasPrefix(f, "etiq"):
		return "etiqueta"
	case strings.Contains(f, "pistola"), strings.Contains(f, "yhd-"), strings.HasPrefix(f, "u27"),
		strings.Contains(f, "gaveta"), strings.Contains(f, "lector"), strings.Contains(f, "scaner"), strings.Contains(f, "escaner"):
		return "accesorio"
	}
	return "equipo"
}
