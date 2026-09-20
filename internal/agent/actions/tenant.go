package actions

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	consultaService "tukifac/internal/consulta/service"
	superadminService "tukifac/internal/superadmin/service"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/cronlock"
	"tukifac/pkg/database"

	"time"
)

// rucLockTTL: TTL del lock por RUC compartido entre commercial_create_trial_tenant
// y commercial_create_tenant_and_subscribe — MISMA clave para ambas, a
// propósito: cierra la ventana de doble alta si el cliente dispara los dos
// caminos casi al mismo tiempo (p. ej. dos mensajes seguidos).
const rucLockTTL = 30 * time.Second

func rucLockKey(ruc string) string { return "assistant:ruc:" + ruc }

type createTenantArgs struct {
	Name     string `json:"name"`
	RUC      string `json:"ruc"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	PlanName string `json:"plan_name"`
	// Address/Ubigeo: SOLO como respaldo si la consulta a SUNAT no trae
	// datos completos — por defecto se usan los de SUNAT, no lo que el
	// modelo haya podido inferir mal.
	Address string `json:"address"`
	Ubigeo  string `json:"ubigeo"`
}

func decodeCreateTenantArgs(raw json.RawMessage) (createTenantArgs, error) {
	var args createTenantArgs
	if err := decodeArgs(raw, &args); err != nil {
		return args, err
	}
	if err := requireNonEmpty("ruc", args.RUC); err != nil {
		return args, err
	}
	if err := requireNonEmpty("email", args.Email); err != nil {
		return args, err
	}
	if err := requireNonEmpty("phone", args.Phone); err != nil {
		return args, err
	}
	if err := requireNonEmpty("plan_name", args.PlanName); err != nil {
		return args, err
	}
	return args, nil
}

// createTenantForChat valida el plan, re-consulta el RUC en SUNAT para
// resolver nombre/dirección/ubigeo reales (no confía en lo que el modelo
// haya podido pasar), genera una contraseña, y crea el tenant vía el mismo
// TenantService que usa el panel de superadmin humano — sin lógica
// paralela. Bajo el lock compartido por RUC (rucLockKey).
func createTenantForChat(
	ctx context.Context,
	consulta *consultaService.ConsultaService,
	tenants *superadminService.TenantService,
	args createTenantArgs,
	months int,
) (*database.Tenant, string, error) {
	ruc := strings.TrimSpace(args.RUC)

	release, acquired := cronlock.TryAcquire(rucLockKey(ruc), rucLockTTL)
	if !acquired {
		return nil, "", fmt.Errorf("ya hay una alta en curso para este RUC, espera un momento y vuelve a intentar")
	}
	defer release()

	var count int64
	if err := database.CentralDB.WithContext(ctx).Unscoped().
		Model(&database.Tenant{}).Where("ruc = ?", ruc).Count(&count).Error; err != nil {
		return nil, "", fmt.Errorf("verificar RUC existente: %w", err)
	}
	if count > 0 {
		return nil, "", fmt.Errorf("ya existe una cuenta de Mistiq registrada con ese RUC")
	}

	name := strings.TrimSpace(args.Name)
	address := strings.TrimSpace(args.Address)
	ubigeo := strings.TrimSpace(args.Ubigeo)

	if res, err := consulta.ConsultaRUC(ruc); err == nil && res.Success {
		if name == "" {
			name = res.RazonSocial
		}
		if address == "" {
			address = res.DireccionCompleta
			if address == "" {
				address = res.Direccion
			}
		}
		if ubigeo == "" {
			ubigeo = res.Ubigeo
		}
	}
	if name == "" {
		return nil, "", fmt.Errorf("no se pudo determinar la razón social del RUC; pide el nombre de la empresa al cliente")
	}
	if address == "" || ubigeo == "" {
		return nil, "", fmt.Errorf("no se pudo resolver la dirección/ubigeo del RUC; pide al cliente distrito y dirección exactos")
	}

	password, err := generatePassword()
	if err != nil {
		return nil, "", fmt.Errorf("generar contraseña: %w", err)
	}

	tenant, err := tenants.Create(superadminService.CreateTenantInput{
		Name:               name,
		Email:              args.Email,
		Phone:              args.Phone,
		RUC:                ruc,
		Plan:               args.PlanName,
		Address:            address,
		Ubigeo:             ubigeo,
		AdminEmail:         args.Email,
		AdminPassword:      password,
		SubscriptionMonths: months,
	})
	if err != nil {
		return nil, "", fmt.Errorf("crear la cuenta: %w", err)
	}
	return tenant, password, nil
}

func generatePassword() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// linkLeadToTenant asocia el lead de esta conversación a la cuenta recién
// creada, para que la Bandeja (Fase 3) pueda navegar directo al tenant.
func linkLeadToTenant(ctx context.Context, bc bizctx.Context, tenantID uint, status string) error {
	lead, err := findOrCreateLead(ctx, bc, "lead")
	if err != nil {
		return err
	}
	return database.CentralDB.WithContext(ctx).Model(&database.AssistantLead{}).
		Where("id = ?", lead.ID).
		Updates(map[string]any{"tenant_id": tenantID, "status": status}).Error
}
