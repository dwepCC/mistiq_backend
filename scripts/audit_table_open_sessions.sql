-- Diagnóstico: mesas con más de una sesión open (ejecutar por tenant).
-- Conservar la más reciente (MAX id) y cancelar el resto antes de aplicar v064.

SELECT table_id, COUNT(*) AS open_sessions
FROM tenant_table_sessions
WHERE status = 'open' AND table_id IS NOT NULL
GROUP BY table_id
HAVING COUNT(*) > 1;

-- Detalle de duplicados
SELECT s.id, s.table_id, s.opened_at, s.status, s.order_code, t.name AS table_name
FROM tenant_table_sessions s
LEFT JOIN tenant_restaurant_tables t ON t.id = s.table_id
WHERE s.status = 'open' AND s.table_id IS NOT NULL
  AND s.table_id IN (
    SELECT table_id
    FROM tenant_table_sessions
    WHERE status = 'open' AND table_id IS NOT NULL
    GROUP BY table_id
    HAVING COUNT(*) > 1
  )
ORDER BY s.table_id, s.id;

-- Mesas ocupadas sin sesión open (desincronización)
SELECT t.id, t.name, t.status
FROM tenant_restaurant_tables t
WHERE t.deleted_at IS NULL
  AND t.status = 'ocupada'
  AND NOT EXISTS (
    SELECT 1 FROM tenant_table_sessions s
    WHERE s.table_id = t.id AND s.status = 'open'
  );

-- Índice esperado (v064 portable):
--   Estrategia A (MySQL 8.0.13+ / MariaDB 10.6+): índice funcional ux_open_session_per_table
--   Estrategia B (MySQL 5.7.6+ / MariaDB 10.2+): columna open_table_key VIRTUAL + mismo índice
SHOW INDEX FROM tenant_table_sessions WHERE Key_name = 'ux_open_session_per_table';
SHOW COLUMNS FROM tenant_table_sessions LIKE 'open_table_key';

-- Mesas libres con sesión open (desincronización)
SELECT t.id, t.name, t.status, s.id AS session_id
FROM tenant_restaurant_tables t
JOIN tenant_table_sessions s ON s.table_id = t.id AND s.status = 'open'
WHERE t.deleted_at IS NULL AND t.status <> 'ocupada';
