package handler

import (
	"testing"

	"tukifac/internal/company/service"
	"tukifac/pkg/database"
)

func seriesItem(name string, active bool) service.SeriesListItem {
	return service.SeriesListItem{
		TenantDocumentSeries: database.TenantDocumentSeries{
			Series: name,
			Active: active,
		},
	}
}

func TestFilterActiveSeries(t *testing.T) {
	cases := []struct {
		name  string
		input []service.SeriesListItem
		want  []string
	}{
		{"sin series", nil, nil},
		{
			"todas activas",
			[]service.SeriesListItem{seriesItem("F001", true), seriesItem("B001", true)},
			[]string{"F001", "B001"},
		},
		{
			// El caso reportado: una boleta desactivada seguía apareciendo al vender.
			"descarta las desactivadas",
			[]service.SeriesListItem{
				seriesItem("F001", true),
				seriesItem("B002", false),
				seriesItem("B001", true),
				seriesItem("F999", false),
			},
			[]string{"F001", "B001"},
		},
		{
			"todas desactivadas",
			[]service.SeriesListItem{seriesItem("B002", false), seriesItem("F999", false)},
			nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterActiveSeries(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("filterActiveSeries devolvió %d series, se esperaban %d", len(got), len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].Series != want {
					t.Errorf("posición %d: se obtuvo %q, se esperaba %q", i, got[i].Series, want)
				}
			}
		})
	}
}

// El filtro no debe alterar el slice original: la pantalla de configuración pide la
// lista completa por el mismo camino.
func TestFilterActiveSeriesNoMutaEntrada(t *testing.T) {
	input := []service.SeriesListItem{
		seriesItem("F001", true),
		seriesItem("B002", false),
		seriesItem("B001", true),
	}
	_ = filterActiveSeries(input)

	want := []string{"F001", "B002", "B001"}
	for i, w := range want {
		if input[i].Series != w {
			t.Errorf("la entrada fue mutada en %d: %q, se esperaba %q", i, input[i].Series, w)
		}
	}
}

func TestIsTruthyFlag(t *testing.T) {
	// Solo estos valores incluyen las series inactivas; cualquier otro deja el filtro
	// puesto, que es el comportamiento seguro.
	cases := map[string]bool{
		"1": true, "true": true, "TRUE": true, "yes": true, " Yes ": true,
		"": false, "0": false, "false": false, "no": false, "si": false, "tru": false,
	}
	for value, want := range cases {
		if got := isTruthyFlag(value); got != want {
			t.Errorf("isTruthyFlag(%q) = %v, se esperaba %v", value, got, want)
		}
	}
}
