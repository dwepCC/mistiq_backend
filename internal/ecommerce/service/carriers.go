package service

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"tukifac/pkg/database"
)

// Tipos de transportista.
const (
	CarrierKindAgency   = "AGENCIA"   // el cliente recoge en la agencia de destino (Shalom, Olva…)
	CarrierKindDelivery = "DOMICILIO" // entrega en la dirección del cliente
	CarrierKindOwn      = "PROPIO"    // reparto propio del negocio
)

var validCarrierKinds = map[string]bool{CarrierKindAgency: true, CarrierKindDelivery: true, CarrierKindOwn: true}

// CarrierInput campos editables de un transportista. Los punteros distinguen "no tocar" de "poner en cero".
type CarrierInput struct {
	Name               *string
	Kind               *string
	Active             *bool
	DispatchDays       *string
	PickupDeadlineDays *int
	WarnDaysBefore     *int
	TrackingURL        *string
	ShowInStore        *bool
	SortOrder          *int
}

// normalizeDispatchDays valida y normaliza "1,3,5" (ISO: 1 lunes … 7 domingo). Vacío = todos los días.
func normalizeDispatchDays(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 7 {
			return "", errors.New("los días de despacho deben ser números del 1 (lunes) al 7 (domingo)")
		}
		seen[n] = true
	}
	days := make([]int, 0, len(seen))
	for n := range seen {
		days = append(days, n)
	}
	sort.Ints(days)
	out := make([]string, len(days))
	for i, n := range days {
		out[i] = strconv.Itoa(n)
	}
	return strings.Join(out, ","), nil
}

func (s *EcommerceService) ListCarriers(activeOnly, storeOnly bool) ([]database.TenantCarrier, error) {
	q := s.db.Model(&database.TenantCarrier{})
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	if storeOnly {
		q = q.Where("show_in_store = ?", true)
	}
	var rows []database.TenantCarrier
	err := q.Order("sort_order ASC, name ASC").Find(&rows).Error
	return rows, err
}

func (s *EcommerceService) GetCarrier(id uint) (*database.TenantCarrier, error) {
	var c database.TenantCarrier
	if err := s.db.First(&c, id).Error; err != nil {
		return nil, errors.New("transportista no encontrado")
	}
	return &c, nil
}

func applyCarrierInput(c *database.TenantCarrier, in CarrierInput) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return errors.New("el nombre del transportista es obligatorio")
		}
		c.Name = name
	}
	if in.Kind != nil {
		kind := strings.ToUpper(strings.TrimSpace(*in.Kind))
		if !validCarrierKinds[kind] {
			return errors.New("tipo de transportista inválido")
		}
		c.Kind = kind
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
	if in.DispatchDays != nil {
		days, err := normalizeDispatchDays(*in.DispatchDays)
		if err != nil {
			return err
		}
		c.DispatchDays = days
	}
	if in.PickupDeadlineDays != nil {
		if *in.PickupDeadlineDays < 1 || *in.PickupDeadlineDays > 180 {
			return errors.New("el plazo de recojo debe estar entre 1 y 180 días")
		}
		c.PickupDeadlineDays = *in.PickupDeadlineDays
	}
	if in.WarnDaysBefore != nil {
		if *in.WarnDaysBefore < 0 || *in.WarnDaysBefore > 180 {
			return errors.New("el aviso previo debe estar entre 0 y 180 días")
		}
		c.WarnDaysBefore = *in.WarnDaysBefore
	}
	if in.TrackingURL != nil {
		u := strings.TrimSpace(*in.TrackingURL)
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return errors.New("el enlace de rastreo debe empezar con http:// o https://")
		}
		c.TrackingURL = u
	}
	if in.ShowInStore != nil {
		c.ShowInStore = *in.ShowInStore
	}
	if in.SortOrder != nil {
		c.SortOrder = *in.SortOrder
	}
	if c.WarnDaysBefore > c.PickupDeadlineDays {
		return fmt.Errorf("el aviso previo (%d) no puede ser mayor que el plazo de recojo (%d)", c.WarnDaysBefore, c.PickupDeadlineDays)
	}
	return nil
}

func (s *EcommerceService) CreateCarrier(in CarrierInput) (*database.TenantCarrier, error) {
	c := database.TenantCarrier{
		Kind: CarrierKindAgency, Active: true, PickupDeadlineDays: 15, WarnDaysBefore: 5, ShowInStore: true,
	}
	if err := applyCarrierInput(&c, in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Name) == "" {
		return nil, errors.New("el nombre del transportista es obligatorio")
	}
	if err := s.db.Create(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *EcommerceService) UpdateCarrier(id uint, in CarrierInput) (*database.TenantCarrier, error) {
	c, err := s.GetCarrier(id)
	if err != nil {
		return nil, err
	}
	if err := applyCarrierInput(c, in); err != nil {
		return nil, err
	}
	if err := s.db.Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteCarrier borra (soft delete) un transportista. Los pedidos y despachos que ya lo usaban
// conservan el nombre en sus propios campos, así que no se rompe ningún historial.
func (s *EcommerceService) DeleteCarrier(id uint) error {
	if _, err := s.GetCarrier(id); err != nil {
		return err
	}
	return s.db.Delete(&database.TenantCarrier{}, id).Error
}
