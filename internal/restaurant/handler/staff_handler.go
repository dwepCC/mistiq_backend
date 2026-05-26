package handler

import (
	"strings"

	"tukifac/internal/restaurant/staff"

	"github.com/gofiber/fiber/v3"
)

// GET /api/restaurant/staff/management — usuarios + perfiles (Ajustes Tukichef)
func (h *RestaurantHandler) ListStaffManagement(c fiber.Ctx) error {
	list, err := staff.New(db(c)).ListStaffManagement()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// GET /api/restaurant/staff
func (h *RestaurantHandler) ListStaff(c fiber.Ctx) error {
	list, err := staff.New(db(c)).ListStaff()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// POST /api/restaurant/staff/users — crear usuario operativo Tukichef
func (h *RestaurantHandler) CreateStaffUser(c fiber.Ctx) error {
	var body struct {
		Name         string `json:"name"`
		Email        string `json:"email"`
		Phone        string `json:"phone"`
		EmployeeType string `json:"employee_type"`
		Pin          string `json:"pin"`
		StaffCode    string `json:"staff_code"`
		DisplayName  string `json:"display_name"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Pin != "" {
		if err := staff.ValidatePINFormat(body.Pin); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	item, err := staff.New(db(c)).CreateStaffUser(staff.CreateStaffUserInput{
		Name: body.Name, Email: body.Email, Phone: body.Phone,
		EmployeeType: body.EmployeeType, Pin: body.Pin,
		StaffCode: body.StaffCode, DisplayName: body.DisplayName,
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": item})
}

// PUT /api/restaurant/users/:id/staff
func (h *RestaurantHandler) UpsertUserStaff(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		EmployeeType   string `json:"employee_type"`
		Pin            string `json:"pin"`
		ClearPin       bool   `json:"clear_pin"`
		StaffCode      string `json:"staff_code"`
		DisplayName    string `json:"display_name"`
		CanCharge      bool   `json:"can_charge"`
		CanDiscount    bool   `json:"can_discount"`
		CanOpenTable   bool   `json:"can_open_table"`
		KitchenAccess  bool   `json:"kitchen_access"`
		DeliveryAccess bool   `json:"delivery_access"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Pin != "" {
		if err := staff.ValidatePINFormat(body.Pin); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	flags := staff.UpsertFlags{
		DisplayName: body.DisplayName, StaffCode: body.StaffCode,
		CanCharge: body.CanCharge, CanDiscount: body.CanDiscount,
		CanOpenTable: body.CanOpenTable, KitchenAccess: body.KitchenAccess,
		DeliveryAccess: body.DeliveryAccess, ClearPin: body.ClearPin,
	}
	svc := staff.New(db(c))
	if err := svc.UpsertStaffForUser(id, body.EmployeeType, body.Pin, flags); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	hasPin := false
	if st, err := svc.GetStaffByUserID(id); err == nil && st != nil {
		hasPin = strings.TrimSpace(st.PinHash) != ""
	}
	return c.JSON(fiber.Map{"success": true, "has_pin": hasPin})
}
