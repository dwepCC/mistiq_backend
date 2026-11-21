package service

import (
	"errors"
	"strings"

	"tukifac/internal/exportacion"
	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
)

// prepareExport valida y deja listo lo necesario para una exportación de servicios (0201/0202/0208): la empresa debe tenerla habilitada,
// solo factura o boleta, sin mezclar con detracción ni anticipos, con los datos que exige la operación. Fuerza la afectación 40 (exportación,
// IGV 0) en todas las líneas ANTES de calcular los totales. Devuelve nil si la operación no es una exportación.
func (s *SaleService) prepareExport(input *CreateSaleInput, sunatCode string) (*database.TenantSaleExport, error) {
	op := strings.TrimSpace(input.OperationTypeCode)
	if !salecurrency.IsExportService(op) {
		if input.Export != nil {
			return nil, errors.New("los datos de exportación solo aplican con un tipo de operación de exportación")
		}
		return nil, nil
	}
	if sunatCode != "01" && sunatCode != "03" {
		return nil, errors.New("la exportación de servicios solo aplica a factura (01) o boleta (03)")
	}
	if op == salecurrency.OpExportHospedaje && sunatCode != "01" {
		return nil, errors.New("el hospedaje a no domiciliados (0202) solo se emite en factura: SUNAT no lo admite en boleta")
	}
	var enabled bool
	if err := s.db.Model(&database.TenantCompanyConfig{}).Select("exports_enabled").Limit(1).Scan(&enabled).Error; err != nil {
		return nil, err
	}
	if !enabled {
		return nil, errors.New("la empresa no tiene habilitada la exportación de servicios: actívela en Ajustes → Empresa")
	}
	if input.Detraccion != nil || (input.Prepayment != nil && (input.Prepayment.Emit || len(input.Prepayment.Deductions) > 0)) {
		return nil, errors.New("una exportación no se puede combinar con detracción ni con anticipos")
	}
	var contact *database.TenantContact
	if input.ContactID != nil && *input.ContactID > 0 {
		var c database.TenantContact
		if s.db.First(&c, *input.ContactID).Error == nil {
			contact = &c
		}
	}
	row, err := exportacion.Normalize(op, input.Export, contact)
	if err != nil {
		return nil, err
	}
	// Exportación: sin IGV. Se fuerza aquí para que el cajero no tenga que tocar la afectación de cada línea.
	for i := range input.Items {
		input.Items[i].IgvAffectationType = "40"
		input.Items[i].TaxRate = 0
	}
	return row, nil
}
