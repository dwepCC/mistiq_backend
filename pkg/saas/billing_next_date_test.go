package saas

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

const testEndDate = "2026-09-15T23:59:59-05:00"

func testNow() time.Time {
	return time.Date(2026, 7, 20, 10, 0, 0, 0, lima())
}

// Sin factura pendiente, el próximo pago es el vencimiento del período.
func TestResolveNextBillingDate_noPendingInvoiceUsesEndDate(t *testing.T) {
	got := ResolveNextBillingDate(testEndDate, nil, 5, testNow())
	if got != testEndDate {
		t.Fatalf("got %q want %q", got, testEndDate)
	}
}

// El caso del reporte: una factura impaga anterior (o heredada) no debe presentarse como
// «próximo pago», porque su vencimiento ya pasó y quedaba antes del inicio del período.
func TestResolveNextBillingDate_ignoresOverdueInvoice(t *testing.T) {
	overdue := &database.SaasBillingCycle{
		SubscriptionID: 5,
		DueDate:        time.Date(2026, 7, 13, 23, 59, 59, 0, lima()),
	}
	got := ResolveNextBillingDate(testEndDate, overdue, 5, testNow())
	if got != testEndDate {
		t.Fatalf("una factura vencida no es el próximo pago: got %q want %q", got, testEndDate)
	}
}

// Factura de otra suscripción: tampoco manda, aunque su vencimiento sea futuro.
func TestResolveNextBillingDate_ignoresInvoiceFromAnotherSubscription(t *testing.T) {
	other := &database.SaasBillingCycle{
		SubscriptionID: 4,
		DueDate:        time.Date(2026, 8, 30, 23, 59, 59, 0, lima()),
	}
	got := ResolveNextBillingDate(testEndDate, other, 5, testNow())
	if got != testEndDate {
		t.Fatalf("got %q want %q", got, testEndDate)
	}
}

// Factura vigente de la suscripción actual: esa sí define el próximo pago.
func TestResolveNextBillingDate_usesCurrentUpcomingInvoice(t *testing.T) {
	due := time.Date(2026, 8, 30, 23, 59, 59, 0, lima())
	upcoming := &database.SaasBillingCycle{SubscriptionID: 5, DueDate: due}
	got := ResolveNextBillingDate(testEndDate, upcoming, 5, testNow())
	want := due.In(lima()).Format(timeRFC3339Lima)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Vence hoy: sigue siendo el próximo pago, no está atrasada.
func TestResolveNextBillingDate_dueTodayCounts(t *testing.T) {
	due := time.Date(2026, 7, 20, 23, 59, 59, 0, lima())
	inv := &database.SaasBillingCycle{SubscriptionID: 5, DueDate: due}
	got := ResolveNextBillingDate(testEndDate, inv, 5, testNow())
	want := due.In(lima()).Format(timeRFC3339Lima)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func limaDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, lima())
}

// La duración contratada sale de las fechas, no del ciclo del plan.
func TestContractedMonths(t *testing.T) {
	cases := []struct {
		name       string
		start, end time.Time
		want       int
	}{
		{"un mes exacto", limaDate(2026, 7, 13), limaDate(2026, 8, 13), 1},
		{"dos meses exactos", limaDate(2026, 7, 13), limaDate(2026, 9, 13), 2},
		{"dos meses y dos días cuentan como tres", limaDate(2026, 7, 13), limaDate(2026, 9, 15), 3},
		{"un año", limaDate(2026, 1, 1), limaDate(2027, 1, 1), 12},
		{"cruce de año", limaDate(2026, 11, 10), limaDate(2027, 2, 10), 3},
		{"periodo corto cuenta como un mes", limaDate(2026, 7, 13), limaDate(2026, 7, 20), 1},
		{"fin anterior al inicio", limaDate(2026, 9, 13), limaDate(2026, 7, 13), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ContractedMonths(c.start, c.end); got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
		})
	}
}
