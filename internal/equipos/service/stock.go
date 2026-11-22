package service

import (
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// semaphore estado del stock contra los umbrales del producto (misma regla que el Stock Maestro del Excel):
// ≥ verde = suficiente, ≥ amarillo = moderado, si no = bajo.
func semaphore(stock, yellow, green int) string {
	switch {
	case stock >= green:
		return "suficiente"
	case stock >= yellow:
		return "moderado"
	default:
		return "bajo"
	}
}

// stockTotals stock actual por producto en el Almacén 1 (el operativo, del que se vende). Lo que hay en otros
// almacenes (depósitos) se ve aparte con stockDepot.
func (s *Service) stockTotals() (map[uint]int, error) {
	type row struct {
		ProductID uint
		Total     int
	}
	var rows []row
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, COALESCE(SUM(quantity), 0) AS total").Where(inOperativeWarehouse).Group("product_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]int, len(rows))
	for _, r := range rows {
		out[r.ProductID] = r.Total
	}
	return out, nil
}

// stockDepot stock por producto fuera del Almacén 1 (suma de los depósitos).
func (s *Service) stockDepot() (map[uint]int, error) {
	var rows []struct {
		ProductID uint
		Total     int
	}
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, COALESCE(SUM(quantity), 0) AS total").Where("NOT " + inOperativeWarehouse).Group("product_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]int, len(rows))
	for _, r := range rows {
		out[r.ProductID] = r.Total
	}
	return out, nil
}

// StockRow una fila del Stock Maestro de un período.
type StockRow struct {
	ProductID   uint   `json:"product_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Opening     int    `json:"opening"`
	Ingresos    int    `json:"ingresos"`
	DirectIndep int    `json:"direct_independiente"`
	DirectPromo int    `json:"direct_promo_tk"`
	ComboIndep  int    `json:"combo_independiente"`
	ComboPromo  int    `json:"combo_promo_tk"`
	TotalOut    int    `json:"total_out"`
	Reentries   int    `json:"reingresos"`
	Adjustments int    `json:"adjustments"` // ajustes y bajas, con signo
	Current     int    `json:"current"`     // stock al cierre del período
	Yellow      int    `json:"yellow_threshold"`
	Green       int    `json:"green_threshold"`
	Semaphore   string `json:"semaphore"`
}

// StockReport arma el Stock Maestro del período YYYY-MM: inicial, salidas directas y por combo separadas por
// tipo de salida, reingresos, stock al cierre y semáforo. Salen del kardex, no de fórmulas.
func (s *Service) StockReport(period string) ([]StockRow, error) {
	start, end, err := periodBounds(period)
	if err != nil {
		return nil, err
	}
	var products []database.EquipProduct
	if err := s.db.Where("active = ?", true).Order("kind ASC, code ASC").Find(&products).Error; err != nil {
		return nil, err
	}
	// Los inactivos que tuvieron movimiento en el período también se informan.
	rows := map[uint]*StockRow{}
	order := []uint{}
	add := func(p database.EquipProduct) {
		if _, ok := rows[p.ID]; ok {
			return
		}
		rows[p.ID] = &StockRow{ProductID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Yellow: p.YellowThreshold, Green: p.GreenThreshold}
		order = append(order, p.ID)
	}
	for _, p := range products {
		add(p)
	}

	type before struct {
		ProductID uint
		Total     int
	}
	var prior []before
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, COALESCE(SUM(quantity), 0) AS total").
		Where(inOperativeWarehouse).Where("occurred_at < ?", start).Group("product_id").Scan(&prior).Error; err != nil {
		return nil, err
	}
	type grouped struct {
		ProductID    uint
		MovementType string
		ViaCombo     int
		SaleType     string
		Total        int
	}
	var inPeriod []grouped
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, movement_type, CASE WHEN via_combo_id IS NULL THEN 0 ELSE 1 END AS via_combo, sale_type_snapshot AS sale_type, COALESCE(SUM(quantity), 0) AS total").
		Where(inOperativeWarehouse).Where("occurred_at >= ? AND occurred_at < ?", start, end).
		Group("product_id, movement_type, CASE WHEN via_combo_id IS NULL THEN 0 ELSE 1 END, sale_type_snapshot").
		Scan(&inPeriod).Error; err != nil {
		return nil, err
	}

	need := map[uint]bool{}
	for _, b := range prior {
		need[b.ProductID] = true
	}
	for _, g := range inPeriod {
		need[g.ProductID] = true
	}
	var missing []uint
	for id := range need {
		if _, ok := rows[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		var extra []database.EquipProduct
		if err := s.db.Where("id IN ?", missing).Find(&extra).Error; err != nil {
			return nil, err
		}
		for _, p := range extra {
			add(p)
		}
	}

	for _, b := range prior {
		if r := rows[b.ProductID]; r != nil {
			r.Opening += b.Total
		}
	}
	for _, g := range inPeriod {
		r := rows[g.ProductID]
		if r == nil {
			continue
		}
		switch g.MovementType {
		case database.EquipMoveApertura:
			r.Opening += g.Total
		case database.EquipMoveIngreso, database.EquipMoveTransferIn:
			// Lo que llega del depósito (o regresa por una anulación) entra como ingreso del Almacén 1.
			r.Ingresos += g.Total
		case database.EquipMoveSalida, database.EquipMoveReenvio:
			q := -g.Total
			promo := g.SaleType == database.EquipSalePromoTK
			switch {
			case g.ViaCombo == 1 && promo:
				r.ComboPromo += q
			case g.ViaCombo == 1:
				r.ComboIndep += q
			case promo:
				r.DirectPromo += q
			default:
				r.DirectIndep += q
			}
		case database.EquipMoveReingreso:
			r.Reentries += g.Total
		case database.EquipMoveAjuste, database.EquipMoveBaja, database.EquipMoveTransferOut:
			r.Adjustments += g.Total
		}
	}

	out := make([]StockRow, 0, len(order))
	for _, id := range order {
		r := rows[id]
		r.TotalOut = r.DirectIndep + r.DirectPromo + r.ComboIndep + r.ComboPromo
		r.Current = r.Opening + r.Ingresos + r.Reentries + r.Adjustments - r.TotalOut
		r.Semaphore = semaphore(r.Current, r.Yellow, r.Green)
		out = append(out, *r)
	}
	return out, nil
}

// MovementView movimiento del kardex con el pedido de origen (si lo hay).
type MovementView struct {
	database.EquipStockMovement
	OrderNumber *int   `json:"order_number"`
	ComboCode   string `json:"combo_code"`
}

// ProductMovements kardex de un producto, del más reciente al más antiguo.
func (s *Service) ProductMovements(productID uint, limit int) ([]MovementView, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows []database.EquipStockMovement
	if err := s.db.Where("product_id = ?", productID).Order("occurred_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	orderIDs, comboIDs := []uint{}, []uint{}
	for _, m := range rows {
		if m.OrderID != nil {
			orderIDs = append(orderIDs, *m.OrderID)
		}
		if m.ViaComboID != nil {
			comboIDs = append(comboIDs, *m.ViaComboID)
		}
	}
	numbers := map[uint]int{}
	if len(orderIDs) > 0 {
		var orders []database.EquipOrder
		if err := s.db.Select("id", "order_number").Where("id IN ?", orderIDs).Find(&orders).Error; err != nil {
			return nil, err
		}
		for _, o := range orders {
			numbers[o.ID] = o.OrderNumber
		}
	}
	combos := map[uint]string{}
	if len(comboIDs) > 0 {
		var cs []database.EquipCombo
		if err := s.db.Select("id", "code").Where("id IN ?", comboIDs).Find(&cs).Error; err != nil {
			return nil, err
		}
		for _, c := range cs {
			combos[c.ID] = c.Code
		}
	}
	out := make([]MovementView, 0, len(rows))
	for _, m := range rows {
		v := MovementView{EquipStockMovement: m}
		if m.OrderID != nil {
			if n, ok := numbers[*m.OrderID]; ok {
				nn := n
				v.OrderNumber = &nn
			}
		}
		if m.ViaComboID != nil {
			v.ComboCode = combos[*m.ViaComboID]
		}
		out = append(out, v)
	}
	return out, nil
}

// MovementInput movimiento manual de stock (ingreso, ajuste, baja o apertura).
type MovementInput struct {
	ProductID    uint       `json:"product_id"`
	MovementType string     `json:"movement_type"`
	Quantity     int        `json:"quantity"`
	OccurredAt   *time.Time `json:"occurred_at"`
	Note         string     `json:"note"`
	UnitCost     *float64   `json:"unit_cost"` // solo ingresos
	// WarehouseID almacén del movimiento (vacío = Almacén 1). Los ingresos de mercadería importada suelen ir al depósito.
	WarehouseID *uint `json:"warehouse_id"`
}

// AddMovement registra un ingreso, ajuste, baja o apertura manual con nota obligatoria. Las salidas por pedido y los
// reingresos por retorno los generan los pedidos y retornos, nunca se cargan a mano.
func (s *Service) AddMovement(in MovementInput, userID uint) (*database.EquipStockMovement, error) {
	note := strings.TrimSpace(in.Note)
	if note == "" {
		return nil, invalid("la nota es obligatoria (ej. «Reposición 2026-09-03»)")
	}
	if in.Quantity == 0 {
		return nil, invalid("la cantidad no puede ser cero")
	}
	var p database.EquipProduct
	if err := s.db.First(&p, in.ProductID).Error; err != nil {
		return nil, invalid("producto no encontrado")
	}
	qty := in.Quantity
	switch in.MovementType {
	case database.EquipMoveIngreso, database.EquipMoveApertura:
		if qty < 0 {
			return nil, invalid("un ingreso debe ser una cantidad positiva")
		}
	case database.EquipMoveBaja:
		if qty > 0 {
			qty = -qty // se escribe en positivo; descuenta
		}
	case database.EquipMoveAjuste:
		// con signo
	default:
		return nil, invalid("tipo de movimiento inválido (ingreso, ajuste, baja o apertura)")
	}
	at := time.Now()
	if in.OccurredAt != nil && !in.OccurredAt.IsZero() {
		at = *in.OccurredAt
	}
	if at.After(time.Now().Add(24 * time.Hour)) {
		return nil, invalid("la fecha del movimiento no puede ser futura")
	}
	if in.UnitCost != nil {
		if in.MovementType != database.EquipMoveIngreso {
			return nil, invalid("el costo unitario solo aplica a ingresos")
		}
		if *in.UnitCost < 0 {
			return nil, invalid("el costo unitario no puede ser negativo")
		}
	}
	if closed, err := s.periodClosed(at); err != nil {
		return nil, err
	} else if closed != "" {
		return nil, invalid("el período %s está cerrado: reábrelo para registrar movimientos en esa fecha", closed)
	}
	m := &database.EquipStockMovement{
		ProductID: p.ID, OccurredAt: at, MovementType: in.MovementType, Quantity: qty, Note: note, UnitCost: in.UnitCost,
	}
	if in.WarehouseID != nil && *in.WarehouseID > 0 {
		def, err := defaultWarehouseID(s.db)
		if err != nil {
			return nil, err
		}
		if _, err := activeWarehouseTx(s.db, *in.WarehouseID, "elegido"); err != nil {
			return nil, err
		}
		if *in.WarehouseID != def { // el Almacén 1 se guarda como NULL, igual que los movimientos de los pedidos
			wid := *in.WarehouseID
			m.WarehouseID = &wid
		}
	}
	if userID > 0 {
		u := userID
		m.CreatedBy = &u
	}
	if err := s.db.Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}

// negativeStock productos que quedarían con stock negativo (útil para advertir).
func negativeStock(rows []StockRow) []StockRow {
	var out []StockRow
	for _, r := range rows {
		if r.Current < 0 {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}
