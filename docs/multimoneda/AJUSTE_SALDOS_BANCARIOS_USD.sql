-- AJUSTE DE SALDOS BANCARIOS POR COBROS EN USD (NO EJECUTADO — requiere revisión y permiso explícito)
--
-- Contexto: antes de la corrección del 11-nov-2026 el saldo de una cuenta bancaria (siempre en soles) subía/bajaba por el monto
-- del documento EN SU MONEDA. Un cobro de US$ 100 (TC 3.80) por Yape/transferencia sumó 100 en vez de 380. Este script suma la
-- diferencia (monto × TC − monto) de cada movimiento bancario ligado a una venta en USD.
--
-- Hallazgo (consulta de solo lectura, 11-nov-2026) — diferencia esperada por cuenta:
--   euro                      cuenta 3 : +4 267.52
--   parabrisasnegociaciones   cuenta 3 : +2 383.24
--   sergenmecatronic          cuenta 1 : +2 442.33
--
-- ⚠ ORDEN SEGURO (revisión de despliegue, 11-nov-2026):
--   * El backend NUEVO ya acredita las cuentas con el equivalente en soles. Si este script corre DESPUÉS del reinicio y hubo cobros USD por banco
--     en el intervalo, esos cobros se contarían dos veces. Por eso TODAS las consultas llevan un tope de id: anote ANTES del reinicio del
--     backend  SELECT MAX(id) FROM saas_tenant_<TENANT>.tenant_bank_movements;  y reemplace <MAX_ID> por ese valor.
--   * Tras aplicarlo, el verificador GET /api/cashbank/bank-accounts/reconcile debe mostrar el mismo «inicial implícito» que antes del
--     desvío (0 en cuentas creadas en cero). Esa es la comprobación definitiva, más que comparar contra las cifras de arriba.
--   * NO incluir la cuenta «CTA BBVA DOLARES» de euro (cuenta 3) hasta decidir con el cliente si es una cuenta en dólares (ver doc finanzas).
--
-- USO (por tenant, UNA sola vez; hacer respaldo antes; ejecutar con un usuario con escritura, NO claude_ro):
--   1) Ejecutar el bloque "A. SIMULACIÓN" y comparar con los valores de arriba. Si no coinciden, NO continuar.
--   2) Ejecutar el bloque "B. APLICAR" dentro de la transacción; revisar el SELECT final y hacer COMMIT (o ROLLBACK).
-- Reemplazar <TENANT> por: euro | parabrisasnegociaciones | sergenmecatronic  (BD saas_tenant_<TENANT>).
-- No es idempotente: correrlo dos veces suma dos veces. Después de aplicarlo no volver a correrlo.
-- Las compras no entran: producción tiene 0 compras en USD y 0 cuentas bancarias en USD.

-- ===================== A. SIMULACIÓN (solo lectura) =====================
SELECT m.bank_account_id,
       a.name,
       a.balance AS saldo_actual,
       ROUND(SUM(CASE WHEN m.type = 'credit' THEN 1 ELSE -1 END * (ROUND(m.amount * s.exchange_rate, 2) - m.amount)), 2) AS ajuste,
       ROUND(a.balance + SUM(CASE WHEN m.type = 'credit' THEN 1 ELSE -1 END * (ROUND(m.amount * s.exchange_rate, 2) - m.amount)), 2) AS saldo_nuevo,
       SUM(s.exchange_rate IS NULL OR s.exchange_rate <= 0) AS movimientos_sin_tc
FROM saas_tenant_<TENANT>.tenant_bank_movements m
JOIN saas_tenant_<TENANT>.tenant_sales s ON s.id = m.sale_id
JOIN saas_tenant_<TENANT>.tenant_bank_accounts a ON a.id = m.bank_account_id
WHERE UPPER(TRIM(s.currency)) = 'USD' AND s.exchange_rate > 0 AND m.id <= <MAX_ID>
GROUP BY m.bank_account_id, a.name, a.balance;
-- Debe devolver UNA fila por cuenta afectada, con `movimientos_sin_tc` = 0.

-- ===================== B. APLICAR =====================
START TRANSACTION;

UPDATE saas_tenant_<TENANT>.tenant_bank_accounts a
JOIN (
    SELECT m.bank_account_id AS id,
           SUM(CASE WHEN m.type = 'credit' THEN 1 ELSE -1 END * (ROUND(m.amount * s.exchange_rate, 2) - m.amount)) AS ajuste
    FROM saas_tenant_<TENANT>.tenant_bank_movements m
    JOIN saas_tenant_<TENANT>.tenant_sales s ON s.id = m.sale_id
    WHERE UPPER(TRIM(s.currency)) = 'USD' AND s.exchange_rate > 0 AND m.id <= <MAX_ID>
    GROUP BY m.bank_account_id
) d ON d.id = a.id
SET a.balance = a.balance + d.ajuste;

SELECT id, name, balance FROM saas_tenant_<TENANT>.tenant_bank_accounts;  -- verificar contra la simulación
-- COMMIT;   -- si coincide
-- ROLLBACK; -- si no
