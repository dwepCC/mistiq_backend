package database

import (
	"bufio"
	"errors"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ubigeoRe código de ubigeo INEI: 6 dígitos (2 departamento + 2 provincia + 2 distrito).
var ubigeoRe = regexp.MustCompile(`^\d{6}$`)

// UpsertUbigeoRegionesProvincias agrega los departamentos y provincias que falten (SeedUbigeoRegionesProvincias solo siembra con la
// tabla vacía, así que una provincia omitida —p. ej. Calca, 080400— nunca llegaba a las BD ya creadas). No modifica los existentes.
func UpsertUbigeoRegionesProvincias(db *gorm.DB) error {
	var regIDs []string
	if err := db.Model(&UbiRegion{}).Pluck("id", &regIDs).Error; err != nil {
		return err
	}
	haveReg := make(map[string]bool, len(regIDs))
	for _, id := range regIDs {
		haveReg[id] = true
	}
	var missingReg []UbiRegion
	for _, r := range ubiRegionesPeru {
		if !haveReg[r.ID] {
			missingReg = append(missingReg, r)
		}
	}
	if len(missingReg) > 0 {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&missingReg).Error; err != nil {
			return err
		}
	}
	var provIDs []string
	if err := db.Model(&UbiProvincia{}).Pluck("id", &provIDs).Error; err != nil {
		return err
	}
	haveProv := make(map[string]bool, len(provIDs))
	for _, id := range provIDs {
		haveProv[id] = true
	}
	var missingProv []UbiProvincia
	for _, p := range ubiProvinciasPeru {
		if !haveProv[p.ID] {
			missingProv = append(missingProv, p)
		}
	}
	if len(missingProv) > 0 {
		return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&missingProv).Error
	}
	return nil
}

// UpsertUbigeoDistritos agrega a ubi_distritos los distritos del dataset embebido que todavía no existan. No modifica ni borra los que ya
// están: es seguro correrlo muchas veces y sobre BD con datos propios. Sirve para propagar los distritos creados después de la carga
// inicial (p. ej. San Antonio, Mariscal Nieto, Moquegua — Ley 31216, ubigeo 180107) a la BD central y a todos los tenants.
// Devuelve cuántos distritos agregó.
func UpsertUbigeoDistritos(db *gorm.DB) (int, error) {
	if strings.TrimSpace(ubigeoDistritosEmbedded) == "" {
		return 0, nil
	}
	var have []string
	if err := db.Model(&UbiDistrito{}).Pluck("id", &have).Error; err != nil {
		return 0, err
	}
	exists := make(map[string]bool, len(have))
	for _, id := range have {
		exists[id] = true
	}

	var missing []UbiDistrito
	sc := bufio.NewScanner(strings.NewReader(ubigeoDistritosEmbedded))
	for i := 0; sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || (i == 0 && strings.HasPrefix(line, "id")) {
			continue
		}
		p := strings.Split(line, "|")
		if len(p) < 5 {
			continue
		}
		id, nombre := strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
		if !ubigeoRe.MatchString(id) || nombre == "" || exists[id] {
			continue
		}
		missing = append(missing, UbiDistrito{
			ID: id, Nombre: nombre, InfoBusqueda: strings.TrimSpace(p[2]),
			ProvinciaID: strings.TrimSpace(p[3]), RegionID: strings.TrimSpace(p[4]),
		})
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if len(missing) == 0 {
		return 0, nil
	}
	// DoNothing por si otro proceso lo insertó entre el SELECT y el INSERT.
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(missing, 200).Error; err != nil {
		return 0, err
	}
	return len(missing), nil
}

// ErrUbigeoInvalid el código no es un ubigeo utilizable (formato o departamento/provincia inexistentes).
var ErrUbigeoInvalid = errors.New("ubigeo inválido")

// AddUbigeoDistrito registra un distrito que el catálogo aún no tiene (p. ej. uno creado por ley después de la última actualización),
// para que un tenant pueda configurarlo y emitir sin esperar una actualización del sistema. Exige que el código tenga 6 dígitos y que su
// departamento y provincia existan; si el distrito ya está, devuelve el existente sin cambiarlo.
func AddUbigeoDistrito(db *gorm.DB, id, nombre string) (*UbiDistrito, bool, error) {
	id, nombre = strings.TrimSpace(id), strings.Join(strings.Fields(nombre), " ")
	if !ubigeoRe.MatchString(id) {
		return nil, false, errors.New("el ubigeo debe tener 6 dígitos (código INEI del distrito)")
	}
	if len([]rune(nombre)) < 3 || len([]rune(nombre)) > 100 {
		return nil, false, errors.New("el nombre del distrito debe tener entre 3 y 100 caracteres")
	}
	var existing UbiDistrito
	if err := db.Where("id = ?", id).First(&existing).Error; err == nil {
		return &existing, false, nil
	}
	provID, regID := id[:4]+"00", id[:2]+"0000"
	var prov UbiProvincia
	if err := db.Where("id = ? AND region_id = ?", provID, regID).First(&prov).Error; err != nil {
		return nil, false, ErrUbigeoInvalid
	}
	var reg UbiRegion
	if err := db.Where("id = ?", regID).First(&reg).Error; err != nil {
		return nil, false, ErrUbigeoInvalid
	}
	d := UbiDistrito{
		ID: id, Nombre: nombre, ProvinciaID: provID, RegionID: regID,
		InfoBusqueda: nombre + " / " + prov.Nombre + "  / " + reg.Nombre,
	}
	if err := db.Create(&d).Error; err != nil {
		return nil, false, err
	}
	return &d, true, nil
}
