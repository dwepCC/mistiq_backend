package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var notificationTestDBCounter int

func setupNotificationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	notificationTestDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), notificationTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{&database.TenantNotification{}, &database.TenantNotificationRead{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

const permOrdersView = "ecommerce.orders_view"

func seedBroadcastNotification(t *testing.T, db *gorm.DB, notifType, title string) database.TenantNotification {
	t.Helper()
	n := database.TenantNotification{Type: notifType, Title: title, LinkPath: "/sales/pedidos-web?id=1"}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func seedDirectNotification(t *testing.T, db *gorm.DB, userID uint, title string) database.TenantNotification {
	t.Helper()
	n := database.TenantNotification{Type: "ecommerce.order.created", Title: title, UserID: &userID}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// ── 1/7. Listado, incluye LinkPath del pedido ──────────────────────────

func TestList_DevuelveNotificacionesVisiblesConLinkPath(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	seedBroadcastNotification(t, db, "ecommerce.order.created", "Nuevo pedido online #10")

	rows, err := svc.List(ListParams{UserID: 1, Permissions: []string{permOrdersView}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("esperaba 1 notificación visible, hay %d", len(rows))
	}
	if rows[0].LinkPath != "/sales/pedidos-web?id=1" {
		t.Errorf("LinkPath = %q, quería el link al pedido", rows[0].LinkPath)
	}
	if rows[0].Read {
		t.Error("una notificación recién creada no debe aparecer como leída")
	}
}

// ── 5. Usuario A no ve notificaciones DIRIGIDAS de usuario B ──────────

func TestList_UsuarioNoVeNotificacionesDirigidasAOtroUsuario(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	seedDirectNotification(t, db, 1, "Solo para el usuario 1")
	seedDirectNotification(t, db, 2, "Solo para el usuario 2")

	rowsA, err := svc.List(ListParams{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 1 || rowsA[0].Title != "Solo para el usuario 1" {
		t.Fatalf("usuario 1 debía ver solo su propia notificación dirigida, vio: %+v", rowsA)
	}

	rowsB, err := svc.List(ListParams{UserID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsB) != 1 || rowsB[0].Title != "Solo para el usuario 2" {
		t.Fatalf("usuario 2 debía ver solo su propia notificación dirigida, vio: %+v", rowsB)
	}
}

// Broadcast sin el permiso correspondiente al Type: no debe verse en absoluto.
func TestList_BroadcastSinPermisoNoEsVisible(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	seedBroadcastNotification(t, db, "ecommerce.order.created", "Nuevo pedido")

	rows, err := svc.List(ListParams{UserID: 1, Permissions: []string{"sales.view"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("sin ecommerce.orders_view (ni ecommerce.manage) no debía ver la notificación broadcast, vio %d", len(rows))
	}
}

// "{modulo}.manage" implica el permiso del tipo — mismo criterio que el resto del RBAC del panel.
func TestList_ManageImplicaVerBroadcast(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	seedBroadcastNotification(t, db, "ecommerce.order.created", "Nuevo pedido")

	rows, err := svc.List(ListParams{UserID: 1, Permissions: []string{"ecommerce.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ecommerce.manage debe implicar ecommerce.orders_view (ver notificación broadcast), vio %d", len(rows))
	}
}

// ── Opción A: dos usuarios divergen en el estado de lectura de la MISMA fila broadcast ──

func TestMarkRead_BroadcastEsPorUsuario_NoAfectaAOtrosLectores(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	notif := seedBroadcastNotification(t, db, "ecommerce.order.created", "Nuevo pedido #100")

	// Almacenero (usuario 1) la marca como leída.
	if err := svc.MarkRead(notif.ID, 1, []string{permOrdersView}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	rowsAlmacenero, _ := svc.List(ListParams{UserID: 1, Permissions: []string{permOrdersView}})
	if len(rowsAlmacenero) != 1 || !rowsAlmacenero[0].Read {
		t.Fatalf("para el usuario 1 (que la marcó) debía verse leída: %+v", rowsAlmacenero)
	}

	// Vendedor (usuario 2) y Supervisor (usuario 3): la misma fila sigue SIN LEER para ellos —
	// este es el punto central de la Opción A confirmada por el usuario.
	for _, otherUserID := range []uint{2, 3} {
		rows, _ := svc.List(ListParams{UserID: otherUserID, Permissions: []string{permOrdersView}})
		if len(rows) != 1 || rows[0].Read {
			t.Fatalf("usuario %d NO marcó la notificación — debía seguir sin leer para él, vio: %+v", otherUserID, rows)
		}
	}

	// La fila TenantNotification.ReadAt en sí NUNCA se toca para una broadcast.
	var raw database.TenantNotification
	db.First(&raw, notif.ID)
	if raw.ReadAt != nil {
		t.Error("TenantNotification.ReadAt no debe modificarse nunca para una notificación broadcast")
	}

	var readRowCount int64
	db.Model(&database.TenantNotificationRead{}).Where("notification_id = ?", notif.ID).Count(&readRowCount)
	if readRowCount != 1 {
		t.Fatalf("debía existir exactamente 1 fila de lectura (la del usuario 1), hay %d", readRowCount)
	}
}

// Marcar dos veces la misma notificación (mismo usuario) no debe fallar ni duplicar filas — cubre
// la protección UNIQUE(notification_id, user_id) + OnConflict DoNothing.
func TestMarkRead_Idempotente(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	notif := seedBroadcastNotification(t, db, "ecommerce.order.created", "Pedido")

	if err := svc.MarkRead(notif.ID, 1, []string{permOrdersView}); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRead(notif.ID, 1, []string{permOrdersView}); err != nil {
		t.Fatalf("marcar como leída dos veces no debe fallar: %v", err)
	}
	var count int64
	db.Model(&database.TenantNotificationRead{}).Count(&count)
	if count != 1 {
		t.Fatalf("no debe duplicar la fila de lectura, hay %d", count)
	}
}

// ── 10. Sin autorización (ni siquiera vía permiso implícito) no puede marcar como leída ──

func TestMarkRead_SinPermisoDelTipo_Rechazado(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	notif := seedBroadcastNotification(t, db, "ecommerce.order.created", "Pedido")

	if err := svc.MarkRead(notif.ID, 1, []string{"sales.view"}); err == nil {
		t.Fatal("un usuario sin ecommerce.orders_view (ni ecommerce.manage) no debe poder marcar como leída una notificación de pedidos")
	}
}

// Una notificación DIRIGIDA a otro usuario nunca puede marcarse leída por un tercero, sin importar
// sus permisos — el "dueño" de una notificación dirigida es exclusivamente su UserID.
func TestMarkRead_NotificacionDirigidaAOtroUsuario_Rechazada(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	notif := seedDirectNotification(t, db, 1, "Solo para el usuario 1")

	if err := svc.MarkRead(notif.ID, 2, []string{"ecommerce.manage"}); err == nil {
		t.Fatal("un usuario no debe poder marcar como leída una notificación dirigida a OTRO usuario, ni con ecommerce.manage")
	}
}

// ── 2. Unread count real, fuente de verdad del backend ─────────────────

func TestUnreadCount_ReflejaLecturasPorUsuario(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	n1 := seedBroadcastNotification(t, db, "ecommerce.order.created", "Pedido 1")
	seedBroadcastNotification(t, db, "ecommerce.order.confirmed", "Pedido 2 confirmado")
	seedDirectNotification(t, db, 1, "Directa para usuario 1")

	count, err := svc.UnreadCount(1, []string{permOrdersView})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("unread_count = %d, quería 3 (2 broadcast + 1 dirigida)", count)
	}

	if err := svc.MarkRead(n1.ID, 1, []string{permOrdersView}); err != nil {
		t.Fatal(err)
	}
	count, err = svc.UnreadCount(1, []string{permOrdersView})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("tras marcar una como leída, unread_count = %d, quería 2", count)
	}

	// Para otro usuario que nunca marcó nada, sigue en 3.
	count2, err := svc.UnreadCount(2, []string{permOrdersView})
	if err != nil {
		t.Fatal(err)
	}
	if count2 != 2 { // 2 broadcast; la directa es de otro usuario, no cuenta para el usuario 2
		t.Fatalf("usuario 2 (sin leer nada, sin notificaciones dirigidas propias) unread_count = %d, quería 2", count2)
	}
}

// ── 4. Marcar todas como leídas: dirigidas + broadcast en una sola operación ──

func TestMarkAllRead_MarcaDirigidasYBroadcast(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	seedBroadcastNotification(t, db, "ecommerce.order.created", "Pedido 1")
	seedBroadcastNotification(t, db, "ecommerce.order.cancelled", "Pedido 2 cancelado")
	seedDirectNotification(t, db, 1, "Directa para usuario 1")

	if err := svc.MarkAllRead(1, []string{permOrdersView}); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	count, err := svc.UnreadCount(1, []string{permOrdersView})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("tras MarkAllRead, unread_count debía ser 0, es %d", count)
	}

	// Otro usuario (que nunca hizo MarkAllRead) sigue viendo las broadcast como no leídas — de
	// nuevo, el punto central de la Opción A: "marcar todas" es por usuario, no global.
	count2, err := svc.UnreadCount(2, []string{permOrdersView})
	if err != nil {
		t.Fatal(err)
	}
	if count2 != 2 {
		t.Fatalf("usuario 2 no ejecutó MarkAllRead — debía seguir con 2 sin leer, tiene %d", count2)
	}
}

// ── 6. Tenant A no ve notificaciones de tenant B (BD completamente separada) ──

func TestList_TenantIsolation(t *testing.T) {
	dbA := setupNotificationTestDB(t)
	dbB := setupNotificationTestDB(t)
	seedBroadcastNotification(t, dbA, "ecommerce.order.created", "Pedido del tenant A")
	seedBroadcastNotification(t, dbB, "ecommerce.order.created", "Pedido del tenant B")

	rowsA, err := NewNotificationService(dbA).List(ListParams{UserID: 1, Permissions: []string{permOrdersView}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 1 || rowsA[0].Title != "Pedido del tenant A" {
		t.Fatalf("tenant A debía ver solo su propia notificación, vio: %+v", rowsA)
	}

	rowsB, err := NewNotificationService(dbB).List(ListParams{UserID: 1, Permissions: []string{permOrdersView}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsB) != 1 || rowsB[0].Title != "Pedido del tenant B" {
		t.Fatalf("tenant B debía ver solo su propia notificación, vio: %+v", rowsB)
	}
}

// Cursor "cargar más" (before_id) — no una prueba de negocio pero sí del contrato de paginación.
func TestList_CursorBeforeID(t *testing.T) {
	db := setupNotificationTestDB(t)
	svc := NewNotificationService(db)
	first := seedBroadcastNotification(t, db, "ecommerce.order.created", "Primera")
	time.Sleep(time.Millisecond)
	seedBroadcastNotification(t, db, "ecommerce.order.created", "Segunda")

	rows, err := svc.List(ListParams{UserID: 1, Permissions: []string{permOrdersView}, BeforeID: first.ID + 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("con before_id=%d y limit=1 debía devolver solo la primera, devolvió: %+v", first.ID+1, rows)
	}
}
