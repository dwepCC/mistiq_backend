package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

func setupRestaurantAdvancedEnv(t *testing.T) *advEnv {
	t.Helper()
	e := setupAdvancedImportEnv(t)
	if err := e.db.AutoMigrate(
		&database.TenantModifierGroup{}, &database.TenantModifierOption{}, &database.TenantProductModifierGroup{},
		&database.TenantComboGroup{}, &database.TenantComboGroupItem{},
	); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *advEnv) runRest(t *testing.T, items ...BulkImportAdvancedItem) *BulkImportAdvancedResult {
	t.Helper()
	res, err := e.svc.BulkImportAdvancedWith(items, e.main.ID, e.userID, BulkImportAdvancedOpts{Restaurant: true})
	if err != nil {
		t.Fatalf("BulkImportAdvancedWith: %v", err)
	}
	return res
}

func (e *advEnv) restProduct(t *testing.T, code string) database.TenantProduct {
	t.Helper()
	var p database.TenantProduct
	if err := e.db.Where("code = ? AND branch_id = ?", code, e.main.ID).First(&p).Error; err != nil {
		t.Fatalf("plato %s: %v", code, err)
	}
	return p
}

func dish(row int, code, name string, price float64) BulkImportItem {
	return BulkImportItem{RowNumber: row, Name: name, Code: code, SalePrice: price, PreparationArea: "cocina"}
}

func extraGroup(row int, name string, required, multi bool, opts ...BulkImportAdvancedModifierOption) BulkImportAdvancedModifierGroup {
	return BulkImportAdvancedModifierGroup{RowNumber: row, DefRowNumber: row, Name: name, Define: true, Required: required, MultiSelect: multi, Options: opts}
}

func comboItem(row int, code string, extra float64, def bool) BulkImportAdvancedComboItem {
	return BulkImportAdvancedComboItem{RowNumber: row, ProductCode: code, ExtraPrice: extra, IsDefault: def}
}

func comboGroup(row int, name, typ string, items ...BulkImportAdvancedComboItem) BulkImportAdvancedComboGroup {
	return BulkImportAdvancedComboGroup{RowNumber: row, Name: name, SelectionType: typ, Items: items}
}

func TestRestaurantAdvanced_ExtrasCreateLinkAndReplace(t *testing.T) {
	e := setupRestaurantAdvancedEnv(t)
	sinAji := BulkImportAdvancedModifierOption{RowNumber: 2, Name: "Sin ají", ExtraPrice: 0}
	queso := BulkImportAdvancedModifierOption{RowNumber: 3, Name: "Extra queso", ExtraPrice: 3}
	res := e.runRest(t,
		BulkImportAdvancedItem{
			BulkImportItem: dish(2, "LOMO", "Lomo saltado", 28),
			Presentations:  []BulkImportAdvancedPresentation{{RowNumber: 2, Name: "Personal", SalePrice: 28}, {RowNumber: 3, Name: "Familiar", SalePrice: 50}},
			ModifierGroups: []BulkImportAdvancedModifierGroup{extraGroup(2, "Personaliza", false, true, sinAji, queso)},
		},
		BulkImportAdvancedItem{
			BulkImportItem: dish(3, "CEV", "Ceviche", 30),
			ModifierGroups: []BulkImportAdvancedModifierGroup{extraGroup(2, "Personaliza", false, true, sinAji, queso)},
		},
	)
	if len(res.Failed) != 0 || res.Created != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	// El grupo es global: se crea una sola vez aunque lo usen dos platos.
	if res.ModifierGroupsCreated != 1 || res.ModifierOptionsCreated != 2 || res.ModifierLinksCreated != 2 || res.PresentationsCreated != 2 {
		t.Fatalf("contadores: %+v", res)
	}
	lomo := e.restProduct(t, "LOMO")
	if !lomo.IsRestaurant || lomo.BranchID != e.main.ID || !lomo.HasModifiers || !lomo.HasVariants {
		t.Fatalf("plato: %+v", lomo)
	}
	var groups []database.TenantModifierGroup
	e.db.Find(&groups)
	if len(groups) != 1 || groups[0].Kind != "extra" || groups[0].Required || !groups[0].MultiSelect {
		t.Fatalf("grupos: %+v", groups)
	}

	// Reemplazo: "Extra queso" cambia de precio, "Sin ají" se desactiva y aparece "Extra huevo".
	res = e.runRest(t, BulkImportAdvancedItem{
		BulkImportItem: dish(2, "LOMO", "Lomo saltado", 28),
		ModifierGroups: []BulkImportAdvancedModifierGroup{extraGroup(2, "personaliza", true, false,
			BulkImportAdvancedModifierOption{RowNumber: 3, Name: "Extra queso", ExtraPrice: 4},
			BulkImportAdvancedModifierOption{RowNumber: 4, Name: "Extra huevo", ExtraPrice: 2})},
	})
	if len(res.Failed) != 0 || res.ModifierGroupsUpdated != 1 || res.ModifierOptionsUpdated != 1 || res.ModifierOptionsCreated != 1 || res.ModifierOptionsDeactivated != 1 {
		t.Fatalf("reemplazo: %+v", res)
	}
	var opts []database.TenantModifierOption
	e.db.Order("id").Find(&opts)
	if len(opts) != 3 {
		t.Fatalf("no debe borrar opciones: %d", len(opts))
	}
	for _, o := range opts {
		switch o.Name {
		case "Sin ají":
			if o.Active {
				t.Error("Sin ají debía desactivarse")
			}
		case "Extra queso":
			if !o.Active || o.ExtraPrice != 4 {
				t.Errorf("queso: %+v", o)
			}
		}
	}
	// CEV no aparecía en esa corrida: no se toca.
	var cev database.TenantProduct
	e.db.Where("code = ?", "CEV").First(&cev)
	var links int64
	e.db.Model(&database.TenantProductModifierGroup{}).Where("product_id = ?", cev.ID).Count(&links)
	if links != 1 || !cev.HasModifiers {
		t.Errorf("CEV no debía tocarse: links=%d has_modifiers=%v", links, cev.HasModifiers)
	}

	// Cambiar la lista de LOMO: el grupo anterior se desvincula y el nuevo se vincula.
	res = e.runRest(t, BulkImportAdvancedItem{
		BulkImportItem: dish(2, "LOMO", "Lomo saltado", 28),
		ModifierGroups: []BulkImportAdvancedModifierGroup{extraGroup(2, "Salsas", false, true, BulkImportAdvancedModifierOption{RowNumber: 2, Name: "Ají"})},
	})
	if len(res.Failed) != 0 || res.ModifierLinksCreated != 1 || res.ModifierLinksRemoved != 1 {
		t.Fatalf("vínculos: %+v", res)
	}
}

func TestRestaurantAdvanced_ExtrasGroupMustExistWhenNotDefined(t *testing.T) {
	e := setupRestaurantAdvancedEnv(t)
	res := e.runRest(t, BulkImportAdvancedItem{
		BulkImportItem: dish(2, "X", "Plato", 10),
		ModifierGroups: []BulkImportAdvancedModifierGroup{{RowNumber: 5, Name: "Fantasma"}},
	})
	if res.Created != 0 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "hoja Extras, fila 5") {
		t.Fatalf("resultado: %+v", res)
	}
	var n int64
	e.db.Model(&database.TenantProduct{}).Count(&n)
	if n != 0 {
		t.Error("rollback del plato")
	}
}

func TestRestaurantAdvanced_ComboCreateReplaceAndRules(t *testing.T) {
	e := setupRestaurantAdvancedEnv(t)
	combo := BulkImportAdvancedItem{
		BulkImportItem: dish(2, "MENU", "Menú del día", 25),
		ComboGroups: []BulkImportAdvancedComboGroup{
			comboGroup(2, "Entrada", "fixed", comboItem(2, "SOPA", 0, false)),
			comboGroup(3, "Fondo", "single", comboItem(3, "LOMO", 0, true), comboItem(4, "CEV", 5, false)),
		},
	}
	// El combo viene PRIMERO en el archivo: el motor lo procesa después de sus componentes.
	res := e.runRest(t, combo,
		BulkImportAdvancedItem{BulkImportItem: dish(3, "SOPA", "Sopa", 8)},
		BulkImportAdvancedItem{BulkImportItem: dish(4, "LOMO", "Lomo", 28)},
		BulkImportAdvancedItem{BulkImportItem: dish(5, "CEV", "Ceviche", 30)},
	)
	if len(res.Failed) != 0 || res.Created != 4 || res.ComboGroupsCreated != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	menu := e.restProduct(t, "MENU")
	if !menu.HasCombo {
		t.Fatal("has_combo")
	}

	// Reemplazo: queda solo "Fondo" → "Entrada" se da de baja.
	res = e.runRest(t, BulkImportAdvancedItem{
		BulkImportItem: dish(2, "MENU", "Menú del día", 25),
		ComboGroups:    []BulkImportAdvancedComboGroup{comboGroup(2, "fondo", "single", comboItem(2, "LOMO", 0, true))},
	})
	if len(res.Failed) != 0 || res.ComboGroupsUpdated != 1 || res.ComboGroupsDeactivated != 1 || res.ComboGroupsCreated != 0 {
		t.Fatalf("reemplazo: %+v", res)
	}
	var active int64
	e.db.Model(&database.TenantComboGroup{}).Where("product_id = ?", menu.ID).Count(&active)
	if active != 1 {
		t.Errorf("grupos activos: %d", active)
	}

	// Reglas: componente inexistente, combo dentro de combo, auto-referencia, combo con extras.
	res = e.runRest(t,
		BulkImportAdvancedItem{BulkImportItem: dish(2, "C1", "Combo 1", 10), ComboGroups: []BulkImportAdvancedComboGroup{comboGroup(7, "G", "fixed", comboItem(7, "NOEXISTE", 0, false))}},
		BulkImportAdvancedItem{BulkImportItem: dish(3, "C2", "Combo 2", 10), ComboGroups: []BulkImportAdvancedComboGroup{comboGroup(8, "G", "fixed", comboItem(8, "C3", 0, false))}},
		BulkImportAdvancedItem{BulkImportItem: dish(4, "C3", "Combo 3", 10), ComboGroups: []BulkImportAdvancedComboGroup{comboGroup(9, "G", "fixed", comboItem(9, "SOPA", 0, false))}},
		BulkImportAdvancedItem{BulkImportItem: dish(5, "C4", "Combo 4", 10), ComboGroups: []BulkImportAdvancedComboGroup{comboGroup(10, "G", "fixed", comboItem(10, "C4", 0, false))}},
		BulkImportAdvancedItem{
			BulkImportItem: dish(6, "C5", "Combo 5", 10),
			ModifierGroups: []BulkImportAdvancedModifierGroup{extraGroup(11, "E", false, false, BulkImportAdvancedModifierOption{RowNumber: 11, Name: "o"})},
			ComboGroups:    []BulkImportAdvancedComboGroup{comboGroup(12, "G", "fixed", comboItem(12, "SOPA", 0, false))},
		},
	)
	if res.Created != 1 || len(res.Failed) != 4 { // solo C3 es válido
		t.Fatalf("resultado: %+v", res)
	}
	want := map[int]string{2: "hoja Combos, fila 7", 3: "otro combo", 5: "contenerse a sí mismo", 6: "no tiene extras"}
	for _, f := range res.Failed {
		if !strings.Contains(f.Error, want[f.Row]) {
			t.Errorf("fila %d: %q no contiene %q", f.Row, f.Error, want[f.Row])
		}
	}
}

func TestRestaurantAdvanced_RejectsTukifacSheets(t *testing.T) {
	e := setupRestaurantAdvancedEnv(t)
	res := e.runRest(t,
		BulkImportAdvancedItem{BulkImportItem: dish(2, "A", "A", 1), SaleUnits: []BulkImportAdvancedSaleUnit{{RowNumber: 2, Name: "U", Unit: "NIU", ConversionFactor: 1, Price1: 1}}},
		BulkImportAdvancedItem{BulkImportItem: dish(3, "B", "B", 1), BranchPrices: []BulkImportAdvancedBranchPrice{{RowNumber: 2, BranchName: "Principal", SalePrice: 1}}},
		BulkImportAdvancedItem{BulkImportItem: dish(4, "C", "C", 1)},
	)
	if res.Created != 1 || len(res.Failed) != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	// Y en Tukifac los extras/combos se rechazan.
	r2 := e.run(t, BulkImportAdvancedItem{BulkImportItem: advProduct(2, "T1", "T", 1), ComboGroups: []BulkImportAdvancedComboGroup{{RowNumber: 3, Name: "G"}}})
	if len(r2.Failed) != 1 || !strings.Contains(r2.Failed[0].Error, "solo existen en la importación de Tukichef") {
		t.Fatalf("tukifac: %+v", r2)
	}
}
