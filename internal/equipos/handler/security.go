package handler

import (
	"strconv"

	consultasvc "tukifac/internal/consulta/service"
	"tukifac/internal/equipos/service"

	"github.com/gofiber/fiber/v3"
)

// requirePin exige el PIN de seguridad (cabecera X-Security-Pin) para acciones sensibles: editar un pedido confirmado,
// anular, revertir cobros, ajustar stock y abrir/cerrar períodos. Devuelve true si se puede continuar.
func requirePin(c fiber.Ctx) bool {
	if err := svc().VerifyPin(c.Get("X-Security-Pin"), userID(c)); err != nil {
		_ = fail(c, err)
		return false
	}
	return true
}

// SetSecurityPin POST /equipos/settings/pin {pin, current_pin}
func (h *Handler) SetSecurityPin(c fiber.Ctx) error {
	var b struct {
		Pin        string `json:"pin"`
		CurrentPin string `json:"current_pin"`
	}
	if err := c.Bind().JSON(&b); err != nil {
		return badBody(c)
	}
	if err := svc().SetSecurityPin(b.Pin, b.CurrentPin, userID(c)); err != nil {
		return fail(c, err)
	}
	audit(c, "equip_security_pin_changed", "equip_settings", 1, nil)
	return c.JSON(fiber.Map{"success": true})
}

type lookupResult struct {
	Success    bool   `json:"success"`
	Name       string `json:"name"`
	DocNumber  string `json:"doc_number"`
	Address    string `json:"address,omitempty"`
	Department string `json:"department,omitempty"`
	Province   string `json:"province,omitempty"`
	District   string `json:"district,omitempty"`
	Status     string `json:"status,omitempty"`
	Condition  string `json:"condition,omitempty"`
}

// Lookup GET /equipos/lookup/:type?number= — consulta RUC o DNI en apiperu.dev (token de ajustes centrales).
func (h *Handler) Lookup(c fiber.Ctx) error {
	num := c.Query("number")
	cs := consultasvc.NewConsultaService()
	switch c.Params("type") {
	case "dni":
		r, err := cs.ConsultaDNI(num)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": lookupResult{Success: r.Success, Name: r.NombreCompleto, DocNumber: r.DocNumber}})
	case "ruc":
		r, err := cs.ConsultaRUC(num)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		addr := r.DireccionCompleta
		if addr == "" {
			addr = r.Direccion
		}
		return c.JSON(fiber.Map{"data": lookupResult{Success: r.Success, Name: r.RazonSocial, DocNumber: r.RUC, Address: addr,
			Department: r.Departamento, Province: r.Provincia, District: r.Distrito, Status: r.Estado, Condition: r.Condicion}})
	}
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "tipo de consulta inválido (dni o ruc)"})
}

var _ = strconv.Itoa
var _ service.Service
