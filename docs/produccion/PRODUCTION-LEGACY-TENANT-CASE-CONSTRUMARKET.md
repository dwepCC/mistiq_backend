# Caso real: tenant legacy CONSTRUMARKET E.I.R.L. (RUC 20611901608)

> **Errata posterior (2026-10-13)** — revisión crítica: (1) la comparación «vendido 10,929 vs producido 6,645» **no es concluyente**: la consulta incluyó ventas de los `item_id` presentes en `production` (incluidos los ítems 5 y 46, ya eliminados), sin stock inicial validado ni compras completas, y el legacy permitía negativos por defecto; no debe usarse para inferir cómo operaba el negocio; (2) «78 de 125 fichas coinciden» compara el **número** de salidas, no las cantidades; puede deberse a cambio de receta o a edición de consumos por ficha; (3) la cifra de 0.65 % de exceso en la receta es aritmética correcta pero no prueba merma esperada; (4) son 10 recetas, 8 con producción.
> Detalle: [PRODUCTION-FINAL-CRITICAL-REVIEW.md](PRODUCTION-FINAL-CRITICAL-REVIEW.md). El texto original se conserva como evidencia del análisis previo.

> Auditoría de SOLO LECTURA (usuario de solo lectura con SELECT) sobre la BD del cliente en el servidor antiguo, el 2026-10-13. No se modificó nada. Evidencia: consultas SQL directas. Marcas: [V] = dato leído de la BD; [I] = interpretación.

## 1. Qué fabrica
Es un **fabricante de pegamentos para cerámica y porcelanato (morteros secos ensacados)** y, en receta, también pintura temple. Mezclan cemento, arena, aditivos y envasan en bolsas de 20 y 25 kg, con marcas de terceros (Tyson, Tyson Power, Celima, Trebol) [V].

| Producto terminado | Fichas | Unidades |
|---|---|---|
| Pegamento Cerámica Gris 20 kg Tyson | 27 | 1,415 |
| Pegamento Cerámica Gris Trebol | 26 | 1,527 |
| Pegamento Porcelanato Blanco 20 kg Tyson | 22 | 1,180 |
| Pegamento Porcelanato Blanco Trebol | 21 | 1,002 |
| Porcelanato Blanco 25 kg Celima | 11 | 577 |
| Cerámica Gris 25 kg Celima | 9 | 561 |
| Cerámica Gris 25 kg Tyson Power | 5 | 270 |
| Porcelanato Blanco 25 kg Tyson Power | 2 | 113 |
| **Total (fichas reales)** | **123 fichas** | **6,645 bolsas** |

- Período: 16-feb-2026 a 23-abr-2026, 34 días con producción, ~54 bolsas/ficha. Hay además **2 fichas de prueba** (id 1 y 2, 16 y 17-feb, ítems 5 y 46 ya eliminados, sin ingreso de terminado) que suman 61 u. y no cuentan en la tabla; 38 "órdenes" (`production_order` es texto: OPROD-00032 agrupa varias fichas del día) [V].
- Pintura temple (2 productos con receta) **nunca se produjo** [V].
- Es un negocio pequeño: 54 ítems, 2 compras, 131 facturas/boletas, 241 notas de venta [V].
- Después del 23-abr-2026 **no hay más producción** [V]; siguen facturando hasta oct-2026.

## 2. Cómo trabajan (receta real)
- Receta por **1 bolsa terminada**, expresada en **kilos** de insumo. Ej. Cerámica Gris 20 kg Tyson: arena 15 kg + cemento rojo 5.06 kg + celulosa HPMC 0.053 + resina redispersable 0.018 + 1 bolsa (envase) [V]. Cerámica gris usa cemento gris; porcelanato usa cemento blanco + carbonato de calcio.
- **Truco de unidades:** los insumos se llevan en **KGM** aunque se compran en sacos (nombre "Cemento… 42,5 Kg"), con precio por kilo (arena S/ 0.1167/kg; HPMC S/ 15.95/kg). Resolvieron conversión saco→kg dando de alta el insumo ya en kg [V]. Un solo envase por producto/marca (7+ envases distintos) → **el envase es un componente de la receta** [V].
- La ficha registra fecha/hora de inicio y fin, **responsable único** (un colaborador, texto libre, también para "mezcla"), cantidad producida y "pactada" [V]. No usan lote (2 fichas con lote), comentarios (0), imperfectos (0), máquinas ni trabajadores del módulo (tablas vacías), ni molino/empaquetado (vacíos) [V].
- Mezclan en tandas del día: 2 a 4 fichas diarias de distintos pegamentos, ~1-3 horas por ficha [V].

## 3. Problemas observados en la BD (confirman riesgos del legacy)
1. **Stock negativo masivo** [V]: Arena fina −90,361 kg; Cemento rojo −479 kg; producto terminado Cerámica Gris 20 kg −582; Cerámica Trebol −273. Se vendió más de lo que se registró producir (vendido en NV 7,718 + facturas/boletas 3,211 = 10,929 u. vs producido 6,645 + stock inicial no medido) y se consumió arena/cemento sin compras registradas (solo 2 compras en todo el sistema) [V]. [I] La producción se registraba para "cuadrar" ventas, no como control real; las compras de insumos no se cargaban.
2. **La receta cambió a lo largo del tiempo:** solo 78 de 125 fichas (incluidas las 2 de prueba) coinciden hoy en cantidad de salidas con la receta vigente [V]. [I] Se editó la receta (productos/insumos) sin versionar; los reportes legacy muestran la receta actual, no lo consumido.
3. **Costos nulos:** no se calcula costo de producción, aunque los insumos tienen precio por kg [V]. Cosas como "el costo del pegamento" no existen en el sistema.
4. **Producción pactada vs real:** 8 fichas con `agreed` ≠ cantidad; ninguna con imperfectos [V]. [I] Las mermas/defectos no se registran (aunque en una mezcladora de morteros hay merma/polvo).
5. Se paró en abril: [I] posiblemente dejaron de registrar producción por la fricción o por el stock negativo.

## 4. Implicaciones para el diseño de la propuesta
| Observación | Impacto en la propuesta |
|---|---|
| Producto de una sola etapa con 4-7 insumos, receta por unidad | Valida el MVP: receta de un nivel, sin rutas. |
| Insumo comprado en saco y consumido en kg | **Caso real del requisito de unidades** (§6.1). La propuesta permite receta en unidad base o SaleUnit del insumo; para estos clientes lo más simple es dar de alta el insumo en kg, como hicieron. Incluir en el piloto. |
| Envase como componente | Soportado como cualquier insumo (unidad). |
| Se ve el uso de **sobreventa sin stock** y negativos | Refuerza: bloquear negativos en backend y mostrar faltantes al planificar; el flujo "ventas sin stock → Producir" (V1.5) calza con su operación (vender y luego producir). |
| Pocas fichas por día, mismo responsable | Un solo paso "Registrar producción" con consumo precargado es suficiente; no hace falta aprobación multi-rol. |
| No usan lote/mermas/máquinas | Confirma diferir lotes, máquinas y centros de trabajo. Mantener merma como % opcional (la receta ya parece tener ~0.65% de exceso sobre 20 kg: suma 20.131). |
| No hay costos | La V1 (costo por registro desde costo de insumos) les daría valor inmediato: ej. Cerámica Gris 20 kg ≈ 15×0.1167 + 5.06×0.6588 + 0.053×15.95 + 0.018×11.35 + 1.2 ≈ S/ 7.33 por bolsa, antes de mano de obra [I, cálculo propio con precios de compra actuales]. |
| Receta cambió en el tiempo | Valida el **snapshot de receta en la orden**. |
| Importación | Existe una tabla `item_supplies` pequeña (10 productos): candidato a importador de una sola vez. |

## 5. Límites
- Solo revisé la BD de este tenant; no el código desplegado en este VPS ni los archivos del servidor (sin permiso).
- No se verificó cómo vendían (pedido vs stock) ni quién es el cliente final; solo cifras.
- Los nombres de personas aparecen en el campo `production_collaborator`; no se incluyen aquí.
- Los pendientes de seguridad de ese acceso se gestionan fuera de este repositorio.

## 6. Actividad actual del tenant (consulta del 13-oct-2026)
- Última factura/boleta: FA02-101, emitida 12-oct-2026 (registrada 13-oct 10:29, S/ 300). Facturación mensual en baja: mayo S/ 34,805 → septiembre S/ 7,994.50.
- Última nota de venta: 27-may-2026. Última compra: 5-mar-2026. Última producción: 23-abr-2026.
- Conclusión [I]: sigue activo solo para facturar; abandonó producción y notas de venta hace meses.

## 7. Verificación de reglas del legacy con datos reales
- **Fórmula de consumo confirmada [V]:** ficha 125 (86 bolsas de Cerámica Gris 20 kg Tyson) generó salidas `101` "Salida por insumo": arena 1,290 (=15×86), cemento rojo 435.16 (=5.06×86), HPMC 4.558, resina 1.548, envase 86; y un ingreso `19` "Ingreso de producción" de 86. Coincide con `receta × cantidad producida` descrito en PRODUCTION-LEGACY-ANALYSIS.md.
- No hay salidas de insumo con cantidad negativa [V] (el riesgo del cliente enviando cantidades negativas no se materializó aquí).
- Las salidas **no están enlazadas** a la ficha: las relacioné por cercanía de fecha/hora de creación (±5 s) [V del método; confirma la debilidad señalada en el análisis legacy].
