package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// nextAvailableDocumentSeries busca el primer código libre con el prefijo dado y un sufijo
// numérico de 2 dígitos (prefix+"01".."99") que todavía no exista en tenant_document_series
// PARA TODO EL TENANT (nunca filtrado por branch_id).
//
// Por qué nunca por branch_id: V052SeriesGlobalUnique crea un índice único sobre `series` a
// nivel de tenant completo (regla SUNAT: el código de serie identifica el correlativo ante
// SUNAT, es único por tenant, no por sucursal — dos sucursales NO pueden compartir el mismo
// código). V062CreditNoteBCSeries y V073Quotations comprobaban disponibilidad filtrando por
// branch_id antes de este fix: para un tenant con 2+ sucursales que necesitaran la serie en la
// misma corrida, la comprobación por-sucursal daba "libre" para ambas y las dos intentaban
// insertar el MISMO código (ej. 'BC01' o 'COT'), violando el índice único de V052 y abortando el
// resto de la migración a mitad de camino. Encontrado en producción auditando
// tenant_migration_history en busca de fallos enmascarados como éxito (bug de 482b922):
// doriconta (V62) e industriassanma (V73) quedaron con esa migración fallida y, por lo tanto,
// sucursales sin su serie.
func nextAvailableDocumentSeries(db *gorm.DB, prefix string) (string, error) {
	for n := 1; n <= 99; n++ {
		code := fmt.Sprintf("%s%02d", prefix, n)
		var count int64
		if err := db.Model(&database.TenantDocumentSeries{}).
			Where("series = ?", code).
			Count(&count).Error; err != nil {
			return "", fmt.Errorf("comprobar disponibilidad de %s: %w", code, err)
		}
		if count == 0 {
			return code, nil
		}
	}
	return "", fmt.Errorf("sin código disponible con prefijo %q (01-99 agotado)", prefix)
}
