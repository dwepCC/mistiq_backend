package service

import "testing"

// Estos checks corren antes de tocar la BD (validación pura), así que un TenantService sin
// conexión real basta para probarlos.
func TestTenantServiceCreate_RequiresAddressAndUbigeo(t *testing.T) {
	svc := &TenantService{}
	base := CreateTenantInput{
		Name:          "Empresa Test",
		AdminEmail:    "admin@test.com",
		AdminPassword: "secret123",
	}

	t.Run("rejects missing address", func(t *testing.T) {
		in := base
		in.Ubigeo = "150101"
		_, err := svc.Create(in)
		if err == nil || err.Error() != "la dirección es requerida" {
			t.Fatalf("expected address-required error, got %v", err)
		}
	})

	t.Run("rejects blank address (solo espacios)", func(t *testing.T) {
		in := base
		in.Address = "   "
		in.Ubigeo = "150101"
		_, err := svc.Create(in)
		if err == nil || err.Error() != "la dirección es requerida" {
			t.Fatalf("expected address-required error, got %v", err)
		}
	})

	t.Run("rejects missing ubigeo", func(t *testing.T) {
		in := base
		in.Address = "Av. Siempre Viva 123"
		_, err := svc.Create(in)
		if err == nil || err.Error() != "debe seleccionar departamento, provincia y distrito" {
			t.Fatalf("expected ubigeo-required error, got %v", err)
		}
	})
}
