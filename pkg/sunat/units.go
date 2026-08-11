package sunat

import "strings"

// Catálogo SUNAT 03 — tabla 6 unidades de medida.
// Fuente: https://github.com/EliuTimana/SunatCatalogos/blob/master/data/08/03.json
var catalog03UnitCodes = map[string]struct{}{
	"BJ": {}, "BLL": {}, "4A": {}, "BG": {}, "BO": {}, "BX": {}, "CT": {}, "CMK": {}, "CMQ": {},
	"CMT": {}, "CEN": {}, "CY": {}, "CJ": {}, "DZN": {}, "DZP": {}, "BE": {}, "GLI": {}, "GRM": {},
	"GRO": {}, "HLT": {}, "LEF": {}, "SET": {}, "KGM": {}, "KTM": {}, "KWH": {}, "KT": {}, "CA": {},
	"LBR": {}, "LTR": {}, "MWH": {}, "MTR": {}, "MTK": {}, "MTQ": {}, "MGM": {}, "MLT": {}, "MMT": {},
	"MMK": {}, "MMQ": {}, "MLL": {}, "MU": {}, "ONZ": {}, "PF": {}, "PK": {}, "PR": {}, "FOT": {},
	"FTK": {}, "FTQ": {}, "C62": {}, "PG": {}, "ST": {}, "INH": {}, "RM": {}, "DR": {}, "STN": {},
	"LTN": {}, "TNE": {}, "TU": {}, "NIU": {}, "ZZ": {}, "GLL": {}, "YRD": {}, "YDK": {},
}

// unitAliases mapea símbolos comerciales o códigos legacy incorrectos → catálogo SUNAT 03.
var unitAliases = map[string]string{
	"": "NIU", "UND": "NIU", "UNIDAD": "NIU", "UNIDADES": "NIU", "UNIT": "NIU", "UNITS": "NIU",
	"U": "NIU", "UN": "NIU", "UNID": "NIU", "PZ": "C62", "PIEZA": "C62", "PIEZAS": "C62",
	"SERVICIO": "ZZ", "SERVICIOS": "ZZ", "SERVICE": "ZZ", "SRV": "ZZ", "SERV": "ZZ",
	"LT": "LTR", "L": "LTR", "LITRO": "LTR", "LITROS": "LTR", "LITRE": "LTR", "LITRES": "LTR",
	"KG": "KGM", "KGS": "KGM", "KILO": "KGM", "KILOS": "KGM", "KILOGRAMO": "KGM", "KILOGRAMOS": "KGM",
	"G": "GRM", "GR": "GRM", "GRS": "GRM", "GRAMO": "GRM", "GRAMOS": "GRM",
	"ML": "MLT", "MILILITRO": "MLT", "MILILITROS": "MLT",
	"GL": "GLL", "GAL": "GLL", "GALON": "GLL", "GALONES": "GLL",
	"TON": "TNE", "TNL": "TNE", "TONELADA": "TNE", "TONELADAS": "TNE",
	"LB": "LBR", "LIBRA": "LBR", "LIBRAS": "LBR",
	"M": "MTR", "METRO": "MTR", "METROS": "MTR",
	"CM": "CMT", "CENTIMETRO": "CMT", "CENTIMETROS": "CMT",
	"M2": "MTK", "M3": "MTQ",
	"CAJ": "BX", "CAJA": "BX", "CAJAS": "BX", "CJA": "BX",
	"BOLS": "BG", "BOLSA": "BG", "BOLSAS": "BG",
	"BOT": "BO", "BOTELLA": "BO", "BOTELLAS": "BO",
	"PQT": "PK", "PAQ": "PK", "PAQUETE": "PK", "PAQUETES": "PK",
	"DOC": "DZN", "DOCENA": "DZN", "DOCENAS": "DZN",
	"JGO": "SET", "JUEGO": "SET", "JUEGOS": "SET",
	"BULT": "BE", "BULTO": "BE", "BULTOS": "BE", "BLT": "BE",
	"FARD": "BE", "FARDO": "BE",
	"LAT": "CA", "LATA": "CA", "LATAS": "CA",
}

// NormalizeUnit devuelve código catálogo SUNAT 03 válido para comprobantes.
// itemType: "product", "service" o vacío (infiera por unidad ZZ).
func NormalizeUnit(unit, itemType string) string {
	t := strings.ToLower(strings.TrimSpace(itemType))
	if t == "service" {
		return "ZZ"
	}
	u := strings.ToUpper(strings.TrimSpace(unit))
	if t == "" && u == "ZZ" {
		return "ZZ"
	}
	if mapped, ok := unitAliases[u]; ok {
		u = mapped
	}
	switch u {
	case "", "UND", "UNIDAD", "UNIDADES", "UNIT", "UNITS", "U", "UN":
		return "NIU"
	case "SERVICIO", "SERVICIOS", "SERVICE", "SRV":
		return "ZZ"
	}
	if t == "service" {
		return "ZZ"
	}
	if _, ok := catalog03UnitCodes[u]; ok {
		return u
	}
	return "NIU"
}

// IsValidUnitCode indica si el código existe en catálogo SUNAT 03.
func IsValidUnitCode(code string) bool {
	_, ok := catalog03UnitCodes[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}

// UnitCorrections devuelve mapa unidad legacy → código SUNAT 03 para backfill en BD.
func UnitCorrections() map[string]string {
	out := make(map[string]string, len(unitAliases)+3)
	for from, to := range unitAliases {
		if from == "" || from == to {
			continue
		}
		out[from] = to
	}
	out["CJA"] = "BX"
	out["PAQ"] = "PK"
	out["BLT"] = "BE"
	return out
}

// SystemUnitCodes códigos expuestos en el ERP (catálogo 03 verificado).
func SystemUnitCodes() []string {
	return []string{
		"NIU", "ZZ", "KGM", "LTR", "MTR", "MTK", "MTQ", "TNE", "GLL", "BX", "BG", "BO",
		"PK", "SET", "KT", "DZN", "GRM", "C62",
	}
}
