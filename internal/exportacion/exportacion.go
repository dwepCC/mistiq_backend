// Package exportacion implementa la exportación de servicios (operaciones 0201, 0202 y 0208 del catálogo 51 de SUNAT): ventas a un cliente
// del exterior que no pagan IGV (afectación 40). Pide solo los datos que la norma exige para cada operación y los envía al facturador.
package exportacion

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/salecurrency"

	"gorm.io/gorm"
)

// LeyendaHospedaje leyenda obligatoria en la factura de hospedaje a no domiciliados (DS 122-2001-EF, art. 6).
const LeyendaHospedaje = "EXPORTACIÓN DE SERVICIOS - DECRETO LEGISLATIVO Nº 919"

// AttrCountryUse código reservado que el backend envía en details[0].atributos y que el facturador convierte en
// cac:Delivery/cac:DeliveryLocation/cac:Address/cac:Country (país del uso, explotación o aprovechamiento del servicio). No es del catálogo 55.
const AttrCountryUse = "EXP_PAIS_USO"

// MaxStayDays permanencia máxima desde el ingreso al país para que el hospedaje cuente como exportación (Ley del IGV, art. 33.4).
const MaxStayDays = 60

// SaleInput lo que el cajero completa en el comprobante. Fechas en formato YYYY-MM-DD.
type SaleInput struct {
	// 0201 y 0208: país donde se usa el servicio (ISO alfa-2, p. ej. US).
	CountryUse string `json:"country_use"`
	// 0202: datos del huésped y su estadía.
	GuestName        string `json:"guest_name"`
	GuestDocType     string `json:"guest_doc_type"` // 7 pasaporte (por defecto), 4 carné de extranjería, B/G/...
	GuestDocNumber   string `json:"guest_doc_number"`
	PassportCountry  string `json:"passport_country"`
	ResidenceCountry string `json:"residence_country"`
	EntryDate        string `json:"entry_date"` // ingreso al país
	CheckIn          string `json:"check_in"`
	CheckOut         string `json:"check_out"`
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

func normCountry(raw string) string { return strings.ToUpper(strings.TrimSpace(raw)) }

func parseDate(label, raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("indique %s", label)
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, fmt.Errorf("%s no es una fecha válida (use AAAA-MM-DD)", label)
	}
	// Fecha de calendario: se guarda a mediodía UTC para que ninguna conversión de zona horaria del driver (UTC, hora local del servidor
	// o Perú) la corra a otro día al escribirla en una columna DATE (a medianoche UTC se guardaba el día anterior en un servidor UTC-5).
	t = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.UTC)
	return &t, nil
}

func daysBetween(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

// GuestFromContact nombre, tipo y número de documento del huésped tomados del cliente de la venta: el cliente ES el huésped, así que no se
// vuelven a pedir. Acepta pasaporte (7) o carné de extranjería (4); con cualquier otro documento (RUC, DNI, «Clientes varios») devuelve error.
func GuestFromContact(c *database.TenantContact) (name, docType, docNumber string, err error) {
	if c == nil || c.ID == 0 {
		return "", "", "", errors.New("elija el cliente: el huésped debe estar registrado como cliente con su pasaporte")
	}
	switch strings.ToUpper(strings.TrimSpace(c.DocType)) {
	case "7", "PASAPORTE":
		docType = "7"
	case "4", "CE", "CARNET", "CARNET EXTRANJERIA", "CARNÉ DE EXTRANJERÍA":
		docType = "4"
	default:
		return "", "", "", errors.New("para el hospedaje a extranjeros el cliente debe tener pasaporte o carné de extranjería: elija o cree el cliente con ese documento")
	}
	name = strings.Join(strings.Fields(c.BusinessName), " ")
	docNumber = strings.TrimSpace(c.DocNumber)
	if len([]rune(name)) < 3 || len(docNumber) < 4 {
		return "", "", "", errors.New("el cliente no tiene nombre o número de documento completos")
	}
	return name, docType, docNumber, nil
}

// Normalize valida los datos según la operación y devuelve la fila a guardar (sin SaleID). Los mensajes están pensados para el cajero.
// contact es el cliente de la venta; en el hospedaje (0202) de él salen el nombre y el documento del huésped. Si la petición trae
// guest_name/guest_doc_*, esos valores mandan (uso por API).
func Normalize(opCode string, in *SaleInput, contact *database.TenantContact) (*database.TenantSaleExport, error) {
	if !salecurrency.IsExportService(opCode) {
		return nil, fmt.Errorf("la operación %s no es una exportación de servicios", opCode)
	}
	if in == nil {
		return nil, errors.New("complete los datos de la exportación")
	}
	row := &database.TenantSaleExport{OperationTypeCode: opCode}

	if opCode != salecurrency.OpExportHospedaje {
		// El país donde se usa el servicio es OPCIONAL: la guía de SUNAT lo marca obligatorio para 0201/0208, pero el ambiente beta acepta factura y
		// boleta sin él (probado 12-nov-2026). Si se indica, se envía; si no, no se pide nada más.
		c := normCountry(in.CountryUse)
		if c != "" {
			if !countryRe.MatchString(c) {
				return nil, errors.New("el país donde se usa el servicio no es válido")
			}
			if c == "PE" {
				return nil, errors.New("en una exportación el servicio se usa fuera del Perú: elija un país del exterior")
			}
		}
		row.CountryUse = c
		return row, nil
	}

	// 0202: hospedaje a no domiciliados.
	row.GuestName = strings.Join(strings.Fields(in.GuestName), " ")
	row.GuestDocType = strings.ToUpper(strings.TrimSpace(in.GuestDocType))
	row.GuestDocNumber = strings.TrimSpace(in.GuestDocNumber)
	if row.GuestName == "" && row.GuestDocNumber == "" {
		name, docType, docNumber, err := GuestFromContact(contact)
		if err != nil {
			return nil, err
		}
		row.GuestName, row.GuestDocType, row.GuestDocNumber = name, docType, docNumber
	}
	if row.GuestDocType == "" {
		row.GuestDocType = "7"
	}
	if len([]rune(row.GuestName)) < 3 {
		return nil, errors.New("indique el nombre del huésped")
	}
	if len(row.GuestDocNumber) < 4 {
		return nil, errors.New("indique el número de pasaporte (o documento) del huésped")
	}
	// País del pasaporte: el indicado en la venta o, si no, el de la ficha del cliente (se registra una sola vez al crear al cliente).
	row.PassportCountry = normCountry(in.PassportCountry)
	if row.PassportCountry == "" && contact != nil {
		row.PassportCountry = normCountry(contact.Country)
	}
	if !countryRe.MatchString(row.PassportCountry) {
		return nil, errors.New("indique el país del pasaporte del huésped (o regístrelo en la ficha del cliente)")
	}
	// Lo habitual: reside en el país que emitió su pasaporte. Solo se pide aparte si es distinto.
	row.ResidenceCountry = normCountry(in.ResidenceCountry)
	if row.ResidenceCountry == "" {
		row.ResidenceCountry = row.PassportCountry
	}
	if !countryRe.MatchString(row.ResidenceCountry) {
		return nil, errors.New("el país de residencia no es válido")
	}
	if row.ResidenceCountry == "PE" {
		return nil, errors.New("el huésped reside en el Perú: no es un no domiciliado, así que el hospedaje no es una exportación (emita una factura o boleta normal con IGV)")
	}
	// Fechas opcionales: lo habitual es que el huésped entre y salga el día de la emisión. Sin fechas se asume hoy (hora de Perú) para el ingreso
	// y la salida del hotel; el ingreso al país se asume igual al ingreso al hotel (el turista ya está en el país; si necesita otra, se indica).
	inCheckIn, inCheckOut, inEntry := strings.TrimSpace(in.CheckIn), strings.TrimSpace(in.CheckOut), strings.TrimSpace(in.EntryDate)
	if inCheckIn == "" {
		inCheckIn = time.Now().In(time.FixedZone("PET", -5*3600)).Format("2006-01-02")
	}
	if inCheckOut == "" {
		inCheckOut = inCheckIn
	}
	if inEntry == "" {
		inEntry = inCheckIn
	}
	entry, err := parseDate("la fecha de ingreso al país", inEntry)
	if err != nil {
		return nil, err
	}
	checkIn, err := parseDate("la fecha de ingreso al hotel", inCheckIn)
	if err != nil {
		return nil, err
	}
	checkOut, err := parseDate("la fecha de salida del hotel", inCheckOut)
	if err != nil {
		return nil, err
	}
	if checkIn.Before(*entry) {
		return nil, errors.New("la fecha de ingreso al hotel no puede ser anterior al ingreso al país")
	}
	if checkOut.Before(*checkIn) {
		return nil, errors.New("la fecha de salida no puede ser anterior a la de ingreso al hotel")
	}
	if daysBetween(*entry, *checkOut) > MaxStayDays {
		return nil, fmt.Errorf("el huésped supera los %d días desde su ingreso al país: ya no califica como exportación (emita una factura o boleta normal con IGV)", MaxStayDays)
	}
	row.EntryDate, row.CheckIn, row.CheckOut = entry, checkIn, checkOut
	row.StayDays = daysBetween(*checkIn, *checkOut)
	if row.StayDays < 1 {
		row.StayDays = 1
	}
	return row, nil
}

// Persist guarda los datos de la exportación de la venta. Idempotente por venta.
func Persist(tx *gorm.DB, saleID uint, row *database.TenantSaleExport) error {
	if row == nil {
		return nil
	}
	row.SaleID = saleID
	return tx.Save(row).Error
}

// LoadBySaleID devuelve los datos de exportación de la venta, o nil si no es una exportación.
func LoadBySaleID(db *gorm.DB, saleID uint) (*database.TenantSaleExport, error) {
	var row database.TenantSaleExport
	if err := db.Where("sale_id = ?", saleID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ApplyToInvoicePayload completa el payload del facturador: adquirente con guion (factura), datos del huésped por línea (0202) o país de
// uso (0201/0208), y la leyenda en el PDF. Las líneas ya vienen con afectación 40 (IGV 0) desde la venta.
func ApplyToInvoicePayload(payload *facturador.InvoicePayload, row *database.TenantSaleExport) {
	if payload == nil || row == nil {
		return
	}
	// El tipo de operación de la exportación manda sobre el del perfil fiscal: ese perfil nace con 0101 (venta interna) y, aplicado antes,
	// lo dejaba en el XML. Con 0101 el adquirente «-» es inválido y SUNAT rechaza con 2800 (tenant 20616204018, 16-nov-2026).
	if row.OperationTypeCode != "" {
		payload.TipoOperacion = row.OperationTypeCode
	}
	// RS 097-2012 art. 17.2 (anexo RS 123-2022): en exportaciones a no domiciliados la factura identifica al adquirente con «-».
	if payload.TipoDoc == "01" {
		payload.Client.TipoDoc, payload.Client.NumDoc = "-", "-"
		payload.Client.Address = nil
	}
	extras := []facturador.InvoicePDFExtra{{Name: "Tipo operación", Value: OperationLabel(row.OperationTypeCode)}}

	switch row.OperationTypeCode {
	case salecurrency.OpExportHospedaje:
		if row.GuestName != "" {
			payload.Client.RznSocial = row.GuestName
		}
		attrs := hospedajeAttributes(row)
		for i := range payload.Details {
			payload.Details[i].Atributos = append(payload.Details[i].Atributos, attrs...)
		}
		extras = append(extras, facturador.InvoicePDFExtra{Name: "Leyenda", Value: LeyendaHospedaje})
	default:
		if row.CountryUse != "" && len(payload.Details) > 0 {
			payload.Details[0].Atributos = append(payload.Details[0].Atributos,
				facturador.DetailAttribute{Code: AttrCountryUse, Name: "País del uso del servicio", Value: row.CountryUse})
		}
	}
	mergePDFExtras(payload, extras)
}

// peruZone UTC-5 fijo (Perú no tiene horario de verano). Las fechas de calendario (ingreso, salida) viajan al facturador a mediodía en
// esta zona: así el día no se corre a otro al convertir de zona horaria al armar el XML (medianoche UTC sería el día anterior en Lima).
var peruZone = time.FixedZone("UTC-5", -5*3600)

func noonPeru(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	y, m, d := t.Date()
	n := time.Date(y, m, d, 12, 0, 0, 0, peruZone)
	return &n
}

// hospedajeAttributes propiedades del catálogo 55 «Beneficio Hospedajes» (4000–4009) que SUNAT exige con la operación 0202.
func hospedajeAttributes(r *database.TenantSaleExport) []facturador.DetailAttribute {
	str := func(code, name, v string) facturador.DetailAttribute {
		return facturador.DetailAttribute{Code: code, Name: name, Value: v}
	}
	out := []facturador.DetailAttribute{
		str("4000", "Beneficio Hospedajes-Paquete turístico: Código de país de emisión del pasaporte", r.PassportCountry),
		str("4001", "Beneficio Hospedajes: Código de país de residencia del sujeto no domiciliado", r.ResidenceCountry),
	}
	if r.EntryDate != nil {
		out = append(out, facturador.DetailAttribute{Code: "4002", Name: "Beneficio Hospedajes: Fecha de ingreso al país", FecInicio: noonPeru(r.EntryDate)})
	}
	if r.CheckIn != nil {
		out = append(out, facturador.DetailAttribute{Code: "4003", Name: "Beneficio Hospedajes: Fecha de Ingreso al Establecimiento", FecInicio: noonPeru(r.CheckIn)})
	}
	if r.CheckOut != nil {
		// SUNAT (beta, 12-nov-2026) rechaza 3065 si la fecha de salida va solo en cbc:EndDate: la espera en cbc:StartDate, como las demás fechas.
		out = append(out, facturador.DetailAttribute{Code: "4004", Name: "Beneficio Hospedajes: Fecha de Salida del Establecimiento", FecInicio: noonPeru(r.CheckOut)})
	}
	days := r.StayDays
	out = append(out, facturador.DetailAttribute{Code: "4005", Name: "Beneficio Hospedajes: Número de Días de Permanencia", Duracion: days})
	if r.CheckIn != nil {
		out = append(out, facturador.DetailAttribute{Code: "4006", Name: "Beneficio Hospedajes: Fecha de Consumo", FecInicio: noonPeru(r.CheckIn)})
	}
	return append(out,
		str("4007", "Beneficio Hospedajes-Paquete turístico: Nombres y apellidos del huesped", r.GuestName),
		str("4008", "Beneficio Hospedajes-Paquete turístico: Tipo de documento de identidad del huesped", r.GuestDocType),
		str("4009", "Beneficio Hospedajes-Paquete turístico: Número de documento de identidad del huesped", r.GuestDocNumber),
	)
}

// OperationLabel nombre corto de la operación para el comprobante.
func OperationLabel(code string) string {
	switch code {
	case salecurrency.OpExportHospedaje:
		return "Exportación de servicios - Hospedaje a no domiciliados (0202)"
	case salecurrency.OpExportParcial:
		return "Exportación de servicios - Realizados parcialmente en el extranjero (0208)"
	default:
		return "Exportación de servicios (0201)"
	}
}

func mergePDFExtras(payload *facturador.InvoicePayload, extras []facturador.InvoicePDFExtra) {
	if payload.Parameters == nil {
		payload.Parameters = &facturador.InvoicePDFParameters{}
	}
	for _, e := range extras {
		dup := false
		for _, cur := range payload.Parameters.User.Extras {
			if strings.EqualFold(strings.TrimSpace(cur.Name), e.Name) {
				dup = true
				break
			}
		}
		if !dup {
			payload.Parameters.User.Extras = append(payload.Parameters.User.Extras, e)
		}
	}
}
