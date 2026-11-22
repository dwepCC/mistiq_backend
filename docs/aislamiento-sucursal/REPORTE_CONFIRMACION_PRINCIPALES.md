# Confirmar con el cliente cuál es su sucursal principal (Doriconta y Naturales)

Fecha: 10-nov-2026. **El criterio actual no cambia** (V179 conservaría la sucursal de menor id) **hasta recibir la confirmación**. Datos de solo lectura de producción.

## Para qué sirve la sucursal principal
Es la sucursal a la que el sistema recurre cuando un usuario no tiene una asignada, donde se registran los documentos que no traen sucursal (por ejemplo retenciones) y la que se toma por defecto en la tienda virtual. No cambia permisos ni borra información: se puede cambiar después en Empresa → Sucursales, marcando otra como principal.

## Doriconta (`doriconta`)
Hoy figuran **dos** sucursales principales (debe haber solo una).

| Sucursal | Creada | Ventas | Usuarios | Cajas abiertas | Marcada principal |
|---|---|---|---|---|---|
| **Principal** (CAL. ATLANTIDA 211 URB. GRAFICOS) | 25-may-2026 | 1 841 | 13 | 7 | sí |
| RESTAURANTE AV. 28 (Calle Arequipa, primera cuadra) | 28-may-2026 | 59 | 6 | 4 | no |
| **sucursal 01** (sin dirección) | 10-oct-2026 | 6 | 1 | 1 | **sí** |

Qué haría V179 si no hay respuesta: dejar **"Principal"** y desmarcar "sucursal 01".
Pregunta para el cliente: *"¿Su sucursal principal es 'Principal' (la de Calle Atlántida)? La sucursal 'sucursal 01', creada el 10 de octubre, quedó también marcada como principal; ¿fue intencional?"*
- Si responde **Principal**: no se hace nada más (V179 lo deja así).
- Si responde **sucursal 01**: antes del despliegue se cambia la marca desde Empresa → Sucursales y se le recomienda completar su dirección y nombre.

## Naturales (`naturales`)
**Ninguna** sucursal está marcada como principal (el sistema ya usa la primera activa mientras tanto).

| Sucursal | Creada | Ventas | Usuarios asignados |
|---|---|---|---|
| **Santas Hierbas - San Borja** (Av. Aviación 2410 int. 58) | 07-oct-2026 | 4 | 2 |
| Santas Hierbas - Surco (Jr. Preciados 127, Santiago de Surco) | 07-oct-2026 | 0 | 2 |

Qué haría V179: marcar **San Borja** (la primera activa y la única con ventas).
Pregunta para el cliente: *"¿Cuál es su local principal o casa matriz: San Borja o Surco?"* (Surco aún no registra ventas pero tiene 2 usuarios asignados).

## Caso adicional para revisar (no hay que confirmar principal)
**Bernapom** (`bernapom`) tampoco tiene principal: "SANTA ANITA" concentra toda la actividad (27 ventas) y V179 la marcaría. Además existe una sucursal "BERNA POM" sin ventas ni usuarios; conviene preguntar si debe existir.
