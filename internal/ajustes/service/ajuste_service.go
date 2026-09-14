package service

import (
	"tukifac/pkg/database"
)

const AjusteID = 1

type AjusteService struct{}

func NewAjusteService() *AjusteService { return &AjusteService{} }

// Get obtiene la configuración central (ID=1). Crea la fila si no existe.
func (s *AjusteService) Get() (*database.CentralAjuste, error) {
	var a database.CentralAjuste
	err := database.CentralDB.First(&a, AjusteID).Error
	if err != nil {
		// Crear fila por defecto si no existe
		a = database.CentralAjuste{ID: AjusteID, NombreSistema: "Mistiq"}
		if createErr := database.CentralDB.Create(&a).Error; createErr != nil {
			return nil, createErr
		}
	}
	return &a, nil
}

// GetTokenConsulta devuelve solo el token para consultas externas (apiperu.dev). Uso interno.
func (s *AjusteService) GetTokenConsulta() (string, error) {
	a, err := s.Get()
	if err != nil {
		return "", err
	}
	return a.TokenConsulta, nil
}

type UpdateAjusteInput struct {
	NombreSistema string `json:"nombre_sistema"`
	Slogan        string `json:"slogan"`
	Direccion     string `json:"direccion"`
	Ubigeo        string `json:"ubigeo"`
	TokenConsulta string `json:"token_consulta"` // opcional; vacío = no cambiar
	EmailContacto string `json:"email_contacto"`
	Telefono      string `json:"telefono"`
}

func (s *AjusteService) Update(input UpdateAjusteInput) error {
	var a database.CentralAjuste
	if err := database.CentralDB.First(&a, AjusteID).Error; err != nil {
		a.ID = AjusteID
		if err := database.CentralDB.Create(&a).Error; err != nil {
			return err
		}
	}
	updates := map[string]interface{}{
		"nombre_sistema": input.NombreSistema,
		"slogan":         input.Slogan,
		"direccion":      input.Direccion,
		"ubigeo":         input.Ubigeo,
		"email_contacto": input.EmailContacto,
		"telefono":       input.Telefono,
	}
	if input.TokenConsulta != "" {
		updates["token_consulta"] = input.TokenConsulta
	}
	return database.CentralDB.Model(&a).Updates(updates).Error
}
