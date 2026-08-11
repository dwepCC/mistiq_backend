package service

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
)

// BulkImportMaxItems tope por solicitud, para no bloquear la BD con un archivo enorme.
const BulkImportMaxItems = 2000

// BulkImportContactItem fila del Excel de clientes/proveedores.
type BulkImportContactItem struct {
	RowNumber    int    `json:"row_number"`
	Type         string `json:"type"`       // customer | supplier | both
	DocType      string `json:"doc_type"`   // 1 DNI, 6 RUC, 0 sin RUC, 4 CE, 7 pasaporte
	DocNumber    string `json:"doc_number"`
	BusinessName string `json:"business_name"`
	TradeName    string `json:"trade_name"`
	Address      string `json:"address"`
	Ubigeo       string `json:"ubigeo"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	ContactPerson string `json:"contact_person"`
	Notes        string `json:"notes"`
}

type BulkImportContactFail struct {
	Row   int    `json:"row"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

type BulkImportContactResult struct {
	Created int                     `json:"created"`
	Updated int                     `json:"updated"`
	Failed  []BulkImportContactFail `json:"failed"`
}

// docTypeLengths: dígitos exigidos por SUNAT según el tipo de documento. 0 = sin regla.
var docTypeLengths = map[string]int{
	"1": 8,  // DNI
	"6": 11, // RUC
}

func normalizeContactDocType(raw string) string {
	code := strings.TrimSpace(strings.ToUpper(raw))
	switch code {
	case "", "6", "RUC":
		return "6"
	case "1", "DNI":
		return "1"
	case "0", "SIN RUC", "SIN_RUC":
		return "0"
	case "4", "CE", "CARNET DE EXTRANJERIA":
		return "4"
	case "7", "PASAPORTE":
		return "7"
	}
	return ""
}

func normalizeContactType(raw string) string {
	t := strings.TrimSpace(strings.ToLower(raw))
	switch t {
	case "", "cliente", "customer":
		return "customer"
	case "proveedor", "supplier":
		return "supplier"
	case "ambos", "both":
		return "both"
	}
	return ""
}

// BulkImportContacts crea o actualiza contactos desde un Excel.
//
// Actualiza en vez de duplicar cuando el número de documento ya existe: reimportar un
// padrón corregido es el caso normal, y duplicar clientes rompería el histórico de ventas.
// Nunca toca el cliente por defecto (IsDefaultWalkIn): lo crea el alta del tenant.
func (s *ContactService) BulkImportContacts(items []BulkImportContactItem) (*BulkImportContactResult, error) {
	if len(items) == 0 {
		return nil, errors.New("no hay filas para importar")
	}
	if len(items) > BulkImportMaxItems {
		return nil, fmt.Errorf("máximo %d filas por solicitud", BulkImportMaxItems)
	}

	result := &BulkImportContactResult{Failed: make([]BulkImportContactFail, 0)}
	docsInFile := make(map[string]int, len(items))

	for _, item := range items {
		name := strings.TrimSpace(item.BusinessName)
		docNumber := strings.TrimSpace(item.DocNumber)
		fail := func(msg string) {
			result.Failed = append(result.Failed, BulkImportContactFail{Row: item.RowNumber, Name: name, Error: msg})
		}

		if name == "" {
			fail("nombre o razón social es requerido")
			continue
		}
		docType := normalizeContactDocType(item.DocType)
		if docType == "" {
			fail("tipo_documento inválido (use DNI, RUC, CE, pasaporte o 0)")
			continue
		}
		if docNumber == "" {
			fail("numero_documento es requerido")
			continue
		}
		if want, ok := docTypeLengths[docType]; ok {
			digits := strings.TrimSpace(docNumber)
			if len(digits) != want {
				fail(fmt.Sprintf("el documento debe tener %d dígitos", want))
				continue
			}
			for _, r := range digits {
				if r < '0' || r > '9' {
					fail("el documento debe contener solo dígitos")
					continue
				}
			}
		}
		contactType := normalizeContactType(item.Type)
		if contactType == "" {
			fail("tipo inválido (use cliente, proveedor o ambos)")
			continue
		}
		if prevRow, dup := docsInFile[docNumber]; dup {
			fail(fmt.Sprintf("documento repetido en el archivo (fila %d)", prevRow))
			continue
		}
		docsInFile[docNumber] = item.RowNumber

		ubigeo := strings.TrimSpace(item.Ubigeo)
		if ubigeo != "" && len(ubigeo) != 6 {
			fail("ubigeo debe tener 6 dígitos")
			continue
		}

		var existing database.TenantContact
		err := s.db.Where("doc_number = ?", docNumber).First(&existing).Error
		if err == nil {
			// El cliente por defecto del tenant no se toca desde una importación.
			if existing.IsDefaultWalkIn {
				fail("es el cliente por defecto del sistema y no puede modificarse")
				continue
			}
			updates := map[string]interface{}{
				"type":           contactType,
				"doc_type":       docType,
				"business_name":  name,
				"trade_name":     strings.TrimSpace(item.TradeName),
				"address":        strings.TrimSpace(item.Address),
				"ubigeo":         ubigeo,
				"phone":          strings.TrimSpace(item.Phone),
				"email":          strings.TrimSpace(item.Email),
				"contact_person": strings.TrimSpace(item.ContactPerson),
				"notes":          strings.TrimSpace(item.Notes),
			}
			if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
				fail(err.Error())
				continue
			}
			result.Updated++
			continue
		}

		row := database.TenantContact{
			Type:          contactType,
			DocType:       docType,
			DocNumber:     docNumber,
			BusinessName:  name,
			TradeName:     strings.TrimSpace(item.TradeName),
			Address:       strings.TrimSpace(item.Address),
			Ubigeo:        ubigeo,
			Phone:         strings.TrimSpace(item.Phone),
			Email:         strings.TrimSpace(item.Email),
			ContactPerson: strings.TrimSpace(item.ContactPerson),
			Notes:         strings.TrimSpace(item.Notes),
			Active:        true,
		}
		if err := s.db.Create(&row).Error; err != nil {
			fail(err.Error())
			continue
		}
		result.Created++
	}

	return result, nil
}
