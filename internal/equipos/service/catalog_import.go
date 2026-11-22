package service

import (
	"math"
	"strconv"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const maxCatalogImportRows = 2000

// CatalogImportRow fila cruda de la plantilla de catálogo (todo texto: la validación vive aquí, no en el navegador).
// Row es el número de fila en el Excel, solo para mostrar el motivo de un error.
type CatalogImportRow struct {
	Row             int    `json:"row"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	ReferencePrice  string `json:"reference_price"`
	YellowThreshold string `json:"yellow_threshold"`
	GreenThreshold  string `json:"green_threshold"`
	Active          string `json:"active"`
	Notes           string `json:"notes"`
}

type CatalogImportItem struct {
	Row    int    `json:"row"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Action string `json:"action"` // crear | actualizar | sin_cambios | error
	Error  string `json:"error,omitempty"`
}

type CatalogImportResult struct {
	Total     int                 `json:"total"`
	Created   int                 `json:"created"`
	Updated   int                 `json:"updated"`
	Unchanged int                 `json:"unchanged"`
	Errors    int                 `json:"errors"`
	Committed bool                `json:"committed"`
	Items     []CatalogImportItem `json:"items"`
}

var kindAliases = map[string]string{
	"equipo": database.EquipKindEquipo, "equipos": database.EquipKindEquipo,
	"consumible": database.EquipKindConsumible, "consumibles": database.EquipKindConsumible,
	"etiqueta": database.EquipKindEtiqueta, "etiquetas": database.EquipKindEtiqueta,
	"accesorio": database.EquipKindAccesorio, "accesorios": database.EquipKindAccesorio,
}

// parseImportNumber acepta «12», «12.50» y «12,50»; vacío devuelve present=false sin error.
func parseImportNumber(raw string) (v float64, present bool, err error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, " ", ""))
	if raw == "" {
		return 0, false, nil
	}
	if strings.Contains(raw, ",") && !strings.Contains(raw, ".") {
		raw = strings.Replace(raw, ",", ".", 1)
	}
	v, err = strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, true, strconv.ErrSyntax
	}
	return v, true, nil
}

func parseImportBool(raw string) (v bool, present bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, false, true
	case "si", "sí", "s", "1", "true", "verdadero", "activo", "x":
		return true, true, true
	case "no", "n", "0", "false", "falso", "inactivo":
		return false, true, true
	}
	return false, true, false
}

// ImportCatalog crea o actualiza productos desde las filas de la plantilla. Sin `commit` solo calcula el resultado.
//
// Regla para un código que ya existe (sin distinguir mayúsculas): se actualizan nombre, tipo, precio, umbrales,
// activo y notas, pero solo los campos que la fila trae con valor; una celda vacía conserva el valor actual.
// Nunca se borra ningún producto ni se toca el stock/kardex. Reimportar el mismo archivo da «sin cambios».
// Las filas con error se omiten; el resto se aplica en una sola transacción.
func (s *Service) ImportCatalog(rows []CatalogImportRow, commit bool) (*CatalogImportResult, error) {
	if len(rows) == 0 {
		return nil, invalid("el archivo no trae productos")
	}
	if len(rows) > maxCatalogImportRows {
		return nil, invalid("el archivo supera las %d filas; divídelo", maxCatalogImportRows)
	}
	var existing []database.EquipProduct
	if err := s.db.Find(&existing).Error; err != nil {
		return nil, err
	}
	byCode := make(map[string]*database.EquipProduct, len(existing))
	for i := range existing {
		byCode[strings.ToLower(existing[i].Code)] = &existing[i]
	}
	var combos []database.EquipCombo
	if err := s.db.Select("code").Find(&combos).Error; err != nil {
		return nil, err
	}
	comboCodes := make(map[string]bool, len(combos))
	for _, c := range combos {
		comboCodes[strings.ToLower(c.Code)] = true
	}

	res := &CatalogImportResult{Committed: commit, Items: []CatalogImportItem{}}
	seen := map[string]int{}
	var toCreate []*database.EquipProduct
	var toUpdate []*database.EquipProduct

	for _, r := range rows {
		code := strings.TrimSpace(r.Code)
		name := strings.TrimSpace(r.Name)
		if code == "" && name == "" && strings.TrimSpace(r.Kind) == "" && strings.TrimSpace(r.ReferencePrice) == "" {
			continue // fila en blanco
		}
		res.Total++
		item := CatalogImportItem{Row: r.Row, Code: code, Name: name}
		bad := func(msg string) {
			item.Action, item.Error = "error", msg
			res.Errors++
			res.Items = append(res.Items, item)
		}
		key := strings.ToLower(code)
		switch {
		case code == "":
			bad("el código es obligatorio")
			continue
		case len(code) > 60:
			bad("el código no puede superar 60 caracteres")
			continue
		case name == "":
			bad("el nombre es obligatorio")
			continue
		case len(name) > 150:
			bad("el nombre no puede superar 150 caracteres")
			continue
		}
		if first, dup := seen[key]; dup {
			bad("código repetido en el archivo (ya aparece en la fila " + strconv.Itoa(first) + ")")
			continue
		}
		seen[key] = r.Row
		if comboCodes[key] {
			bad("el código ya lo usa un combo")
			continue
		}

		cur := byCode[key]
		var p database.EquipProduct // valores finales
		if cur != nil {
			p = *cur
		} else {
			p = database.EquipProduct{Code: code, Kind: database.EquipKindEquipo, Active: true}
		}
		p.Name = name

		if k := strings.ToLower(strings.TrimSpace(r.Kind)); k != "" {
			kind, ok := kindAliases[k]
			if !ok {
				bad("tipo inválido «" + strings.TrimSpace(r.Kind) + "» (equipo, consumible, etiqueta o accesorio)")
				continue
			}
			p.Kind = kind
		}
		price, present, err := parseImportNumber(r.ReferencePrice)
		if err != nil {
			bad("el precio de referencia no es un número")
			continue
		}
		if present {
			if price < 0 {
				bad("el precio de referencia no puede ser negativo")
				continue
			}
			p.ReferencePrice = math.Round(price*100) / 100
		}
		failed := false
		for _, th := range []struct {
			raw   string
			label string
			dst   *int
		}{{r.YellowThreshold, "amarillo", &p.YellowThreshold}, {r.GreenThreshold, "verde", &p.GreenThreshold}} {
			v, present, err := parseImportNumber(th.raw)
			switch {
			case err != nil:
				bad("el umbral " + th.label + " no es un número")
				failed = true
			case present && (v < 0 || v != math.Trunc(v)):
				bad("el umbral " + th.label + " debe ser un entero no negativo")
				failed = true
			case present && v > 1e9:
				bad("el umbral " + th.label + " es demasiado grande")
				failed = true
			case present:
				*th.dst = int(v)
			}
			if failed {
				break
			}
		}
		if failed {
			continue
		}
		if p.YellowThreshold > p.GreenThreshold {
			bad("el umbral amarillo no puede ser mayor que el verde")
			continue
		}
		active, present, ok := parseImportBool(r.Active)
		if !ok {
			bad("«activo» debe ser SI o NO")
			continue
		}
		if present {
			p.Active = active
		}
		if n := strings.TrimSpace(r.Notes); n != "" {
			if len(n) > 255 {
				bad("las notas no pueden superar 255 caracteres")
				continue
			}
			p.Notes = n
		}

		switch {
		case cur == nil:
			item.Action = "crear"
			res.Created++
			toCreate = append(toCreate, &p)
		case sameProduct(*cur, p):
			item.Action = "sin_cambios"
			res.Unchanged++
		default:
			item.Action = "actualizar"
			res.Updated++
			toUpdate = append(toUpdate, &p)
		}
		res.Items = append(res.Items, item)
	}
	if res.Total == 0 {
		return nil, invalid("el archivo no trae productos")
	}
	if !commit {
		return res, nil
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, p := range toCreate {
			wantActive := p.Active
			if err := tx.Create(p).Error; err != nil {
				return err
			}
			// gorm aplica el default:true al crear con false (valor cero): se corrige explícitamente.
			if !wantActive {
				if err := tx.Model(p).Update("active", false).Error; err != nil {
					return err
				}
			}
		}
		for _, p := range toUpdate {
			if err := tx.Save(p).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func sameProduct(a, b database.EquipProduct) bool {
	return a.Name == b.Name && a.Kind == b.Kind && a.Notes == b.Notes && a.Active == b.Active &&
		a.YellowThreshold == b.YellowThreshold && a.GreenThreshold == b.GreenThreshold &&
		math.Abs(a.ReferencePrice-b.ReferencePrice) < 0.005
}
