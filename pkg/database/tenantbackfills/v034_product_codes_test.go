package tenantbackfills

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Modelos mínimos para el test (evita depender del paquete database completo).
type tprod struct {
	ID        uint `gorm:"primaryKey"`
	Code      string
	Name      string
	DeletedAt *string
}

func (tprod) TableName() string { return "tenant_products" }

type tsitem struct {
	ID          uint `gorm:"primaryKey"`
	SaleID      uint
	ProductID   *uint
	Code        string
	Description string
}

func (tsitem) TableName() string { return "tenant_sale_items" }

func setupProductCodesDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&tprod{}, &tsitem{}); err != nil {
		t.Fatal(err)
	}
	return db
}

var codePattern = regexp.MustCompile(`^[A-Z0-9]{6}$`)

func TestV034_AsignaCodigoYLoPropagaAlSnapshot(t *testing.T) {
	db := setupProductCodesDB(t)
	borrado := "2026-07-18 11:16:52"
	db.Create(&tprod{ID: 1, Name: "Teclado", Code: ""})
	db.Create(&tprod{ID: 2, Name: "Mouse", Code: "MIO123"})
	// Producto borrado: sus ventas pasadas siguen necesitando código para poder emitirse.
	db.Create(&tprod{ID: 3, Name: "Combo julio", Code: "", DeletedAt: &borrado})

	p1, p2, p3 := uint(1), uint(2), uint(3)
	db.Create(&tsitem{ID: 10, SaleID: 1, ProductID: &p1, Code: "", Description: "Teclado"})
	db.Create(&tsitem{ID: 11, SaleID: 1, ProductID: &p2, Code: "", Description: "Mouse"})
	db.Create(&tsitem{ID: 12, SaleID: 2, ProductID: &p3, Code: "", Description: "Combo julio"})

	if err := (V034ProductCodes{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var prods []tprod
	db.Order("id").Find(&prods)
	for _, p := range prods {
		if !codePattern.MatchString(p.Code) {
			t.Fatalf("producto %d con código %q: no cumple el patrón alfanumérico", p.ID, p.Code)
		}
	}
	if prods[1].Code != "MIO123" {
		t.Fatalf("el código propio del negocio no debía cambiar, quedó %q", prods[1].Code)
	}

	var items []tsitem
	db.Order("id").Find(&items)
	for i, it := range items {
		if it.Code != prods[i].Code {
			t.Fatalf("línea %d: code=%q, se esperaba el del producto %q", it.ID, it.Code, prods[i].Code)
		}
	}
}

// Las líneas sin producto son las de los comprobantes que SUNAT rechazó: también deben quedar
// con código, cada una con el suyo.
func TestV034_LineasSinProductoTambienRecibenCodigo(t *testing.T) {
	db := setupProductCodesDB(t)
	fantasma := uint(999) // producto que ya no existe
	db.Create(&tsitem{ID: 20, SaleID: 3, ProductID: nil, Code: "", Description: "Servicio de instalación"})
	db.Create(&tsitem{ID: 21, SaleID: 3, ProductID: &fantasma, Code: "", Description: "Producto eliminado"})

	if err := (V034ProductCodes{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var items []tsitem
	db.Order("id").Find(&items)
	for _, it := range items {
		if !codePattern.MatchString(it.Code) {
			t.Fatalf("línea %d quedó con código %q", it.ID, it.Code)
		}
	}
	if items[0].Code == items[1].Code {
		t.Fatal("cada línea debía recibir su propio código")
	}
}

func TestV034_NoPisaCodigosExistentes(t *testing.T) {
	db := setupProductCodesDB(t)
	db.Create(&tprod{ID: 1, Name: "Teclado", Code: "ELMIO1"})
	p1 := uint(1)
	db.Create(&tsitem{ID: 10, SaleID: 1, ProductID: &p1, Code: "ELDELCLIENTE", Description: "Teclado"})

	if err := (V034ProductCodes{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p tprod
	db.First(&p, 1)
	if p.Code != "ELMIO1" {
		t.Fatalf("código del producto = %q, no debía cambiar", p.Code)
	}
	var it tsitem
	db.First(&it, 10)
	if it.Code != "ELDELCLIENTE" {
		t.Fatalf("código de la línea = %q, no debía cambiar", it.Code)
	}
}

// Correrlo dos veces no debe cambiar nada la segunda: el cron y el panel pueden repetirlo.
func TestV034_Idempotente(t *testing.T) {
	db := setupProductCodesDB(t)
	db.Create(&tprod{ID: 1, Name: "Teclado", Code: ""})
	p1 := uint(1)
	db.Create(&tsitem{ID: 10, SaleID: 1, ProductID: &p1, Code: "", Description: "Teclado"})
	db.Create(&tsitem{ID: 11, SaleID: 1, ProductID: nil, Code: "", Description: "Manual"})

	if err := (V034ProductCodes{}).Run(db); err != nil {
		t.Fatalf("primera pasada: %v", err)
	}
	var antes []tsitem
	db.Order("id").Find(&antes)

	if err := (V034ProductCodes{}).Run(db); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}
	var despues []tsitem
	db.Order("id").Find(&despues)

	for i := range antes {
		if antes[i].Code != despues[i].Code {
			t.Fatalf("línea %d cambió entre pasadas: %q → %q", antes[i].ID, antes[i].Code, despues[i].Code)
		}
	}
}
