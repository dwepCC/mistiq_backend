package handler

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/internal/contacts/service"
	"tukifac/pkg/middleware"
	"tukifac/pkg/pagination"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/uploadlimits"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ContactHandler struct{}

func NewContactHandler() *ContactHandler { return &ContactHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}
func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}

func (h *ContactHandler) ListPage(c fiber.Ctx) error {
	svc := service.NewContactService(db(c))
	contacts, _ := svc.List(service.ContactListParams{
		Query: c.Query("q"),
		Type:  c.Query("type"),
	})
	return c.Render("contacts/index", fiber.Map{
		"Title":     "Clientes y Proveedores",
		"UserEmail": email(c),
		"Contacts":  contacts,
		"Query":     c.Query("q"),
		"Type":      c.Query("type"),
		"Success":   c.Query("success"),
	}, "layouts/base")
}

func (h *ContactHandler) NewPage(c fiber.Ctx) error {
	return c.Render("contacts/form", fiber.Map{
		"Title":     "Nuevo Contacto",
		"UserEmail": email(c),
		"IsEdit":    false,
		"TypeParam": c.Query("type", "customer"),
	}, "layouts/base")
}

func (h *ContactHandler) CreateForm(c fiber.Ctx) error {
	svc := service.NewContactService(db(c))
	input := service.ContactInput{
		Type:          c.FormValue("type"),
		DocType:       c.FormValue("doc_type"),
		DocNumber:     c.FormValue("doc_number"),
		BusinessName:  c.FormValue("business_name"),
		TradeName:     c.FormValue("trade_name"),
		Address:       c.FormValue("address"),
		Phone:         c.FormValue("phone"),
		Email:         c.FormValue("email"),
		ContactPerson: c.FormValue("contact_person"),
		Notes:         c.FormValue("notes"),
	}
	if _, err := svc.Create(input); err != nil {
		return c.Render("contacts/form", fiber.Map{
			"Title":     "Nuevo Contacto",
			"UserEmail": email(c),
			"IsEdit":    false,
			"Error":     err.Error(),
			"Input":     input,
		}, "layouts/base")
	}
	return c.Redirect().To("/contacts?success=created")
}

func (h *ContactHandler) EditPage(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	contact, err := service.NewContactService(db(c)).GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Contacto no encontrado")
	}
	return c.Render("contacts/form", fiber.Map{
		"Title":     "Editar Contacto",
		"UserEmail": email(c),
		"IsEdit":    true,
		"Contact":   contact,
	}, "layouts/base")
}

func (h *ContactHandler) UpdateForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	svc := service.NewContactService(db(c))
	input := service.ContactInput{
		Type:          c.FormValue("type"),
		DocType:       c.FormValue("doc_type"),
		DocNumber:     c.FormValue("doc_number"),
		BusinessName:  c.FormValue("business_name"),
		TradeName:     c.FormValue("trade_name"),
		Address:       c.FormValue("address"),
		Phone:         c.FormValue("phone"),
		Email:         c.FormValue("email"),
		ContactPerson: c.FormValue("contact_person"),
		Notes:         c.FormValue("notes"),
	}
	if err := svc.Update(uint(id), input); err != nil {
		contact, _ := svc.GetByID(uint(id))
		return c.Render("contacts/form", fiber.Map{
			"Title":     "Editar Contacto",
			"UserEmail": email(c),
			"IsEdit":    true,
			"Contact":   contact,
			"Error":     err.Error(),
		}, "layouts/base")
	}
	return c.Redirect().To("/contacts?success=updated")
}

func (h *ContactHandler) DeleteForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	if err := service.NewContactService(db(c)).Delete(uint(id)); err != nil {
		return c.Redirect().To("/contacts?error=" + url.QueryEscape(err.Error()))
	}
	return c.Redirect().To("/contacts?success=deleted")
}

type contactPersonBody struct {
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	Relationship string `json:"relationship"`
}

type contactBody struct {
	Type                            string              `json:"type"`
	DocType                         string              `json:"doc_type"`
	DocNumber                       string              `json:"doc_number"`
	BusinessName                    string              `json:"business_name"`
	TradeName                       string              `json:"trade_name"`
	Address                         string              `json:"address"`
	Ubigeo                          string              `json:"ubigeo"`
	Country                         string              `json:"country"`
	Phone                           string              `json:"phone"`
	Email                           string              `json:"email"`
	PhotoURL                        string              `json:"photo_url"`
	ContactPersons                  []contactPersonBody `json:"contact_persons"`
	EsAgenteDeRetencion             *bool               `json:"es_agente_de_retencion"`
	EsAgenteDePercepcion            *bool               `json:"es_agente_de_percepcion"`
	EsAgenteDePercepcionCombustible *bool               `json:"es_agente_de_percepcion_combustible"`
	EsBuenContribuyente             *bool               `json:"es_buen_contribuyente"`
}

func bodyToInput(b contactBody) service.ContactInput {
	persons := make([]service.ContactPersonInput, 0, len(b.ContactPersons))
	for _, p := range b.ContactPersons {
		persons = append(persons, service.ContactPersonInput{
			Name:         p.Name,
			Phone:        p.Phone,
			Email:        p.Email,
			Relationship: p.Relationship,
		})
	}
	return service.ContactInput{
		Type:                            b.Type,
		DocType:                         b.DocType,
		DocNumber:                       b.DocNumber,
		BusinessName:                    b.BusinessName,
		TradeName:                       b.TradeName,
		Address:                         b.Address,
		Ubigeo:                          b.Ubigeo,
		Country:                         b.Country,
		Phone:                           b.Phone,
		Email:                           b.Email,
		PhotoURL:                        b.PhotoURL,
		ContactPersons:                  persons,
		EsAgenteDeRetencion:             b.EsAgenteDeRetencion,
		EsAgenteDePercepcion:            b.EsAgenteDePercepcion,
		EsAgenteDePercepcionCombustible: b.EsAgenteDePercepcionCombustible,
		EsBuenContribuyente:             b.EsBuenContribuyente,
	}
}

func (h *ContactHandler) SearchAPI(c fiber.Ctx) error {
	svc := service.NewContactService(db(c))
	params := service.ContactListParams{
		Query:  c.Query("q"),
		Type:   c.Query("type"),
		Status: c.Query("status"),
	}
	scope := middleware.ContactsScopeFrom(c)
	if (params.Type == "customer" && !scope.Customers) || (params.Type == "supplier" && !scope.Suppliers) {
		return contactAccessError(c, fiber.StatusForbidden)
	}
	params.TypesIn = scope.TypesFor()

	// Paginación opt-in: solo cuando el cliente manda per_page. Sin él se devuelve todo, que
	// es lo que necesitan los selectores del POS/ventas (retrocompatible).
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	if perPage > 0 {
		page, _ := strconv.Atoi(c.Query("page"))
		if page < 1 {
			page = 1
		}
		contacts, total, err := svc.ListPaged(params, page, perPage)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{
			"data":        contacts,
			"total":       total,
			"page":        page,
			"per_page":    perPage,
			"total_pages": pagination.TotalPages(total, perPage),
		})
	}

	contacts, _ := svc.List(params)
	return c.JSON(fiber.Map{"data": contacts})
}

func (h *ContactHandler) DefaultClientAPI(c fiber.Ctx) error {
	if !middleware.ContactsScopeFrom(c).Customers {
		return contactAccessError(c, fiber.StatusForbidden)
	}
	contact, err := service.NewContactService(db(c)).GetDefaultClient()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if contact == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No hay cliente por defecto configurado"})
	}
	return c.JSON(fiber.Map{"data": contact})
}

func (h *ContactHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	contact, err := service.NewContactService(db(c)).GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No encontrado"})
	}
	if !middleware.ContactsScopeFrom(c).Allows(contactTypeOf(contact.Type), false) {
		return contactAccessError(c, fiber.StatusForbidden)
	}
	return c.JSON(fiber.Map{"data": contact})
}

func (h *ContactHandler) CreateAPI(c fiber.Ctx) error {
	var body contactBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if !middleware.ContactsScopeFrom(c).Allows(contactTypeOf(body.Type), true) {
		return contactAccessError(c, fiber.StatusForbidden)
	}
	svc := service.NewContactService(db(c))
	contact, err := svc.Create(bodyToInput(body))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": contact})
}

func (h *ContactHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body contactBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	svc := service.NewContactService(db(c))
	if status := scopedContactStatus(c, svc, uint(id), true); status != 0 {
		return contactAccessError(c, status)
	}
	// Cambiar el tipo también exige poder actuar sobre el tipo nuevo.
	if !middleware.ContactsScopeFrom(c).Allows(contactTypeOf(body.Type), true) {
		return contactAccessError(c, fiber.StatusForbidden)
	}
	if err := svc.Update(uint(id), bodyToInput(body)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	contact, _ := svc.GetByID(uint(id))
	return c.JSON(fiber.Map{"data": contact})
}

func (h *ContactHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewContactService(db(c))
	if status := scopedContactStatus(c, svc, uint(id), true); status != 0 {
		return contactAccessError(c, status)
	}
	if err := svc.Delete(uint(id)); err != nil {
		var inUse *service.ContactInUseError
		if errors.As(err, &inUse) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": inUse.Error(), "code": "contact_in_use", "documents": inUse.Documents,
			})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *ContactHandler) ToggleAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewContactService(db(c))
	if status := scopedContactStatus(c, svc, uint(id), true); status != 0 {
		return contactAccessError(c, status)
	}
	if err := svc.ToggleActive(uint(id)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

const maxContactPhotoSize = uploadlimits.MaxFileBytes

// UploadPhotoAPI POST /api/contacts/:id/photo — multipart, campo "image".
func (h *ContactHandler) UploadPhotoAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	svc := service.NewContactService(db(c))
	contact, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "contacto no encontrado"})
	}
	if !middleware.ContactsScopeFrom(c).Allows(contactTypeOf(contact.Type), true) {
		return contactAccessError(c, fiber.StatusForbidden)
	}
	oldPhotoURL := strings.TrimSpace(contact.PhotoURL)

	file, err := c.FormFile("image")
	if err != nil || file == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "envía un archivo en el campo 'image'"})
	}
	if file.Size > maxContactPhotoSize {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "la imagen no debe superar 10 MB"})
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato no permitido. Usa JPG, PNG o WebP"})
	}

	dir := tenantstorage.TenantUploadDir(ruc, "contacts")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo crear la carpeta de imágenes"})
	}
	filename := fmt.Sprintf("%d_%s_%d%s", id, uuid.New().String()[:8], time.Now().Unix(), ext)
	savePath := filepath.Join(dir, filename)
	if err := c.SaveFile(file, savePath); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error guardando la imagen"})
	}
	photoURL := tenantstorage.TenantUploadPublicURL(ruc, "contacts", filename)
	if err := svc.UpdatePhotoURL(uint(id), photoURL); err != nil {
		_ = os.Remove(savePath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error actualizando el contacto"})
	}
	if oldPhotoURL != "" && oldPhotoURL != photoURL {
		_ = tenantstorage.DeleteUploadByPublicURL(oldPhotoURL)
	}
	return c.JSON(fiber.Map{"photo_url": photoURL})
}
