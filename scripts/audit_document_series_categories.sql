-- Auditoría: series NC/ND con categoría incorrecta (ejecutar por BD tenant).
-- Regla: sunat_code 07 → nota_credito | sunat_code 08 → nota_debito

-- Filas que v051 corregiría
SELECT id, branch_id, doc_type, sunat_code, category, series, active
FROM tenant_document_series
WHERE TRIM(sunat_code) = '07' AND category <> 'nota_credito';

SELECT id, branch_id, doc_type, sunat_code, category, series, active
FROM tenant_document_series
WHERE TRIM(sunat_code) = '08' AND category <> 'nota_debito';

-- Posibles falsos positivos en POS (category=venta pero tipo administrativo)
SELECT id, branch_id, doc_type, sunat_code, category, series
FROM tenant_document_series
WHERE category = 'venta'
  AND (
    TRIM(sunat_code) IN ('07', '08', '09', '20', '40')
    OR UPPER(doc_type) LIKE '%CREDITO%'
    OR UPPER(doc_type) LIKE '%DEBITO%'
    OR UPPER(doc_type) LIKE '%GUIA%'
  );
