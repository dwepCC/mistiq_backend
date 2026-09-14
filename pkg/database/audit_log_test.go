package database

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openAuditLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&AuditLog{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// La app corre con ProxyHeader = X-Forwarded-For, así que c.IP() entrega la cadena completa del
// header. Detrás de Cloudflare y con cliente IPv6 supera los 45 de la columna, MySQL estricto
// devolvía 1406 y la auditoría se perdía porque los llamadores descartan el error.
func TestAuditLogNormalizaIPDeCadenaProxy(t *testing.T) {
	casos := []struct {
		nombre   string
		entrada  string
		esperado string
	}{
		{
			// El caso real que destapó el bug: 53 caracteres.
			nombre:   "IPv6 de cliente más edge de Cloudflare",
			entrada:  "2001:1388:49e6:6666:4309:f270:aac1:3c05, 172.71.11.91",
			esperado: "2001:1388:49e6:6666:4309:f270:aac1:3c05",
		},
		{
			nombre:   "IPv4 con varios saltos",
			entrada:  "190.42.184.39, 172.71.11.91, 10.0.0.3",
			esperado: "190.42.184.39",
		},
		{
			nombre:   "IP suelta se guarda igual",
			entrada:  "200.121.141.16",
			esperado: "200.121.141.16",
		},
		{
			nombre:   "vacía no rompe",
			entrada:  "",
			esperado: "",
		},
		{
			// Cinturón: si llegara algo que no es una IP, se trunca en vez de fallar el INSERT.
			nombre:   "basura larga sin comas se trunca a 45",
			entrada:  strings.Repeat("x", 80),
			esperado: strings.Repeat("x", 45),
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			db := openAuditLogTestDB(t)
			log := AuditLog{TenantID: 52, UserID: 1, Action: "subscription_validity_adjusted",
				Entity: "saas_subscription", EntityID: 52, IPAddress: c.entrada}
			if err := db.Create(&log).Error; err != nil {
				t.Fatalf("crear audit log: %v", err)
			}

			var guardado AuditLog
			if err := db.First(&guardado, log.ID).Error; err != nil {
				t.Fatalf("releer audit log: %v", err)
			}
			if guardado.IPAddress != c.esperado {
				t.Errorf("ip_address = %q, se esperaba %q", guardado.IPAddress, c.esperado)
			}
			if len(guardado.IPAddress) > 45 {
				t.Errorf("ip_address mide %d, no entra en varchar(45)", len(guardado.IPAddress))
			}
		})
	}
}

// capturarLogDeErrores redirige el slog por defecto a un buffer mientras corre el test.
func capturarLogDeErrores(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(previo) })
	return &buf
}

func TestWriteAuditLogGuardaLaFila(t *testing.T) {
	db := openAuditLogTestDB(t)
	previo := CentralDB
	CentralDB = db
	t.Cleanup(func() { CentralDB = previo })

	WriteAuditLog(&AuditLog{TenantID: 52, UserID: 1, Action: "master_access",
		Entity: "tenant_user", EntityID: 7, IPAddress: "200.121.141.16, 172.64.222.84"})

	var guardado AuditLog
	if err := db.First(&guardado, "action = ?", "master_access").Error; err != nil {
		t.Fatalf("la fila no se guardó: %v", err)
	}
	if guardado.IPAddress != "200.121.141.16" {
		t.Errorf("ip_address = %q, se esperaba la IP del cliente sin los saltos del proxy", guardado.IPAddress)
	}
}

// El punto del refactor: auditar no puede tumbar la operación auditada, pero el fallo tiene que
// dejar rastro. Antes se descartaba con `_ =` y nadie se enteraba.
func TestWriteAuditLogRegistraElFalloSinPropagarlo(t *testing.T) {
	buf := capturarLogDeErrores(t)
	// Base sin la tabla: el INSERT falla igual que fallaba en producción por el error 1406.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	WriteAuditLogTx(db, &AuditLog{TenantID: 52, UserID: 1,
		Action: "subscription_validity_adjusted", Entity: "saas_subscription", EntityID: 52})

	salida := buf.String()
	if !strings.Contains(salida, "auditoría no registrada") {
		t.Fatalf("el fallo no se registró en el log; salida: %q", salida)
	}
	if !strings.Contains(salida, "subscription_validity_adjusted") {
		t.Errorf("el log no identifica la acción perdida; salida: %q", salida)
	}
}

func TestWriteAuditLogToleraEntradaNulaYBDAusente(t *testing.T) {
	previo := CentralDB
	CentralDB = nil
	t.Cleanup(func() { CentralDB = previo })

	WriteAuditLog(nil) // no debe hacer nada ni entrar en pánico

	buf := capturarLogDeErrores(t)
	WriteAuditLog(&AuditLog{Action: "plan_updated", Entity: "saas_plan", EntityID: 3})
	if !strings.Contains(buf.String(), "BD central no disponible") {
		t.Errorf("sin BD central el fallo debe quedar registrado; salida: %q", buf.String())
	}
}
