package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupComboTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{},
		&database.TenantCategory{},
		&database.TenantPreparationArea{},
		&database.TenantComboGroup{},
		&database.TenantComboGroupItem{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

// comboSeedProduct crea un componente de catálogo restaurante.
func comboSeedProduct(t *testing.T, db *gorm.DB, name string, price float64, branchID uint, areaID *uint) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code:              strings.ToUpper(strings.ReplaceAll(name, " ", "-")),
		Name:              name,
		Type:              "product",
		Unit:              "NIU",
		SalePrice:         price,
		IsRestaurant:      true,
		BranchID:          branchID,
		PreparationAreaID: areaID,
		Active:            true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func uptr(v uint) *uint { return &v }

// TestComboFamiliar_CreateWithFixedGroups cubre el caso real: pollo (20) + agua (2.50)
// vendidos juntos como "Combo Familiar" a precio fijo 18.
func TestComboFamiliar_CreateWithFixedGroups(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	cocina, bar := uptr(1), uptr(2)
	pollo := comboSeedProduct(t, db, "Pollo a la brasa", 20, 1, cocina)
	agua := comboSeedProduct(t, db, "Agua mineral", 2.50, 1, bar)

	groups := []ComboGroupInput{
		{Name: "Plato principal", SelectionType: database.ComboSelectionFixed,
			Items: []ComboGroupItemInput{{ProductID: pollo.ID, DefaultQuantity: 1}}},
		{Name: "Bebida", SelectionType: database.ComboSelectionFixed,
			Items: []ComboGroupItemInput{{ProductID: agua.ID, DefaultQuantity: 1}}},
	}
	combo, _, err := svc.Create(ProductInput{
		Code:               "COMBO-FAM",
		Name:               "Combo Familiar",
		Type:               "product",
		Unit:               "NIU",
		SalePrice:          18,
		IgvAffectationType: "10",
		IsRestaurant:       true,
		BranchID:           1,
		Active:             true,
		ComboGroups:        &groups,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !combo.HasCombo {
		t.Error("esperaba has_combo = true")
	}

	// El flag debe estar persistido, no solo en memoria.
	var stored database.TenantProduct
	if err := db.First(&stored, combo.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.HasCombo {
		t.Error("esperaba has_combo persistido en la fila")
	}
	if stored.Type != "product" {
		t.Errorf("un combo se factura como producto normal: type = %q, want product", stored.Type)
	}

	views, err := svc.ListComboGroups(combo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("esperaba 2 grupos, got %d", len(views))
	}
	for _, g := range views {
		if g.MinSelect != 1 || g.MaxSelect != 1 {
			t.Errorf("grupo fijo '%s': esperaba min=max=1, got min=%d max=%d", g.Name, g.MinSelect, g.MaxSelect)
		}
		if len(g.Items) != 1 {
			t.Fatalf("grupo '%s': esperaba 1 item, got %d", g.Name, len(g.Items))
		}
	}

	// El área de cada componente debe quedar snapshotada: es lo que rutea la comanda.
	if got := views[0].Items[0].PreparationAreaID; got == nil || *got != 1 {
		t.Errorf("pollo: esperaba preparation_area_id = 1 (cocina), got %v", got)
	}
	if got := views[1].Items[0].PreparationAreaID; got == nil || *got != 2 {
		t.Errorf("agua: esperaba preparation_area_id = 2 (bar), got %v", got)
	}

	// El ahorro que se muestra en el panel: 20 + 2.50 = 22.50 frente a los 18 del combo.
	total, err := svc.ComboComponentsTotal(combo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 22.50 {
		t.Errorf("suma de componentes = %.2f, want 22.50", total)
	}
}

func TestComboGroups_SingleAndMultipleSelection(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	pollo := comboSeedProduct(t, db, "Pollo", 20, 1, uptr(1))
	agua := comboSeedProduct(t, db, "Agua", 2.50, 1, uptr(2))
	gaseosa := comboSeedProduct(t, db, "Gaseosa", 4, 1, uptr(2))
	papas := comboSeedProduct(t, db, "Papas", 6, 1, uptr(1))
	ensalada := comboSeedProduct(t, db, "Ensalada", 5, 1, uptr(1))

	groups := []ComboGroupInput{
		{Name: "Plato", SelectionType: database.ComboSelectionFixed,
			Items: []ComboGroupItemInput{{ProductID: pollo.ID}}},
		{Name: "Tu bebida", SelectionType: database.ComboSelectionSingle,
			Items: []ComboGroupItemInput{
				{ProductID: agua.ID, IsDefault: true},
				{ProductID: gaseosa.ID, ExtraPrice: 1.50},
			}},
		{Name: "Tus guarniciones", SelectionType: database.ComboSelectionMultiple,
			MinSelect: 1, MaxSelect: 2, AllowQuantity: true,
			Items: []ComboGroupItemInput{
				{ProductID: papas.ID, DefaultQuantity: 1, MaxQuantity: 3},
				{ProductID: ensalada.ID, DefaultQuantity: 1, MaxQuantity: 2},
			}},
	}
	combo, _, err := svc.Create(ProductInput{
		Code: "COMBO-ARMA", Name: "Arma tu combo", Type: "product", Unit: "NIU",
		SalePrice: 25, IgvAffectationType: "10", IsRestaurant: true, BranchID: 1,
		Active: true, ComboGroups: &groups,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	views, err := svc.ListComboGroups(combo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 3 {
		t.Fatalf("esperaba 3 grupos, got %d", len(views))
	}

	single := views[1]
	if single.MinSelect != 1 || single.MaxSelect != 1 {
		t.Errorf("selección única: esperaba min=max=1, got min=%d max=%d", single.MinSelect, single.MaxSelect)
	}
	if single.AllowQuantity {
		t.Error("selección única no debe permitir cantidad")
	}
	if single.Items[1].ExtraPrice != 1.50 {
		t.Errorf("gaseosa: esperaba sobreprecio 1.50, got %.2f", single.Items[1].ExtraPrice)
	}

	multi := views[2]
	if multi.MinSelect != 1 || multi.MaxSelect != 2 {
		t.Errorf("selección múltiple: esperaba min=1 max=2, got min=%d max=%d", multi.MinSelect, multi.MaxSelect)
	}
	if !multi.AllowQuantity {
		t.Error("esperaba allow_quantity = true")
	}
	if multi.Items[0].MaxQuantity != 3 {
		t.Errorf("papas: esperaba max_quantity 3, got %.0f", multi.Items[0].MaxQuantity)
	}
}

// TestComboGroups_MultipleDefaultsMaxToItemCount: sin max explícito, el tope es el nº de items.
func TestComboGroups_MultipleDefaultsMaxToItemCount(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	a := comboSeedProduct(t, db, "Papas", 6, 1, uptr(1))
	b := comboSeedProduct(t, db, "Ensalada", 5, 1, uptr(1))

	groups := []ComboGroupInput{{
		Name: "Guarniciones", SelectionType: database.ComboSelectionMultiple,
		Items: []ComboGroupItemInput{{ProductID: a.ID}, {ProductID: b.ID}},
	}}
	combo, _, err := svc.Create(ProductInput{
		Code: "C1", Name: "Combo", Type: "product", Unit: "NIU", SalePrice: 10,
		IgvAffectationType: "10", IsRestaurant: true, BranchID: 1, Active: true,
		ComboGroups: &groups,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	views, _ := svc.ListComboGroups(combo.ID)
	if views[0].MaxSelect != 2 {
		t.Errorf("esperaba max_select = 2 (nº de items), got %d", views[0].MaxSelect)
	}
}

func TestComboGroups_Validations(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	pollo := comboSeedProduct(t, db, "Pollo", 20, 1, uptr(1))
	agua := comboSeedProduct(t, db, "Agua", 2.50, 1, uptr(2))
	otraSucursal := comboSeedProduct(t, db, "Pollo sucursal 2", 20, 2, uptr(1))

	inactivo := comboSeedProduct(t, db, "Descontinuado", 5, 1, uptr(1))
	db.Model(&database.TenantProduct{}).Where("id = ?", inactivo.ID).Update("active", false)

	// Un combo ya existente, para probar el anidamiento.
	nested := []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
		Items: []ComboGroupItemInput{{ProductID: pollo.ID}}}}
	otroCombo, _, err := svc.Create(ProductInput{
		Code: "COMBO-X", Name: "Otro combo", Type: "product", Unit: "NIU", SalePrice: 15,
		IgvAffectationType: "10", IsRestaurant: true, BranchID: 1, Active: true,
		ComboGroups: &nested,
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		groups  []ComboGroupInput
		wantErr string
	}{
		{
			name: "grupo sin nombre",
			groups: []ComboGroupInput{{Name: "  ", SelectionType: database.ComboSelectionFixed,
				Items: []ComboGroupItemInput{{ProductID: pollo.ID}}}},
			wantErr: "nombre",
		},
		{
			name:    "grupo sin productos",
			groups:  []ComboGroupInput{{Name: "Vacío", SelectionType: database.ComboSelectionFixed}},
			wantErr: "al menos un producto",
		},
		{
			name: "grupo fijo con dos productos",
			groups: []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
				Items: []ComboGroupItemInput{{ProductID: pollo.ID}, {ProductID: agua.ID}}}},
			wantErr: "exactamente un producto",
		},
		{
			name: "tipo de selección inválido",
			groups: []ComboGroupInput{{Name: "Plato", SelectionType: "aleatorio",
				Items: []ComboGroupItemInput{{ProductID: pollo.ID}}}},
			wantErr: "inválido",
		},
		{
			name: "producto inexistente",
			groups: []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
				Items: []ComboGroupItemInput{{ProductID: 99999}}}},
			wantErr: "no existe",
		},
		{
			name: "producto repetido en el grupo",
			groups: []ComboGroupInput{{Name: "Bebidas", SelectionType: database.ComboSelectionMultiple,
				Items: []ComboGroupItemInput{{ProductID: agua.ID}, {ProductID: agua.ID}}}},
			wantErr: "repetido",
		},
		{
			name: "componente de otra sucursal",
			groups: []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
				Items: []ComboGroupItemInput{{ProductID: otraSucursal.ID}}}},
			wantErr: "otra sucursal",
		},
		{
			name: "componente inactivo",
			groups: []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
				Items: []ComboGroupItemInput{{ProductID: inactivo.ID}}}},
			wantErr: "inactivo",
		},
		{
			name: "combo dentro de combo",
			groups: []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
				Items: []ComboGroupItemInput{{ProductID: otroCombo.ID}}}},
			wantErr: "no puede contener otro combo",
		},
		{
			name: "min mayor que max",
			groups: []ComboGroupInput{{Name: "Guarnición", SelectionType: database.ComboSelectionMultiple,
				MinSelect: 2, MaxSelect: 1,
				Items: []ComboGroupItemInput{{ProductID: pollo.ID}, {ProductID: agua.ID}}}},
			wantErr: "no puede superar el máximo",
		},
		{
			name: "max mayor que el nº de productos",
			groups: []ComboGroupInput{{Name: "Guarnición", SelectionType: database.ComboSelectionMultiple,
				MinSelect: 1, MaxSelect: 5,
				Items: []ComboGroupItemInput{{ProductID: pollo.ID}, {ProductID: agua.ID}}}},
			wantErr: "supera los 2 productos",
		},
		{
			name: "sobreprecio negativo",
			groups: []ComboGroupInput{{Name: "Bebida", SelectionType: database.ComboSelectionSingle,
				Items: []ComboGroupItemInput{{ProductID: agua.ID, ExtraPrice: -1}}}},
			wantErr: "no puede ser negativo",
		},
		{
			name: "cantidad máxima menor que la de por defecto",
			groups: []ComboGroupInput{{Name: "Bebida", SelectionType: database.ComboSelectionSingle,
				Items: []ComboGroupItemInput{{ProductID: agua.ID, DefaultQuantity: 3, MaxQuantity: 2}}}},
			wantErr: "menor que la cantidad por defecto",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			groups := tc.groups
			_, _, err := svc.Create(ProductInput{
				Code: fmt.Sprintf("COMBO-INV-%d", i), Name: "Combo inválido", Type: "product",
				Unit: "NIU", SalePrice: 18, IgvAffectationType: "10",
				IsRestaurant: true, BranchID: 1, Active: true, ComboGroups: &groups,
			})
			if err == nil {
				t.Fatalf("esperaba error con %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, esperaba que contuviera %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestComboGroups_SelfReferenceRejected(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	pollo := comboSeedProduct(t, db, "Pollo", 20, 1, uptr(1))
	groups := []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
		Items: []ComboGroupItemInput{{ProductID: pollo.ID}}}}
	combo, _, err := svc.Create(ProductInput{
		Code: "COMBO-SELF", Name: "Combo", Type: "product", Unit: "NIU", SalePrice: 18,
		IgvAffectationType: "10", IsRestaurant: true, BranchID: 1, Active: true,
		ComboGroups: &groups,
	})
	if err != nil {
		t.Fatal(err)
	}

	self := []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
		Items: []ComboGroupItemInput{{ProductID: combo.ID}}}}
	_, err = svc.Update(combo.ID, ProductInput{
		Code: "COMBO-SELF", Name: "Combo", Type: "product", Unit: "NIU", SalePrice: 18,
		IgvAffectationType: "10", IsRestaurant: true, ComboGroups: &self,
	})
	if err == nil || !strings.Contains(err.Error(), "sí mismo") {
		t.Fatalf("esperaba rechazo por autorreferencia, got %v", err)
	}
}

// TestComboGroups_UpdateReplacesAndClears: nil = no tocar; [] = deja de ser combo.
func TestComboGroups_UpdateReplacesAndClears(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	pollo := comboSeedProduct(t, db, "Pollo", 20, 1, uptr(1))
	agua := comboSeedProduct(t, db, "Agua", 2.50, 1, uptr(2))
	gaseosa := comboSeedProduct(t, db, "Gaseosa", 4, 1, uptr(2))

	groups := []ComboGroupInput{
		{Name: "Plato", SelectionType: database.ComboSelectionFixed,
			Items: []ComboGroupItemInput{{ProductID: pollo.ID}}},
		{Name: "Bebida", SelectionType: database.ComboSelectionFixed,
			Items: []ComboGroupItemInput{{ProductID: agua.ID}}},
	}
	combo, _, err := svc.Create(ProductInput{
		Code: "COMBO-UPD", Name: "Combo", Type: "product", Unit: "NIU", SalePrice: 18,
		IgvAffectationType: "10", IsRestaurant: true, BranchID: 1, Active: true,
		ComboGroups: &groups,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := ProductInput{
		Code: "COMBO-UPD", Name: "Combo", Type: "product", Unit: "NIU", SalePrice: 18,
		IgvAffectationType: "10", IsRestaurant: true,
	}

	// nil: el combo no se toca.
	if _, err := svc.Update(combo.ID, base); err != nil {
		t.Fatal(err)
	}
	if views, _ := svc.ListComboGroups(combo.ID); len(views) != 2 {
		t.Fatalf("con ComboGroups nil esperaba 2 grupos intactos, got %d", len(views))
	}

	// Reemplazo: la bebida pasa a ser elegible entre agua y gaseosa.
	replaced := ProductInput{}
	replaced = base
	newGroups := []ComboGroupInput{
		{Name: "Plato", SelectionType: database.ComboSelectionFixed,
			Items: []ComboGroupItemInput{{ProductID: pollo.ID}}},
		{Name: "Tu bebida", SelectionType: database.ComboSelectionSingle,
			Items: []ComboGroupItemInput{{ProductID: agua.ID, IsDefault: true}, {ProductID: gaseosa.ID}}},
	}
	replaced.ComboGroups = &newGroups
	if _, err := svc.Update(combo.ID, replaced); err != nil {
		t.Fatal(err)
	}
	views, _ := svc.ListComboGroups(combo.ID)
	if len(views) != 2 {
		t.Fatalf("esperaba 2 grupos tras reemplazo, got %d", len(views))
	}
	if views[1].SelectionType != database.ComboSelectionSingle || len(views[1].Items) != 2 {
		t.Errorf("esperaba grupo single con 2 items, got %s con %d", views[1].SelectionType, len(views[1].Items))
	}

	// Lista vacía: deja de ser combo.
	cleared := base
	empty := []ComboGroupInput{}
	cleared.ComboGroups = &empty
	if _, err := svc.Update(combo.ID, cleared); err != nil {
		t.Fatal(err)
	}
	if views, _ := svc.ListComboGroups(combo.ID); len(views) != 0 {
		t.Errorf("esperaba 0 grupos, got %d", len(views))
	}
	var stored database.TenantProduct
	db.First(&stored, combo.ID)
	if stored.HasCombo {
		t.Error("esperaba has_combo = false tras vaciar los grupos")
	}
}

// TestProductUpdate_ImageURLPreservedWhenOmitted: omitir image_url significa «no tocar la
// imagen». Antes la vaciaba, así que editar un combo sin volver a subir la foto la borraba.
func TestProductUpdate_ImageURLPreservedWhenOmitted(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	p, _, err := svc.Create(ProductInput{
		Code: "IMG-1", Name: "Combo con foto", Type: "product", Unit: "NIU",
		SalePrice: 18, IgvAffectationType: "10", IsRestaurant: true, BranchID: 1,
		Active: true, ImageURL: "/uploads/combo.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}

	base := ProductInput{
		Code: "IMG-1", Name: "Combo con foto (editado)", Type: "product", Unit: "NIU",
		SalePrice: 20, IgvAffectationType: "10", IsRestaurant: true,
	}

	// Sin ImageURLSet: la imagen se conserva.
	if _, err := svc.Update(p.ID, base); err != nil {
		t.Fatal(err)
	}
	var stored database.TenantProduct
	db.First(&stored, p.ID)
	if stored.ImageURL != "/uploads/combo.jpg" {
		t.Errorf("esperaba conservar la imagen, got %q", stored.ImageURL)
	}
	if stored.Name != "Combo con foto (editado)" {
		t.Errorf("el resto del producto sí debe actualizarse, got %q", stored.Name)
	}

	// Con ImageURLSet y valor nuevo: se reemplaza.
	replaced := base
	replaced.ImageURL = "/uploads/otra.jpg"
	replaced.ImageURLSet = true
	if _, err := svc.Update(p.ID, replaced); err != nil {
		t.Fatal(err)
	}
	db.First(&stored, p.ID)
	if stored.ImageURL != "/uploads/otra.jpg" {
		t.Errorf("esperaba reemplazar la imagen, got %q", stored.ImageURL)
	}

	// Con ImageURLSet y valor vacío: se quita (así se borra a propósito).
	cleared := base
	cleared.ImageURL = ""
	cleared.ImageURLSet = true
	if _, err := svc.Update(p.ID, cleared); err != nil {
		t.Fatal(err)
	}
	db.First(&stored, p.ID)
	if stored.ImageURL != "" {
		t.Errorf("esperaba quitar la imagen al enviarla vacía, got %q", stored.ImageURL)
	}
}

func TestComboListFilters(t *testing.T) {
	db := setupComboTestDB(t)
	svc := NewProductService(db)

	pollo := comboSeedProduct(t, db, "Pollo", 20, 1, uptr(1))
	comboSeedProduct(t, db, "Agua", 2.50, 1, uptr(2))

	groups := []ComboGroupInput{{Name: "Plato", SelectionType: database.ComboSelectionFixed,
		Items: []ComboGroupItemInput{{ProductID: pollo.ID}}}}
	if _, _, err := svc.Create(ProductInput{
		Code: "COMBO-F", Name: "Combo Familiar", Type: "product", Unit: "NIU", SalePrice: 18,
		IgvAffectationType: "10", IsRestaurant: true, BranchID: 1, Active: true,
		ComboGroups: &groups,
	}); err != nil {
		t.Fatal(err)
	}

	combos, _, err := svc.List(ProductListParams{CombosOnly: true, ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 1 || combos[0].Name != "Combo Familiar" {
		t.Fatalf("combos_only: esperaba solo el Combo Familiar, got %d filas", len(combos))
	}

	// Candidatos a componente: los productos normales, sin combos.
	rest, _, err := svc.List(ProductListParams{ExcludeCombos: true, ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 {
		t.Fatalf("exclude_combos: esperaba 2 productos, got %d", len(rest))
	}
	for _, p := range rest {
		if p.HasCombo {
			t.Errorf("exclude_combos devolvió el combo '%s'", p.Name)
		}
	}
}
