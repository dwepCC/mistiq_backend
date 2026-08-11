package service

import (
	"strings"
	"unicode"

	"tukifac/pkg/database"
)

// BarcodeCandidates genera variantes del valor leído (cámara o escáner HID).
// Cubre UPC-A (12) vs EAN-13 (13 con 0 inicial) y lecturas con caracteres de control.
func BarcodeCandidates(raw string) []string {
	base := strings.TrimSpace(raw)
	base = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, base)
	if base == "" {
		return nil
	}
	seen := map[string]struct{}{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
	}
	add(base)
	digits := extractDigits(base)
	if digits != "" {
		add(digits)
		if len(digits) == 12 {
			add("0" + digits)
		}
		if len(digits) == 13 && digits[0] == '0' {
			add(digits[1:])
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out
}

func extractDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *ProductService) findActiveProductByCodeCandidate(cand string, branchID uint) (*database.TenantProduct, error) {
	if branchID > 0 {
		p, err := s.GetByCodeInBranch(cand, branchID)
		if err != nil {
			return nil, err
		}
		if p != nil && p.Active {
			if err := s.EnsureRestaurantBranchAccess(p, branchID); err == nil {
				return p, nil
			}
		}
	}
	p, err := s.GetByCode(cand)
	if err != nil {
		return nil, err
	}
	if p == nil || !p.Active {
		return nil, nil
	}
	if err := s.EnsureRestaurantBranchAccess(p, branchID); err != nil {
		return nil, nil
	}
	return p, nil
}

// FindProductByBarcode busca un producto activo por código exacto (y variantes EAN/UPC).
func (s *ProductService) FindProductByBarcode(code string, branchID uint) (*database.TenantProduct, error) {
	for _, cand := range BarcodeCandidates(code) {
		p, err := s.findActiveProductByCodeCandidate(cand, branchID)
		if err != nil {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
	}
	return nil, nil
}

// FindRestaurantProductByBarcode busca un plato activo por código exacto (y variantes EAN/UPC).
func (s *ProductService) FindRestaurantProductByBarcode(code string, branchID uint) (*database.TenantProduct, error) {
	p, err := s.FindProductByBarcode(code, branchID)
	if err != nil || p == nil || !p.IsRestaurant {
		return nil, err
	}
	return p, nil
}
