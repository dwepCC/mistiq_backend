package service

import (
	"errors"
	"regexp"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/secrets"

	"gorm.io/gorm"
)

// Accounts cuentas contables resueltas (configuradas o por defecto).
type Accounts struct {
	ReceivablePEN, ReceivableUSD string
	IgvSalesPEN, IgvSalesUSD     string
	TaxedPEN, TaxedUSD           string
	Exonerated, Unaffected       string
	Exportation, Icbper          string
	SubdiarioSales               string
	// ServiceCharge cuenta del recargo al consumo cobrado: no es ingreso del negocio sino un pasivo con los trabajadores (PCGE 41, remuneraciones
	// por pagar). Por ahora es la cuenta por defecto, sin edición por tenant: confirmar con el contador antes de usarla.
	ServiceCharge string
}

// DefaultAccounts valores PCGE usados cuando el tenant no configuró la cuenta (los mismos que traía el
// sistema anterior para ventas gravadas). Son editables en Ajustes → Contabilidad.
var DefaultAccounts = Accounts{
	ReceivablePEN: "12121", ReceivableUSD: "12122",
	IgvSalesPEN: "40111", IgvSalesUSD: "40111",
	TaxedPEN: "70111", TaxedUSD: "70111",
	Exonerated: "70111", Unaffected: "70111", Exportation: "70121", Icbper: "40199",
	SubdiarioSales: "05",
}

// ServiceChargeAccount cuenta por defecto del recargo al consumo (pasivo con los trabajadores). Fuera de DefaultAccounts a propósito: no se siembra
// en la base ni se edita por tenant todavía.
const ServiceChargeAccount = "41199"

var accountRe = regexp.MustCompile(`^[0-9A-Za-z.\-]{1,20}$`)

func pick(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func (s *Service) Settings() (*database.TenantAccountingSettings, error) {
	var st database.TenantAccountingSettings
	err := s.db.First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &database.TenantAccountingSettings{}, nil
	}
	return &st, err
}

// ResolvedAccounts cuentas efectivas (configuradas o por defecto).
func (s *Service) ResolvedAccounts() (Accounts, error) {
	st, err := s.Settings()
	if err != nil {
		return Accounts{}, err
	}
	d := DefaultAccounts
	return Accounts{
		ReceivablePEN: pick(st.ReceivablePEN, d.ReceivablePEN), ReceivableUSD: pick(st.ReceivableUSD, d.ReceivableUSD),
		IgvSalesPEN: pick(st.IgvSalesPEN, d.IgvSalesPEN), IgvSalesUSD: pick(st.IgvSalesUSD, d.IgvSalesUSD),
		TaxedPEN: pick(st.TaxedPEN, d.TaxedPEN), TaxedUSD: pick(st.TaxedUSD, d.TaxedUSD),
		Exonerated: pick(st.Exonerated, d.Exonerated), Unaffected: pick(st.Unaffected, d.Unaffected),
		Exportation: pick(st.Exportation, d.Exportation), Icbper: pick(st.Icbper, d.Icbper),
		SubdiarioSales: pick(st.SubdiarioSales, d.SubdiarioSales),
		ServiceCharge:  ServiceChargeAccount,
	}, nil
}

// FillDefaults completa con el valor por defecto (PCGE) cada cuenta de ventas que esté vacía: las cuentas
// son las mismas en todo el Perú, así que nunca se guardan vacías (el tenant las edita si su contador usa otras).
func FillDefaults(st *database.TenantAccountingSettings) {
	d := DefaultAccounts
	st.ReceivablePEN, st.ReceivableUSD = pick(st.ReceivablePEN, d.ReceivablePEN), pick(st.ReceivableUSD, d.ReceivableUSD)
	st.IgvSalesPEN, st.IgvSalesUSD = pick(st.IgvSalesPEN, d.IgvSalesPEN), pick(st.IgvSalesUSD, d.IgvSalesUSD)
	st.TaxedPEN, st.TaxedUSD = pick(st.TaxedPEN, d.TaxedPEN), pick(st.TaxedUSD, d.TaxedUSD)
	st.Exonerated, st.Unaffected = pick(st.Exonerated, d.Exonerated), pick(st.Unaffected, d.Unaffected)
	st.Exportation, st.Icbper = pick(st.Exportation, d.Exportation), pick(st.Icbper, d.Icbper)
	st.SubdiarioSales = pick(st.SubdiarioSales, d.SubdiarioSales)
}

// SaveSettings valida y guarda las cuentas (texto, sin perder ceros iniciales). Una cuenta de ventas
// borrada vuelve a su valor por defecto.
func (s *Service) SaveSettings(in database.TenantAccountingSettings) (*database.TenantAccountingSettings, error) {
	fields := []*string{
		&in.ReceivablePEN, &in.ReceivableUSD, &in.IgvSalesPEN, &in.IgvSalesUSD, &in.TaxedPEN, &in.TaxedUSD,
		&in.Exonerated, &in.Unaffected, &in.Exportation, &in.Icbper, &in.PayablePEN, &in.PayableUSD,
		&in.IgvPurchases, &in.PurchaseCost,
	}
	for _, f := range fields {
		*f = strings.TrimSpace(*f)
		if *f != "" && !accountRe.MatchString(*f) {
			return nil, errors.New("las cuentas contables solo admiten letras, números, punto y guion (máx. 20 caracteres)")
		}
	}
	in.SubdiarioSales = strings.TrimSpace(in.SubdiarioSales)
	if len(in.SubdiarioSales) > 5 {
		return nil, errors.New("el subdiario de ventas admite hasta 5 caracteres")
	}
	FillDefaults(&in)
	cur, err := s.Settings()
	if err != nil {
		return nil, err
	}
	in.ID = cur.ID
	if err := s.db.Save(&in).Error; err != nil {
		return nil, err
	}
	return &in, nil
}

// ── SIRE: credenciales ───────────────────────────────────────────────────────────────────────────

// SireSettingsView lo único que sale al navegador: nunca los secretos.
type SireSettingsView struct {
	ClientID        string `json:"client_id"`
	SolUsername     string `json:"sol_username"`
	HasClientSecret bool   `json:"has_client_secret"`
	HasSolPassword  bool   `json:"has_sol_password"`
	EncryptionOK    bool   `json:"encryption_ok"`
}

func (s *Service) SireSettings() (*database.TenantSireSettings, error) {
	var st database.TenantSireSettings
	err := s.db.First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &database.TenantSireSettings{}, nil
	}
	return &st, err
}

func (s *Service) SireSettingsView() (*SireSettingsView, error) {
	st, err := s.SireSettings()
	if err != nil {
		return nil, err
	}
	_, encErr := secrets.Encrypt("x")
	return &SireSettingsView{
		ClientID: st.ClientID, SolUsername: st.SolUsername,
		HasClientSecret: st.ClientSecretEnc != "", HasSolPassword: st.SolPasswordEnc != "",
		EncryptionOK: encErr == nil,
	}, nil
}

// SaveSireSettings guarda credenciales. Un secreto vacío CONSERVA el anterior (el formulario nunca los
// recibe de vuelta, así que no puede reenviarlos).
func (s *Service) SaveSireSettings(clientID, clientSecret, solUser, solPass string) error {
	st, err := s.SireSettings()
	if err != nil {
		return err
	}
	st.ClientID = strings.TrimSpace(clientID)
	st.SolUsername = strings.TrimSpace(solUser)
	if strings.TrimSpace(clientSecret) != "" {
		enc, err := secrets.Encrypt(strings.TrimSpace(clientSecret))
		if err != nil {
			return err
		}
		st.ClientSecretEnc = enc
	}
	if strings.TrimSpace(solPass) != "" {
		enc, err := secrets.Encrypt(strings.TrimSpace(solPass))
		if err != nil {
			return err
		}
		st.SolPasswordEnc = enc
	}
	return s.db.Save(st).Error
}
