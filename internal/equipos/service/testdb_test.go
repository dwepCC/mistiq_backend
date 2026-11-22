package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupEquiposDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.EquipProduct{}, &database.EquipCombo{}, &database.EquipComboItem{}, &database.EquipCustomer{},
		&database.EquipOrder{}, &database.EquipOrderItem{}, &database.EquipPayment{}, &database.EquipPaymentAllocation{},
		&database.EquipCarrier{}, &database.EquipShipment{}, &database.EquipReturn{}, &database.EquipStockMovement{},
		&database.EquipStockPeriod{}, &database.EquipSettings{}, &database.EquipImportBatch{}, &database.SuperAdminUser{},
		&database.EquipWarehouse{}, &database.EquipRequirement{}, &database.EquipRequirementItem{}, &database.EquipTransfer{}, &database.EquipTransferItem{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}
