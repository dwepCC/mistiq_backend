package service

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"tukifac/pkg/database"

	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestRecordMovementTx_ConcurrentOut_NoOversell reproduce y verifica el fix de la race condition
// de internal/inventory/service/inventory_service.go RecordMovementTx (Contrato ecommerce v2 §12,
// Fase 1.5): sin locking, dos transacciones concurrentes pueden leer el mismo currentQty antes de
// que la otra confirme, permitiendo que ambas validen "stock suficiente" contra el mismo saldo y
// terminen sobrevendiendo.
//
// Por qué este test usa MySQL real y no sqlite (a diferencia del resto de la suite de
// internal/inventory/service): sqlite serializa TODAS las transacciones de escritura a nivel de
// archivo completo (un solo writer a la vez, incluso en modo WAL) — dos transacciones concurrentes
// contra sqlite nunca pueden intercalar sus lecturas/escrituras de la forma en que sí puede
// pasar en MySQL/InnoDB (que solo bloquea a nivel de fila, y un SELECT sin FOR UPDATE no bloquea a
// nadie). Un test contra sqlite pasaría igual con o sin el fix — no probaría nada real sobre la
// race condition que existe en producción (MySQL). Mismo criterio y mismo patrón (env var opcional
// con DSN, skip si no está configurada) que pkg/saas/docusage/concurrency_test.go
// (TestConcurrentReserve_100goroutines_5quota), la única otra prueba de concurrencia real del
// repo.
//
// Cómo correrlo: INVENTORY_MYSQL_DSN="user:pass@tcp(127.0.0.1:3306)/<db_de_prueba_ya_creada>" go
// test -run TestRecordMovementTx_ConcurrentOut_NoOversell ./internal/inventory/service/...
func TestRecordMovementTx_ConcurrentOut_NoOversell(t *testing.T) {
	dsn := os.Getenv("INVENTORY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("INVENTORY_MYSQL_DSN no configurado: este test necesita MySQL real para poder " +
			"reproducir la race condition (sqlite serializa escrituras a nivel de archivo y " +
			"nunca la expondría, con o sin el fix). Ver comentario del test.")
	}

	db, err := gorm.Open(mysqldriver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	for _, m := range []interface{}{
		&database.TenantProduct{}, &database.TenantBranch{}, &database.TenantProductStock{},
		&database.TenantProductPresentation{}, &database.TenantProductPresentationStock{},
		&database.TenantStockMovement{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("automigrate %T: %v", m, err)
		}
	}
	// Limpieza: el test es repetible contra la misma BD de prueba.
	db.Exec("DELETE FROM tenant_stock_movements")
	db.Exec("DELETE FROM tenant_product_stocks")
	db.Exec("DELETE FROM tenant_products")
	db.Exec("DELETE FROM tenant_branches")

	branch := database.TenantBranch{Name: "Sucursal test"}
	if err := db.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}
	product := database.TenantProduct{Code: "RACE1", Name: "Producto race", Type: "product", Unit: "NIU", SalePrice: 10, ManageStock: true, BranchID: branch.ID, Active: true}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	const initialStock = 10
	if err := db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: branch.ID, Quantity: initialStock}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &InventoryService{db: db}

	const attackers = 30 // más que el stock disponible — a propósito, para forzar la contención
	var okCount int32
	var insufficientCount int32
	var unexpectedErrs int32
	var wg sync.WaitGroup
	wg.Add(attackers)
	for i := 0; i < attackers; i++ {
		go func() {
			defer wg.Done()
			err := svc.RecordMovement(MovementInput{
				ProductID: product.ID, BranchID: branch.ID, Type: "out", Quantity: 1,
			})
			switch {
			case err == nil:
				atomic.AddInt32(&okCount, 1)
			case strings.Contains(err.Error(), "stock insuficiente"):
				atomic.AddInt32(&insufficientCount, 1)
			default:
				atomic.AddInt32(&unexpectedErrs, 1)
				t.Logf("error inesperado: %v", err)
			}
		}()
	}
	wg.Wait()

	if unexpectedErrs != 0 {
		t.Fatalf("hubo %d errores inesperados (no 'stock insuficiente')", unexpectedErrs)
	}
	// LA ASERCIÓN CENTRAL: exactamente `initialStock` salidas deben tener éxito, ni una más — sin
	// el fix, este número puede superar initialStock (sobreventa) bajo la interleaving correcta.
	if okCount != initialStock {
		t.Fatalf("REGRESIÓN/RACE: se esperaban exactamente %d salidas exitosas, hubo %d (sobreventa si es mayor)", initialStock, okCount)
	}
	if insufficientCount != attackers-initialStock {
		t.Fatalf("se esperaban %d rechazos por stock insuficiente, hubo %d", attackers-initialStock, insufficientCount)
	}

	var finalStock database.TenantProductStock
	if err := db.Where("product_id = ? AND branch_id = ?", product.ID, branch.ID).First(&finalStock).Error; err != nil {
		t.Fatal(err)
	}
	if finalStock.Quantity != 0 {
		t.Fatalf("REGRESIÓN/RACE: stock final = %.2f, se esperaba 0 (negativo = sobreventa real, positivo = updates perdidos)", finalStock.Quantity)
	}

	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ? AND branch_id = ?", product.ID, branch.ID).Count(&movementCount)
	if movementCount != initialStock {
		t.Fatalf("se esperaban %d movimientos de kardex registrados, hay %d", initialStock, movementCount)
	}
}

// TestRecordMovementTx_SecuencialSigueFuncionando: el fix no debe cambiar el comportamiento
// normal (no concurrente) — mismo caso feliz que ya cubrían los tests existentes de este paquete,
// repetido acá explícitamente contra MySQL para descartar que el locking rompa el flujo secuencial.
func TestRecordMovementTx_SecuencialSigueFuncionando(t *testing.T) {
	dsn := os.Getenv("INVENTORY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("INVENTORY_MYSQL_DSN no configurado")
	}
	db, err := gorm.Open(mysqldriver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	for _, m := range []interface{}{&database.TenantProduct{}, &database.TenantBranch{}, &database.TenantProductStock{}, &database.TenantStockMovement{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec("DELETE FROM tenant_stock_movements")
	db.Exec("DELETE FROM tenant_product_stocks")
	db.Exec("DELETE FROM tenant_products")
	db.Exec("DELETE FROM tenant_branches")

	branch := database.TenantBranch{Name: "Sucursal secuencial"}
	db.Create(&branch)
	product := database.TenantProduct{Code: "SEQ1", Name: "Producto secuencial", Type: "product", Unit: "NIU", SalePrice: 10, ManageStock: true, BranchID: branch.ID, Active: true}
	db.Create(&product)

	svc := &InventoryService{db: db}
	if err := svc.RecordMovement(MovementInput{ProductID: product.ID, BranchID: branch.ID, Type: "in", Quantity: 20}); err != nil {
		t.Fatalf("entrada inicial: %v", err)
	}
	if err := svc.RecordMovement(MovementInput{ProductID: product.ID, BranchID: branch.ID, Type: "out", Quantity: 7}); err != nil {
		t.Fatalf("salida: %v", err)
	}
	if err := svc.RecordMovement(MovementInput{ProductID: product.ID, BranchID: branch.ID, Type: "out", Quantity: 20}); err == nil {
		t.Fatal("una salida mayor al stock disponible (13) debe seguir rechazándose")
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, branch.ID).First(&stock)
	if stock.Quantity != 13 {
		t.Fatalf("stock = %.2f, se esperaba 13 (20-7)", stock.Quantity)
	}
}
