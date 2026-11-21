package service

import (
	"fmt"
	"sync"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Fase 7G — concurrencia: dos devoluciones parciales simultáneas sobre la MISMA línea nunca deben
// poder sumar más de lo vendido. Este test ejercita directamente el mecanismo real usado dentro
// de CreateCreditNoteAndVoidSale (lock SELECT ... FOR UPDATE sobre la venta + cálculo de cantidad
// ya devuelta + validación + creación del ítem, todo en una única transacción), reutilizando las
// mismas funciones (sumReturnedQuantitiesByOriginalItem, buildPartialNoteItems) que usa el código
// de producción — no una reimplementación paralela de la lógica.
//
// Documentación honesta (mismo criterio que internal/superadmin/service/sa_user_service_test.go
// §28): corre contra glebarez/sqlite (:memory:, pool limitado a 1 conexión física). SQLite no
// tiene locking por fila como MySQL/InnoDB — clause.Locking{Strength:"UPDATE"} se traduce a
// "SELECT ... FOR UPDATE", que el dialector sqlite de GORM simplemente omite de la consulta (no
// es error, tampoco un lock real). Lo que sí serializa las dos transacciones en este test es el
// pool limitado a 1 conexión física: la segunda goroutine espera a que la primera libere la
// conexión antes de poder ejecutar su propia transacción, y para entonces ya ve el ítem de NC que
// la primera dejó committeado. El resultado observable (una gana, la otra es rechazada, nunca
// +24 en vez de +12) es el mismo que produciría el lock real de fila en MySQL/InnoDB en
// producción — ver CreateCreditNoteAndVoidSale (billing_service.go) para el lock real.
//
// No se pudo probar CreateCreditNoteAndVoidSale de punta a punta con esta concurrencia: esa
// función también llama a reserveGenericDocument, que usa database.CentralDB (una BD central
// global, no configurada en un test unitario) y haría panic con CentralDB=nil — limitación
// preexistente del código, no introducida por esta fase. Este test cubre en su lugar el mecanismo
// exacto de bloqueo+validación acumulada que es el que sostiene la invariante.

func setupNotePartialConcurrencyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // fuerza que las dos goroutines compitan por la misma conexión física.
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

// attemptPartialReturn replica la secuencia real de CreateCreditNoteAndVoidSale para una nota
// parcial: lock de la venta original, cálculo de cantidad ya devuelta, validación, creación del
// ítem de la nota — todo en una transacción.
func attemptPartialReturn(db *gorm.DB, origSaleID, origItemID uint, qty float64) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var orig database.TenantSale
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&orig, origSaleID).Error; err != nil {
			return err
		}
		var origItem database.TenantSaleItem
		if err := tx.First(&origItem, origItemID).Error; err != nil {
			return err
		}
		alreadyReturned, err := sumReturnedQuantitiesByOriginalItem(tx, []uint{origItemID})
		if err != nil {
			return err
		}
		partialItems, _, _, _, err := buildPartialNoteItems(origSaleID, []database.TenantSaleItem{origItem}, []NoteItemSelection{
			{OriginalItemID: origItemID, Quantity: qty},
		}, alreadyReturned)
		if err != nil {
			return err
		}
		note := database.TenantSale{
			DocType: "NOTA_CREDITO", Series: "FC01", Number: fmt.Sprintf("FC01-%p", &partialItems),
			Status: "paid", BillingStatus: "pending", OriginalSaleID: &origSaleID,
			Subtotal: partialItems[0].Subtotal, Total: partialItems[0].Total,
		}
		if err := tx.Create(&note).Error; err != nil {
			return err
		}
		partialItems[0].SaleID = note.ID
		return tx.Create(&partialItems[0]).Error
	})
}

func TestConcurrentPartialReturn_OnlyOneSucceeds_NeverExceedsSold(t *testing.T) {
	db := setupNotePartialConcurrencyDB(t)
	origSale := database.TenantSale{DocType: "FACTURA", Series: "F001", Number: "F001-1", Status: "paid", BillingStatus: "accepted", Subtotal: 76.28, Total: 90}
	if err := db.Create(&origSale).Error; err != nil {
		t.Fatal(err)
	}
	suid := uint(45)
	origItem := database.TenantSaleItem{
		SaleID: origSale.ID, ProductID: uintPtr(7), SaleUnitID: &suid,
		Description: "Teclado", Unit: "BX", Quantity: 1, UnitPrice: 45,
		Subtotal: 76.28, TaxAmount: 13.72, Total: 90,
	}
	if err := db.Create(&origItem).Error; err != nil {
		t.Fatal(err)
	}

	// Venta de 1 Caja. Dos procesos intentan devolver 1 Caja cada uno, simultáneamente.
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = attemptPartialReturn(db, origSale.ID, origItem.ID, 1) }()
	go func() { defer wg.Done(); errs[1] = attemptPartialReturn(db, origSale.ID, origItem.ID, 1) }()
	wg.Wait()

	successes, rejections := 0, 0
	for _, e := range errs {
		if e == nil {
			successes++
		} else {
			rejections++
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("successes=%d rejections=%d, want 1 y 1 — la invariante permitió 0 o 2 devoluciones concurrentes", successes, rejections)
	}

	var totalReturned float64
	db.Model(&database.TenantSaleItem{}).
		Where("original_sale_item_id = ?", origItem.ID).
		Select("COALESCE(SUM(quantity), 0)").Scan(&totalReturned)
	if totalReturned != 1 {
		t.Fatalf("cantidad total devuelta = %.3f, want exactamente 1 (nunca 2, que sería +24 en vez de +12 en unidad base)", totalReturned)
	}
}
