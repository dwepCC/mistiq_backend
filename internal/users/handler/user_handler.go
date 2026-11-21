package handler

import (
	"strconv"

	"tukifac/internal/users/service"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type UserHandler struct{}

func NewUserHandler() *UserHandler { return &UserHandler{} }

func tenantDB(c fiber.Ctx) *gorm.DB {
	db, _ := c.Locals("tenantDB").(*gorm.DB)
	return db
}

func userEmail(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}

// UserRow es el DTO aplanado para la vista de lista de usuarios.
type UserRow struct {
	ID         uint
	Name       string
	Email      string
	RoleName   string
	BranchName string
	IsActive   bool
}

func (h *UserHandler) ListPage(c fiber.Ctx) error {
	db := tenantDB(c)
	svc := service.NewUserService(db)
	roleSvc := service.NewRoleService(db)

	users, _ := svc.List(service.UserListParams{Query: c.Query("q")})
	roles, _ := roleSvc.List()

	// Índices rápidos
	roleMap := make(map[uint]string)
	for _, r := range roles {
		roleMap[r.ID] = r.Name
	}

	var branches []database.TenantBranch
	db.Where("active = ?", true).Find(&branches)
	branchMap := make(map[uint]string)
	for _, b := range branches {
		branchMap[b.ID] = b.Name
	}

	// Aplanar datos para la vista
	rows := make([]UserRow, 0, len(users))
	for _, u := range users {
		row := UserRow{
			ID:       u.ID,
			Name:     u.Name,
			Email:    u.Email,
			RoleName: roleMap[u.RoleID],
			IsActive: u.Active,
		}
		if u.BranchID != nil {
			row.BranchName = branchMap[*u.BranchID]
		}
		rows = append(rows, row)
	}

	return c.Render("users/index", fiber.Map{
		"Title":     "Usuarios",
		"UserEmail": userEmail(c),
		"Users":     rows,
		"Roles":     roles,
		"Query":     c.Query("q"),
		"Success":   c.Query("success"),
	}, "layouts/base")
}

func (h *UserHandler) NewPage(c fiber.Ctx) error {
	db := tenantDB(c)
	roles, _ := service.NewRoleService(db).List()
	var branches []database.TenantBranch
	db.Where("active = ?", true).Find(&branches)

	return c.Render("users/form", fiber.Map{
		"Title":     "Nuevo Usuario",
		"UserEmail": userEmail(c),
		"Roles":     roles,
		"Branches":  branches,
		"IsEdit":    false,
		"User":      database.TenantUser{},
	}, "layouts/base")
}

func (h *UserHandler) CreateForm(c fiber.Ctx) error {
	db := tenantDB(c)
	svc := service.NewUserService(db)

	roleID, _ := strconv.ParseUint(c.FormValue("role_id"), 10, 32)
	var branchID *uint
	if bid, err := strconv.ParseUint(c.FormValue("branch_id"), 10, 32); err == nil && bid > 0 {
		v := uint(bid)
		branchID = &v
	}

	input := service.CreateUserInput{
		RoleID:   uint(roleID),
		BranchID: branchID,
		Name:     c.FormValue("name"),
		Email:    c.FormValue("email"),
		Password: c.FormValue("password"),
		Phone:    c.FormValue("phone"),
		Active:   c.FormValue("active") == "1",
	}

	if _, err := svc.Create(input); err != nil {
		roles, _ := service.NewRoleService(db).List()
		var branches []database.TenantBranch
		db.Where("active = ?", true).Find(&branches)
		return c.Render("users/form", fiber.Map{
			"Title":     "Nuevo Usuario",
			"UserEmail": userEmail(c),
			"Roles":     roles,
			"Branches":  branches,
			"IsEdit":    false,
			"Error":     err.Error(),
			"User": database.TenantUser{
				Name:  input.Name,
				Email: input.Email,
				Phone: input.Phone,
			},
		}, "layouts/base")
	}

	return c.Redirect().To("/users?success=created")
}

func (h *UserHandler) EditPage(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	db := tenantDB(c)
	user, err := service.NewUserService(db).GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Usuario no encontrado")
	}
	roles, _ := service.NewRoleService(db).List()
	var branches []database.TenantBranch
	db.Where("active = ?", true).Find(&branches)

	return c.Render("users/form", fiber.Map{
		"Title":     "Editar Usuario",
		"UserEmail": userEmail(c),
		"IsEdit":    true,
		"User":      user,
		"Roles":     roles,
		"Branches":  branches,
	}, "layouts/base")
}

func (h *UserHandler) UpdateForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	db := tenantDB(c)
	svc := service.NewUserService(db)

	roleID, _ := strconv.ParseUint(c.FormValue("role_id"), 10, 32)
	var branchID *uint
	if bid, err2 := strconv.ParseUint(c.FormValue("branch_id"), 10, 32); err2 == nil && bid > 0 {
		v := uint(bid)
		branchID = &v
	}

	input := service.UpdateUserInput{
		RoleID:   uint(roleID),
		BranchID: branchID,
		Name:     c.FormValue("name"),
		Email:    c.FormValue("email"),
		Password: c.FormValue("password"),
		Phone:    c.FormValue("phone"),
		Active:   c.FormValue("active") == "1",
	}

	if err := svc.Update(uint(id), input); err != nil {
		roles, _ := service.NewRoleService(db).List()
		var branches []database.TenantBranch
		db.Where("active = ?", true).Find(&branches)
		existingUser, _ := service.NewUserService(db).GetByID(uint(id))
		return c.Render("users/form", fiber.Map{
			"Title":     "Editar Usuario",
			"UserEmail": userEmail(c),
			"IsEdit":    true,
			"Error":     err.Error(),
			"Roles":     roles,
			"Branches":  branches,
			"User":      existingUser,
		}, "layouts/base")
	}

	return c.Redirect().To("/users?success=updated")
}

func (h *UserHandler) ToggleForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	service.NewUserService(tenantDB(c)).ToggleActive(uint(id))
	return c.Redirect().To("/users?success=updated")
}

func (h *UserHandler) DeleteForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	if err := service.NewUserService(tenantDB(c)).Delete(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/users?success=deleted")
}
