package docusage

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, lima())
}

func endOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), lima())
}

// subFor arma una suscripción que empieza el día `start` y dura `months` meses, igual
// que hace extendSubscriptionTx (EndDate = fin del día).
func subFor(start time.Time, months int) *database.SaasSubscription {
	return &database.SaasSubscription{
		ID: 1, TenantID: 1, PlanID: 1,
		StartDate: start,
		EndDate:   endOfDay(start.AddDate(0, months, 0)),
		Status:    database.SaasSubActive,
	}
}

// El caso reportado: pagar 6 meses debe dar 6 cupos mensuales, no uno repartido entre 6.
func TestTotalQuotaPeriods(t *testing.T) {
	cases := []struct {
		name   string
		months int
		want   int
	}{
		{"mensual", 1, 1},
		{"trimestral", 3, 3},
		{"semestral", 6, 6},
		{"anual", 12, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := subFor(day(2026, time.August, 3), tc.months)
			if got := TotalQuotaPeriods(sub); got != tc.want {
				t.Errorf("TotalQuotaPeriods = %d, se esperaban %d", got, tc.want)
			}
		})
	}
}

func TestQuotaPeriodBoundsAt_Semestral(t *testing.T) {
	sub := subFor(day(2026, time.August, 3), 6)

	cases := []struct {
		name      string
		at        time.Time
		wantStart time.Time
		wantEnd   time.Time
		wantIndex int
	}{
		{"primer día", day(2026, time.August, 3), day(2026, time.August, 3), day(2026, time.September, 3), 1},
		{"a mitad del mes 1", day(2026, time.August, 20), day(2026, time.August, 3), day(2026, time.September, 3), 1},
		{"día de renovación", day(2026, time.September, 3), day(2026, time.September, 3), day(2026, time.October, 3), 2},
		{"mes 5", day(2026, time.December, 10), day(2026, time.December, 3), day(2027, time.January, 3), 5},
		{"último mes", day(2027, time.January, 15), day(2027, time.January, 3), sub.EndDate, 6},
		// El último día no debe abrir un séptimo período: EndDate es fin de día y el
		// período 6 tiene que absorberlo.
		{"último día de la suscripción", day(2027, time.February, 3), day(2027, time.January, 3), sub.EndDate, 6},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, index := QuotaPeriodBoundsAt(sub, tc.at)
			if !start.Equal(tc.wantStart) {
				t.Errorf("start = %s, se esperaba %s", start, tc.wantStart)
			}
			if !end.Equal(tc.wantEnd) {
				t.Errorf("end = %s, se esperaba %s", end, tc.wantEnd)
			}
			if index != tc.wantIndex {
				t.Errorf("index = %d, se esperaba %d", index, tc.wantIndex)
			}
		})
	}
}

// Los períodos deben cubrir toda la suscripción sin huecos ni solapes: cualquier día
// tiene que caer en exactamente un período, y el siguiente debe empezar donde acaba el
// anterior.
func TestQuotaPeriodsCubrenSinHuecos(t *testing.T) {
	for _, months := range []int{1, 3, 6, 12} {
		sub := subFor(day(2026, time.August, 3), months)
		total := TotalQuotaPeriods(sub)

		var prevEnd time.Time
		for n := 1; n <= total; n++ {
			at := addMonthsClamped(quotaAnchor(sub), n-1)
			start, end, index := QuotaPeriodBoundsAt(sub, at)
			if index != n {
				t.Fatalf("%d meses: en %s se esperaba índice %d, se obtuvo %d", months, at, n, index)
			}
			if n == 1 && !start.Equal(quotaAnchor(sub)) {
				t.Fatalf("%d meses: el primer período no arranca en el inicio de la suscripción", months)
			}
			if n > 1 && !start.Equal(prevEnd) {
				t.Fatalf("%d meses: hueco/solape en el período %d (%s vs %s)", months, n, start, prevEnd)
			}
			if !end.After(start) {
				t.Fatalf("%d meses: período %d vacío o invertido", months, n)
			}
			prevEnd = end
		}
		if !prevEnd.Equal(sub.EndDate) {
			t.Fatalf("%d meses: el último período acaba en %s y la suscripción en %s",
				months, prevEnd, sub.EndDate)
		}
	}
}

// Contratar un 31 no puede desplazar el aniversario a marzo: debe recortar al último
// día del mes destino.
func TestAddMonthsClampedMesesCortos(t *testing.T) {
	cases := []struct {
		from time.Time
		n    int
		want time.Time
	}{
		{day(2026, time.January, 31), 1, day(2026, time.February, 28)},
		{day(2028, time.January, 31), 1, day(2028, time.February, 29)}, // bisiesto
		{day(2026, time.January, 31), 2, day(2026, time.March, 31)},
		{day(2026, time.March, 31), 1, day(2026, time.April, 30)},
		{day(2026, time.August, 3), 6, day(2027, time.February, 3)},
	}
	for _, tc := range cases {
		got := addMonthsClamped(tc.from, tc.n)
		if !got.Equal(tc.want) {
			t.Errorf("addMonthsClamped(%s, %d) = %s, se esperaba %s",
				tc.from.Format("2006-01-02"), tc.n, got.Format("2006-01-02"), tc.want.Format("2006-01-02"))
		}
	}
}

// Renovar antes de que venza el plan anterior alarga la suscripción más allá de N meses
// exactos. Los períodos deben seguir cubriéndola entera, sin dejar días sin cupo.
func TestQuotaPeriodsRenovacionAnticipada(t *testing.T) {
	// Empieza hoy (3/08) pero termina 6 meses después del vencimiento anterior (20/09).
	sub := &database.SaasSubscription{
		ID: 1, TenantID: 1, PlanID: 1,
		StartDate: day(2026, time.August, 3),
		EndDate:   endOfDay(day(2027, time.March, 20)),
		Status:    database.SaasSubActive,
	}

	total := TotalQuotaPeriods(sub)
	if total < 7 {
		t.Fatalf("una suscripción de ~7,5 meses debe tener al menos 7 períodos, tiene %d", total)
	}

	// El último día de la suscripción tiene que caer dentro del último período.
	_, end, index := QuotaPeriodBoundsAt(sub, day(2027, time.March, 18))
	if index != total {
		t.Errorf("el 18/03 debería caer en el período %d, cayó en el %d", total, index)
	}
	if !end.Equal(sub.EndDate) {
		t.Errorf("el último período acaba en %s, se esperaba %s", end, sub.EndDate)
	}
}

// Una fecha anterior al inicio (reloj desfasado, dato viejo) no debe producir índices
// inválidos: se trata como el primer período.
func TestQuotaPeriodBoundsAntesDelInicio(t *testing.T) {
	sub := subFor(day(2026, time.August, 3), 6)
	start, _, index := QuotaPeriodBoundsAt(sub, day(2026, time.July, 1))
	if index != 1 {
		t.Errorf("índice = %d, se esperaba 1", index)
	}
	if !start.Equal(quotaAnchor(sub)) {
		t.Errorf("start = %s, se esperaba el inicio de la suscripción", start)
	}
}
