package summary

import (
	"testing"
	"time"
)

func sortedSlugs(rows []*Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Slug
	}
	return out
}

func sameOrder(t *testing.T, label string, got []*Row, want ...string) {
	t.Helper()
	g := sortedSlugs(got)
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, g, want)
		}
	}
}

func TestSortRows_columnasNumericasEnAmbasDirecciones(t *testing.T) {
	mk := func() []*Row {
		return []*Row{
			{Slug: "a", Name: "A", Emitted: 10, Accepted: 8, Sent: 1, Rejected: 0, ToSend: 1},
			{Slug: "b", Name: "B", Emitted: 30, Accepted: 20, Sent: 5, Rejected: 2, ToSend: 4},
			{Slug: "c", Name: "C", Emitted: 20, Accepted: 25, Sent: 3, Rejected: 9, ToSend: 0},
		}
	}
	cases := []struct {
		key, dir string
		want     []string
	}{
		{"emitted", "", []string{"b", "c", "a"}}, // natural: mayor a menor
		{"emitted", "desc", []string{"b", "c", "a"}},
		{"emitted", "asc", []string{"a", "c", "b"}},
		{"accepted", "asc", []string{"a", "b", "c"}},
		{"sent", "desc", []string{"b", "c", "a"}},
		{"sent", "asc", []string{"a", "c", "b"}},
		{"rejected", "desc", []string{"c", "b", "a"}},
		{"to_send", "", []string{"b", "a", "c"}},
		{"to_send", "asc", []string{"c", "a", "b"}},
		{"name", "", []string{"a", "b", "c"}},
		{"name", "desc", []string{"c", "b", "a"}},
		{"desconocida", "", []string{"b", "a", "c"}}, // clave inválida = to_send
	}
	for _, c := range cases {
		rows := mk()
		sortRows(rows, c.key, c.dir)
		sameOrder(t, c.key+"/"+c.dir, rows, c.want...)
	}
}

func TestSortRows_fechasSinDatoSiempreAlFinal(t *testing.T) {
	d := func(day int) *time.Time { v := time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC); return &v }
	mk := func() []*Row {
		return []*Row{
			{Slug: "sin", Name: "A"},
			{Slug: "viejo", Name: "B", OldestOpenAt: d(1), LastIssueAt: d(2)},
			{Slug: "nuevo", Name: "C", OldestOpenAt: d(9), LastIssueAt: d(20)},
		}
	}
	for _, key := range []string{"oldest", "last_issue"} {
		for _, dir := range []string{"asc", "desc"} {
			rows := mk()
			sortRows(rows, key, dir)
			if rows[2].Slug != "sin" {
				t.Fatalf("%s/%s: el tenant sin dato debe quedar al final, got %v", key, dir, sortedSlugs(rows))
			}
		}
	}
	rows := mk()
	sortRows(rows, "oldest", "asc")
	sameOrder(t, "oldest asc", rows, "viejo", "nuevo", "sin")
	rows = mk()
	sortRows(rows, "oldest", "desc")
	sameOrder(t, "oldest desc", rows, "nuevo", "viejo", "sin")
	rows = mk()
	sortRows(rows, "last_issue", "")
	sameOrder(t, "last_issue natural (más reciente primero)", rows, "nuevo", "viejo", "sin")
}
