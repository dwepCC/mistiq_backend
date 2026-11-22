package service

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"tukifac/internal/shalom"
	"tukifac/pkg/database"
	"tukifac/pkg/secrets"
)

// Integración con Shalom API Perú. Regla de diseño: el panel lee SIEMPRE de la BD (agencias, departamentos,
// provincias, distritos, estado del envío); la API solo se llama desde la sincronización (botón o job) y desde
// acciones explícitas sobre un envío. Nada de aquí crea guías reales.

// newShalomClient se sustituye en los tests por un cliente apuntando a un servidor falso.
var newShalomClient = func(baseURL, apiKey string) *shalom.Client { return shalom.New(baseURL, apiKey, nil) }

// agenciesSyncInterval: el job automático solo vuelve a sincronizar si pasó este tiempo.
const agenciesSyncInterval = 24 * time.Hour

// ── Configuración ──────────────────────────────────────────────────────────

// ProviderView configuración visible en pantalla: la API key NUNCA se devuelve, solo si existe y sus 4 últimos caracteres.
type ProviderView struct {
	Provider              string     `json:"provider"`
	Enabled               bool       `json:"enabled"`
	BaseURL               string     `json:"base_url"`
	APIKeySet             bool       `json:"api_key_set"`
	APIKeyHint            string     `json:"api_key_hint"`
	ProEmail              string     `json:"pro_email"`
	ProPasswordSet        bool       `json:"pro_password_set"`
	EncryptionReady       bool       `json:"encryption_ready"`
	WebhookURL            string     `json:"webhook_url"`
	WebhookSecretSet      bool       `json:"webhook_secret_set"`
	WebhookVerified       bool       `json:"webhook_verified"`
	LastAgenciesSyncAt    *time.Time `json:"last_agencies_sync_at"`
	LastAgenciesSyncCount int        `json:"last_agencies_sync_count"`
	LastLocationsSyncAt   *time.Time `json:"last_locations_sync_at"`
	AgencyCount           int64      `json:"agency_count"`
}

// ProviderSettingsInput campos editables; nil = no cambia. APIKey vacío no borra la clave (usar ClearAPIKey).
type ProviderSettingsInput struct {
	APIKey      *string `json:"api_key"`
	ClearAPIKey bool    `json:"clear_api_key"`
	BaseURL     *string `json:"base_url"`
	Enabled     *bool   `json:"enabled"`
	WebhookURL  *string `json:"webhook_url"`
	// Cuenta de Shalom Pro (cotizar tarifas). ProPassword vacío no borra la existente (usar ClearPro).
	ProEmail    *string `json:"pro_email"`
	ProPassword *string `json:"pro_password"`
	ClearPro    bool    `json:"clear_pro"`
}

func (s *Service) providerSettings() (*database.EquipProviderSettings, error) {
	var st database.EquipProviderSettings
	found, err := findOne(s.db, &st, "provider = ?", database.ProviderShalom)
	if err != nil {
		return nil, err
	}
	if !found {
		st = database.EquipProviderSettings{Provider: database.ProviderShalom, BaseURL: shalom.DefaultBaseURL}
		if err := s.db.Create(&st).Error; err != nil {
			return nil, err
		}
	}
	return &st, nil
}

func (s *Service) shalomCarrier() (*database.EquipCarrier, error) {
	var c database.EquipCarrier
	found, err := findOne(s.db, &c, "code = ?", database.ProviderShalom)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, invalid("no existe el transportista «shalom»: créalo en Transportistas o importa el Excel")
	}
	return &c, nil
}

func (s *Service) ProviderSettingsView() (*ProviderView, error) {
	st, err := s.providerSettings()
	if err != nil {
		return nil, err
	}
	var n int64
	if c, err := s.shalomCarrier(); err == nil {
		s.db.Model(&database.EquipCarrierAgency{}).Where("carrier_id = ? AND provider_ref > 0", c.ID).Count(&n)
	}
	_, encErr := secrets.Encrypt("test")
	return &ProviderView{
		Provider: st.Provider, Enabled: st.Enabled, BaseURL: st.BaseURL,
		APIKeySet: st.APIKeyEnc != "", APIKeyHint: st.APIKeyHint, ProEmail: st.ProEmail, ProPasswordSet: st.ProPasswordEnc != "", EncryptionReady: encErr == nil,
		WebhookURL: st.WebhookURL, WebhookSecretSet: st.WebhookSecretEnc != "", WebhookVerified: st.WebhookVerified,
		LastAgenciesSyncAt: st.LastAgenciesSyncAt, LastAgenciesSyncCount: st.LastAgenciesSyncCount,
		LastLocationsSyncAt: st.LastLocationsSyncAt, AgencyCount: n,
	}, nil
}

func (s *Service) SaveProviderSettings(in ProviderSettingsInput) (*ProviderView, error) {
	st, err := s.providerSettings()
	if err != nil {
		return nil, err
	}
	if in.BaseURL != nil {
		u := strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
		if u == "" {
			u = shalom.DefaultBaseURL
		}
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" {
			return nil, invalid("la URL base debe ser https")
		}
		st.BaseURL = u
	}
	if in.WebhookURL != nil {
		u := strings.TrimSpace(*in.WebhookURL)
		if u != "" {
			if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" {
				return nil, invalid("la URL del webhook debe ser https y pública")
			}
		}
		if u != st.WebhookURL {
			st.WebhookVerified = false // otra URL exige volver a pasar la verificación
		}
		st.WebhookURL = u
	}
	switch {
	case in.ClearAPIKey:
		st.APIKeyEnc, st.APIKeyHint, st.Enabled = "", "", false
	case in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "":
		key := strings.TrimSpace(*in.APIKey)
		enc, err := secrets.Encrypt(key)
		if err != nil {
			return nil, invalid("no se puede guardar la API key: %v", err)
		}
		st.APIKeyEnc, st.APIKeyHint = enc, secrets.Hint(key)
	}
	if in.ClearPro {
		st.ProEmail, st.ProPasswordEnc = "", ""
		clearProSession()
	} else {
		if in.ProEmail != nil {
			st.ProEmail = strings.TrimSpace(*in.ProEmail)
			clearProSession()
		}
		if in.ProPassword != nil && strings.TrimSpace(*in.ProPassword) != "" {
			enc, err := secrets.Encrypt(*in.ProPassword)
			if err != nil {
				return nil, invalid("no se puede guardar la clave de Shalom Pro: %v", err)
			}
			st.ProPasswordEnc = enc
			clearProSession()
		}
	}
	if in.Enabled != nil {
		if *in.Enabled && st.APIKeyEnc == "" {
			return nil, invalid("carga primero la API key de Shalom")
		}
		st.Enabled = *in.Enabled
	}
	if err := s.db.Save(st).Error; err != nil {
		return nil, err
	}
	return s.ProviderSettingsView()
}

// shalomClient devuelve el cliente listo (API key descifrada). Falla con un error de validación claro si la
// integración no está activa: así ninguna pantalla llama a la API sin haberla habilitado.
func (s *Service) shalomClient() (*shalom.Client, *database.EquipProviderSettings, error) {
	st, err := s.providerSettings()
	if err != nil {
		return nil, nil, err
	}
	if !st.Enabled || st.APIKeyEnc == "" {
		return nil, nil, invalid("la integración con Shalom no está activa: carga la API key y habilítala en Transportistas")
	}
	key, err := secrets.Decrypt(st.APIKeyEnc)
	if err != nil {
		return nil, nil, invalid("%v", err)
	}
	return newShalomClient(st.BaseURL, key), st, nil
}

// providerError traduce errores de la API a mensajes de validación para el panel.
func providerError(err error) error {
	if err == nil {
		return nil
	}
	if ae, ok := shalom.AsAPIError(err); ok {
		switch {
		case ae.IsAuth():
			return invalid("Shalom rechazó la API key (%s): revísala en Transportistas", ae.Code)
		case ae.Status == 429:
			return invalid("Shalom limitó las consultas; reintenta en %d s", int(ae.RetryAfter.Seconds())+1)
		case ae.IsNotFound():
			return invalid("Shalom no encontró el recurso: %s", ae.Message)
		}
		return invalid("Shalom respondió un error (%d): %s", ae.Status, ae.Message)
	}
	return fmt.Errorf("no se pudo consultar a Shalom: %w", err)
}

// ── Agencias (BD primero; la API solo al sincronizar) ──────────────────────

type SyncAgenciesResult struct {
	Skipped bool `json:"skipped"` // el job automático omitió la sincronización (hay una reciente)
	Total   int  `json:"total"`
	Created int  `json:"created"`
	Updated int  `json:"updated"`
}

// SyncAgencies baja el catálogo de agencias de Shalom y lo fusiona en EquipCarrierAgency sin pisar lo que el
// usuario ya corrigió: a las existentes solo se les completa lo vacío y se enlaza el id del proveedor; la
// activación solo se baja (si Shalom deshabilita una agencia), nunca se reactiva sola. force=false (job) omite
// la llamada si la última sincronización tiene menos de 24 h.
func (s *Service) SyncAgencies(ctx context.Context, force bool) (*SyncAgenciesResult, error) {
	cl, st, err := s.shalomClient()
	if err != nil {
		return nil, err
	}
	if !force && st.LastAgenciesSyncAt != nil && time.Since(*st.LastAgenciesSyncAt) < agenciesSyncInterval {
		return &SyncAgenciesResult{Skipped: true}, nil
	}
	carrier, err := s.shalomCarrier()
	if err != nil {
		return nil, err
	}
	list, err := cl.Agencies(ctx)
	if err != nil {
		return nil, providerError(err)
	}
	res := &SyncAgenciesResult{Total: len(list)}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var existing []database.EquipCarrierAgency
		if err := tx.Where("carrier_id = ?", carrier.ID).Find(&existing).Error; err != nil {
			return err
		}
		byRef := map[int]*database.EquipCarrierAgency{}
		byCode := map[string]*database.EquipCarrierAgency{}
		byName := map[string]*database.EquipCarrierAgency{}
		for i := range existing {
			a := &existing[i]
			if a.ProviderRef > 0 {
				byRef[a.ProviderRef] = a
			}
			if a.Code != "" {
				byCode[strings.ToLower(a.Code)] = a
			}
			byName[strings.ToLower(a.Name)] = a
		}
		for _, p := range list {
			name := collapse(p.Name)
			if len(name) > 150 {
				name = name[:150]
			}
			if name == "" {
				continue
			}
			dep, prov, dist := agencyPlace(p)
			ubigeo := ""
			if p.UbiID > 0 {
				ubigeo = fmt.Sprintf("%06d", p.UbiID)
			}
			enabled, aereo := p.Enabled, p.Aereo
			a := byRef[p.ID]
			// El catálogo cargado del Excel usa como código el ID de la agencia en Shalom (ej. 637), no la abreviatura.
			if a == nil {
				if c := byCode[strconv.Itoa(p.ID)]; c != nil && c.ProviderRef == 0 {
					a = c
				}
			}
			if a == nil && p.Abbreviation != "" {
				if c := byCode[strings.ToLower(p.Abbreviation)]; c != nil && c.ProviderRef == 0 {
					a = c
				}
			}
			if a == nil {
				if c := byName[strings.ToLower(name)]; c != nil && c.ProviderRef == 0 {
					a = c
				}
			}
			if a != nil { // existente: enlazar y completar sin pisar
				changed := a.ProviderRef != p.ID
				a.ProviderRef = p.ID
				if a.ProviderEnabled == nil || *a.ProviderEnabled != enabled {
					a.ProviderEnabled, changed = &enabled, true
				}
				if a.ProviderAereo == nil || *a.ProviderAereo != aereo {
					a.ProviderAereo, changed = &aereo, true
				}
				fill := func(dst *string, v string) {
					if *dst == "" && v != "" {
						*dst, changed = v, true
					}
				}
				fill(&a.Code, p.Abbreviation)
				fill(&a.Address, collapse(p.Address))
				fill(&a.Department, dep)
				fill(&a.Province, prov)
				fill(&a.District, dist)
				fill(&a.Ubigeo, ubigeo)
				if !enabled && a.Active {
					a.Active, changed = false, true
				}
				if changed {
					if err := tx.Save(a).Error; err != nil {
						return err
					}
					res.Updated++
				}
				continue
			}
			if c := byName[strings.ToLower(name)]; c != nil { // mismo nombre de otra agencia ya enlazada
				suffix := " (" + p.Abbreviation + ")"
				if p.Abbreviation == "" || len(name)+len(suffix) > 150 {
					suffix = fmt.Sprintf(" (%d)", p.ID)
				}
				name = collapse(name + suffix)
			}
			status := database.AgencyLocPending
			if dep != "" {
				status = database.AgencyLocConfirmed
			}
			n := &database.EquipCarrierAgency{
				CarrierID: carrier.ID, Code: p.Abbreviation, Name: name, Department: dep, Province: prov, District: dist,
				Ubigeo: ubigeo, Address: collapse(p.Address), LocationStatus: status, Active: enabled,
				ProviderRef: p.ID, ProviderEnabled: &enabled, ProviderAereo: &aereo,
			}
			if err := tx.Create(n).Error; err != nil {
				return err
			}
			if !enabled { // el default:true de la columna pisa el false al insertar: se baja explícitamente
				if err := tx.Model(n).Update("active", false).Error; err != nil {
					return err
				}
			}
			byName[strings.ToLower(name)], byRef[p.ID] = n, n
			res.Created++
		}
		now := time.Now()
		return tx.Model(&database.EquipProviderSettings{}).Where("id = ?", st.ID).
			Updates(map[string]any{"last_agencies_sync_at": now, "last_agencies_sync_count": len(list)}).Error
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// agencyPlace deriva departamento/provincia/distrito de la agencia. «nombre» viene como
// "DEPARTAMENTO / PROVINCIA / DISTRITO / LOCAL"; el departamento se normaliza con el catálogo canónico.
func agencyPlace(p shalom.Agency) (dep, prov, dist string) {
	dep, prov = collapse(p.Department), collapse(p.Province)
	if parts := strings.Split(p.Name, "/"); len(parts) >= 3 {
		dist = collapse(parts[2])
	}
	if name, ok := peruDepartments[foldText(dep)]; ok {
		dep = name
	}
	return dep, prov, dist
}

// ── Ubicaciones (solo al elegir departamento) ──────────────────────────────

func (s *Service) upsertLocations(rows []database.EquipProviderLocation) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}, {Name: "kind"}, {Name: "dep_id"}, {Name: "prov_id"}, {Name: "external_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "ubigeo", "synced_at"}),
	}).CreateInBatches(&rows, 200).Error
}

func (s *Service) locationsFromDB(kind string, depID, provID int) ([]database.EquipProviderLocation, error) {
	var rows []database.EquipProviderLocation
	err := s.db.Where("provider = ? AND kind = ? AND dep_id = ? AND prov_id = ?", database.ProviderShalom, kind, depID, provID).
		Order("name ASC").Find(&rows).Error
	return rows, err
}

func (s *Service) touchLocationsSync() {
	s.db.Model(&database.EquipProviderSettings{}).Where("provider = ?", database.ProviderShalom).Update("last_locations_sync_at", time.Now())
}

// ProviderDepartments lee los departamentos de la BD; solo si no hay ninguno (o refresh) llama a la API.
func (s *Service) ProviderDepartments(ctx context.Context, refresh bool) ([]database.EquipProviderLocation, error) {
	rows, err := s.locationsFromDB(database.ProviderLocDepartment, 0, 0)
	if err != nil || (len(rows) > 0 && !refresh) {
		return rows, err
	}
	cl, _, err := s.shalomClient()
	if err != nil {
		if len(rows) > 0 {
			return rows, nil
		}
		return nil, err
	}
	deps, err := cl.Departments(ctx)
	if err != nil {
		return nil, providerError(err)
	}
	now := time.Now()
	out := make([]database.EquipProviderLocation, 0, len(deps))
	for _, d := range deps {
		out = append(out, database.EquipProviderLocation{Provider: database.ProviderShalom, Kind: database.ProviderLocDepartment, ExternalID: d.ID, Name: collapse(d.Name), SyncedAt: now})
	}
	if err := s.upsertLocations(out); err != nil {
		return nil, err
	}
	s.touchLocationsSync()
	return s.locationsFromDB(database.ProviderLocDepartment, 0, 0)
}

// ProviderProvinces devuelve las provincias de un departamento. Al elegir el departamento por primera vez (o con
// refresh) se sincronizan sus provincias Y sus distritos (1 + N llamadas); después todo sale de la BD.
func (s *Service) ProviderProvinces(ctx context.Context, depID int, refresh bool) ([]database.EquipProviderLocation, error) {
	if depID <= 0 {
		return nil, invalid("departamento inválido")
	}
	rows, err := s.locationsFromDB(database.ProviderLocProvince, depID, 0)
	if err != nil || (len(rows) > 0 && !refresh) {
		return rows, err
	}
	cl, _, err := s.shalomClient()
	if err != nil {
		if len(rows) > 0 {
			return rows, nil
		}
		return nil, err
	}
	provs, err := cl.Provinces(ctx, depID)
	if err != nil {
		return nil, providerError(err)
	}
	now := time.Now()
	var all []database.EquipProviderLocation
	for _, p := range provs {
		all = append(all, database.EquipProviderLocation{Provider: database.ProviderShalom, Kind: database.ProviderLocProvince, DepID: depID, ExternalID: p.ID, Name: collapse(p.Name), SyncedAt: now})
		dists, err := cl.Districts(ctx, depID, p.ID)
		if err != nil {
			return nil, providerError(err)
		}
		for _, d := range dists {
			ub := ""
			if d.UbiID > 0 {
				ub = fmt.Sprintf("%06d", d.UbiID)
			}
			all = append(all, database.EquipProviderLocation{Provider: database.ProviderShalom, Kind: database.ProviderLocDistrict, DepID: depID, ProvID: p.ID, ExternalID: d.ID, Name: collapse(d.Name), Ubigeo: ub, SyncedAt: now})
		}
	}
	if err := s.upsertLocations(all); err != nil {
		return nil, err
	}
	s.touchLocationsSync()
	return s.locationsFromDB(database.ProviderLocProvince, depID, 0)
}

// ProviderDistricts lee los distritos de una provincia de la BD (se sincronizaron junto con el departamento).
func (s *Service) ProviderDistricts(depID, provID int) ([]database.EquipProviderLocation, error) {
	if depID <= 0 || provID <= 0 {
		return nil, invalid("departamento o provincia inválidos")
	}
	return s.locationsFromDB(database.ProviderLocDistrict, depID, provID)
}
