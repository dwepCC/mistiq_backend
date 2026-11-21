// Package branchlogo resuelve qué logo usar en un comprobante: el logo propio de la
// sucursal emisora si lo tiene, o si no el logo global de la empresa (TenantCompanyConfig).
package branchlogo

import "strings"

// ResolveURL es el ÚNICO punto que decide esta cadena de fallback: nadie más debe repetirla.
// Mismo patrón override+fallback que pkg/saleunit.ResolvePrice usa para precios por sucursal.
func ResolveURL(branchLogoURL, companyLogoURL string) string {
	if url := strings.TrimSpace(branchLogoURL); url != "" {
		return url
	}
	return companyLogoURL
}

// ResolvePrintLogo devuelve el logo a imprimir de una sucursal: embebido (data:) cuando se pudo,
// y la URL cruda cuando no. Nunca mezcla niveles: si la sucursal TIENE logo propio pero no se
// pudo embeber (archivo >512 KB, ausente o de extensión no soportada), se devuelve su URL
// remota — no el data URL del logo de la empresa, que sería el de OTRA sucursal a ojos del
// cliente. Solo cae al logo de la empresa cuando la sucursal no tiene logo propio.
func ResolvePrintLogo(branchLogoURL, branchDataURL, companyLogoURL, companyDataURL string) string {
	if strings.TrimSpace(branchLogoURL) != "" {
		return ResolveURL(branchDataURL, branchLogoURL)
	}
	return ResolveURL(companyDataURL, companyLogoURL)
}
