package prepayment

import (
	"fmt"
	"strings"

	"tukifac/pkg/tax"
)

// AffectationGroupFromItem infiere el grupo IGV de un ítem según catálogo N°07.
func AffectationGroupFromItem(igvAffectationType string) string {
	aff := strings.TrimSpace(igvAffectationType)
	if aff == "" {
		aff = "10"
	}
	if strings.HasPrefix(aff, "2") {
		return AffectationExonerado
	}
	if strings.HasPrefix(aff, "3") {
		return AffectationInafecto
	}
	if tax.IsGravado(aff) {
		return AffectationGravado
	}
	return AffectationGravado
}

// ValidateItemsAffectationGroup verifica que todos los ítems pertenezcan al grupo declarado.
func ValidateItemsAffectationGroup(declaredGroup string, itemAffs []string) error {
	group := strings.TrimSpace(declaredGroup)
	if !IsValidAffectationGroup(group) {
		return fmt.Errorf("grupo de afectación de anticipo no válido: use gravado, exonerado o inafecto")
	}
	if len(itemAffs) == 0 {
		return fmt.Errorf("el comprobante de anticipo requiere al menos un ítem")
	}
	for _, aff := range itemAffs {
		if AffectationGroupFromItem(aff) != group {
			return fmt.Errorf("todos los ítems deben tener la misma afectación IGV que el anticipo (%s)", group)
		}
	}
	return nil
}
