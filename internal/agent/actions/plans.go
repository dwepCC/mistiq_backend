package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tukifac/internal/plans/service"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
)

type LookupPlans struct {
	Plans *service.PlanService
}

func NewLookupPlans(plans *service.PlanService) *LookupPlans { return &LookupPlans{Plans: plans} }

func (LookupPlans) Name() string { return "commercial_lookup_plans" }
func (LookupPlans) Description() string {
	return "Devuelve los planes activos de Mistiq con su precio y ciclo de facturación. Úsala cuando el cliente pregunte por precios, planes o qué incluye cada uno — nunca inventes precios."
}
func (LookupPlans) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (LookupPlans) Meta() actions.Meta { return actions.Meta{ReadOnly: true, Idempotent: true} }

func (a *LookupPlans) Execute(ctx context.Context, bc bizctx.Context, args json.RawMessage) (actions.Result, error) {
	list, err := a.Plans.List()
	if err != nil {
		return actions.Result{}, fmt.Errorf("consultar planes: %w", err)
	}
	var b strings.Builder
	found := 0
	for _, p := range list {
		if !p.Active {
			continue
		}
		found++
		fmt.Fprintf(&b, "- %s: S/ %.2f / %s. Módulos: %s.\n", p.Name, p.Price, billingCycleEs(p.BillingCycle), strings.Join(orDash(p.Modules), ", "))
	}
	if found == 0 {
		return actions.Result{Message: "No hay planes activos configurados en este momento."}, nil
	}
	return actions.Result{Message: b.String()}, nil
}

func billingCycleEs(c string) string {
	switch c {
	case "yearly":
		return "año"
	case "lifetime":
		return "pago único"
	default:
		return "mes"
	}
}

func orDash(items []string) []string {
	if len(items) == 0 {
		return []string{"—"}
	}
	return items
}
