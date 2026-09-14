package tenantbackfills

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v035LinkedContactTables tablas que referencian tenant_contacts.contact_id — hay que reasignar
// el duplicado al activo en cada una ANTES de borrar el duplicado. Mantener sincronizado con
// cualquier tabla nueva que agregue una FK a tenant_contacts.
var v035LinkedContactTables = []string{
	"tenant_sales",
	"tenant_purchases",
	"tenant_sale_prepayment_vouchers",
	"tenant_quotations",
	"tenant_table_sessions",
	"tenant_memberships",
	"tenant_contact_persons",
}

// V035MergeDuplicateContacts fusiona cada contacto duplicado desactivado con el único contacto
// activo de su mismo grupo (doc_type + doc_number + type), reasignando la referencia en todas
// las tablas que la usan antes de eliminar el duplicado.
//
// Sigue a la limpieza manual del 2026-08-11 (ver memoria project_contacts_unique_index_pending):
// esa limpieza dejó exactamente 1 contacto activo por grupo duplicado en los 60 tenants
// afectados, desactivando el resto — pero nunca reasignó ni borró nada, porque las ventas/
// compras/etc. de los duplicados seguían apuntándoles. Acá se cierra ese paso.
//
// El grupo se identifica en vivo contra el estado actual de tenant_contacts, no contra una lista
// guardada de la limpieza original — así el backfill sigue siendo correcto aunque corra en un
// tenant que nunca pasó por esa limpieza puntual, o si algún duplicado nuevo aparece después.
// Solo actúa sobre grupos con EXACTAMENTE 1 contacto activo: si un grupo tiene 0 o 2+ activos no
// hay un destino inequívoco al cual fusionar, y se salta a propósito (no falla el backfill).
//
// Nunca toca el walk-in por defecto (is_default_walkin=1): ese contacto nunca se desactivó en la
// limpieza original y no participa de ningún duplicado real.
//
// Idempotente: una vez fusionados y borrados los duplicados, no quedan filas con active=0 en un
// grupo con un activo — correrlo de nuevo no encuentra nada que hacer.
type V035MergeDuplicateContacts struct{}

func (V035MergeDuplicateContacts) Version() int { return 35 }
func (V035MergeDuplicateContacts) Name() string { return "merge_duplicate_contacts" }

func (V035MergeDuplicateContacts) Description() string {
	return "Fusiona los contactos duplicados desactivados el 2026-08-11 con su contacto activo " +
		"(mismo tipo de documento + número + rol): reasigna la referencia en ventas, compras, " +
		"anticipos, cotizaciones, sesiones de mesa, membresías y personas de contacto, y recién " +
		"ahí elimina el duplicado. Solo actúa sobre grupos con exactamente un contacto activo; " +
		"nunca toca el cliente genérico (walk-in) por defecto."
}

type v035DupRow struct {
	DuplicateID uint
	ActiveID    uint
}

func (b V035MergeDuplicateContacts) Run(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_contacts") {
		return nil
	}

	var dups []v035DupRow
	if err := db.Raw(`
		SELECT d.id AS duplicate_id, a.id AS active_id
		FROM tenant_contacts d
		JOIN tenant_contacts a
		  ON a.doc_type = d.doc_type
		 AND a.doc_number = d.doc_number
		 AND a.type = d.type
		 AND a.active = 1
		 AND a.deleted_at IS NULL
		WHERE d.active = 0
		  AND d.deleted_at IS NULL
		  AND d.is_default_walkin = 0
		  AND d.id != a.id
		  AND (
		    SELECT COUNT(*) FROM tenant_contacts a2
		    WHERE a2.doc_type = d.doc_type AND a2.doc_number = d.doc_number
		      AND a2.type = d.type AND a2.active = 1 AND a2.deleted_at IS NULL
		  ) = 1
	`).Scan(&dups).Error; err != nil {
		return fmt.Errorf("buscar contactos duplicados: %w", err)
	}
	if len(dups) == 0 {
		return nil
	}

	existingTables := make([]string, 0, len(v035LinkedContactTables))
	for _, table := range v035LinkedContactTables {
		if db.Migrator().HasTable(table) {
			existingTables = append(existingTables, table)
		}
	}

	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		for _, d := range dups {
			for _, table := range existingTables {
				if err := tx.Table(table).
					Where("contact_id = ?", d.DuplicateID).
					Update("contact_id", d.ActiveID).Error; err != nil {
					return fmt.Errorf("reasignar contact_id en %s (duplicado %d -> activo %d): %w",
						table, d.DuplicateID, d.ActiveID, err)
				}
			}
			// Soft delete manual (mismo efecto que gorm.DeletedAt): evita importar el struct acá
			// y mantiene el estilo del resto de este paquete (tablas por nombre, no por modelo).
			if err := tx.Table("tenant_contacts").
				Where("id = ?", d.DuplicateID).
				Update("deleted_at", now).Error; err != nil {
				return fmt.Errorf("eliminar contacto duplicado %d: %w", d.DuplicateID, err)
			}
		}
		return nil
	})
}
