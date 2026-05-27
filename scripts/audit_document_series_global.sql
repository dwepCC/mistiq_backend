-- Auditoría: código de serie único a nivel tenant (SUNAT).
-- Ejemplo inválido: B001 en sucursal A y B001 en sucursal B.
-- Ejecutar por cada BD tenant antes de migración v052.

SELECT
  UPPER(TRIM(series)) AS series_code,
  COUNT(*) AS cantidad,
  GROUP_CONCAT(id ORDER BY id) AS series_row_ids,
  GROUP_CONCAT(DISTINCT branch_id ORDER BY branch_id) AS branch_ids,
  GROUP_CONCAT(DISTINCT doc_type ORDER BY doc_type SEPARATOR ' | ') AS doc_types
FROM tenant_document_series
GROUP BY UPPER(TRIM(series))
HAVING COUNT(*) > 1
ORDER BY cantidad DESC, series_code;

-- Resumen (0 = seguro aplicar UNIQUE(series) en v052)
SELECT COUNT(*) AS duplicate_series_codes
FROM (
  SELECT UPPER(TRIM(series))
  FROM tenant_document_series
  GROUP BY UPPER(TRIM(series))
  HAVING COUNT(*) > 1
) t;
