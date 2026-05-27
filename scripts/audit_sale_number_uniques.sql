-- Auditoría numeración ventas (por BD tenant). No modifica datos.
-- Regla SUNAT: correlativo único POR serie (series_id); el mismo correlativo en series distintas es válido.

-- 1) Duplicados (series_id, correlative) — debe ser 0 antes de migración v050
SELECT
  series_id,
  correlative,
  COUNT(*) AS cantidad,
  GROUP_CONCAT(id ORDER BY id) AS sale_ids
FROM tenant_sales
WHERE deleted_at IS NULL
GROUP BY series_id, correlative
HAVING COUNT(*) > 1
ORDER BY cantidad DESC, series_id, correlative;

-- 2) Informativo: mismo número completo en distintas sucursales (puede ser válido si series_id distinto;
--    si comparten prefijo B001 en dos sucursales, el problema está en tenant_document_series — ver audit_document_series_global.sql)
SELECT
  ts.number,
  COUNT(DISTINCT ts.branch_id) AS sucursales,
  COUNT(*) AS ventas,
  GROUP_CONCAT(DISTINCT ts.series_id ORDER BY ts.series_id) AS series_ids
FROM tenant_sales ts
WHERE ts.deleted_at IS NULL AND TRIM(ts.number) <> ''
GROUP BY ts.number
HAVING COUNT(DISTINCT ts.branch_id) > 1
ORDER BY ventas DESC
LIMIT 50;

-- 3) Resumen v050
SELECT
  (SELECT COUNT(*) FROM (
    SELECT 1 FROM tenant_sales WHERE deleted_at IS NULL
    GROUP BY series_id, correlative HAVING COUNT(*) > 1
  ) t) AS dup_series_correlative;
