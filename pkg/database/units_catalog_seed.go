package database

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// unitCatalogDefault fila del catálogo SUNAT N°03 (Unidades de Medida Comercial) usado como
// semilla. Código + descripción + símbolo replican exactamente el listado del sistema legacy
// (facturador-tukifac, tabla cat_unit_types) para que un tenant migrado no note diferencia.
type unitCatalogDefault struct {
	Code   string
	Name   string
	Symbol string
	Active bool
}

// defaultUnitsCatalog las 8 marcadas Active=true son las que el ERP mostraba seleccionables por
// defecto en el sistema legacy (list-units); el resto queda en el catálogo (inactivo) para que el
// tenant las active desde Tukifac sin tener que recordar/tipear el código SUNAT de memoria.
var defaultUnitsCatalog = []unitCatalogDefault{
	{Code: "AV", Name: "Cápsula", Symbol: "CAPS"},
	{Code: "BE", Name: "Fardo", Symbol: "FARD"},
	{Code: "BG", Name: "Bolsa", Symbol: "BOLS", Active: true},
	{Code: "BJ", Name: "Balde", Symbol: "BALD"},
	{Code: "BLL", Name: "Barril", Symbol: "BRL"},
	{Code: "BO", Name: "Botellas", Symbol: "BOT"},
	{Code: "BT", Name: "Tornillo", Symbol: "TORN"},
	{Code: "BX", Name: "Caja", Symbol: "CAJ", Active: true},
	{Code: "C62", Name: "Piezas", Symbol: "PZ"},
	{Code: "CA", Name: "Latas", Symbol: "LT"},
	{Code: "CEN", Name: "Centenar o ciento", Symbol: "CTO"},
	{Code: "CH", Name: "Envase", Symbol: "ENV"},
	{Code: "CMK", Name: "Centímetro cuadrado", Symbol: "CM2"},
	{Code: "CMQ", Name: "Centímetro cúbico", Symbol: "CM3"},
	{Code: "CMT", Name: "Centímetro", Symbol: "CM"},
	{Code: "CT", Name: "Cartón", Symbol: "CTON"},
	{Code: "CY", Name: "Cilindro", Symbol: "CIL"},
	{Code: "DZN", Name: "Docena", Symbol: "DOC"},
	{Code: "DZP", Name: "Docena de paquetes", Symbol: "DOC2"},
	{Code: "FOT", Name: "Pies", Symbol: "PIE"},
	{Code: "FTK", Name: "Pies cuadrados", Symbol: "PIE2"},
	{Code: "FTQ", Name: "Pies cúbicos", Symbol: "PIE3"},
	{Code: "GLI", Name: "Galón inglés", Symbol: "GL"},
	{Code: "GLL", Name: "Galones", Symbol: "GL"},
	{Code: "GRM", Name: "Gramos", Symbol: "GR"},
	{Code: "HD", Name: "Media docena", Symbol: "1/2 DOC"},
	{Code: "HT", Name: "Media hora", Symbol: "1/2 H"},
	{Code: "HUR", Name: "Hora", Symbol: "HR"},
	{Code: "INH", Name: "Pulgadas", Symbol: "INCH"},
	{Code: "JG", Name: "Jarra", Symbol: "JARR"},
	{Code: "JR", Name: "Frasco", Symbol: "FCO"},
	{Code: "KGM", Name: "Kilos", Symbol: "KG", Active: true},
	{Code: "KT", Name: "Kit", Symbol: "KIT"},
	{Code: "KTM", Name: "Kilómetro", Symbol: "KM"},
	{Code: "KWH", Name: "Kilovatio hora", Symbol: "KWxH"},
	{Code: "LBR", Name: "Libras", Symbol: "LB"},
	{Code: "LEF", Name: "Hoja", Symbol: "HOJA"},
	{Code: "LTR", Name: "Litros", Symbol: "LT", Active: true},
	{Code: "MGM", Name: "Miligramos", Symbol: "MG"},
	{Code: "MIL", Name: "Millar", Symbol: "MIL"},
	{Code: "MLT", Name: "Mililitro", Symbol: "ML"},
	{Code: "MMK", Name: "Milímetro cuadrado", Symbol: "MM2"},
	{Code: "MMQ", Name: "Milímetro cúbico", Symbol: "MM3"},
	{Code: "MMT", Name: "Milímetro", Symbol: "MM"},
	{Code: "MTK", Name: "Metro cuadrado", Symbol: "M2"},
	{Code: "MTQ", Name: "Metro cúbico", Symbol: "M3"},
	{Code: "MTR", Name: "Metros", Symbol: "M", Active: true},
	{Code: "MWH", Name: "Megavatio hora", Symbol: "MWxH"},
	{Code: "NIU", Name: "Unidades", Symbol: "UND", Active: true},
	{Code: "ONZ", Name: "Onzas", Symbol: "ONZ"},
	{Code: "PF", Name: "Paletas", Symbol: "PAL"},
	{Code: "PG", Name: "Placas", Symbol: "PLAC"},
	{Code: "PK", Name: "Paquete", Symbol: "PQT", Active: true},
	{Code: "PR", Name: "Par", Symbol: "PAR"},
	{Code: "QD", Name: "Cuarto de docena", Symbol: "1/4 DOC"},
	{Code: "RD", Name: "Varilla", Symbol: "VAR"},
	{Code: "RL", Name: "Carrete", Symbol: "CRR"},
	{Code: "RM", Name: "Resma", Symbol: "RESM"},
	{Code: "SA", Name: "Saco", Symbol: "SCO"},
	{Code: "SEC", Name: "Segundo", Symbol: "SEG"},
	{Code: "SET", Name: "Juego", Symbol: "JGO"},
	{Code: "ST", Name: "Pliego", Symbol: "PLGO"},
	{Code: "TNE", Name: "Toneladas", Symbol: "TNL"},
	{Code: "TU", Name: "Tubos", Symbol: "TB"},
	{Code: "U2", Name: "Tableta o blister", Symbol: "BLIST"},
	{Code: "UM", Name: "Millón", Symbol: "MILL"},
	{Code: "YRD", Name: "Yardas", Symbol: "YD"},
	{Code: "ZZ", Name: "Servicio", Symbol: "SERV", Active: true},
}

// SeedUnitsCatalog siembra el catálogo de unidades de medida de forma idempotente: crea las filas
// que falten (IsSystem=true) y nunca pisa lo que el tenant ya haya personalizado (nombre/símbolo/
// activo) en una fila existente — mismo patrón "ensure" que SeedFinancialCatalog.
func SeedUnitsCatalog(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i, def := range defaultUnitsCatalog {
			if err := ensureUnitCatalogRow(tx, def, i); err != nil {
				return err
			}
		}
		return nil
	})
}

func ensureUnitCatalogRow(tx *gorm.DB, def unitCatalogDefault, sortOrder int) error {
	code := strings.ToUpper(strings.TrimSpace(def.Code))
	var existing TenantUnit
	err := tx.Where("code = ?", code).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row := TenantUnit{
			Code:      code,
			Name:      def.Name,
			Symbol:    def.Symbol,
			IsSystem:  true,
			SortOrder: sortOrder,
			Active:    def.Active,
		}
		return tx.Create(&row).Error
	}
	if err != nil {
		return err
	}
	// Fila ya existente (reseed / backfill de tenant viejo): solo completa lo que esté vacío, no
	// pisa nombre/símbolo/activo si el tenant ya los tenía definidos a su manera.
	updates := map[string]interface{}{"is_system": true}
	if strings.TrimSpace(existing.Name) == "" {
		updates["name"] = def.Name
	}
	if strings.TrimSpace(existing.Symbol) == "" {
		updates["symbol"] = def.Symbol
	}
	return tx.Model(&existing).Updates(updates).Error
}

// DefaultUnitByCode busca la definición SUNAT conocida para un código — usado al materializar por
// primera vez una fila del catálogo a partir de texto suelto (alta antigua, importación masiva)
// para que la unidad nazca con un nombre legible en vez de solo el código.
func DefaultUnitByCode(code string) (unitCatalogDefault, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, def := range defaultUnitsCatalog {
		if def.Code == code {
			return def, true
		}
	}
	return unitCatalogDefault{}, false
}

// EnsureUnitByCode obtiene (o crea si no existe) la fila del catálogo para un código SUNAT ya
// normalizado. Usado por ProductService para garantizar que todo producto quede vinculado por ID,
// incluso cuando el caller solo mandó el código en texto (bulk import, clientes de API viejos).
func EnsureUnitByCode(db *gorm.DB, code string) (uint, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		code = "NIU"
	}
	var u TenantUnit
	err := db.Where("code = ?", code).First(&u).Error
	if err == nil {
		return u.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	name := code
	symbol := ""
	if def, ok := DefaultUnitByCode(code); ok {
		name = def.Name
		symbol = def.Symbol
	}
	row := TenantUnit{Code: code, Name: name, Symbol: symbol, Active: true}
	if err := db.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// BackfillProductUnitIDs vincula por ID los productos existentes cuyo unit_id sea NULL, buscando
// (o creando) la fila del catálogo cuyo código coincida con su Unit (texto) actual.
func BackfillProductUnitIDs(db *gorm.DB) error {
	var products []struct {
		ID   uint
		Unit string
	}
	if err := db.Model(&TenantProduct{}).Select("id, unit").Where("unit_id IS NULL").Find(&products).Error; err != nil {
		return err
	}
	cache := make(map[string]uint, len(defaultUnitsCatalog))
	for _, p := range products {
		code := strings.ToUpper(strings.TrimSpace(p.Unit))
		if code == "" {
			code = "NIU"
		}
		id, ok := cache[code]
		if !ok {
			resolved, err := EnsureUnitByCode(db, code)
			if err != nil {
				return err
			}
			id = resolved
			cache[code] = id
		}
		if err := db.Model(&TenantProduct{}).Where("id = ?", p.ID).UpdateColumn("unit_id", id).Error; err != nil {
			return err
		}
	}
	return nil
}
