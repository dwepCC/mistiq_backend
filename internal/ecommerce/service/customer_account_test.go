package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var customerAccountTestDBCounter int

func setupCustomerAccountTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	customerAccountTestDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), customerAccountTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantEcommerceCustomerAccount{}, &database.TenantEcommerceCustomerAddress{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceDispatch{}, &database.TenantEcommerceDispatchStatusHistory{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func mustRegisterCustomer(t *testing.T, svc *EcommerceService, phone string) *database.TenantEcommerceCustomerAccount {
	t.Helper()
	acc, err := svc.RegisterCustomer(RegisterCustomerInput{Name: "Cliente Test", Phone: phone, Password: "clave123"})
	if err != nil {
		t.Fatalf("RegisterCustomer: %v", err)
	}
	return acc
}

// ── 1/4. Registro correcto + password nunca en texto plano ─────────

func TestRegisterCustomer_Correcto(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	acc, err := svc.RegisterCustomer(RegisterCustomerInput{
		Name: "Ana Torres", Phone: "999111222", Email: "ana@example.com", Password: "clave123",
	})
	if err != nil {
		t.Fatalf("RegisterCustomer: %v", err)
	}
	if acc.Name != "Ana Torres" || acc.Phone != "999111222" || acc.Email == nil || *acc.Email != "ana@example.com" {
		t.Fatalf("cuenta creada incorrecta: %+v", acc)
	}
	if !acc.Active {
		t.Error("la cuenta debe quedar activa por defecto")
	}
	// Punto 4: password NUNCA en texto plano.
	if acc.PasswordHash == "clave123" || acc.PasswordHash == "" {
		t.Fatalf("PasswordHash inválido: %q", acc.PasswordHash)
	}
	if !strings.HasPrefix(acc.PasswordHash, "$2a$") && !strings.HasPrefix(acc.PasswordHash, "$2b$") {
		t.Fatalf("PasswordHash no parece bcrypt: %q", acc.PasswordHash)
	}
	if !acc.CheckPassword("clave123") {
		t.Fatal("CheckPassword debe validar la contraseña real")
	}
	if acc.CheckPassword("otra-clave") {
		t.Fatal("CheckPassword no debe validar una contraseña incorrecta")
	}
}

// ── 2. Registro duplicado DENTRO del mismo tenant → rechazo ─────────

func TestRegisterCustomer_TelefonoDuplicado_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	mustRegisterCustomer(t, svc, "999333444")

	_, err := svc.RegisterCustomer(RegisterCustomerInput{Name: "Otro", Phone: "999333444", Password: "clave456"})
	if err == nil {
		t.Fatal("un segundo registro con el mismo teléfono en el mismo tenant debe rechazarse")
	}
	var count int64
	db.Model(&database.TenantEcommerceCustomerAccount{}).Where("phone = ?", "999333444").Count(&count)
	if count != 1 {
		t.Fatalf("debe seguir existiendo exactamente 1 cuenta con ese teléfono, hay %d", count)
	}
}

func TestRegisterCustomer_EmailDuplicado_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	svc.RegisterCustomer(RegisterCustomerInput{Name: "A", Phone: "999000001", Email: "dup@example.com", Password: "clave123"})

	_, err := svc.RegisterCustomer(RegisterCustomerInput{Name: "B", Phone: "999000002", Email: "DUP@example.com", Password: "clave123"})
	if err == nil {
		t.Fatal("un email duplicado (sin importar mayúsculas) debe rechazarse")
	}
}

func TestRegisterCustomer_EmailInvalido_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	_, err := svc.RegisterCustomer(RegisterCustomerInput{Name: "A", Phone: "999000003", Email: "no-es-un-correo", Password: "clave123"})
	if err == nil {
		t.Fatal("un email con formato inválido debe rechazarse")
	}
}

// ── 3/26. Mismo teléfono en TENANTS DIFERENTES → permitido (aislamiento real) ──

func TestRegisterCustomer_MismoTelefonoEnOtroTenant_Permitido(t *testing.T) {
	dbA := setupCustomerAccountTestDB(t)
	dbB := setupCustomerAccountTestDB(t)
	svcA := &EcommerceService{db: dbA}
	svcB := &EcommerceService{db: dbB}

	if _, err := svcA.RegisterCustomer(RegisterCustomerInput{Name: "Cliente A", Phone: "999555000", Password: "clave123"}); err != nil {
		t.Fatalf("registro en tenant A: %v", err)
	}
	// Mismo teléfono, tenant B (BD completamente separada) — debe permitirse, no hay unicidad global.
	if _, err := svcB.RegisterCustomer(RegisterCustomerInput{Name: "Cliente B", Phone: "999555000", Password: "clave456"}); err != nil {
		t.Fatalf("el mismo teléfono en OTRO tenant debe permitirse, falló: %v", err)
	}
}

// ── 5/6. Login ───────────────────────────────────────────────────────

func TestLoginCustomer_Correcto(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	mustRegisterCustomer(t, svc, "999666000")

	acc, err := svc.LoginCustomer(LoginCustomerInput{Phone: "999666000", Password: "clave123"})
	if err != nil {
		t.Fatalf("LoginCustomer: %v", err)
	}
	if acc.Phone != "999666000" {
		t.Errorf("cuenta incorrecta: %+v", acc)
	}
}

func TestLoginCustomer_PasswordIncorrecta_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	mustRegisterCustomer(t, svc, "999777000")

	_, err := svc.LoginCustomer(LoginCustomerInput{Phone: "999777000", Password: "clave-mala"})
	if err == nil {
		t.Fatal("login con contraseña incorrecta debe rechazarse")
	}
}

func TestLoginCustomer_CuentaInexistente_MismoMensajeQuePasswordIncorrecta(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	mustRegisterCustomer(t, svc, "999888000")

	_, errNoExiste := svc.LoginCustomer(LoginCustomerInput{Phone: "999000999", Password: "cualquiera"})
	_, errPassMala := svc.LoginCustomer(LoginCustomerInput{Phone: "999888000", Password: "cualquiera"})
	if errNoExiste == nil || errPassMala == nil {
		t.Fatal("ambos casos deben fallar")
	}
	// No debe ser posible distinguir "no existe" de "password incorrecta" por el mensaje — mismo
	// criterio de no facilitar enumeración de cuentas que ya sigue el resto del sistema.
	if errNoExiste.Error() != errPassMala.Error() {
		t.Fatalf("los mensajes deben ser idénticos para no revelar si la cuenta existe: %q vs %q", errNoExiste.Error(), errPassMala.Error())
	}
}

// ── 11/12/13. Pedidos del cliente — solo los propios, ownership real ──

func TestListCustomerOrders_SoloPropios(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	clienteA := mustRegisterCustomer(t, svc, "999111000")
	clienteB := mustRegisterCustomer(t, svc, "999222000")

	db.Create(&database.TenantEcommerceOrder{CustomerName: "A", CustomerPhone: "999111000", CustomerAccountID: &clienteA.ID, ItemsJSON: "[]", Total: 10, Status: OrderStatusPendiente})
	db.Create(&database.TenantEcommerceOrder{CustomerName: "A2", CustomerPhone: "999111000", CustomerAccountID: &clienteA.ID, ItemsJSON: "[]", Total: 20, Status: OrderStatusPendiente})
	db.Create(&database.TenantEcommerceOrder{CustomerName: "B", CustomerPhone: "999222000", CustomerAccountID: &clienteB.ID, ItemsJSON: "[]", Total: 30, Status: OrderStatusPendiente})
	db.Create(&database.TenantEcommerceOrder{CustomerName: "Invitado", CustomerPhone: "999333000", ItemsJSON: "[]", Total: 40, Status: OrderStatusPendiente}) // sin cuenta

	ordersA, err := svc.ListCustomerOrders(clienteA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordersA) != 2 {
		t.Fatalf("cliente A debe ver exactamente sus 2 pedidos, vio %d", len(ordersA))
	}
}

func TestGetCustomerOrder_DeOtroCliente_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	clienteA := mustRegisterCustomer(t, svc, "999444000")
	clienteB := mustRegisterCustomer(t, svc, "999555000")
	orderB := database.TenantEcommerceOrder{CustomerName: "B", CustomerPhone: "999555000", CustomerAccountID: &clienteB.ID, ItemsJSON: "[]", Total: 30, Status: OrderStatusPendiente}
	db.Create(&orderB)

	// Cliente A intenta consultar el pedido de Cliente B conociendo su ID — debe fallar.
	_, _, _, err := svc.GetCustomerOrder(clienteA.ID, orderB.ID)
	if err == nil {
		t.Fatal("un cliente NUNCA debe poder consultar el pedido de otro solo conociendo el ID")
	}
}

func TestGetCustomerOrder_DeOtroTenant_Rechazado(t *testing.T) {
	dbA := setupCustomerAccountTestDB(t)
	dbB := setupCustomerAccountTestDB(t)
	svcA := &EcommerceService{db: dbA}
	svcB := &EcommerceService{db: dbB}

	clienteA := mustRegisterCustomer(t, svcA, "999666111")
	orderA := database.TenantEcommerceOrder{CustomerName: "A", CustomerPhone: "999666111", CustomerAccountID: &clienteA.ID, ItemsJSON: "[]", Total: 10, Status: OrderStatusPendiente}
	dbA.Create(&orderA)

	clienteB := mustRegisterCustomer(t, svcB, "999666111") // mismo teléfono, otro tenant — IDs pueden coincidir
	_, _, _, err := svcB.GetCustomerOrder(clienteB.ID, orderA.ID)
	if err == nil {
		t.Fatal("un pedido que solo existe en OTRO tenant (BD separada) nunca debe resolverse")
	}
}

// ── 14/15/16/17/18. Direcciones — CRUD con ownership real ───────────

func TestCreateCustomerAddress_Correcto(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	acc := mustRegisterCustomer(t, svc, "999000111")

	addr, err := svc.CreateCustomerAddress(acc.ID, CustomerAddressInput{AddressLine: "Av. Test 123", Ubigeo: "150101"})
	if err != nil {
		t.Fatalf("CreateCustomerAddress: %v", err)
	}
	if addr.CustomerAccountID != acc.ID {
		t.Errorf("CustomerAccountID = %d, quería %d", addr.CustomerAccountID, acc.ID)
	}
}

func TestListCustomerAddresses_SoloPropias(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	accA := mustRegisterCustomer(t, svc, "999000222")
	accB := mustRegisterCustomer(t, svc, "999000333")
	svc.CreateCustomerAddress(accA.ID, CustomerAddressInput{AddressLine: "Dirección A1"})
	svc.CreateCustomerAddress(accA.ID, CustomerAddressInput{AddressLine: "Dirección A2"})
	svc.CreateCustomerAddress(accB.ID, CustomerAddressInput{AddressLine: "Dirección B1"})

	rows, err := svc.ListCustomerAddresses(accA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("cliente A debe ver exactamente sus 2 direcciones, vio %d", len(rows))
	}
}

func TestUpdateCustomerAddress_Correcto(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	acc := mustRegisterCustomer(t, svc, "999000444")
	addr, _ := svc.CreateCustomerAddress(acc.ID, CustomerAddressInput{AddressLine: "Original"})

	updated, err := svc.UpdateCustomerAddress(acc.ID, addr.ID, CustomerAddressInput{AddressLine: "Actualizada", Label: "Casa"})
	if err != nil {
		t.Fatalf("UpdateCustomerAddress: %v", err)
	}
	if updated.AddressLine != "Actualizada" || updated.Label != "Casa" {
		t.Fatalf("actualización no aplicada: %+v", updated)
	}
}

func TestUpdateCustomerAddress_DeOtroCliente_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	accA := mustRegisterCustomer(t, svc, "999000555")
	accB := mustRegisterCustomer(t, svc, "999000666")
	addrB, _ := svc.CreateCustomerAddress(accB.ID, CustomerAddressInput{AddressLine: "De B"})

	_, err := svc.UpdateCustomerAddress(accA.ID, addrB.ID, CustomerAddressInput{AddressLine: "Hackeada"})
	if err == nil {
		t.Fatal("cliente A NUNCA debe poder modificar una dirección de cliente B")
	}
	var check database.TenantEcommerceCustomerAddress
	db.First(&check, addrB.ID)
	if check.AddressLine != "De B" {
		t.Fatal("la dirección de B no debe haber cambiado")
	}
}

func TestDeleteCustomerAddress_DeOtroCliente_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	accA := mustRegisterCustomer(t, svc, "999000777")
	accB := mustRegisterCustomer(t, svc, "999000888")
	addrB, _ := svc.CreateCustomerAddress(accB.ID, CustomerAddressInput{AddressLine: "De B"})

	err := svc.DeleteCustomerAddress(accA.ID, addrB.ID)
	if err == nil {
		t.Fatal("cliente A NUNCA debe poder eliminar una dirección de cliente B")
	}
	var count int64
	db.Model(&database.TenantEcommerceCustomerAddress{}).Where("id = ?", addrB.ID).Count(&count)
	if count != 1 {
		t.Fatal("la dirección de B no debe haberse eliminado")
	}
}

// ── 24/25. Vinculación guest → account ──────────────────────────────

func TestLinkGuestOrder_Correcto(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	acc := mustRegisterCustomer(t, svc, "999123456")
	guestOrder := database.TenantEcommerceOrder{CustomerName: "Invitado", CustomerPhone: "999123456", ItemsJSON: "[]", Total: 50, Status: OrderStatusPendiente}
	db.Create(&guestOrder)

	linked, err := svc.LinkGuestOrder(acc.ID, guestOrder.ID)
	if err != nil {
		t.Fatalf("LinkGuestOrder: %v", err)
	}
	if linked.CustomerAccountID == nil || *linked.CustomerAccountID != acc.ID {
		t.Fatalf("el pedido debe quedar vinculado a la cuenta, quedó: %v", linked.CustomerAccountID)
	}
}

func TestLinkGuestOrder_TelefonoNoCoincide_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	acc := mustRegisterCustomer(t, svc, "999111999") // cuenta registrada con ESTE teléfono
	// Pedido de invitado con un teléfono DISTINTO — no puede ser reclamado por esta cuenta aunque
	// conozca el order_id (evita secuestrar pedidos de otra persona, punto 25).
	otherOrder := database.TenantEcommerceOrder{CustomerName: "Otra persona", CustomerPhone: "999000000", ItemsJSON: "[]", Total: 50, Status: OrderStatusPendiente}
	db.Create(&otherOrder)

	_, err := svc.LinkGuestOrder(acc.ID, otherOrder.ID)
	if err == nil {
		t.Fatal("un pedido con teléfono distinto al de la cuenta NUNCA debe vincularse")
	}
	var check database.TenantEcommerceOrder
	db.First(&check, otherOrder.ID)
	if check.CustomerAccountID != nil {
		t.Fatal("el pedido no debe haber quedado vinculado")
	}
}

func TestLinkGuestOrder_YaVinculado_Rechazado(t *testing.T) {
	db := setupCustomerAccountTestDB(t)
	svc := &EcommerceService{db: db}
	accA := mustRegisterCustomer(t, svc, "999222333")
	accB := mustRegisterCustomer(t, svc, "999333222") // teléfono distinto para B

	order := database.TenantEcommerceOrder{CustomerName: "X", CustomerPhone: "999222333", ItemsJSON: "[]", Total: 10, Status: OrderStatusPendiente}
	db.Create(&order)
	if _, err := svc.LinkGuestOrder(accA.ID, order.ID); err != nil {
		t.Fatalf("primera vinculación (legítima) falló: %v", err)
	}
	// accB intenta reclamar el mismo pedido ahora que ya tiene dueño — debe fallar sin importar
	// si su teléfono coincidiera (acá ni siquiera coincide, doble motivo de rechazo).
	if _, err := svc.LinkGuestOrder(accB.ID, order.ID); err == nil {
		t.Fatal("un pedido ya vinculado a una cuenta no debe poder revincularse a otra")
	}
}
