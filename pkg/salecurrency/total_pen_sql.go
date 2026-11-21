package salecurrency

// TotalPENSQL expresión SQL que lleva un importe del documento a soles: los documentos en USD con TC
// válido se multiplican por él; cualquier otro caso (PEN, o USD sin TC en datos históricos) queda igual.
// `alias` es el prefijo de la tabla que tiene `currency` y `exchange_rate` (ej. "tenant_purchases"; vacío si
// la consulta es de una sola tabla). `col` es el importe, ya calificado.
func TotalPENSQL(alias, col string) string {
	p := ""
	if alias != "" {
		p = alias + "."
	}
	return "CASE WHEN " + p + "currency = 'USD' AND " + p + "exchange_rate > 0 THEN " + col + " * " + p + "exchange_rate ELSE " + col + " END"
}
