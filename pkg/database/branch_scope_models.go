package database

import (
	"tukifac/pkg/branchscope"

	"gorm.io/gorm"
)

// BranchScopedModels son las entidades con branch_id PROPIO que el scope global de sucursal (pkg/branchscope) restringe.
//
// Criterio: pertenencia exclusiva a una sucursal. NO se incluyen:
//   - entidades globales del tenant (contactos, marcas, métodos de pago, bancos, configuración, roles);
//   - categorías: tienen branch_id (V192, NULL = todas) pero NO se filtran con este scope global — la regla propia (las de todas
//     más las de la sucursal) vive en pkg/categorybranch y el administrador necesita ver todas;
//   - TenantProduct (catálogo compartido; su visibilidad va por tenant_product_branches y TenantProduct.BranchID es legacy);
//   - precios por sucursal y el puente de visibilidad de productos (configuración que administran quienes ven el catálogo);
//   - transferencias (dos sucursales: origen y destino) y usuarios/asignaciones;
//   - entidades que heredan la sucursal de otra (ítems y pagos de venta, comprobantes, comandas, movimientos de caja...):
//     se protegen por su entidad padre en los handlers (internal/sales/branchguard).
func BranchScopedModels() []interface{} {
	return []interface{}{
		&TenantDocumentSeries{},
		&TenantProductSerial{},
		&TenantProductStock{},
		&TenantProductPresentationStock{},
		&TenantStockMovement{},
		&TenantInventoryDocument{},
		&TenantSale{},
		&TenantQuotation{},
		&TenantDespatch{},
		&TenantPurchase{},
		&TenantPurchaseOrder{},
		&TenantCashSession{},
		&TenantRestaurantFloor{},
		&TenantRestaurantTable{},
		&TenantTableSession{},
		&TenantBranchDailyComandaCounter{},
		&TenantMembership{},
	}
}

// installBranchScope registra el scope de sucursal en un pool de tenant recién abierto.
func installBranchScope(db *gorm.DB) error {
	return branchscope.Install(db, BranchScopedModels()...)
}
