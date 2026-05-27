package handler

import (
	"encoding/json"
	"errors"
	"strings"

	"tukifac/internal/products/service"
)

type modifierGroupBody struct {
	Name        string          `json:"name"`
	Required    bool            `json:"required"`
	MultiSelect bool            `json:"multi_select"`
	Options     json.RawMessage `json:"options"`
}

func (b modifierGroupBody) parsedOptions() ([]service.ModifierOptionInput, error) {
	if len(b.Options) == 0 {
		return nil, errors.New("agrega al menos una opción")
	}
	var asStrings []string
	if err := json.Unmarshal(b.Options, &asStrings); err == nil {
		out := make([]service.ModifierOptionInput, 0, len(asStrings))
		for _, s := range asStrings {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, service.ModifierOptionInput{Name: s, ExtraPrice: 0})
		}
		if len(out) == 0 {
			return nil, errors.New("agrega al menos una opción")
		}
		return out, nil
	}
	var asObjs []struct {
		Name       string  `json:"name"`
		ExtraPrice float64 `json:"extra_price"`
	}
	if err := json.Unmarshal(b.Options, &asObjs); err != nil {
		return nil, errors.New("formato de opciones inválido")
	}
	out := make([]service.ModifierOptionInput, 0, len(asObjs))
	for _, o := range asObjs {
		name := strings.TrimSpace(o.Name)
		if name == "" {
			continue
		}
		price := o.ExtraPrice
		if price < 0 {
			price = 0
		}
		out = append(out, service.ModifierOptionInput{Name: name, ExtraPrice: price})
	}
	if len(out) == 0 {
		return nil, errors.New("agrega al menos una opción")
	}
	return out, nil
}
