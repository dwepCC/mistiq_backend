package handler

import (
	"strconv"

	"tukifac/internal/users/service"
	"tukifac/pkg/branch"

	"github.com/gofiber/fiber/v3"
)

// GET /api/users?q=&role_id=
func (h *UserHandler) ListAPI(c fiber.Ctx) error {
	db := tenantDB(c)
	svc := service.NewUserService(db)
	roleID, _ := strconv.ParseUint(c.Query("role_id"), 10, 32)
	users, err := svc.List(service.UserListParams{Query: c.Query("q"), RoleID: uint(roleID)})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	roleNames := make(map[uint]string)
	branchNames := make(map[uint]string)
	{
		var roles []struct { ID uint; Name string }
		db.Model(&struct { ID uint; Name string }{}).Table("tenant_roles").Find(&roles)
		for _, r := range roles {
			roleNames[r.ID] = r.Name
		}
		var branches []struct { ID uint; Name string }
		db.Model(&struct { ID uint; Name string }{}).Table("tenant_branches").Find(&branches)
		for _, b := range branches {
			branchNames[b.ID] = b.Name
		}
	}
	type UserOut struct {
		ID             uint     `json:"id"`
		Name           string   `json:"name"`
		Email          string   `json:"email"`
		RoleID         uint     `json:"role_id"`
		RoleName       string   `json:"role_name"`
		BranchID       *uint    `json:"branch_id"`
		BranchName     string   `json:"branch_name"`
		BranchIDs      []uint   `json:"branch_ids,omitempty"`
		BranchNames    []string `json:"branch_names,omitempty"`
		Active         bool     `json:"active"`
		RoleEditLocked bool     `json:"role_edit_locked"`
	}
	out := make([]UserOut, 0, len(users))
	for _, u := range users {
		ro := UserOut{ID: u.ID, Name: u.Name, Email: u.Email, RoleID: u.RoleID, BranchID: u.BranchID, Active: u.Active}
		ro.RoleName = roleNames[u.RoleID]
		if locked, err := svc.TenantRoleEditLocked(u.ID, u.RoleID); err == nil {
			ro.RoleEditLocked = locked
		}
		if u.BranchID != nil {
			ro.BranchName = branchNames[*u.BranchID]
		}
		if ids, err := branch.ResolveDisplayBranchIDs(db, u.ID, u.HomeBranchID, u.BranchID); err == nil && len(ids) > 0 {
			ro.BranchIDs = ids
			names := make([]string, 0, len(ids))
			for _, id := range ids {
				if n, ok := branchNames[id]; ok {
					names = append(names, n)
				}
			}
			ro.BranchNames = names
		}
		out = append(out, ro)
	}
	return c.JSON(fiber.Map{"data": out})
}

// GET /api/users/:id
func (h *UserHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	u, err := service.NewUserService(tenantDB(c)).GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Usuario no encontrado"})
	}
	data := fiber.Map{
		"id": u.ID, "name": u.Name, "email": u.Email,
		"role_id": u.RoleID, "branch_id": u.BranchID, "active": u.Active,
	}
	if locked, err := service.NewUserService(tenantDB(c)).TenantRoleEditLocked(u.ID, u.RoleID); err == nil {
		data["role_edit_locked"] = locked
	}
	if ids, err := branch.ResolveDisplayBranchIDs(tenantDB(c), u.ID, u.HomeBranchID, u.BranchID); err == nil && len(ids) > 0 {
		data["branch_ids"] = ids
	}
	return c.JSON(fiber.Map{"data": data})
}

// POST /api/users
func (h *UserHandler) CreateAPI(c fiber.Ctx) error {
	var input service.CreateUserInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	u, err := service.NewUserService(tenantDB(c)).Create(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": fiber.Map{
		"id": u.ID, "name": u.Name, "email": u.Email,
		"role_id": u.RoleID, "branch_id": u.BranchID,
	}})
}

// PUT /api/users/:id
func (h *UserHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var input service.UpdateUserInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := service.NewUserService(tenantDB(c)).Update(uint(id), input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/users/:id
func (h *UserHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewUserService(tenantDB(c)).Delete(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/users/:id/toggle — activar/desactivar
func (h *UserHandler) ToggleAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewUserService(tenantDB(c)).ToggleActive(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Roles ──────────────────────────────────────────────────────────────────

// GET /api/roles
func (h *RoleHandler) ListAPI(c fiber.Ctx) error {
	roles, err := service.NewRoleService(tenantDB(c)).List()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": roles})
}

// GET /api/roles/:id
func (h *RoleHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	roleSvc := service.NewRoleService(tenantDB(c))
	role, err := roleSvc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Rol no encontrado"})
	}
	perms, _ := roleSvc.RolePermissions(uint(id))
	return c.JSON(fiber.Map{"data": role, "permission_ids": perms})
}

// POST /api/roles
func (h *RoleHandler) CreateAPI(c fiber.Ctx) error {
	var body struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		PermissionIDs []uint `json:"permission_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	roleSvc := service.NewRoleService(tenantDB(c))
	role, err := roleSvc.Create(body.Name, body.Description)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if len(body.PermissionIDs) > 0 {
		_ = roleSvc.SetRolePermissions(role.ID, body.PermissionIDs)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"success": true, "data": role})
}

// PUT /api/roles/:id
func (h *RoleHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		PermissionIDs []uint `json:"permission_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	roleSvc := service.NewRoleService(tenantDB(c))
	if err := roleSvc.Update(uint(id), body.Name, body.Description); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if len(body.PermissionIDs) > 0 {
		_ = roleSvc.SetRolePermissions(uint(id), body.PermissionIDs)
	}
	return c.JSON(fiber.Map{"success": true})
}

// DELETE /api/roles/:id
func (h *RoleHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewRoleService(tenantDB(c)).Delete(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/permissions — lista todos los permisos disponibles
func (h *RoleHandler) ListPermissionsAPI(c fiber.Ctx) error {
	perms, err := service.NewRoleService(tenantDB(c)).AllPermissions()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": perms})
}
