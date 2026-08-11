package pagination

import "math"

// Normalize valida page y per_page (10, 20, 25, 50, 100).
func Normalize(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	switch perPage {
	case 10, 20, 25, 50, 100:
	default:
		perPage = 25
	}
	return page, perPage
}

// Offset calcula offset SQL.
func Offset(page, perPage int) int {
	return (page - 1) * perPage
}

// TotalPages páginas totales.
func TotalPages(total int64, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(perPage)))
}
