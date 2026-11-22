package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ImportCounts cantidades que se importarían.
type ImportCounts struct {
	Products       int `json:"products"`
	NewProducts    int `json:"new_products"`
	Combos         int `json:"combos"`
	Orders         int `json:"orders"`
	Items          int `json:"items"`
	Payments       int `json:"payments"`
	Returns        int `json:"returns"`
	Movements      int `json:"movements"`
	NewCustomers   int `json:"new_customers"`
	ClientesVarios int `json:"orders_without_customer"`
}

// ImportPreview resultado de analizar el libro, sin guardar nada.
type ImportPreview struct {
	Period          string                     `json:"period"`
	FileName        string                     `json:"file_name"`
	StockManager    string                     `json:"stock_manager"`
	Counts          ImportCounts               `json:"counts"`
	SalesTotal      float64                    `json:"sales_total"`
	CollectedTotal  float64                    `json:"collected_total"`
	Issues          []ImportIssue              `json:"issues"`
	IssuesTruncated bool                       `json:"issues_truncated"`
	SeverityCounts  map[string]int             `json:"severity_counts"`
	CodeCounts      map[string]int             `json:"code_counts"`
	Reconciliation  []ImportReconRow           `json:"reconciliation"`
	ReconOK         bool                       `json:"reconciliation_ok"`
	AlreadyImported *database.EquipImportBatch `json:"already_imported"`
	CanCommit       bool                       `json:"can_commit"`
	BlockedBy       []string                   `json:"blocked_by"`
}

const maxPreviewIssues = 400

func customerKey(d docInfo) string {
	if d.DocType == "SIN_DOC" || d.DocNumber == "" {
		return ""
	}
	return d.DocType + "|" + d.DocNumber
}

// buildPreview valida el plan contra la base de datos (lote previo, números de pedido repetidos, clientes y
// productos nuevos) y arma la respuesta de la vista previa.
func (s *Service) buildPreview(plan *importPlan) (*ImportPreview, error) {
	pv := &ImportPreview{Period: plan.Period, FileName: plan.FileName, StockManager: plan.StockManager,
		SeverityCounts: map[string]int{}, CodeCounts: map[string]int{}}

	if plan.Period != "" {
		var b database.EquipImportBatch
		found, err := findOne(s.db, &b, "period = ?", plan.Period)
		if err != nil {
			return nil, err
		}
		if found {
			pv.AlreadyImported = &b
			plan.add(SevError, "periodo_importado", "", 0, 0, "El período %s ya fue importado el %s (lote #%d)", plan.Period, b.CreatedAt.In(limaLoc()).Format("02/01/2006 15:04"), b.ID)
		}
	}
	if len(plan.Orders) > 0 {
		nums := make([]int, 0, len(plan.Orders))
		for _, o := range plan.Orders {
			nums = append(nums, o.Number)
		}
		var existing []int
		if err := s.db.Model(&database.EquipOrder{}).Where("order_number IN ?", nums).Pluck("order_number", &existing).Error; err != nil {
			return nil, err
		}
		if len(existing) > 0 {
			sort.Ints(existing)
			plan.add(SevError, "pedido_existente", sheetOrders, 0, 0, "Ya existen pedidos con estos números en el sistema: %v", firstInts(existing, 10))
		}
	}

	existingProducts := map[string]bool{}
	var prods []database.EquipProduct
	if err := s.db.Select("id", "code").Find(&prods).Error; err != nil {
		return nil, err
	}
	for _, p := range prods {
		existingProducts[key(p.Code)] = true
	}
	var combosDB []database.EquipCombo
	if err := s.db.Select("id", "code").Find(&combosDB).Error; err != nil {
		return nil, err
	}
	for _, c := range combosDB {
		if plan.productByKey(key(c.Code)) != nil {
			plan.add(SevError, "codigo_combo", sheetCatalog, 0, 0, "El código %q ya existe como combo en el sistema; no puede ser también un producto", c.Code)
		}
	}
	for _, p := range plan.Products {
		if !existingProducts[key(p.Code)] {
			pv.Counts.NewProducts++
		}
	}
	// Clientes nuevos.
	known := map[string]bool{}
	var custs []database.EquipCustomer
	if err := s.db.Where("doc_number IS NOT NULL").Find(&custs).Error; err != nil {
		return nil, err
	}
	for _, c := range custs {
		if c.DocNumber != nil {
			known[c.DocType+"|"+*c.DocNumber] = true
		}
	}
	newCust := map[string]bool{}
	for _, o := range plan.Orders {
		k := customerKey(o.Doc)
		switch {
		case k == "":
			pv.Counts.ClientesVarios++
		case !known[k]:
			newCust[k] = true
		}
		pv.Counts.Items += len(o.Items)
		pv.Counts.Payments += len(o.Payments)
		sum := 0.0
		for _, it := range o.Items {
			sum += it.Subtotal
		}
		pv.SalesTotal += sum
		for _, pay := range o.Payments {
			pv.CollectedTotal += pay.Amount
		}
	}
	pv.Counts.NewCustomers = len(newCust)
	pv.SalesTotal, pv.CollectedTotal = round2(pv.SalesTotal), round2(pv.CollectedTotal)
	pv.Counts.Products, pv.Counts.Combos = len(plan.Products), len(plan.Combos)
	pv.Counts.Orders, pv.Counts.Returns, pv.Counts.Movements = len(plan.Orders), len(plan.Returns), len(plan.Movements)

	pv.Reconciliation, pv.ReconOK = plan.reconcile()
	plan.explainReingresos(pv.Reconciliation)

	// Hallazgos: errores primero, luego advertencias e información; se cuentan todos pero se listan los más relevantes.
	for _, i := range plan.Issues {
		pv.SeverityCounts[i.Severity]++
		pv.CodeCounts[i.Code]++
	}
	rank := map[string]int{SevError: 0, SevWarning: 1, SevInfo: 2}
	sorted := append([]ImportIssue(nil), plan.Issues...)
	sort.SliceStable(sorted, func(a, b int) bool { return rank[sorted[a].Severity] < rank[sorted[b].Severity] })
	if len(sorted) > maxPreviewIssues {
		sorted, pv.IssuesTruncated = sorted[:maxPreviewIssues], true
	}
	pv.Issues = sorted

	if n := pv.SeverityCounts[SevError]; n > 0 {
		var first []string
		for _, i := range sorted {
			if i.Severity == SevError && len(first) < 3 {
				first = append(first, i.Message)
			}
		}
		pv.BlockedBy = append(pv.BlockedBy, fmt.Sprintf("hay %d error(es) en el libro: %s", n, strings.Join(first, " · ")))
	}
	if !pv.ReconOK {
		bad := 0
		for _, r := range pv.Reconciliation {
			if !r.Match {
				bad++
			}
		}
		pv.BlockedBy = append(pv.BlockedBy, fmt.Sprintf("el stock calculado no coincide con el Stock Maestro del libro en %d producto(s)", bad))
	}
	pv.CanCommit = len(pv.BlockedBy) == 0
	return pv, nil
}

// findOne busca un registro con Limit(1)+Find: «no existe» no es un error ni ensucia el log (a diferencia de First).
func findOne(tx *gorm.DB, dest any, query string, args ...any) (bool, error) {
	res := tx.Where(query, args...).Limit(1).Find(dest)
	return res.RowsAffected > 0, res.Error
}

func firstInts(v []int, n int) []int {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// ImportPreview lee el libro y muestra qué se importaría y si el stock cuadra con el Stock Maestro. No guarda nada.
func (s *Service) ImportPreview(payload ImportPayload) (*ImportPreview, error) {
	if len(payload.Sheets) == 0 {
		return nil, invalid("el archivo no trae hojas")
	}
	plan := analyze(payload)
	return s.buildPreview(plan)
}

// ImportResult resultado de una importación confirmada.
type ImportResult struct {
	Batch   database.EquipImportBatch `json:"batch"`
	Preview *ImportPreview            `json:"preview"`
}

// ImportCommit guarda el libro del mes. Solo se confirma si no hay errores y el stock calculado coincide EXACTAMENTE
// con el Stock Maestro del Excel; además se vuelve a verificar contra la base de datos antes de confirmar la transacción.
func (s *Service) ImportCommit(payload ImportPayload, userID uint) (*ImportResult, error) {
	if len(payload.Sheets) == 0 {
		return nil, invalid("el archivo no trae hojas")
	}
	plan := analyze(payload)
	pv, err := s.buildPreview(plan)
	if err != nil {
		return nil, err
	}
	if !pv.CanCommit {
		return nil, invalid("No se puede importar: %s", strings.Join(pv.BlockedBy, "; "))
	}

	var batch database.EquipImportBatch
	err = s.db.Transaction(func(tx *gorm.DB) error {
		return s.persistPlan(tx, plan, payload.FileName, userID, &batch)
	})
	if err != nil {
		return nil, err
	}
	return &ImportResult{Batch: batch, Preview: pv}, nil
}

func (s *Service) persistPlan(tx *gorm.DB, plan *importPlan, fileName string, userID uint, batch *database.EquipImportBatch) error {
	var uid *uint
	if userID > 0 {
		u := userID
		uid = &u
	}
	now := time.Now()
	*batch = database.EquipImportBatch{Period: plan.Period, FileName: fileName, CreatedBy: uid}
	if err := tx.Create(batch).Error; err != nil {
		return err
	}
	bid := &batch.ID

	// Transportista por defecto (todo el libro se envió por Shalom).
	var carrier database.EquipCarrier
	if found, err := findOne(tx, &carrier, "code = ?", "shalom"); err != nil {
		return err
	} else if !found {
		var n int64
		tx.Model(&database.EquipCarrier{}).Where("is_default = ?", true).Count(&n)
		carrier = database.EquipCarrier{Code: "shalom", Name: "Shalom", DispatchDays: "1,3,5", PickupDays: 15,
			GuideLabel: "N° de guía Shalom", IsDefault: n == 0, Active: true}
		if err := tx.Create(&carrier).Error; err != nil {
			return err
		}
	}

	// Productos.
	productID := map[string]uint{}
	for _, pp := range plan.Products {
		var p database.EquipProduct
		found, err := findOne(tx, &p, "LOWER(code) = ?", strings.ToLower(pp.Code))
		switch {
		case err != nil:
			return err
		case !found:
			p = database.EquipProduct{Code: pp.Code, Name: pp.Name, Kind: pp.Kind, Notes: pp.Notes,
				YellowThreshold: pp.Yellow, GreenThreshold: pp.Green, Active: true}
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
		default:
			upd := map[string]any{}
			if p.YellowThreshold == 0 && p.GreenThreshold == 0 && (pp.Yellow > 0 || pp.Green > 0) {
				upd["yellow_threshold"], upd["green_threshold"] = pp.Yellow, pp.Green
			}
			if p.Notes == "" && pp.Notes != "" {
				upd["notes"] = pp.Notes
			}
			if len(upd) > 0 {
				if err := tx.Model(&p).Updates(upd).Error; err != nil {
					return err
				}
			}
		}
		productID[key(pp.Code)] = p.ID
	}
	pidOf := func(code string) (uint, error) {
		if id, ok := productID[key(code)]; ok {
			return id, nil
		}
		return 0, fmt.Errorf("producto %q sin resolver", code)
	}

	// Combos.
	comboID := map[string]uint{}
	for _, pc := range plan.Combos {
		var c database.EquipCombo
		found, err := findOne(tx, &c, "LOWER(code) = ?", strings.ToLower(pc.Code))
		switch {
		case err != nil:
			return err
		case !found:
			c = database.EquipCombo{Code: pc.Code, Name: pc.Code, Active: true}
			if err := tx.Create(&c).Error; err != nil {
				return err
			}
		default:
			if err := tx.Where("combo_id = ?", c.ID).Delete(&database.EquipComboItem{}).Error; err != nil {
				return err
			}
		}
		for _, it := range pc.Items {
			pid, err := pidOf(it.ProductCode)
			if err != nil {
				return err
			}
			if err := tx.Create(&database.EquipComboItem{ComboID: c.ID, ProductID: pid, Quantity: it.Quantity}).Error; err != nil {
				return err
			}
		}
		comboID[key(pc.Code)] = c.ID
	}

	// Clientes existentes.
	customerID := map[string]uint{}
	var existing []database.EquipCustomer
	if err := tx.Where("doc_number IS NOT NULL").Find(&existing).Error; err != nil {
		return err
	}
	for _, c := range existing {
		if c.DocNumber != nil {
			customerID[c.DocType+"|"+*c.DocNumber] = c.ID
		}
	}

	orderID := map[int]uint{}
	shipmentByGuide := map[string]uint{}
	orderByGuide := map[string]uint{}
	paymentsCount := 0
	maxOrder := 0
	for _, o := range plan.Orders {
		var custID *uint
		if k := customerKey(o.Doc); k != "" {
			id, ok := customerID[k]
			if !ok {
				num := o.Doc.DocNumber
				c := database.EquipCustomer{Name: o.CustomerName, DocType: o.Doc.DocType, DocNumber: &num,
					ContactDNI: o.Doc.ContactDNI, Phone: o.Phone, PhoneKind: o.PhoneKind}
				if c.Name == "" {
					c.Name = "(sin nombre)"
				}
				if err := tx.Create(&c).Error; err != nil {
					return err
				}
				id = c.ID
				customerID[k] = id
			}
			custID = &id
		}
		total := 0.0
		for _, it := range o.Items {
			total += it.Subtotal
		}
		total = round2(total)
		paid := 0.0
		for _, pay := range o.Payments {
			paid += pay.Amount
		}
		paid = round2(paid)
		balance := round2(total - paid)
		if balance < 0 {
			balance = 0
		}
		status := "pendiente"
		switch {
		case total <= 0 || paid >= total-0.005:
			status = "pagado"
		case paid > 0:
			status = "parcial"
		}
		ord := database.EquipOrder{
			OrderNumber: o.Number, SaleType: o.SaleType, OrderDate: o.OrderDate, RegisteredAt: now, ConfirmedAt: &now,
			IsGift: o.IsGift, CustomerID: custID, CustomerName: o.CustomerName, CustomerDocType: o.Doc.DocType,
			CustomerDocNumber: o.Doc.DocNumber, ContactDNI: o.Doc.ContactDNI, CustomerPhone: o.Phone,
			BillingDocType: o.BillingDoc, TotalAmount: total, PaidAmount: paid, BalanceAmount: balance, PaymentStatus: status,
			ValidationStatus: "validado", Status: "registrado", ImportBatchID: bid, CreatedBy: uid,
		}
		if ord.CustomerName == "" {
			ord.CustomerName = "Clientes varios"
		}
		if err := tx.Create(&ord).Error; err != nil {
			return err
		}
		orderID[o.Number] = ord.ID
		if o.Number > maxOrder {
			maxOrder = o.Number
		}

		for _, it := range o.Items {
			row := database.EquipOrderItem{OrderID: ord.ID, LineNo: it.Line, LineType: it.Type, Description: it.Description,
				PlanMonths: it.PlanMonths, Quantity: it.Quantity, UnitPrice: it.UnitPrice, Subtotal: it.Subtotal,
				IsCourtesy: it.IsCourtesy, Notes: it.Notes}
			switch it.Type {
			case "producto":
				pid, err := pidOf(it.Code)
				if err != nil {
					return err
				}
				row.ProductID = &pid
			case "combo":
				id := comboID[key(it.Code)]
				row.ComboID = &id
				if c := plan.comboByKey(key(it.Code)); c != nil {
					type snap struct {
						ProductID uint   `json:"product_id"`
						Code      string `json:"code"`
						Quantity  int    `json:"quantity"`
					}
					var snaps []snap
					for _, ci := range c.Items {
						pid, err := pidOf(ci.ProductCode)
						if err != nil {
							return err
						}
						snaps = append(snaps, snap{ProductID: pid, Code: ci.ProductCode, Quantity: ci.Quantity})
					}
					b, _ := json.Marshal(snaps)
					row.ComboSnapshotJSON = string(b)
				}
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}

		for _, pay := range o.Payments {
			p := database.EquipPayment{CustomerID: custID, Amount: pay.Amount, PaidAt: pay.PaidAt, Method: pay.Method, Moment: pay.Moment,
				Reference: pay.Reference, InvoiceDocType: pay.DocType, InvoiceSeries: pay.Series, InvoiceNumber: pay.Number,
				Status: "vigente", Notes: pay.Notes, ImportBatchID: bid, CreatedBy: uid}
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.EquipPaymentAllocation{PaymentID: p.ID, OrderID: ord.ID, Amount: pay.Amount}).Error; err != nil {
				return err
			}
			paymentsCount++
		}

		sh := database.EquipShipment{OrderID: ord.ID, CarrierID: &carrier.ID, GuideNumber: o.Shipment.Guide,
			DestinationAgency: o.Shipment.Agency, DestinationDepartment: o.Shipment.Department,
			DestinationProvince: o.Shipment.Province, DestinationDistrict: o.Shipment.District, DeliveryMode: o.Shipment.Mode,
			ScheduledDispatchDate: o.Shipment.ScheduledDispatch, DispatchedAt: o.Shipment.DispatchedAt,
			ArrivedAt: o.Shipment.ArrivedAt, Status: o.Shipment.Status, IsCurrent: true}
		if sh.ArrivedAt != nil {
			dl := sh.ArrivedAt.AddDate(0, 0, carrier.PickupDays)
			sh.PickupDeadline = &dl
		}
		if sh.Status == "entregado" {
			sh.PickedUpAt = sh.ArrivedAt
		}
		if err := tx.Create(&sh).Error; err != nil {
			return err
		}
		if g := strings.TrimSpace(o.Shipment.Guide); g != "" {
			shipmentByGuide[g], orderByGuide[g] = sh.ID, ord.ID
		}
	}

	// Retornos.
	returnID := map[int]uint{}
	for _, r := range plan.Returns {
		type snap struct {
			ProductID uint   `json:"product_id"`
			Code      string `json:"code"`
			Quantity  int    `json:"quantity"`
		}
		var snaps []snap
		for _, it := range r.Items {
			pid, err := pidOf(it.Code)
			if err != nil {
				return err
			}
			snaps = append(snaps, snap{ProductID: pid, Code: it.Code, Quantity: int(it.Quantity)})
		}
		b, _ := json.Marshal(snaps)
		row := database.EquipReturn{ReturnNumber: r.Number, CustomerName: r.CustomerName, GuideNumber: r.Guide,
			RequestedAt: r.RequestedAt, ReceivedAt: r.ReceivedAt, ItemsJSON: string(b), ItemsText: r.ItemsText,
			UnpaidBalance: round2(r.Unpaid), ReturnCost: round2(r.Cost), Status: r.Status, Condition: r.Condition,
			ActionTaken: "retorno", Notes: r.Notes, ImportBatchID: bid}
		if id, ok := shipmentByGuide[strings.TrimSpace(r.Guide)]; ok && r.Guide != "" {
			sid, oid := id, orderByGuide[strings.TrimSpace(r.Guide)]
			row.ShipmentID, row.OrderID = &sid, &oid
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		returnID[r.Number] = row.ID
	}

	// Kardex.
	moves := make([]database.EquipStockMovement, 0, len(plan.Movements))
	for _, m := range plan.Movements {
		pid, err := pidOf(m.ProductCode)
		if err != nil {
			return err
		}
		mv := database.EquipStockMovement{ProductID: pid, OccurredAt: m.At, MovementType: m.Type, Quantity: m.Quantity,
			SaleTypeSnapshot: m.SaleType, Note: m.Note, ImportBatchID: bid, CreatedBy: uid}
		if m.OrderNumber > 0 {
			id := orderID[m.OrderNumber]
			mv.OrderID = &id
		}
		if m.ReturnNo > 0 {
			id := returnID[m.ReturnNo]
			mv.ReturnID = &id
		}
		if m.ComboCode != "" {
			id := comboID[key(m.ComboCode)]
			mv.ViaComboID = &id
		}
		moves = append(moves, mv)
	}
	if len(moves) > 0 {
		if err := tx.CreateInBatches(&moves, 200).Error; err != nil {
			return err
		}
	}

	// Configuración: siguiente pedido, responsable y transportista por defecto.
	st := database.EquipSettings{}
	if found, err := findOne(tx, &st, "id = ?", 1); err != nil {
		return err
	} else if !found {
		st = database.EquipSettings{ID: 1, AlertYellowDays: 5, AlertRedDays: 12, NextOrderNumber: 1}
		if err := tx.Create(&st).Error; err != nil {
			return err
		}
	}
	if maxOrder+1 > st.NextOrderNumber {
		st.NextOrderNumber = maxOrder + 1
	}
	if st.StockManager == "" {
		st.StockManager = plan.StockManager
	}
	if st.DefaultCarrierID == nil {
		st.DefaultCarrierID = &carrier.ID
	}
	if err := tx.Save(&st).Error; err != nil {
		return err
	}

	batch.Products, batch.Combos, batch.Orders = len(plan.Products), len(plan.Combos), len(plan.Orders)
	batch.Payments, batch.Returns, batch.Movements = paymentsCount, len(plan.Returns), len(moves)
	if err := tx.Save(batch).Error; err != nil {
		return err
	}

	// Verificación final contra la base: el Stock Maestro calculado desde el kardex guardado debe coincidir con el libro.
	return New(tx).verifyAgainstExpected(plan)
}

// verifyAgainstExpected compara el StockReport del período (leído de la BD) con el Stock Maestro del libro.
func (s *Service) verifyAgainstExpected(plan *importPlan) error {
	report, err := s.StockReport(plan.Period)
	if err != nil {
		return err
	}
	byCode := map[string]StockRow{}
	for _, r := range report {
		byCode[key(r.Code)] = r
	}
	var diffs []string
	for _, e := range plan.Expected {
		r, ok := byCode[key(e.Code)]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("%s: no existe en el stock", e.Code))
			continue
		}
		if r.Opening != e.Opening || r.DirectIndep != e.DirectI || r.DirectPromo != e.DirectP ||
			r.ComboIndep != e.ComboI || r.ComboPromo != e.ComboP || r.Reentries != e.Reentries || r.Current != e.Current {
			diffs = append(diffs, fmt.Sprintf("%s: libro %d vs sistema %d", e.Code, e.Current, r.Current))
		}
	}
	if len(diffs) > 0 {
		if len(diffs) > 8 {
			diffs = append(diffs[:8], "…")
		}
		return invalid("El stock guardado no coincide con el Stock Maestro del libro; no se importó nada (%s)", strings.Join(diffs, "; "))
	}
	return nil
}

// ImportBatches lotes ya importados, del más reciente al más antiguo.
func (s *Service) ImportBatches() ([]database.EquipImportBatch, error) {
	var rows []database.EquipImportBatch
	err := s.db.Order("period DESC").Find(&rows).Error
	return rows, err
}
