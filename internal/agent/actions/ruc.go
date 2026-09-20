package actions

import (
	"context"
	"encoding/json"
	"fmt"

	consultaService "tukifac/internal/consulta/service"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/database"
)

type ValidateRUC struct {
	Consulta *consultaService.ConsultaService
}

func NewValidateRUC(consulta *consultaService.ConsultaService) *ValidateRUC {
	return &ValidateRUC{Consulta: consulta}
}

func (ValidateRUC) Name() string { return "commercial_validate_ruc" }
func (ValidateRUC) Description() string {
	return "Consulta un RUC contra SUNAT y devuelve razón social, estado, condición y ubigeo. Úsala para validar el RUC del negocio antes de registrar un lead o crear una cuenta — nunca inventes estos datos."
}
func (ValidateRUC) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["ruc"],"properties":{"ruc":{"type":"string","description":"RUC de 11 dígitos"}}}`)
}
func (ValidateRUC) Meta() actions.Meta { return actions.Meta{ReadOnly: true, Idempotent: true} }

type validateRUCArgs struct {
	RUC string `json:"ruc"`
}

func (a *ValidateRUC) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args validateRUCArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("ruc", args.RUC); err != nil {
		return actions.Result{}, err
	}

	res, err := a.Consulta.ConsultaRUC(args.RUC)
	if err != nil {
		return actions.Result{}, fmt.Errorf("consultar RUC en SUNAT: %w", err)
	}
	if !res.Success {
		return actions.Result{Message: "No se encontró información para ese RUC en SUNAT. Verifica el número con el cliente."}, nil
	}

	msg := fmt.Sprintf(
		"RUC %s: %s. Estado: %s, condición: %s. Ubicación: %s, %s, %s (ubigeo %s).",
		res.RUC, res.RazonSocial, res.Estado, res.Condicion, res.Distrito, res.Provincia, res.Departamento, res.Ubigeo,
	)
	return actions.Result{Message: msg}, nil
}

type CheckRUCHasAccount struct{}

func NewCheckRUCHasAccount() *CheckRUCHasAccount { return &CheckRUCHasAccount{} }

func (CheckRUCHasAccount) Name() string { return "commercial_check_ruc_has_account" }
func (CheckRUCHasAccount) Description() string {
	return "Verifica si un RUC ya tiene una cuenta de Mistiq registrada. Úsala antes de ofrecer crear una cuenta nueva, para no duplicar ni confundir a un cliente que ya es tenant."
}
func (CheckRUCHasAccount) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["ruc"],"properties":{"ruc":{"type":"string"}}}`)
}
func (CheckRUCHasAccount) Meta() actions.Meta { return actions.Meta{ReadOnly: true, Idempotent: true} }

func (a *CheckRUCHasAccount) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args validateRUCArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("ruc", args.RUC); err != nil {
		return actions.Result{}, err
	}

	var count int64
	if err := database.CentralDB.WithContext(ctx).Unscoped().
		Model(&database.Tenant{}).Where("ruc = ?", args.RUC).Count(&count).Error; err != nil {
		return actions.Result{}, fmt.Errorf("verificar RUC en cuentas existentes: %w", err)
	}
	if count > 0 {
		return actions.Result{Message: "Ese RUC ya tiene una cuenta de Mistiq registrada. No ofrezcas crear una nueva; deriva a un asesor si el cliente necesita ayuda con su cuenta existente."}, nil
	}
	return actions.Result{Message: "Ese RUC no tiene ninguna cuenta de Mistiq registrada todavía."}, nil
}
