package handler

import (
	"errors"
	"strings"

	"tukifac/internal/restaurant/staff"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// GET /api/restaurant/staff/management — usuarios + perfiles (Ajustes Tukichef)
func (h *RestaurantHandler) ListStaffManagement(c fiber.Ctx) error {
	onlyDeleted := c.Query("deleted") == "1" || c.Query("deleted") == "true"
	list, err := staff.New(db(c)).ListStaffManagement(onlyDeleted)
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
		BranchIDs    []uint `json:"branch_ids"`
		// ReplaceDeleted: el correo es de un usuario eliminado y se quiere crear uno nuevo.
		ReplaceDeleted bool `json:"replace_deleted"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Pin != "" {
		if err := staff.ValidatePINFormat(body.Pin); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	item, err := staff.New(db(c)).WithTenant(middleware.TenantIDFromClaims(c)).CreateStaffUser(staff.CreateStaffUserInput{
		Name: body.Name, Email: body.Email, Phone: body.Phone,
		EmployeeType: body.EmployeeType, Pin: body.Pin,
		StaffCode: body.StaffCode, DisplayName: body.DisplayName,
		BranchIDs: body.BranchIDs, ReplaceDeleted: body.ReplaceDeleted,
	})
	if err != nil {
		if middleware.RespondQuotaError(c, err) {
			return nil
		}
		var de *database.DeletedUserEmailError
		if errors.As(err, &de) {
			return c.Status(409).JSON(fiber.Map{
				"error": de.Error(),
				"code":  "user_deleted_exists",
				"deleted_user": fiber.Map{
					"id": de.UserID, "name": de.Name, "email": de.Email,
					"role_name": de.RoleName, "deleted_at": de.DeletedAt,
					// Solo los usuarios operativos se restauran desde Tukichef; los del ERP, no.
					"restorable_here": staff.IsOperativeRestaurantRole(de.RoleName),
				},
			})
		}
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"success": true, "data": item})
}

// POST /api/restaurant/staff/users/:id/restore — restaurar un usuario operativo eliminado
func (h *RestaurantHandler) RestoreStaffUser(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var body struct {
		Name         string `json:"name"`
		Phone        string `json:"phone"`
		EmployeeType string `json:"employee_type"`
		Pin          string `json:"pin"`
		StaffCode    string `json:"staff_code"`
		DisplayName  string `json:"display_name"`
		BranchIDs    []uint `json:"branch_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
	}
	item, err := staff.New(db(c)).WithTenant(middleware.TenantIDFromClaims(c)).RestoreStaffUser(id, staff.CreateStaffUserInput{
		Name: body.Name, Phone: body.Phone, EmployeeType: body.EmployeeType, Pin: body.Pin,
		StaffCode: body.StaffCode, DisplayName: body.DisplayName, BranchIDs: body.BranchIDs,
	})
	if err != nil {
		if middleware.RespondQuotaError(c, err) {
			return nil
		}
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "data": item})
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
		BranchIDs      []uint `json:"branch_ids"`
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
	if len(body.BranchIDs) > 0 {
		if err := svc.AssignUserBranches(id, body.BranchIDs); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	hasPin := false
	if st, err := svc.GetStaffByUserID(id); err == nil && st != nil {
		hasPin = strings.TrimSpace(st.PinHash) != ""
	}
	return c.JSON(fiber.Map{"success": true, "has_pin": hasPin})
}
