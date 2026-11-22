package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ── Entradas ───────────────────────────────────────────────────────────────

type TransferItemInput struct {
	ProductID uint `json:"product_id"`
	Quantity  int  `json:"quantity"`
}

// RequirementInput solicitud del Almacén 1 (o el que se indique) a un depósito.
type RequirementInput struct {
	RequestingWarehouseID uint                `json:"requesting_warehouse_id"` // vacío = Almacén 1
	SupplyingWarehouseID  uint                `json:"supplying_warehouse_id"`
	Date                  string              `json:"date"` // AAAA-MM-DD; vacío = hoy
	Notes                 string              `json:"notes"`
	Items                 []TransferItemInput `json:"items"`
}

// TransferInput transferencia entre almacenes. Con RequirementID atiende ese requerimiento (origen y destino salen de él).
type TransferInput struct {
	FromWarehouseID uint                `json:"from_warehouse_id"`
	ToWarehouseID   uint                `json:"to_warehouse_id"`
	RequirementID   *uint               `json:"requirement_id"`
	Date            string              `json:"date"`
	Notes           string              `json:"notes"`
	Items           []TransferItemInput `json:"items"`
	AllowNegative   bool                `json:"allow_negative"`
	NegativeNote    string              `json:"negative_note"`
}

// ── Vistas ─────────────────────────────────────────────────────────────────

type ProductLine struct {
	ProductID uint   `json:"product_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	Sent      int    `json:"sent,omitempty"`    // requerimientos: ya enviado
	Pending   int    `json:"pending,omitempty"` // requerimientos: falta enviar
}

type TransferView struct {
	database.EquipTransfer
	FromName          string        `json:"from_name"`
	ToName            string        `json:"to_name"`
	RequirementNumber *int          `json:"requirement_number"`
	CreatedByName     string        `json:"created_by_name"`
	Items             []ProductLine `json:"items"`
}

type RequirementView struct {
	database.EquipRequirement
	RequestingName string        `json:"requesting_name"`
	SupplyingName  string        `json:"supplying_name"`
	CreatedByName  string        `json:"created_by_name"`
	Items          []ProductLine `json:"items"`
	Transfers      []int         `json:"transfers"` // números de las transferencias vigentes que lo atienden
}

// ── Utilidades ─────────────────────────────────────────────────────────────

// mergeItems junta líneas repetidas del mismo producto y valida cantidades.
func mergeItems(in []TransferItemInput) ([]TransferItemInput, error) {
	if len(in) == 0 {
		return nil, invalid("agrega al menos un producto")
	}
	sum := map[uint]int{}
	order := []uint{}
	for _, it := range in {
		if it.ProductID == 0 {
			return nil, invalid("elige el producto de cada línea")
		}
		if it.Quantity <= 0 {
			return nil, invalid("las cantidades deben ser mayores a cero")
		}
		if _, ok := sum[it.ProductID]; !ok {
			order = append(order, it.ProductID)
		}
		sum[it.ProductID] += it.Quantity
	}
	out := make([]TransferItemInput, 0, len(order))
	for _, id := range order {
		out = append(out, TransferItemInput{ProductID: id, Quantity: sum[id]})
	}
	return out, nil
}

func productInfo(db *gorm.DB, ids []uint) (map[uint]database.EquipProduct, error) {
	out := map[uint]database.EquipProduct{}
	if len(ids) == 0 {
		return out, nil
	}
	var ps []database.EquipProduct
	if err := db.Where("id IN ?", ids).Find(&ps).Error; err != nil {
		return nil, err
	}
	for _, p := range ps {
		out[p.ID] = p
	}
	return out, nil
}

func warehouseNames(db *gorm.DB) map[uint]string {
	var ws []database.EquipWarehouse
	_ = db.Find(&ws).Error
	out := map[uint]string{}
	for _, w := range ws {
		out[w.ID] = w.Name
	}
	return out
}

func userName(db *gorm.DB, id *uint) string {
	if id == nil {
		return ""
	}
	var name string
	_ = db.Table("super_admin_users").Select("name").Where("id = ?", *id).Scan(&name).Error
	return name
}

func docDate(raw string) (time.Time, error) {
	d, err := parseDateOnly(raw)
	if err != nil {
		return time.Time{}, err
	}
	if d == nil {
		return todayNoon(), nil
	}
	if d.After(time.Now().Add(24 * time.Hour)) {
		return time.Time{}, invalid("la fecha no puede ser futura")
	}
	return *d, nil
}

func nextNumber(tx *gorm.DB, model any) int {
	var max struct{ N int }
	_ = tx.Model(model).Select("COALESCE(MAX(number), 0) AS n").Scan(&max).Error
	return max.N + 1
}

// ── Requerimientos ─────────────────────────────────────────────────────────

func (s *Service) requirementView(db *gorm.DB, r database.EquipRequirement) (*RequirementView, error) {
	var items []database.EquipRequirementItem
	if err := db.Where("requirement_id = ?", r.ID).Order("id").Find(&items).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ProductID)
	}
	prods, err := productInfo(db, ids)
	if err != nil {
		return nil, err
	}
	names := warehouseNames(db)
	v := &RequirementView{EquipRequirement: r, RequestingName: names[r.RequestingWarehouseID], SupplyingName: names[r.SupplyingWarehouseID], CreatedByName: userName(db, r.CreatedBy), Items: []ProductLine{}, Transfers: []int{}}
	for _, it := range items {
		p := prods[it.ProductID]
		v.Items = append(v.Items, ProductLine{ProductID: it.ProductID, Code: p.Code, Name: p.Name, Quantity: it.Quantity, Sent: it.Sent, Pending: it.Quantity - it.Sent})
	}
	var nums []int
	_ = db.Model(&database.EquipTransfer{}).Where("requirement_id = ? AND status = ?", r.ID, "registrada").Order("number").Pluck("number", &nums).Error
	if nums != nil {
		v.Transfers = nums
	}
	return v, nil
}

func (s *Service) CreateRequirement(in RequirementInput, userID uint) (*RequirementView, error) {
	items, err := mergeItems(in.Items)
	if err != nil {
		return nil, err
	}
	at, err := docDate(in.Date)
	if err != nil {
		return nil, err
	}
	var id uint
	err = s.db.Transaction(func(tx *gorm.DB) error {
		def, err := defaultWarehouseID(tx)
		if err != nil {
			return err
		}
		from := in.RequestingWarehouseID
		if from == 0 {
			from = def
		}
		if _, err := activeWarehouseTx(tx, from, "solicitante"); err != nil {
			return err
		}
		if _, err := activeWarehouseTx(tx, in.SupplyingWarehouseID, "que abastece"); err != nil {
			return err
		}
		if from == in.SupplyingWarehouseID {
			return invalid("el almacén que pide y el que abastece deben ser distintos")
		}
		ids := make([]uint, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ProductID)
		}
		prods, err := productInfo(tx, ids)
		if err != nil {
			return err
		}
		for _, it := range items {
			if p, ok := prods[it.ProductID]; !ok || !p.Active {
				return invalid("un producto del requerimiento no existe o está inactivo")
			}
		}
		r := database.EquipRequirement{
			Number: nextNumber(tx, &database.EquipRequirement{}), RequestingWarehouseID: from, SupplyingWarehouseID: in.SupplyingWarehouseID,
			Status: "pendiente", RequestedAt: at, Notes: strings.TrimSpace(in.Notes),
		}
		if userID > 0 {
			u := userID
			r.CreatedBy = &u
		}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		for _, it := range items {
			if err := tx.Create(&database.EquipRequirementItem{RequirementID: r.ID, ProductID: it.ProductID, Quantity: it.Quantity}).Error; err != nil {
				return err
			}
		}
		id = r.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetRequirement(id)
}

func (s *Service) GetRequirement(id uint) (*RequirementView, error) {
	var r database.EquipRequirement
	if found, err := findOne(s.db, &r, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("requerimiento no encontrado")
	}
	return s.requirementView(s.db, r)
}

func (s *Service) ListRequirements(status string) ([]RequirementView, error) {
	q := s.db.Model(&database.EquipRequirement{})
	switch strings.TrimSpace(status) {
	case "":
	case "abiertos":
		q = q.Where("status IN ?", []string{"pendiente", "parcial"})
	default:
		q = q.Where("status = ?", status)
	}
	var rows []database.EquipRequirement
	if err := q.Order("number DESC").Limit(500).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]RequirementView, 0, len(rows))
	for _, r := range rows {
		v, err := s.requirementView(s.db, r)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// CancelRequirement anula un requerimiento que todavía no se atendió. Uno atendido parcial o totalmente se anula
// después de anular las transferencias que lo atienden.
func (s *Service) CancelRequirement(id uint, reason string) (*RequirementView, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("indica el motivo de la anulación")
	}
	if len(reason) > 255 {
		reason = reason[:255]
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var r database.EquipRequirement
		if found, err := findOne(tx, &r, "id = ?", id); err != nil {
			return err
		} else if !found {
			return invalid("requerimiento no encontrado")
		}
		if r.Status == "anulado" {
			return invalid("el requerimiento ya está anulado")
		}
		if r.Status != "pendiente" {
			return invalid("el requerimiento ya tiene transferencias (%s): anúlalas primero", r.Status)
		}
		now := time.Now()
		r.Status, r.CancelledAt, r.CancelReason = "anulado", &now, reason
		return tx.Save(&r).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetRequirement(id)
}

// recomputeRequirementTx recalcula el estado según lo enviado.
func recomputeRequirementTx(tx *gorm.DB, requirementID uint) error {
	var items []database.EquipRequirementItem
	if err := tx.Where("requirement_id = ?", requirementID).Find(&items).Error; err != nil {
		return err
	}
	sent, done := 0, true
	for _, it := range items {
		sent += it.Sent
		if it.Sent < it.Quantity {
			done = false
		}
	}
	status := "pendiente"
	switch {
	case len(items) > 0 && done:
		status = "atendido"
	case sent > 0:
		status = "parcial"
	}
	return tx.Model(&database.EquipRequirement{}).Where("id = ?", requirementID).Update("status", status).Error
}

// ── Transferencias ─────────────────────────────────────────────────────────

func (s *Service) transferView(db *gorm.DB, t database.EquipTransfer) (*TransferView, error) {
	var items []database.EquipTransferItem
	if err := db.Where("transfer_id = ?", t.ID).Order("id").Find(&items).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ProductID)
	}
	prods, err := productInfo(db, ids)
	if err != nil {
		return nil, err
	}
	names := warehouseNames(db)
	v := &TransferView{EquipTransfer: t, FromName: names[t.FromWarehouseID], ToName: names[t.ToWarehouseID], CreatedByName: userName(db, t.CreatedBy), Items: []ProductLine{}}
	for _, it := range items {
		p := prods[it.ProductID]
		v.Items = append(v.Items, ProductLine{ProductID: it.ProductID, Code: p.Code, Name: p.Name, Quantity: it.Quantity})
	}
	if t.RequirementID != nil {
		var n int
		_ = db.Model(&database.EquipRequirement{}).Where("id = ?", *t.RequirementID).Select("number").Scan(&n).Error
		if n > 0 {
			v.RequirementNumber = &n
		}
	}
	return v, nil
}

func (s *Service) GetTransfer(id uint) (*TransferView, error) {
	var t database.EquipTransfer
	if found, err := findOne(s.db, &t, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("transferencia no encontrada")
	}
	return s.transferView(s.db, t)
}

func (s *Service) CreateTransfer(in TransferInput, userID uint) (*TransferView, error) {
	items, err := mergeItems(in.Items)
	if err != nil {
		return nil, err
	}
	at, err := docDate(in.Date)
	if err != nil {
		return nil, err
	}
	if closed, err := s.periodClosed(at); err != nil {
		return nil, err
	} else if closed != "" {
		return nil, invalid("el período %s está cerrado: reábrelo para registrar movimientos en esa fecha", closed)
	}
	var id uint
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := defaultWarehouseID(tx); err != nil {
			return err
		}
		from, to := in.FromWarehouseID, in.ToWarehouseID
		var req *database.EquipRequirement
		var reqItems map[uint]*database.EquipRequirementItem
		if in.RequirementID != nil {
			var r database.EquipRequirement
			if found, err := findOne(tx, &r, "id = ?", *in.RequirementID); err != nil {
				return err
			} else if !found {
				return invalid("requerimiento no encontrado")
			}
			if r.Status != "pendiente" && r.Status != "parcial" {
				return invalid("el requerimiento está %s: ya no admite transferencias", r.Status)
			}
			// El depósito que abastece envía al almacén que pidió.
			if from != 0 && from != r.SupplyingWarehouseID || to != 0 && to != r.RequestingWarehouseID {
				return invalid("el origen y el destino deben ser los del requerimiento")
			}
			from, to = r.SupplyingWarehouseID, r.RequestingWarehouseID
			req = &r
			var rows []database.EquipRequirementItem
			if err := tx.Where("requirement_id = ?", r.ID).Find(&rows).Error; err != nil {
				return err
			}
			reqItems = map[uint]*database.EquipRequirementItem{}
			for i := range rows {
				reqItems[rows[i].ProductID] = &rows[i]
			}
			for _, it := range items {
				ri := reqItems[it.ProductID]
				if ri == nil {
					return invalid("un producto no figura en el requerimiento")
				}
				if it.Quantity > ri.Quantity-ri.Sent {
					return invalid("no puedes enviar más de lo pendiente del requerimiento (quedan %d)", ri.Quantity-ri.Sent)
				}
			}
		}
		fw, err := activeWarehouseTx(tx, from, "de origen")
		if err != nil {
			return err
		}
		tw, err := activeWarehouseTx(tx, to, "de destino")
		if err != nil {
			return err
		}
		if fw.ID == tw.ID {
			return invalid("el origen y el destino deben ser distintos")
		}
		ids := make([]uint, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ProductID)
		}
		prods, err := productInfo(tx, ids)
		if err != nil {
			return err
		}
		for _, it := range items {
			if p, ok := prods[it.ProductID]; !ok || !p.Active {
				return invalid("un producto de la transferencia no existe o está inactivo")
			}
		}
		// Stock negativo en el origen: solo advierte (con nota), como en los pedidos.
		cur, err := stockAtTx(tx, fw.ID, ids)
		if err != nil {
			return err
		}
		var bad []NegativeStockItem
		for _, it := range items {
			if res := cur[it.ProductID] - it.Quantity; res < 0 {
				bad = append(bad, NegativeStockItem{ProductID: it.ProductID, Code: prods[it.ProductID].Code, Current: cur[it.ProductID], Needed: it.Quantity, Resulting: res})
			}
		}
		if len(bad) > 0 {
			sort.Slice(bad, func(i, j int) bool { return bad[i].Code < bad[j].Code })
			if !in.AllowNegative {
				return &NegativeStockError{Items: bad}
			}
			if strings.TrimSpace(in.NegativeNote) == "" {
				return invalid("transferir con stock negativo en el origen exige una nota que explique el motivo")
			}
		}
		t := database.EquipTransfer{
			Number: nextNumber(tx, &database.EquipTransfer{}), FromWarehouseID: fw.ID, ToWarehouseID: tw.ID, RequirementID: in.RequirementID,
			Status: "registrada", TransferredAt: at, Notes: strings.TrimSpace(in.Notes),
		}
		if len(bad) > 0 {
			t.Notes = strings.TrimSpace(t.Notes + " | Con stock negativo en el origen: " + strings.TrimSpace(in.NegativeNote))
		}
		var uid *uint
		if userID > 0 {
			u := userID
			uid = &u
			t.CreatedBy = uid
		}
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		note := fmt.Sprintf("Transferencia N° %d (%s → %s)", t.Number, fw.Name, tw.Name)
		tid, fwid, twid := t.ID, fw.ID, tw.ID
		for _, it := range items {
			if err := tx.Create(&database.EquipTransferItem{TransferID: t.ID, ProductID: it.ProductID, Quantity: it.Quantity}).Error; err != nil {
				return err
			}
			out := database.EquipStockMovement{ProductID: it.ProductID, OccurredAt: at, MovementType: database.EquipMoveTransferOut, Quantity: -it.Quantity, WarehouseID: &fwid, TransferID: &tid, Note: note, CreatedBy: uid}
			in2 := database.EquipStockMovement{ProductID: it.ProductID, OccurredAt: at, MovementType: database.EquipMoveTransferIn, Quantity: it.Quantity, WarehouseID: &twid, TransferID: &tid, Note: note, CreatedBy: uid}
			if err := tx.Create(&out).Error; err != nil {
				return err
			}
			if err := tx.Create(&in2).Error; err != nil {
				return err
			}
			if ri := reqItems[it.ProductID]; ri != nil {
				ri.Sent += it.Quantity
				if err := tx.Model(&database.EquipRequirementItem{}).Where("id = ?", ri.ID).Update("sent", ri.Sent).Error; err != nil {
					return err
				}
			}
		}
		if req != nil {
			if err := recomputeRequirementTx(tx, req.ID); err != nil {
				return err
			}
		}
		id = t.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(id)
}

// CancelTransfer anula una transferencia con los movimientos contrarios. Se bloquea si el almacén destino ya no tiene lo
// que recibió (por ejemplo, porque ya lo vendió): no se puede devolver lo que no está.
func (s *Service) CancelTransfer(id uint, reason string, userID uint) (*TransferView, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("indica el motivo de la anulación")
	}
	if len(reason) > 255 {
		reason = reason[:255]
	}
	now := time.Now()
	if closed, err := s.periodClosed(now); err != nil {
		return nil, err
	} else if closed != "" {
		return nil, invalid("el período %s está cerrado: reábrelo para anular", closed)
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var t database.EquipTransfer
		if found, err := findOne(tx, &t, "id = ?", id); err != nil {
			return err
		} else if !found {
			return invalid("transferencia no encontrada")
		}
		if t.Status == "anulada" {
			return invalid("la transferencia ya está anulada")
		}
		var items []database.EquipTransferItem
		if err := tx.Where("transfer_id = ?", t.ID).Find(&items).Error; err != nil {
			return err
		}
		ids := make([]uint, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ProductID)
		}
		prods, err := productInfo(tx, ids)
		if err != nil {
			return err
		}
		cur, err := stockAtTx(tx, t.ToWarehouseID, ids)
		if err != nil {
			return err
		}
		var short []string
		for _, it := range items {
			if cur[it.ProductID] < it.Quantity {
				short = append(short, fmt.Sprintf("%s (recibió %d, hoy tiene %d)", prods[it.ProductID].Code, it.Quantity, cur[it.ProductID]))
			}
		}
		if len(short) > 0 {
			return invalid("el almacén destino ya no tiene lo transferido: %s. No se puede anular", strings.Join(short, ", "))
		}
		var uid *uint
		if userID > 0 {
			u := userID
			uid = &u
		}
		note := fmt.Sprintf("Anulación de la transferencia N° %d", t.Number)
		tid, fwid, twid := t.ID, t.FromWarehouseID, t.ToWarehouseID
		for _, it := range items {
			back := database.EquipStockMovement{ProductID: it.ProductID, OccurredAt: now, MovementType: database.EquipMoveTransferIn, Quantity: it.Quantity, WarehouseID: &fwid, TransferID: &tid, Note: note, CreatedBy: uid}
			out := database.EquipStockMovement{ProductID: it.ProductID, OccurredAt: now, MovementType: database.EquipMoveTransferOut, Quantity: -it.Quantity, WarehouseID: &twid, TransferID: &tid, Note: note, CreatedBy: uid}
			if err := tx.Create(&back).Error; err != nil {
				return err
			}
			if err := tx.Create(&out).Error; err != nil {
				return err
			}
		}
		if t.RequirementID != nil {
			for _, it := range items {
				if err := tx.Model(&database.EquipRequirementItem{}).Where("requirement_id = ? AND product_id = ?", *t.RequirementID, it.ProductID).
					Update("sent", gorm.Expr("CASE WHEN sent >= ? THEN sent - ? ELSE 0 END", it.Quantity, it.Quantity)).Error; err != nil {
					return err
				}
			}
			if err := recomputeRequirementTx(tx, *t.RequirementID); err != nil {
				return err
			}
		}
		t.Status, t.CancelledAt, t.CancelReason, t.CancelledBy = "anulada", &now, reason, uid
		return tx.Save(&t).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(id)
}

// TransferFilter filtros del historial de transferencias.
type TransferFilter struct {
	From, To      string // fechas AAAA-MM-DD
	WarehouseID   uint   // origen o destino
	ProductID     uint
	Status        string
	Page, PerPage int
}

type TransferListResult struct {
	Rows    []TransferView `json:"rows"`
	Total   int64          `json:"total"`
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
}

func (s *Service) ListTransfers(f TransferFilter) (*TransferListResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 200 {
		f.PerPage = 25
	}
	q := s.db.Model(&database.EquipTransfer{})
	if d, err := parseDateOnly(f.From); err != nil {
		return nil, err
	} else if d != nil {
		q = q.Where("transferred_at >= ?", d.Format("2006-01-02"))
	}
	if d, err := parseDateOnly(f.To); err != nil {
		return nil, err
	} else if d != nil {
		q = q.Where("transferred_at < ?", d.AddDate(0, 0, 1).Format("2006-01-02"))
	}
	if f.WarehouseID > 0 {
		q = q.Where("from_warehouse_id = ? OR to_warehouse_id = ?", f.WarehouseID, f.WarehouseID)
	}
	if f.ProductID > 0 {
		q = q.Where("id IN (?)", s.db.Model(&database.EquipTransferItem{}).Select("transfer_id").Where("product_id = ?", f.ProductID))
	}
	if st := strings.TrimSpace(f.Status); st != "" {
		q = q.Where("status = ?", st)
	}
	res := &TransferListResult{Page: f.Page, PerPage: f.PerPage, Rows: []TransferView{}}
	if err := q.Session(&gorm.Session{}).Count(&res.Total).Error; err != nil {
		return nil, err
	}
	var rows []database.EquipTransfer
	if err := q.Order("number DESC").Limit(f.PerPage).Offset((f.Page - 1) * f.PerPage).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, t := range rows {
		v, err := s.transferView(s.db, t)
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, *v)
	}
	return res, nil
}
