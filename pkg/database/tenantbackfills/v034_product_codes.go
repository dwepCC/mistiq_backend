package tenantbackfills

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"gorm.io/gorm"
)

// productCodeAlphabet mismo alfabeto que el formulario de productos: A–Z y 0–9 (ej. «UKVE8N»).
const productCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

const productCodeLength = 6

// V034ProductCodes asigna código a los productos que no lo tienen y lo propaga al snapshot de
// las ventas ya registradas.
//
// SUNAT exige código de producto en cada línea del comprobante. El alta de productos y de
// ventas ya no deja ninguno vacío, pero los registros anteriores a ese cambio siguen en la base
// y el problema solo aparece al emitir —con el comprobante guardado y el cliente esperando—;
// hasta ahora la única salida era editar la tabla a mano.
//
// El código generado no es correlativo a propósito: un número de secuencia sugiere un orden del
// catálogo que no existe y choca con los códigos que el propio negocio ya usa. El usuario puede
// reemplazarlo después por el suyo desde el formulario.
//
// Incluye los productos borrados (soft delete): no es por el catálogo, sino porque sus ventas
// pasadas siguen necesitando el código para poder emitirse.
//
// No deja ninguna línea sin código, ni siquiera las que no referencian un producto del catálogo
// (ítems escritos a mano, o cuyo producto ya no existe): son justamente las de los comprobantes
// que SUNAT rechazó o que quedaron sin emitir, y sin código seguirían sin poder emitirse. Cuando
// no hay producto del cual copiar, la línea recibe un código propio generado igual.
//
// Idempotente: una vez que no quedan códigos vacíos, correrlo de nuevo no cambia nada.
type V034ProductCodes struct{}

func (V034ProductCodes) Version() int { return 34 }
func (V034ProductCodes) Name() string { return "product_codes" }

func (V034ProductCodes) Description() string {
	return "Asigna código a los productos que no lo tienen y lo copia a las líneas de las ventas ya " +
		"registradas. SUNAT exige código por línea: sin él el comprobante se rechaza o no llega a " +
		"emitirse. Las líneas sin producto del catálogo reciben un código propio."
}

func (b V034ProductCodes) Run(db *gorm.DB) error {
	if !db.Migrator().HasTable("tenant_products") {
		return nil
	}

	if err := b.fillProductCodes(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable("tenant_sale_items") {
		return nil
	}
	return b.fillSaleItemCodes(db)
}

// fillProductCodes genera un código libre para cada producto que no tenga uno.
func (b V034ProductCodes) fillProductCodes(db *gorm.DB) error {
	used, err := usedProductCodes(db)
	if err != nil {
		return err
	}

	var ids []uint
	// Consulta directa a la tabla para incluir los borrados: GORM los filtraría por deleted_at.
	if err := db.Table("tenant_products").
		Where("code IS NULL OR TRIM(code) = ''").
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("listar productos sin código: %w", err)
	}

	for _, id := range ids {
		code, err := freeProductCode(used)
		if err != nil {
			return err
		}
		used[code] = struct{}{}
		if err := db.Table("tenant_products").
			Where("id = ?", id).
			Update("code", code).Error; err != nil {
			return fmt.Errorf("asignar código al producto %d: %w", id, err)
		}
	}
	return nil
}

// fillSaleItemCodes completa el código de todas las líneas de venta que no lo tengan.
//
// Primero copia el del producto que la línea referencia —así el comprobante y el catálogo dicen
// lo mismo— y después resuelve una por una las que quedan sin producto al cual recurrir.
func (b V034ProductCodes) fillSaleItemCodes(db *gorm.DB) error {
	// Subconsulta y no JOIN para que corra igual en MySQL/MariaDB (producción) y SQLite (tests).
	// La subconsulta lee otra tabla, así que MySQL no se queja de leer y escribir la misma.
	if err := db.Exec(`
		UPDATE tenant_sale_items
		SET code = (
		    SELECT p.code FROM tenant_products p WHERE p.id = tenant_sale_items.product_id
		)
		WHERE (code IS NULL OR TRIM(code) = '')
		  AND product_id IS NOT NULL
		  AND EXISTS (
		      SELECT 1 FROM tenant_products p
		      WHERE p.id = tenant_sale_items.product_id
		        AND p.code IS NOT NULL AND TRIM(p.code) <> ''
		  )
	`).Error; err != nil {
		return fmt.Errorf("copiar código del producto al snapshot: %w", err)
	}

	// Lo que sobra no tiene producto del cual copiar. Se le genera uno propio: son las líneas de
	// los comprobantes rechazados o sin emitir, y el código es lo que les falta para poder salir.
	var ids []uint
	if err := db.Table("tenant_sale_items").
		Where("code IS NULL OR TRIM(code) = ''").
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("listar líneas sin código: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	used, err := usedProductCodes(db)
	if err != nil {
		return err
	}
	for _, id := range ids {
		code, err := freeProductCode(used)
		if err != nil {
			return err
		}
		used[code] = struct{}{}
		if err := db.Table("tenant_sale_items").
			Where("id = ?", id).
			Update("code", code).Error; err != nil {
			return fmt.Errorf("asignar código a la línea %d: %w", id, err)
		}
	}
	return nil
}

// usedProductCodes códigos ya ocupados en el tenant, para no repetir ninguno.
func usedProductCodes(db *gorm.DB) (map[string]struct{}, error) {
	var codes []string
	if err := db.Table("tenant_products").
		Where("code IS NOT NULL AND TRIM(code) <> ''").
		Pluck("code", &codes).Error; err != nil {
		return nil, fmt.Errorf("listar códigos existentes: %w", err)
	}
	used := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		used[strings.ToUpper(strings.TrimSpace(c))] = struct{}{}
	}
	return used, nil
}

func freeProductCode(used map[string]struct{}) (string, error) {
	// Con 36^6 combinaciones la colisión es rarísima, pero el código debe ser único sí o sí.
	for attempt := 0; attempt < 50; attempt++ {
		code, err := randomProductCode()
		if err != nil {
			return "", err
		}
		if _, taken := used[code]; !taken {
			return code, nil
		}
	}
	return "", fmt.Errorf("no se pudo generar un código de producto libre")
}

func randomProductCode() (string, error) {
	max := big.NewInt(int64(len(productCodeAlphabet)))
	var sb strings.Builder
	for i := 0; i < productCodeLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(productCodeAlphabet[n.Int64()])
	}
	return sb.String(), nil
}
