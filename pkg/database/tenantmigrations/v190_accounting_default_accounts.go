package tenantmigrations

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// AccountingSeedDefaults cuentas contables de ventas sembradas en todo tenant (PCGE). Son las mismas para
// todo el Perú, por eso no se dejan vacías; el tenant las edita en Ajustes → Contabilidad si su contador
// usa otras. Espejo de internal/accounting/service.DefaultAccounts (una prueba verifica que coincidan).
var AccountingSeedDefaults = database.TenantAccountingSettings{
	ReceivablePEN: "12121", ReceivableUSD: "12122",
	IgvSalesPEN: "40111", IgvSalesUSD: "40111",
	TaxedPEN: "70111", TaxedUSD: "70111",
	Exonerated: "70111", Unaffected: "70111", Exportation: "70121", Icbper: "40199",
	SubdiarioSales: "05",
}

// V190AccountingDefaultAccounts siembra las cuentas contables de ventas con sus valores por defecto.
//
// V189 creó la tabla vacía y el sistema usaba el valor por defecto "por detrás" cuando faltaba la cuenta; el
// sistema anterior, en cambio, las sembraba al crear el tenant y la pantalla solo las editaba. Aquí: si no hay fila,
// se crea con los valores por defecto; si la hay, solo se completan los campos VACÍOS (lo que el tenant ya
// configuró no se toca). Corre en tenants existentes y en los nuevos. Idempotente.
type V190AccountingDefaultAccounts struct{}

func (V190AccountingDefaultAccounts) Version() int { return 190 }
func (V190AccountingDefaultAccounts) Name() string { return "accounting_default_accounts" }

func (V190AccountingDefaultAccounts) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantAccountingSettings{}) {
		return nil
	}
	var st database.TenantAccountingSettings
	err := db.First(&st).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	fill := func(cur *string, def string) {
		if strings.TrimSpace(*cur) == "" {
			*cur = def
		}
	}
	d := AccountingSeedDefaults
	fill(&st.ReceivablePEN, d.ReceivablePEN)
	fill(&st.ReceivableUSD, d.ReceivableUSD)
	fill(&st.IgvSalesPEN, d.IgvSalesPEN)
	fill(&st.IgvSalesUSD, d.IgvSalesUSD)
	fill(&st.TaxedPEN, d.TaxedPEN)
	fill(&st.TaxedUSD, d.TaxedUSD)
	fill(&st.Exonerated, d.Exonerated)
	fill(&st.Unaffected, d.Unaffected)
	fill(&st.Exportation, d.Exportation)
	fill(&st.Icbper, d.Icbper)
	fill(&st.SubdiarioSales, d.SubdiarioSales)
	return db.Save(&st).Error
}
