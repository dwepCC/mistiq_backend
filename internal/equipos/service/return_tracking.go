package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tukifac/internal/shalom"
	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Seguimiento de retornos con Shalom: el N° de guía del retorno se vincula igual que el de un pedido y la pantalla muestra
// su etapa (En origen / En tránsito / En destino / Entregado). Un aviso del panel indica cuándo llegó a destino para ir a
// recogerlo. El stock NO se mueve solo: alguien debe revisar el equipo y marcarlo como recibido (ver ReceiveReturn).

const (
	NotifReturnArrival   = "retorno_destino"
	NotifReturnDelivered = "retorno_entregado"
)

// returnStage etapa del retorno según el último estado de Shalom ("" si aún no está vinculado).
func returnStage(providerStatus string) (stage, label string) {
	switch providerStatus {
	case "registrado", "origen":
		return "origen", "En origen"
	case "transito", "demora":
		return "transito", "En tránsito"
	case "destino", "reparto":
		return "destino", "En destino"
	case "entregado":
		return "entregado", "Entregado"
	}
	return "", ""
}

var openReturnStatuses = []string{"solicitado", "en_camino"}

// returnRef número y código a consultar: el código guardado manda; si no, el que trae la propia guía.
func returnRef(r *database.EquipReturn) (numero, code string) {
	numero, code = shalomRef(r.GuideNumber)
	if c := strings.ToUpper(strings.TrimSpace(r.ProviderCode)); c != "" {
		code = c
	}
	return numero, code
}

// SetReturnGuide registra (o cambia) el N° de guía del retorno. Si trae formato de Shalom y la integración está activa,
// lo vincula, suscribe el webhook y trae el estado actual. Todo lo del proveedor es «lo mejor posible»: la guía se guarda
// siempre y los avisos explican qué faltó.
func (s *Service) SetReturnGuide(ctx context.Context, id uint, guide, code string) (*ReturnView, []string, error) {
	guide = strings.TrimSpace(guide)
	if len(guide) > 40 {
		return nil, nil, invalid("el N° de guía es demasiado largo")
	}
	var r database.EquipReturn
	if found, err := findOne(s.db, &r, "id = ?", id); err != nil {
		return nil, nil, err
	} else if !found {
		return nil, nil, invalid("retorno no encontrado")
	}
	if r.Status == "anulado" {
		return nil, nil, invalid("el retorno está anulado")
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code != "" && !providerCodeRe.MatchString(code) {
		return nil, nil, invalid("el código de Shalom son 4 letras o números")
	}
	changed := guide != r.GuideNumber
	upd := map[string]any{"guide_number": guide}
	if changed { // otra guía = otro paquete: se descarta el seguimiento de la anterior
		upd["provider_code"], upd["provider_status"], upd["provider_synced_at"], upd["provider_sub_id"], upd["provider_ose_id"] = "", "", nil, 0, ""
	}
	if err := s.db.Model(&database.EquipReturn{}).Where("id = ?", id).Updates(upd).Error; err != nil {
		return nil, nil, err
	}
	var warnings []string
	if guide != "" {
		numero, fromGuide := shalomRef(guide)
		if code == "" {
			code = fromGuide
		}
		switch {
		case numero == "":
			warnings = append(warnings, "La guía se guardó, pero no tiene el formato de Shalom (8 a 10 dígitos): no se puede seguir su estado automáticamente.")
		case code == "":
			warnings = append(warnings, "La guía se guardó. Para seguirla en Shalom indica además su código de 4 caracteres.")
		default:
			if err := s.db.Model(&database.EquipReturn{}).Where("id = ?", id).Update("provider_code", code).Error; err != nil {
				return nil, nil, err
			}
			if w, err := s.subscribeReturn(ctx, id); err != nil {
				warnings = append(warnings, err.Error())
			} else if w != "" {
				warnings = append(warnings, w)
			}
			if err := s.refreshReturn(ctx, id); err != nil {
				warnings = append(warnings, err.Error())
			}
		}
	}
	v, err := s.getReturnView(id)
	return v, warnings, err
}

// RefreshReturnFromProvider «Actualizar desde Shalom» para un retorno.
func (s *Service) RefreshReturnFromProvider(ctx context.Context, id uint) (*ReturnView, error) {
	if err := s.refreshReturn(ctx, id); err != nil {
		return nil, err
	}
	return s.getReturnView(id)
}

func (s *Service) getReturnView(id uint) (*ReturnView, error) {
	var r database.EquipReturn
	if found, err := findOne(s.db, &r, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("retorno no encontrado")
	}
	v := s.returnView(s.db, r)
	return &v, nil
}

func (s *Service) refreshReturn(ctx context.Context, id uint) error {
	var r database.EquipReturn
	if found, err := findOne(s.db, &r, "id = ?", id); err != nil {
		return err
	} else if !found {
		return invalid("retorno no encontrado")
	}
	numero, code := returnRef(&r)
	if numero == "" || code == "" {
		return invalid("falta el N° de guía de Shalom del retorno o su código de 4 caracteres")
	}
	cl, _, err := s.shalomClient()
	if err != nil {
		return err
	}
	tr, err := cl.Track(ctx, numero, code)
	if err != nil {
		return providerError(err)
	}
	if event := s.applyReturnProviderState(id, stateFromTracking(tr)); event != "" {
		s.notifyReturnEvent(id, event)
	}
	return nil
}

func (s *Service) subscribeReturn(ctx context.Context, id uint) (string, error) {
	var r database.EquipReturn
	if found, err := findOne(s.db, &r, "id = ?", id); err != nil || !found {
		return "", err
	}
	numero, code := returnRef(&r)
	if r.ProviderSubID > 0 || numero == "" || code == "" {
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
	return "", s.db.Model(&database.EquipReturn{}).Where("id = ?", id).Update("provider_sub_id", uint(sub.ID)).Error
}

// applyReturnProviderState guarda el estado de Shalom en el retorno. Un retorno abierto pasa de «solicitado» a «en camino»
// cuando Shalom ya lo tiene en movimiento. Devuelve el aviso a emitir (llegó a destino / entregado) o "".
func (s *Service) applyReturnProviderState(id uint, ps providerState) string {
	var event string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var r database.EquipReturn
		if found, err := findOne(tx, &r, "id = ?", id); err != nil || !found {
			return err
		}
		prevStage, _ := returnStage(r.ProviderStatus)
		now := time.Now()
		r.ProviderStatus, r.ProviderSyncedAt = ps.Status, &now
		if ps.OseID > 0 {
			r.ProviderOseID = fmt.Sprint(ps.OseID)
		}
		stage, _ := returnStage(ps.Status)
		open := r.Status == "solicitado" || r.Status == "en_camino"
		if open {
			if r.Status == "solicitado" && stage != "" {
				r.Status = "en_camino"
			}
			switch {
			case stage == "destino" && prevStage != "destino":
				event = NotifReturnArrival
			case stage == "entregado" && prevStage != "entregado":
				event = NotifReturnDelivered
			}
		}
		return tx.Save(&r).Error
	})
	if err != nil {
		return ""
	}
	return event
}

// notifyReturnEvent aviso del panel. Es idempotente por retorno y tipo.
func (s *Service) notifyReturnEvent(id uint, kind string) {
	var r database.EquipReturn
	if found, _ := findOne(s.db, &r, "id = ?", id); !found {
		return
	}
	who := strings.TrimSpace(r.CustomerName)
	if who == "" {
		who = "sin nombre"
	}
	n := database.EquipNotification{OrderNumber: 0, DedupKey: fmt.Sprintf("ret:%d:%s", id, kind)}
	if r.OrderID != nil {
		n.OrderID = *r.OrderID
		var num int
		_ = s.db.Model(&database.EquipOrder{}).Where("id = ?", *r.OrderID).Select("order_number").Scan(&num).Error
		n.OrderNumber = num
	}
	switch kind {
	case NotifReturnArrival:
		n.Kind, n.Severity, n.Title = kind, "success", fmt.Sprintf("Retorno N° %d llegó a destino", r.ReturnNumber)
		n.Message = who + " · ya puede recogerse en la agencia"
	case NotifReturnDelivered:
		n.Kind, n.Severity, n.Title = kind, "info", fmt.Sprintf("Retorno N° %d fue entregado", r.ReturnNumber)
		n.Message = who + " · revisa el equipo y márcalo como recibido"
	default:
		return
	}
	s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&n)
}

// applyReturnEvent aplica un aviso del webhook de Shalom a un retorno abierto con esa guía.
func (s *Service) applyReturnEvent(d *shalom.TrackingData) string {
	var cands []database.EquipReturn
	if err := s.db.Where("status IN ? AND guide_number LIKE ?", openReturnStatuses, "%"+d.Numero+"%").Order("id DESC").Find(&cands).Error; err != nil {
		return "error al buscar el retorno: " + err.Error()
	}
	for i := range cands {
		if n, _ := shalomRef(cands[i].GuideNumber); n == d.Numero {
			ps := providerState{Status: d.Status, OseID: d.OseID}
			if event := s.applyReturnProviderState(cands[i].ID, ps); event != "" {
				s.notifyReturnEvent(cands[i].ID, event)
				return "retorno " + d.Status + " según Shalom"
			}
			return "sin cambios (retorno)"
		}
	}
	return "sin envío vigente con la guía " + d.Numero
}

// syncOpenReturns alinea con Shalom los retornos abiertos que ya tienen guía y código (de a 50 por llamada).
func (s *Service) syncOpenReturns(ctx context.Context, cl *shalom.Client, res *SyncShipmentsResult) error {
	var rows []database.EquipReturn
	if err := s.db.Where("status IN ? AND guide_number <> ''", openReturnStatuses).Find(&rows).Error; err != nil {
		return err
	}
	for i := 0; i < len(rows); i += 50 {
		end := i + 50
		if end > len(rows) {
			end = len(rows)
		}
		items := make([]shalom.BatchItem, 0, end-i)
		for _, r := range rows[i:end] {
			r := r
			numero, code := returnRef(&r)
			if numero == "" || code == "" {
				res.Skipped++
				continue
			}
			items = append(items, shalom.BatchItem{CustomID: fmt.Sprint(r.ID), Numero: numero, Codigo: code})
		}
		if len(items) == 0 {
			continue
		}
		results, err := cl.TrackBatch(ctx, items)
		if err != nil {
			return providerError(err)
		}
		for _, r := range results {
			res.Checked++
			if !r.OK || r.Tracking == nil {
				res.Failed++
				continue
			}
			var id uint
			fmt.Sscan(r.CustomID, &id)
			before := ""
			var cur database.EquipReturn
			if found, _ := findOne(s.db, &cur, "id = ?", id); found {
				before = cur.ProviderStatus
			}
			event := s.applyReturnProviderState(id, stateFromTracking(r.Tracking))
			if st := stateFromTracking(r.Tracking).Status; st != before {
				res.Changed++
			}
			if event != "" {
				s.notifyReturnEvent(id, event)
			}
		}
	}
	return nil
}

// ReceiveReturn «Recibido»: cierra el retorno con la condición del equipo. En buen estado reingresa al stock (la misma
// regla de Gestionar); dañado solo se registra. No hace falta repetir costos ni saldo: se conservan.
func (s *Service) ReceiveReturn(id uint, condition string, userID uint) (*ReturnView, error) {
	var r database.EquipReturn
	if found, err := findOne(s.db, &r, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("retorno no encontrado")
	}
	if r.Status != "solicitado" && r.Status != "en_camino" {
		return nil, invalid("el retorno ya está %s", strings.ReplaceAll(r.Status, "_", " "))
	}
	if condition == "" {
		condition = "buen_estado"
	}
	return s.UpdateReturn(id, ReturnUpdate{Status: "recibido", Condition: condition, ReturnCost: r.ReturnCost, UnpaidBalance: r.UnpaidBalance}, userID)
}
