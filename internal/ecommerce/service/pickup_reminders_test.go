package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

func dispatchInAgency(t *testing.T, svc *EcommerceService, productID uint, deadline time.Time, carrierID *uint) uint {
	t.Helper()
	o := newPickupOrder(t, svc, productID, 1)
	d := database.TenantEcommerceDispatch{OrderID: o.ID, Status: DispatchStatusEnAgencia, PickupDeadline: &deadline, CarrierID: carrierID}
	if err := svc.db.Create(&d).Error; err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestAvisoRecojo_PorVencerYVencidoUnaSolaVez(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "AV1", "Prod", 10)
	now := time.Now()

	far := dispatchInAgency(t, svc, p.ID, now.Add(12*24*time.Hour), nil)  // lejos: sin aviso
	soon := dispatchInAgency(t, svc, p.ID, now.Add(3*24*time.Hour), nil)  // dentro de los 5 días por defecto
	late := dispatchInAgency(t, svc, p.ID, now.Add(-2*24*time.Hour), nil) // vencido
	db.Where("1 = 1").Delete(&database.TenantNotification{})              // ignora los «nuevo pedido»

	n, err := svc.NotifyPickupDeadlines(now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("creó %d avisos, esperaba 2 (por vencer + vencido)", n)
	}
	var notes []database.TenantNotification
	db.Find(&notes)
	types := map[string]string{}
	for _, x := range notes {
		types[x.LinkPath] = x.Type
	}
	if types["/sales/pedidos-web?id="+itoa(soon)] != notifPickupSoon || types["/sales/pedidos-web?id="+itoa(late)] != notifPickupExpired {
		t.Errorf("tipos incorrectos: %v", types)
	}
	if _, ok := types["/sales/pedidos-web?id="+itoa(far)]; ok {
		t.Error("el que está lejos de vencer no debe avisar")
	}
	for _, x := range notes {
		if x.Type == notifPickupSoon && !strings.Contains(x.Title, "3 días") {
			t.Errorf("título: %q", x.Title)
		}
	}
	if again, _ := svc.NotifyPickupDeadlines(now); again != 0 {
		t.Errorf("repetir no debe duplicar avisos, creó %d", again)
	}
	// Con el tiempo, el «por vencer» ya enviado pasa a «vencido» y sí avisa.
	if later, _ := svc.NotifyPickupDeadlines(now.Add(4 * 24 * time.Hour)); later != 1 {
		t.Errorf("al vencer el plazo debe avisar una vez más, creó %d", later)
	}
}

func TestAvisoRecojo_UsaElAvisoPrevioDeLaEmpresa(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "AV2", "Prod", 10)
	car := seedCarrier(t, svc, "Olva", 15)
	db.Model(car).Update("warn_days_before", 2)
	now := time.Now()
	dispatchInAgency(t, svc, p.ID, now.Add(4*24*time.Hour), &car.ID) // 4 días > aviso de 2
	db.Where("1 = 1").Delete(&database.TenantNotification{})
	if n, _ := svc.NotifyPickupDeadlines(now); n != 0 {
		t.Errorf("con aviso previo de 2 días, faltando 4 no se avisa (creó %d)", n)
	}
	if n, _ := svc.NotifyPickupDeadlines(now.Add(2 * 24 * time.Hour)); n != 1 {
		t.Errorf("faltando 2 días sí se avisa (creó %d)", n)
	}
}

func itoa(n uint) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}
