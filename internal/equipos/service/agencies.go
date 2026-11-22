package service

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// AgencyInput datos editables de una agencia de un transportista.
type AgencyInput struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Province   string `json:"province"`
	District   string `json:"district"`
	Ubigeo     string `json:"ubigeo"`
	Address    string `json:"address"`
	Active     *bool  `json:"active"`
}

func (in *AgencyInput) normalize() error {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = collapse(in.Name)
	in.Address = collapse(in.Address)
	in.Department, in.Province, in.District = collapse(in.Department), collapse(in.Province), collapse(in.District)
	in.Ubigeo = strings.TrimSpace(in.Ubigeo)
	if in.Name == "" {
		return invalid("el nombre de la agencia es obligatorio")
	}
	if len(in.Name) > 150 {
		return invalid("el nombre de la agencia es demasiado largo")
	}
	if in.Ubigeo != "" && (!onlyDigits(in.Ubigeo) || len(in.Ubigeo) != 6) {
		return invalid("el ubigeo debe tener 6 dígitos")
	}
	if (in.Province != "" || in.District != "") && in.Department == "" {
		return invalid("indica el departamento de la agencia")
	}
	if name, ok := peruDepartments[foldText(in.Department)]; ok {
		in.Department = name
	}
	return nil
}

func locationStatus(dep string) string {
	if dep == "" {
		return database.AgencyLocPending
	}
	return database.AgencyLocConfirmed
}

// ListAgencies agencias de un transportista (cientos como máximo: se devuelven todas para filtrar en pantalla).
func (s *Service) ListAgencies(carrierID uint, includeInactive bool) ([]database.EquipCarrierAgency, error) {
	var rows []database.EquipCarrierAgency
	tx := s.db.Where("carrier_id = ?", carrierID)
	if !includeInactive {
		tx = tx.Where("active = ?", true)
	}
	err := tx.Order("name ASC").Find(&rows).Error
	return rows, err
}

func (s *Service) agencyNameTaken(tx *gorm.DB, carrierID uint, name string, exceptID uint) (bool, error) {
	q := tx.Model(&database.EquipCarrierAgency{}).Where("carrier_id = ? AND LOWER(name) = ?", carrierID, strings.ToLower(name))
	if exceptID > 0 {
		q = q.Where("id <> ?", exceptID)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Service) CreateAgency(carrierID uint, in AgencyInput) (*database.EquipCarrierAgency, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	var c database.EquipCarrier
	if found, err := findOne(s.db, &c, "id = ?", carrierID); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("el transportista no existe")
	}
	if taken, err := s.agencyNameTaken(s.db, carrierID, in.Name, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, invalid("%s ya tiene una agencia llamada %q", c.Name, in.Name)
	}
	a := &database.EquipCarrierAgency{
		CarrierID: carrierID, Code: in.Code, Name: in.Name, Department: in.Department, Province: in.Province,
		District: in.District, Ubigeo: in.Ubigeo, Address: in.Address, LocationStatus: locationStatus(in.Department), Active: true,
	}
	if in.Active != nil {
		a.Active = *in.Active
	}
	if err := s.db.Create(a).Error; err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) UpdateAgency(id uint, in AgencyInput) (*database.EquipCarrierAgency, error) {
	var a database.EquipCarrierAgency
	if found, err := findOne(s.db, &a, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("agencia no encontrada")
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	if taken, err := s.agencyNameTaken(s.db, a.CarrierID, in.Name, id); err != nil {
		return nil, err
	} else if taken {
		return nil, invalid("ya existe otra agencia llamada %q en este transportista", in.Name)
	}
	a.Code, a.Name, a.Department, a.Province, a.District = in.Code, in.Name, in.Department, in.Province, in.District
	a.Ubigeo, a.Address, a.LocationStatus = in.Ubigeo, in.Address, locationStatus(in.Department)
	if in.Active != nil {
		a.Active = *in.Active
	}
	if !a.Active {
		var c database.EquipCarrier
		if found, _ := findOne(s.db, &c, "id = ? AND default_origin_agency_id = ?", a.CarrierID, a.ID); found {
			return nil, invalid("esta agencia es el origen por defecto de %s: cámbialo antes de desactivarla", c.Name)
		}
	}
	if err := s.db.Save(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// checkOriginAgency valida que la agencia exista, esté activa y sea del transportista.
func (s *Service) checkOriginAgency(tx *gorm.DB, carrierID uint, agencyID *uint) error {
	if agencyID == nil {
		return nil
	}
	var a database.EquipCarrierAgency
	if found, err := findOne(tx, &a, "id = ? AND carrier_id = ? AND active = ?", *agencyID, carrierID, true); err != nil {
		return err
	} else if !found {
		return invalid("la agencia de origen no existe, está inactiva o no es de este transportista")
	}
	return nil
}

// ── Importación masiva ─────────────────────────────────────────────────────

// AgencyImportRow una fila del archivo de agencias. Departamento/provincia/distrito son opcionales.
type AgencyImportRow struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Province   string `json:"province"`
	District   string `json:"district"`
	Address    string `json:"address"`
}

type AgencyImportItem struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Province   string `json:"province"`
	District   string `json:"district"`
	Status     string `json:"location_status"`
	Action     string `json:"action"` // crear | actualizar | sin_cambios
}

type AgencyImportResult struct {
	Total     int                `json:"total"`
	Created   int                `json:"created"`
	Updated   int                `json:"updated"`
	Unchanged int                `json:"unchanged"`
	Duplicate int                `json:"duplicate"`
	Confirmed int                `json:"confirmed"`
	Suggested int                `json:"suggested"`
	Pending   int                `json:"pending"`
	Committed bool               `json:"committed"`
	Warnings  []string           `json:"warnings"`
	Review    []AgencyImportItem `json:"review"` // muestra de las que quedan por revisar (sugeridas y pendientes)
}

const (
	maxAgencyImportRows = 5000
	maxAgencyReviewRows = 80
)

// ImportAgencies crea o actualiza las agencias de un transportista. Sin `commit` solo calcula el resultado.
// Las agencias cuya ubicación ya fue confirmada a mano no se tocan; el resto se vuelve a cruzar con el ubigeo.
func (s *Service) ImportAgencies(carrierID uint, rows []AgencyImportRow, commit bool) (*AgencyImportResult, error) {
	var c database.EquipCarrier
	if found, err := findOne(s.db, &c, "id = ?", carrierID); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("el transportista no existe")
	}
	if len(rows) == 0 {
		return nil, invalid("el archivo no trae agencias")
	}
	if len(rows) > maxAgencyImportRows {
		return nil, invalid("el archivo supera las %d agencias; divídelo", maxAgencyImportRows)
	}
	ix, err := loadUbiIndex(s.db)
	if err != nil {
		return nil, err
	}
	res := &AgencyImportResult{Committed: commit}
	if len(ix.places) == 0 {
		res.Warnings = append(res.Warnings, "El catálogo de ubigeo está vacío: las agencias se cargarán sin ubicación.")
	}
	var existing []database.EquipCarrierAgency
	if err := s.db.Where("carrier_id = ?", carrierID).Find(&existing).Error; err != nil {
		return nil, err
	}
	byName := make(map[string]*database.EquipCarrierAgency, len(existing))
	for i := range existing {
		byName[strings.ToLower(existing[i].Name)] = &existing[i]
	}

	var toCreate []database.EquipCarrierAgency
	var toUpdate []*database.EquipCarrierAgency
	seen := map[string]bool{}
	for _, r := range rows {
		name := collapse(r.Name)
		if name == "" {
			continue
		}
		k := strings.ToLower(name)
		if seen[k] {
			res.Duplicate++
			continue
		}
		seen[k] = true
		res.Total++

		in := AgencyInput{Code: r.Code, Name: name, Department: r.Department, Province: r.Province, District: r.District, Address: r.Address}
		if err := in.normalize(); err != nil {
			res.Warnings = append(res.Warnings, name+": "+err.Error())
			res.Total--
			continue
		}
		loc := AgencyGuess{Department: in.Department, Province: in.Province, District: in.District, Status: locationStatus(in.Department)}
		if in.Department == "" {
			loc = ix.Guess(name)
		}

		item := AgencyImportItem{Code: in.Code, Name: name, Department: loc.Department, Province: loc.Province, District: loc.District, Status: loc.Status}
		cur := byName[k]
		switch {
		case cur == nil:
			item.Action = "crear"
			res.Created++
			toCreate = append(toCreate, database.EquipCarrierAgency{
				CarrierID: carrierID, Code: in.Code, Name: name, Department: loc.Department, Province: loc.Province, District: loc.District,
				Ubigeo: loc.Ubigeo, Address: in.Address, LocationStatus: loc.Status, Active: true,
			})
		default:
			changed := false
			if in.Code != "" && cur.Code != in.Code {
				cur.Code, changed = in.Code, true
			}
			if in.Address != "" && cur.Address != in.Address {
				cur.Address, changed = in.Address, true
			}
			// Lo confirmado a mano se respeta; lo demás se recalcula.
			if cur.LocationStatus != database.AgencyLocConfirmed || in.Department != "" {
				if cur.Department != loc.Department || cur.Province != loc.Province || cur.District != loc.District || cur.LocationStatus != loc.Status {
					cur.Department, cur.Province, cur.District, cur.Ubigeo, cur.LocationStatus = loc.Department, loc.Province, loc.District, loc.Ubigeo, loc.Status
					changed = true
				}
			} else {
				item.Department, item.Province, item.District, item.Status = cur.Department, cur.Province, cur.District, cur.LocationStatus
			}
			if changed {
				item.Action = "actualizar"
				res.Updated++
				toUpdate = append(toUpdate, cur)
			} else {
				item.Action = "sin_cambios"
				res.Unchanged++
			}
		}
		switch item.Status {
		case database.AgencyLocConfirmed:
			res.Confirmed++
		case database.AgencyLocSuggested:
			res.Suggested++
			if len(res.Review) < maxAgencyReviewRows {
				res.Review = append(res.Review, item)
			}
		default:
			res.Pending++
			if len(res.Review) < maxAgencyReviewRows {
				res.Review = append(res.Review, item)
			}
		}
	}
	if !commit {
		return res, nil
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if len(toCreate) > 0 {
			if err := tx.CreateInBatches(toCreate, 200).Error; err != nil {
				return err
			}
		}
		for _, a := range toUpdate {
			if err := tx.Save(a).Error; err != nil {
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

// applyAgencies fija las agencias de origen y destino del envío a partir del catálogo del transportista.
// El destino copia nombre y ubicación para que el rótulo conserve lo que se imprimió aunque la agencia cambie después.
func (s *Service) applyAgencies(tx *gorm.DB, sh *database.EquipShipment, in *ShipmentInput) error {
	if sh.CarrierID == nil {
		sh.OriginAgencyID, sh.DestinationAgencyID = nil, nil
		return nil
	}
	cid := *sh.CarrierID
	origin := in.OriginAgencyID
	if origin == nil && sh.ID == 0 {
		// Envío nuevo: se propone la agencia donde el dueño entrega normalmente los paquetes.
		if c, err := s.carrierByIDTx(tx, sh.CarrierID); err != nil {
			return err
		} else if c != nil {
			origin = c.DefaultOriginAgencyID
		}
	}
	if err := s.checkOriginAgency(tx, cid, origin); err != nil {
		return err
	}
	sh.OriginAgencyID = origin

	if in.DestinationAgencyID == nil {
		sh.DestinationAgencyID = nil
		return nil
	}
	var a database.EquipCarrierAgency
	if found, err := findOne(tx, &a, "id = ? AND carrier_id = ?", *in.DestinationAgencyID, cid); err != nil {
		return err
	} else if !found {
		return invalid("la agencia de destino no es de este transportista")
	}
	id := a.ID
	sh.DestinationAgencyID = &id
	sh.DestinationAgency = a.Name
	if a.Address != "" {
		sh.DestinationAgency = a.Name + " - " + a.Address
	}
	if len(sh.DestinationAgency) > 150 {
		sh.DestinationAgency = sh.DestinationAgency[:150]
	}
	if a.Department != "" {
		sh.DestinationDepartment, sh.DestinationProvince, sh.DestinationDistrict, sh.Ubigeo = a.Department, a.Province, a.District, a.Ubigeo
	}
	return nil
}

// agencyName nombre de una agencia (vacío si no existe); para mostrarlo en el detalle del envío.
func (s *Service) agencyName(tx *gorm.DB, id *uint) string {
	if id == nil {
		return ""
	}
	var a database.EquipCarrierAgency
	if found, _ := findOne(tx, &a, "id = ?", *id); !found {
		return ""
	}
	return a.Name
}
