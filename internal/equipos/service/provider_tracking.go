package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"tukifac/internal/shalom"
	"tukifac/pkg/database"
	"tukifac/pkg/secrets"
)

// ── Webhook ────────────────────────────────────────────────────────────────

// SetupWebhook registra en Shalom la URL configurada y guarda el secreto de firma (cifrado). Shalom envía
// enseguida un ping al endpoint público; hasta que responda no se entregan eventos.
func (s *Service) SetupWebhook(ctx context.Context) (*ProviderView, error) {
	cl, st, err := s.shalomClient()
	if err != nil {
		return nil, err
	}
	if st.WebhookURL == "" {
		return nil, invalid("indica la URL pública del webhook (https)")
	}
	resp, err := cl.SetWebhook(ctx, st.WebhookURL)
	if err != nil {
		return nil, providerError(err)
	}
	upd := map[string]any{"webhook_verified": resp.Verified}
	if resp.SigningSecret != "" {
		enc, err := secrets.Encrypt(resp.SigningSecret)
		if err != nil {
			return nil, invalid("no se puede guardar el secreto del webhook: %v", err)
		}
		upd["webhook_secret_enc"] = enc
	}
	if err := s.db.Model(&database.EquipProviderSettings{}).Where("id = ?", st.ID).Updates(upd).Error; err != nil {
		return nil, err
	}
	return s.ProviderSettingsView()
}

// RotateWebhookSecret genera un secreto nuevo (el anterior deja de valer de inmediato).
func (s *Service) RotateWebhookSecret(ctx context.Context) (*ProviderView, error) {
	cl, st, err := s.shalomClient()
	if err != nil {
		return nil, err
	}
	resp, err := cl.RotateWebhookSecret(ctx)
	if err != nil {
		return nil, providerError(err)
	}
	if resp.SigningSecret == "" {
		return nil, invalid("Shalom no devolvió el nuevo secreto: vuelve a registrar el webhook")
	}
	enc, err := secrets.Encrypt(resp.SigningSecret)
	if err != nil {
		return nil, invalid("no se puede guardar el secreto del webhook: %v", err)
	}
	if err := s.db.Model(&database.EquipProviderSettings{}).Where("id = ?", st.ID).Update("webhook_secret_enc", enc).Error; err != nil {
		return nil, err
	}
	return s.ProviderSettingsView()
}

// WebhookResult qué debe responder el endpoint público.
type WebhookResult struct {
	Status int    // código HTTP
	Body   string // cuerpo de la respuesta (el challenge del ping)
	Note   string // para el log
}

// HandleWebhook procesa una entrega de Shalom. El ping se contesta SIN exigir firma (llega antes de tener el
// secreto); el resto exige firma válida sobre el cuerpo crudo, se deduplica por id de evento y se aplica al envío.
func (s *Service) HandleWebhook(body []byte, signature string, now time.Time) WebhookResult {
	var ev shalom.Event
	if err := json.Unmarshal(body, &ev); err != nil || ev.Event == "" {
		return WebhookResult{Status: 400, Note: "cuerpo inválido"}
	}
	if ev.Event == shalom.EventPing {
		var d struct {
			Challenge string `json:"challenge"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		_ = s.db.Model(&database.EquipProviderSettings{}).Where("provider = ?", database.ProviderShalom).Update("webhook_verified", true).Error
		return WebhookResult{Status: 200, Body: d.Challenge, Note: "ping"}
	}
	st, err := s.providerSettings()
	if err != nil || st.WebhookSecretEnc == "" {
		return WebhookResult{Status: 401, Note: "webhook sin secreto configurado"}
	}
	secret, err := secrets.Decrypt(st.WebhookSecretEnc)
	if err != nil {
		return WebhookResult{Status: 500, Note: "no se pudo descifrar el secreto del webhook"}
	}
	if err := shalom.VerifySignature(secret, signature, body, now); err != nil {
		return WebhookResult{Status: 401, Note: err.Error()}
	}
	if ev.ID == "" {
		return WebhookResult{Status: 400, Note: "evento sin id"}
	}
	var data shalom.TrackingData
	_ = json.Unmarshal(ev.Data, &data)

	var seen int64
	s.db.Model(&database.EquipProviderEvent{}).Where("provider = ? AND event_id = ?", database.ProviderShalom, ev.ID).Count(&seen)
	if seen > 0 {
		return WebhookResult{Status: 200, Note: "evento duplicado"} // entrega «al menos una vez»: ya procesado
	}
	row := database.EquipProviderEvent{Provider: database.ProviderShalom, EventID: ev.ID, EventType: ev.Event, Reference: data.Numero, Payload: string(body)}
	if err := s.db.Create(&row).Error; err != nil {
		// Carrera con otra entrega del mismo evento: el índice único la frena.
		s.db.Model(&database.EquipProviderEvent{}).Where("provider = ? AND event_id = ?", database.ProviderShalom, ev.ID).Count(&seen)
		if seen > 0 {
			return WebhookResult{Status: 200, Note: "evento duplicado"}
		}
		return WebhookResult{Status: 500, Note: "no se pudo registrar el evento"}
	}
	note := s.applyTrackingEvent(&data, ev.Event)
	nowT := time.Now()
	s.db.Model(&row).Updates(map[string]any{"processed_at": nowT, "note": note})
	return WebhookResult{Status: 200, Note: note}
}

func (s *Service) applyTrackingEvent(d *shalom.TrackingData, eventType string) string {
	if d.Numero == "" {
		return "evento sin N° de guía"
	}
	if eventType == shalom.EventExpired {
		return "suscripción vencida en Shalom (sin cambios)"
	}
	var cands []database.EquipShipment
	if err := s.db.Where("is_current = ? AND guide_number LIKE ?", true, d.Numero+"%").Order("id DESC").Find(&cands).Error; err != nil {
		return "error al buscar el envío: " + err.Error()
	}
	var sh *database.EquipShipment
	for i := range cands {
		if n, _ := shalomRef(cands[i].GuideNumber); n == d.Numero {
			sh = &cands[i]
			break
		}
	}
	if sh == nil {
		// No es la guía de un envío: puede ser la de un retorno.
		return s.applyReturnEvent(d)
	}
	ps := providerState{
		Status: d.Status, OseID: d.OseID,
		Transit: firstTime(d.MilestoneDate("origen"), d.MilestoneDate("transito")),
		Arrived: d.MilestoneDate("destino"), Delivered: d.MilestoneDate("entregado"),
	}
	if eventType == shalom.EventDelivered && ps.Delivered == nil {
		now := time.Now()
		ps.Delivered = &now
	}
	note, event := s.applyProviderStateEv(sh.ID, ps)
	if event != "" {
		s.notifyShipmentEvent(sh.ID, event)
	}
	return note
}

// ── Aplicar el estado del proveedor a un envío ─────────────────────────────

// providerState lo que informa Shalom, en fechas de calendario (Lima): salida, llegada a agencia y entrega.
type providerState struct {
	Status    string
	OseID     int
	Transit   *time.Time
	Arrived   *time.Time
	Delivered *time.Time
}

func firstTime(ts ...*time.Time) *time.Time {
	for _, t := range ts {
		if t != nil {
			return t
		}
	}
	return nil
}

func dayNoon(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	n := noonLima(t.In(limaLoc()))
	return &n
}

// applyProviderState avanza el envío según lo informado por Shalom. Reglas: nunca retrocede un estado,
// nunca pisa una fecha que ya existe (la del usuario manda), nunca bloquea por orden de fechas (las fechas
// previas se ajustan, igual que en las acciones manuales) y no toca envíos entregados ni en retorno.
func (s *Service) applyProviderState(shipmentID uint, ps providerState) string {
	note, _ := s.applyProviderStateEv(shipmentID, ps)
	return note
}

// applyProviderStateEv igual que applyProviderState; además devuelve el hito relevante que cambió (llegada,
// entregado, demora) para avisar en el panel. Vacío si no hubo nada que avisar.
func (s *Service) applyProviderStateEv(shipmentID uint, ps providerState) (string, string) {
	var note, event string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var sh database.EquipShipment
		if found, err := findOne(tx, &sh, "id = ?", shipmentID); err != nil || !found {
			return err
		}
		now := time.Now()
		prevStatus := sh.ProviderStatus
		sh.ProviderStatus, sh.ProviderSyncedAt = ps.Status, &now
		if ps.OseID > 0 {
			sh.ProviderOseID = fmt.Sprint(ps.OseID)
		}
		transit, arrived, delivered := dayNoon(ps.Transit), dayNoon(ps.Arrived), dayNoon(ps.Delivered)
		if sh.Status == "entregado" || sh.Status == "retorno" {
			note = "el envío ya estaba " + shipmentStatusLabel(sh.Status)
			return tx.Save(&sh).Error
		}
		var carrier database.EquipCarrier
		pickupDays := 15
		if sh.CarrierID != nil {
			if found, _ := findOne(tx, &carrier, "id = ?", *sh.CarrierID); found && carrier.PickupDays > 0 {
				pickupDays = carrier.PickupDays
			}
		}
		setDispatched := func(t *time.Time) {
			if t == nil {
				return
			}
			if sh.DispatchedAt == nil || t.Before(noonLima(*sh.DispatchedAt)) {
				sh.DispatchedAt = t
			}
		}
		switch {
		case delivered != nil:
			setDispatched(firstTime(transit, arrived, delivered))
			if sh.ArrivedAt == nil && sh.DeliveryMode == "agencia" {
				sh.ArrivedAt = firstTime(arrived, delivered)
			}
			if sh.ArrivedAt != nil && delivered.Before(noonLima(*sh.ArrivedAt)) {
				sh.ArrivedAt = delivered
			}
			sh.PickedUpAt, sh.Status = delivered, "entregado"
			note, event = "entregado según Shalom", NotifDelivered
		case arrived != nil:
			setDispatched(firstTime(transit, arrived))
			if sh.Status == "pendiente_envio" || sh.Status == "en_transito" {
				sh.Status = "en_agencia"
				if sh.ArrivedAt == nil {
					sh.ArrivedAt = arrived
					dl := arrived.AddDate(0, 0, pickupDays)
					sh.PickupDeadline = &dl
				}
				note, event = "llegó a agencia según Shalom", NotifArrival
			} else if sh.ArrivedAt == nil { // ya en agencia pero sin fecha: se completa
				sh.ArrivedAt = arrived
				dl := arrived.AddDate(0, 0, pickupDays)
				sh.PickupDeadline = &dl
				note = "se completó la fecha de llegada"
			}
		case transit != nil || ps.Status == "transito" || ps.Status == "origen":
			if sh.Status == "pendiente_envio" {
				sh.Status = "en_transito"
				setDispatched(firstTime(transit, &now))
				note = "en tránsito según Shalom"
			}
		}
		if ps.Status == "demora" && prevStatus != "demora" && event == "" {
			event = NotifDelay
		}
		return tx.Save(&sh).Error
	})
	if err != nil {
		return "error al aplicar el estado: " + err.Error(), ""
	}
	if note == "" {
		note = "sin cambios"
	}
	return note, event
}

// ── Acciones sobre un envío ────────────────────────────────────────────────

var providerCodeRe = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)

// shalomGuideRe reconoce «NÚMERO - CÓDIGO» tal como se registra la guía de Shalom (ej. «100439617 - M7JK»,
// «99526318-kHHT», «101732548 -3WJC»): 8 a 10 dígitos, separador opcional y el código de 4 caracteres.
var shalomGuideRe = regexp.MustCompile(`^\s*(\d{8,10})\s*[-–—]?\s*([A-Za-z0-9]{4})\s*$`)
var shalomNumberRe = regexp.MustCompile(`^\s*(\d{8,10})\s*$`)

// shalomRef extrae del N° de guía el número (obligatorio) y el código (si viene en el mismo texto).
func shalomRef(guide string) (numero, code string) {
	if m := shalomGuideRe.FindStringSubmatch(guide); m != nil {
		return m[1], strings.ToUpper(m[2])
	}
	if m := shalomNumberRe.FindStringSubmatch(guide); m != nil {
		return m[1], ""
	}
	return "", ""
}

// shipmentRef número y código a consultar: el código guardado manda; si no, el que trae la propia guía.
func shipmentRef(sh *database.EquipShipment) (numero, code string) {
	numero, code = shalomRef(sh.GuideNumber)
	if c := strings.ToUpper(strings.TrimSpace(sh.ProviderCode)); c != "" {
		code = c
	}
	return numero, code
}

func (s *Service) currentShalomShipment(tx *gorm.DB, orderID uint) (*database.EquipShipment, error) {
	var sh database.EquipShipment
	if found, err := findOne(tx, &sh, "order_id = ? AND is_current = ?", orderID, true); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("el pedido no tiene envío")
	}
	c, err := s.shalomCarrier()
	if err != nil {
		return nil, err
	}
	if sh.CarrierID == nil || *sh.CarrierID != c.ID {
		return nil, invalid("el envío no es de Shalom")
	}
	return &sh, nil
}

// LinkShipmentTracking guarda el código de 4 caracteres de la guía, trae el estado actual y deja el envío
// suscrito al webhook (si está verificado). Es lo único que el operador debe hacer para activar el seguimiento.
func (s *Service) LinkShipmentTracking(ctx context.Context, orderID uint, code string) (*OrderResult, error) {
	sh, err := s.currentShalomShipment(s.db, orderID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sh.GuideNumber) == "" {
		return nil, invalid("registra primero el N° de guía del envío")
	}
	numero, fromGuide := shalomRef(sh.GuideNumber)
	if numero == "" {
		return nil, invalid("el N° de guía no tiene el formato de Shalom (8 a 10 dígitos): corrígelo antes de vincular")
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		code = fromGuide // «100439617 - M7JK»: el código ya viene en la guía
	}
	if !providerCodeRe.MatchString(code) {
		return nil, invalid("el código de Shalom son 4 letras o números")
	}
	if err := s.db.Model(&database.EquipShipment{}).Where("id = ?", sh.ID).Update("provider_code", code).Error; err != nil {
		return nil, err
	}
	var warnings []string
	if w, err := s.subscribeShipment(ctx, sh.ID); err != nil {
		warnings = append(warnings, err.Error())
	} else if w != "" {
		warnings = append(warnings, w)
	}
	if err := s.refreshShipment(ctx, sh.ID); err != nil {
		warnings = append(warnings, err.Error())
	}
	v, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v, Warnings: warnings}, nil
}

// RefreshShipmentFromProvider consulta a Shalom el estado de UN envío y lo aplica ("Actualizar desde Shalom").
func (s *Service) RefreshShipmentFromProvider(ctx context.Context, orderID uint) (*OrderResult, error) {
	sh, err := s.currentShalomShipment(s.db, orderID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshShipment(ctx, sh.ID); err != nil {
		return nil, err
	}
	v, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v}, nil
}

func (s *Service) refreshShipment(ctx context.Context, shipmentID uint) error {
	var sh database.EquipShipment
	if found, err := findOne(s.db, &sh, "id = ?", shipmentID); err != nil || !found {
		return err
	}
	numero, code := shipmentRef(&sh)
	if numero == "" || code == "" {
		return invalid("falta el N° de guía de Shalom o su código de 4 caracteres")
	}
	cl, _, err := s.shalomClient()
	if err != nil {
		return err
	}
	tr, err := cl.Track(ctx, numero, code)
	if err != nil {
		return providerError(err)
	}
	s.recordPollSnapshot(shipmentID, numero, tr)
	// Al vincular, la consulta corre antes de que llegue el primer evento del webhook y ya mueve el envío: el aviso
	// debe nacer aquí (es idempotente por envío y tipo, así que el webhook posterior no lo duplica).
	if _, event := s.applyProviderStateEv(shipmentID, stateFromTracking(tr)); event != "" {
		s.notifyShipmentEvent(shipmentID, event)
	}
	return nil
}

func stateFromTracking(tr *shalom.Tracking) providerState {
	st := tr.Status
	ps := providerState{OseID: tr.Order.OseID,
		Transit: firstTime(st.Origen.Date(), st.Transito.Date()), // «registrado» = guía creada en Shalom, el paquete aún no salió
		Arrived: st.Destino.Date(), Delivered: st.Entregado.Date()}
	switch {
	case st.Entregado != nil:
		ps.Status = "entregado"
	case st.Reparto != nil:
		ps.Status = "reparto"
	case st.Destino != nil:
		ps.Status = "destino"
	case st.Demora != nil:
		ps.Status = "demora"
	case st.Transito != nil:
		ps.Status = "transito"
	case st.Origen != nil:
		ps.Status = "origen"
	case st.Registrado != nil:
		ps.Status = "registrado"
	}
	return ps
}

// subscribeShipment suscribe la guía al webhook si la integración está lista. Devuelve un aviso (no un error)
// cuando simplemente aún no se puede suscribir: el seguimiento manual sigue funcionando.
func (s *Service) subscribeShipment(ctx context.Context, shipmentID uint) (string, error) {
	var sh database.EquipShipment
	if found, err := findOne(s.db, &sh, "id = ?", shipmentID); err != nil || !found {
		return "", err
	}
	numero, code := shipmentRef(&sh)
	if sh.ProviderSubID > 0 || numero == "" || code == "" {
		return "", nil
	}
	cl, st, err := s.shalomClient()
	if err != nil {
		return "", nil // integración inactiva: nada que hacer
	}
	if !st.WebhookVerified {
		return "Seguimiento vinculado, pero el webhook de Shalom aún no está verificado: usa «Actualizar desde Shalom» mientras tanto.", nil
	}
	sub, err := cl.Subscribe(ctx, numero, code)
	if err != nil {
		return "", providerError(err)
	}
	return "", s.db.Model(&database.EquipShipment{}).Where("id = ?", sh.ID).Update("provider_sub_id", uint(sub.ID)).Error
}

// SubscribeOrderShipment versión «best effort» para llamar tras despachar: nunca falla el despacho.
func (s *Service) SubscribeOrderShipment(ctx context.Context, orderID uint) {
	var sh database.EquipShipment
	if found, _ := findOne(s.db, &sh, "order_id = ? AND is_current = ?", orderID, true); found {
		_, _ = s.subscribeShipment(ctx, sh.ID)
	}
}

// SyncShipmentsResult resumen de la alineación en lote.
type SyncShipmentsResult struct {
	Checked int `json:"checked"`
	Changed int `json:"changed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"` // sin N° de guía con formato Shalom
}

// SyncShalomShipments alinea con Shalom los envíos en tránsito o en agencia que ya tienen guía y código, de a
// 50 por llamada. Sirve para corregir de una vez los envíos que quedaron sin marcar.
func (s *Service) SyncShalomShipments(ctx context.Context) (*SyncShipmentsResult, error) {
	cl, _, err := s.shalomClient()
	if err != nil {
		return nil, err
	}
	c, err := s.shalomCarrier()
	if err != nil {
		return nil, err
	}
	var rows []database.EquipShipment
	if err := s.db.Where("is_current = ? AND carrier_id = ? AND status IN ? AND guide_number <> ''",
		true, c.ID, []string{"pendiente_envio", "en_transito", "en_agencia"}).Find(&rows).Error; err != nil {
		return nil, err
	}
	res := &SyncShipmentsResult{}
	for i := 0; i < len(rows); i += 50 {
		end := i + 50
		if end > len(rows) {
			end = len(rows)
		}
		items := make([]shalom.BatchItem, 0, end-i)
		for _, sh := range rows[i:end] {
			numero, code := shipmentRef(&sh)
			if numero == "" || code == "" {
				res.Skipped++
				continue
			}
			if sh.ProviderCode == "" { // queda guardado para las próximas consultas
				s.db.Model(&database.EquipShipment{}).Where("id = ?", sh.ID).Update("provider_code", code)
			}
			items = append(items, shalom.BatchItem{CustomID: fmt.Sprint(sh.ID), Numero: numero, Codigo: code})
		}
		if len(items) == 0 {
			continue
		}
		results, err := cl.TrackBatch(ctx, items)
		if err != nil {
			return res, providerError(err)
		}
		for _, r := range results {
			res.Checked++
			if !r.OK || r.Tracking == nil {
				res.Failed++
				continue
			}
			var id uint
			fmt.Sscan(r.CustomID, &id)
			s.recordPollSnapshot(id, r.Numero, r.Tracking)
			n, event := s.applyProviderStateEv(id, stateFromTracking(r.Tracking))
			if n != "sin cambios" {
				res.Changed++
			}
			// En lote solo se avisa lo accionable (llegó a agencia, demora); entregas antiguas serían ruido.
			if event == NotifArrival || event == NotifDelay {
				s.notifyShipmentEvent(id, event)
			}
		}
	}
	// Los retornos abiertos con guía también se alinean.
	if err := s.syncOpenReturns(ctx, cl, res); err != nil {
		return res, err
	}
	return res, nil
}

// maybeSubscribeAsync tras despachar: si la integración está activa y el webhook verificado, suscribe la guía
// en segundo plano (la llamada puede tardar) sin demorar ni hacer fallar el despacho.
func (s *Service) maybeSubscribeAsync(orderID uint) {
	st, err := s.providerSettings()
	if err != nil || !st.Enabled || !st.WebhookVerified {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		s.SubscribeOrderShipment(ctx, orderID)
	}()
}
