package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// jsonWithETag responde `v` como JSON con un ETag débil y `Cache-Control: private, no-cache`.
//
// company/config y company/branches pesan 100-300 KB (logo embebido) y el cliente los pide en CADA carga de
// página; casi nunca cambian. Con el ETag el navegador revalida con If-None-Match y, si no cambió, la
// respuesta es un 304 vacío: se paga solo el viaje (~250 ms) y no los ~470 ms de bajar el cuerpo.
//
// - La respuesta (cuando hay cuerpo) es idéntica a la de c.JSON: ningún cliente cambia.
// - `private`: no se guarda en cachés compartidas (la respuesta es por tenant/usuario).
// - `no-cache`: se guarda pero SIEMPRE se revalida, así que un cambio de logo/config se ve de inmediato.
func jsonWithETag(c fiber.Ctx, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	sum := sha256.Sum256(body)
	etag := `W/"` + hex.EncodeToString(sum[:16]) + `"`

	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderCacheControl, "private, no-cache")
	c.Set(fiber.HeaderVary, "Authorization, X-Tenant-Slug, X-Branch-Id, X-Branch-Mode")

	if etagMatches(c.Get(fiber.HeaderIfNoneMatch), etag) {
		return c.SendStatus(fiber.StatusNotModified)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Send(body)
}

// etagMatches compara If-None-Match (lista separada por comas, "*" o validadores débiles) con el ETag actual.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	norm := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "W/") }
	want := norm(etag)
	for _, part := range strings.Split(header, ",") {
		if norm(part) == want {
			return true
		}
	}
	return false
}
