# Auditoría de producción: uso de dólares (11-nov-2026, solo lectura)

Consulta de solo lectura (usuario `claude_ro`) sobre todas las bases de tenants. Se contaron documentos, cuentas y precios; **no se leyeron ni se modificaron datos personales ni documentos** y no se tocó ningún registro.

## Resultado global
- **Solo 6 de ~790 tenants** tienen alguna actividad en USD (uno de ellos es el tenant `demo`).
- **0 compras en USD** y **0 cuentas bancarias en USD** en toda la plataforma.
- En total: 14 ventas no anuladas en USD, 8 cotizaciones en USD y 11 filas de pago asociadas a ventas en USD.

| Tenant | Ventas USD | Total USD | Cotiz. USD | Líneas de catálogo con precio = catálogo (soles) | Lectura |
|---|---|---|---|---|---|
| `industrialrafaz` | 2 | 331.94 | 1 | **4 de 4** | **Afectado por el defecto** (ver abajo) |
| `euro` | 5 | 4 666.72 | 0 | 0 | Convierte a mano (precio ≈ catálogo ÷ TC): práctica correcta |
| `machupicchuinkas` | 3 | 1 044.00 | 0 | 0 | Líneas manuales |
| `parabrisasnegociaciones` | 2 | 1 953.48 | 0 | 0 | Líneas manuales |
| `sergenmecatronic` | 1 | 990.00 | 7 | 0 | Convierte a mano; cotizaciones con líneas manuales |
| `demo` | 1 | 12.00 | 0 | 1 | Prueba interna (mismo defecto) |

## Caso afectado: `industrialrafaz`
Sus productos se vendieron **muchas veces en soles al mismo precio** (p. ej. el producto de 35.00 se vendió 39 veces en PEN a S/ 35.00) y, en las ventas en USD, la línea quedó con **ese mismo número en dólares**. Es el defecto del carrito en USD (el precio de catálogo en soles etiquetado como dólares):
- Factura electrónica **F001-00000456** del 20-oct-2026 por **US$ 35.00** (TC 3.431): equivale a ≈ US$ 10.20.
- Nota de venta **123456-00123463** del 24-oct-2026 por **US$ 296.94** (TC 3.451).
- Cotización USD 25 con la misma línea de 35.00.

**Qué hacer (a decidir con el cliente, no se modificó nada):** confirmar con el cliente qué cobró realmente; si fue lo correcto en soles, regularizar la factura en USD (nota de crédito/ nueva emisión según corresponda). El hotfix (`92d79b2` + `090b7b4`) evita que se repita; no corrige documentos históricos.

## Qué confirma para las decisiones
- **Catálogo en soles**: los tenants que venden en USD convierten a mano (`precio_línea ≈ precio_catálogo ÷ TC`), lo que confirma que el catálogo actual está en soles (por eso la moneda por producto debe ser `PEN` por defecto).
- **Cuentas bancarias en USD: no existen** → se puede **forzar cuentas solo en soles** sin migrar nada.
- **Compras en USD: no existen** → la rama `feature/orden-compra` no tiene historia que migrar.
- Volumen muy bajo de ventas en USD → el riesgo de backfill de caja/pagos es pequeño (11 pagos).
- Antes de cualquier migración de caja: revisar esas 11 filas de pago de ventas en USD (se valúan con el TC del documento).
