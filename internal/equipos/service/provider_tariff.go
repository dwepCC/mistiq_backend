package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tukifac/internal/shalom"
	"tukifac/pkg/database"
	"tukifac/pkg/secrets"
)

// Cotización de fletes con Shalom Pro. Solo lectura: no crea guías ni cobra nada. Requiere la cuenta de
// Shalom Pro guardada (correo + clave cifrada) para abrir una sesión de 2 horas, que se reutiliza.

var (
	proMu      sync.Mutex
	proToken   string
	proExpires time.Time
	tariffMu   sync.Mutex
	tariffMemo = map[string]tariffMemoEntry{}
)

type tariffMemoEntry struct {
	res *shalom.TariffResult
	at  time.Time
}

// tariffCacheTTL las tarifas casi no cambian: una misma ruta no vuelve a la API por 30 minutos.
const tariffCacheTTL = 30 * time.Minute

func clearProSession() {
	proMu.Lock()
	proToken, proExpires = "", time.Time{}
	proMu.Unlock()
	tariffMu.Lock()
	tariffMemo = map[string]tariffMemoEntry{}
	tariffMu.Unlock()
}

// proSession devuelve un token vigente de Shalom Pro; inicia sesión solo si no hay uno (renueva 5 min antes de vencer).
func (s *Service) proSession(ctx context.Context, cl *shalom.Client, st *database.EquipProviderSettings, force bool) (string, error) {
	proMu.Lock()
	defer proMu.Unlock()
	if !force && proToken != "" && time.Until(proExpires) > 5*time.Minute {
		return proToken, nil
	}
	if st.ProEmail == "" || st.ProPasswordEnc == "" {
		return "", invalid("carga el correo y la clave de Shalom Pro en Integración para cotizar")
	}
	pass, err := secrets.Decrypt(st.ProPasswordEnc)
	if err != nil {
		return "", invalid("%v", err)
	}
	sess, err := cl.CreateSession(ctx, st.ProEmail, pass)
	if err != nil {
		if ae, ok := shalom.AsAPIError(err); ok && ae.Code == "shalom_auth_failed" {
			return "", invalid("Shalom Pro rechazó el correo o la clave: revísalos en Integración")
		}
		if ae, ok := shalom.AsAPIError(err); ok && ae.Code == "shalom_login_unavailable" {
			return "", invalid("el acceso a Shalom Pro está saturado; reintenta en %d s", int(ae.RetryAfter.Seconds())+1)
		}
		return "", providerError(err)
	}
	proToken, proExpires = sess.Token, sess.ExpiresAt
	if proExpires.IsZero() {
		proExpires = time.Now().Add(2 * time.Hour)
	}
	return proToken, nil
}

// TariffQuote resultado para la pantalla: precio por tipo de paquete.
type TariffQuote struct {
	Currency  string             `json:"currency"`
	Aereo     bool               `json:"aereo"`
	Breakdown map[string]float64 `json:"breakdown"`
	Origin    string             `json:"origin"`
	Dest      string             `json:"destination"`
	FromCache bool               `json:"from_cache"`
}

// QuoteShipmentTariff cotiza el envío vigente de un pedido entre su agencia de origen y destino.
func (s *Service) QuoteShipmentTariff(ctx context.Context, orderID uint, aereo bool) (*TariffQuote, error) {
	var sh database.EquipShipment
	if found, err := findOne(s.db, &sh, "order_id = ? AND is_current = ?", orderID, true); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("el pedido no tiene envío")
	}
	if sh.OriginAgencyID == nil || sh.DestinationAgencyID == nil {
		return nil, invalid("elige la agencia de origen y la de destino del envío para cotizar")
	}
	var origin, dest database.EquipCarrierAgency
	if found, _ := findOne(s.db, &origin, "id = ?", *sh.OriginAgencyID); !found {
		return nil, invalid("la agencia de origen ya no existe")
	}
	if found, _ := findOne(s.db, &dest, "id = ?", *sh.DestinationAgencyID); !found {
		return nil, invalid("la agencia de destino ya no existe")
	}
	if origin.ProviderRef == 0 || dest.ProviderRef == 0 {
		return nil, invalid("las agencias deben estar sincronizadas con Shalom (Integración → Sincronizar agencias)")
	}
	if aereo && ((dest.ProviderAereo != nil && !*dest.ProviderAereo) || (origin.ProviderAereo != nil && !*origin.ProviderAereo)) {
		return nil, invalid("alguna de las agencias no despacha por vía aérea")
	}
	cl, st, err := s.shalomClient()
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d>%d|%t", origin.ProviderRef, dest.ProviderRef, aereo)
	tariffMu.Lock()
	if e, ok := tariffMemo[key]; ok && time.Since(e.at) < tariffCacheTTL {
		tariffMu.Unlock()
		return quoteFrom(e.res, origin, dest, true), nil
	}
	tariffMu.Unlock()

	req := shalom.TariffRequest{OriginTerminalID: origin.ProviderRef, DestinyTerminalID: dest.ProviderRef, Aereo: aereo}
	res, err := s.tariffWithSession(ctx, cl, st, req)
	if err != nil {
		return nil, err
	}
	tariffMu.Lock()
	tariffMemo[key] = tariffMemoEntry{res: res, at: time.Now()}
	tariffMu.Unlock()
	return quoteFrom(res, origin, dest, false), nil
}

// tariffWithSession pide la tarifa; si Shalom dice que la sesión no sirve (vencida o revocada), inicia otra y reintenta una vez.
func (s *Service) tariffWithSession(ctx context.Context, cl *shalom.Client, st *database.EquipProviderSettings, req shalom.TariffRequest) (*shalom.TariffResult, error) {
	token, err := s.proSession(ctx, cl, st, false)
	if err != nil {
		return nil, err
	}
	res, err := cl.Tariff(ctx, token, req)
	if ae, ok := shalom.AsAPIError(err); ok && ae.Code == "shalom_auth_failed" {
		if token, err = s.proSession(ctx, cl, st, true); err != nil {
			return nil, err
		}
		res, err = cl.Tariff(ctx, token, req)
	}
	if err != nil {
		if ae, ok := shalom.AsAPIError(err); ok && ae.Status == 422 {
			return nil, invalid("Shalom no cotiza esta ruta: %s", strings.TrimPrefix(ae.Message, "Shalom Pro: "))
		}
		return nil, providerError(err)
	}
	return res, nil
}

func quoteFrom(r *shalom.TariffResult, origin, dest database.EquipCarrierAgency, cached bool) *TariffQuote {
	cur := r.Currency
	if cur == "" {
		cur = "PEN"
	}
	return &TariffQuote{Currency: cur, Aereo: r.Aereo, Breakdown: r.Breakdown, Origin: origin.Name, Dest: dest.Name, FromCache: cached}
}
