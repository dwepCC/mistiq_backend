# Fase 7 — Eliminación controlada del índice duplicado

**Fecha:** 2026-09-20
**Alcance:** exclusivamente `DROP INDEX idx_fiscal_doc_tenant_status ON fiscal_documents`, preparada y validada en un entorno LOCAL seguro (Laragon MySQL 8.0.30, `127.0.0.1:3306`, base `lycet`, la misma que usa `.env` de `facturador_lycet` para desarrollo). **Producción NO fue tocada** — ni por SSH ni por ningún otro medio en esta fase.

---

## 1. Verificaciones previas (antes de crear la migración)

- `git status` en los 3 repos: idéntico al cierre de Fase 6 (confirmado antes y después).
- Última migración existente: `migrations/Version20260603000000.php`.
- Nombre exacto del índice confirmado con `SHOW INDEX`: `idx_fiscal_doc_tenant_status`.
- `grep -rn "idx_fiscal_doc_tenant_status" .` en todo el repo: solo aparece en su migración de origen (`Version20260525000000.php`, líneas 21 y 32) — **ninguna migración posterior ni código de aplicación depende de su nombre**.
- `php bin/console doctrine:migrations:status`: reveló que el entorno local estaba 3 migraciones detrás de la última disponible (todas aditivas: `error_type`/`retryable`, `tenant_sync_*`, `original_sunat_mode`/`reissue_count` — los mismos campos ya trabajados en fases anteriores). Se aplicaron esas 3 primero para dejar el schema local equivalente al de producción antes de probar la nueva migración.

---

## 2. Migración creada

**Archivo:** `facturador_lycet/migrations/Version20260920000000.php` (nuevo, no aplicado a producción)

```php
final class Version20260920000000 extends AbstractMigration
{
    public function getDescription(): string
    {
        return 'Elimina idx_fiscal_doc_tenant_status: duplicado exacto de IDX_FISCAL_TENANT_STATUS (tenant_slug, status) en fiscal_documents';
    }

    public function up(Schema $schema): void
    {
        $this->addSql('DROP INDEX idx_fiscal_doc_tenant_status ON fiscal_documents');
    }

    public function down(Schema $schema): void
    {
        $this->addSql('CREATE INDEX idx_fiscal_doc_tenant_status ON fiscal_documents (tenant_slug, status)');
    }
}
```

**Índice eliminado:** `idx_fiscal_doc_tenant_status` (tenant_slug, status).
**Índice conservado:** `IDX_FISCAL_TENANT_STATUS` (tenant_slug, status) — el original.
**Ningún otro índice fue tocado** (ver verificación en sección 4).

---

## 3. Validación en entorno local — antes/después

### `SHOW INDEX FROM fiscal_documents` — ANTES (19 filas, 11 índices)

Incluía tanto `IDX_FISCAL_TENANT_STATUS` como `idx_fiscal_doc_tenant_status`, ambos con las mismas 2 columnas — confirmado idéntico a lo documentado en Fase 6 para producción (el schema local estaba en sincronía).

### Ejecución

```
php bin/console doctrine:migrations:migrate --dry-run --no-interaction
  → 4 migraciones (las 3 pendientes + la nueva), 18 queries, sin errores

php bin/console doctrine:migrations:migrate --no-interaction
  → Successfully migrated to version: DoctrineMigrations\Version20260920000000
```

### `SHOW INDEX FROM fiscal_documents` — DESPUÉS (17 filas, 10 índices)

```
IDX_FISCAL_CREATED, idx_fiscal_doc_cursor, idx_fiscal_doc_series_number,
idx_fiscal_doc_tenant_id, idx_fiscal_doc_tenant_slug_status, IDX_FISCAL_SALE,
IDX_FISCAL_TENANT_STATUS, PRIMARY, UNIQ_FISCAL_DOC_UUID, UNIQ_FISCAL_FINGERPRINT
```

`idx_fiscal_doc_tenant_status` **ya no existe**. `IDX_FISCAL_TENANT_STATUS` **sigue presente**. Los otros 8 índices son exactamente los mismos de antes, sin ningún cambio.

### Prueba de rollback (`down()`)

```
php bin/console doctrine:migrations:migrate prev --no-interaction
  → Successfully migrated to version: DoctrineMigrations\Version20260603000000
SHOW INDEX ... → idx_fiscal_doc_tenant_status RECREADO correctamente (tenant_slug, status)

php bin/console doctrine:migrations:migrate --no-interaction   (se re-aplicó para dejar validado el estado final)
  → Successfully migrated to version: DoctrineMigrations\Version20260920000000
SHOW INDEX ... → idx_fiscal_doc_tenant_status ausente de nuevo, IDX_FISCAL_TENANT_STATUS presente
```

El ciclo `up()`→`down()`→`up()` se ejecutó completo y sin errores — el rollback funciona exactamente como se documentó.

---

## 4. EXPLAIN post-migración (los 4 casos pedidos, contra el entorno local ya migrado)

**A) `WHERE tenant_slug IS NOT NULL AND status IN (...) GROUP BY tenant_slug`:**
```
possible_keys: IDX_FISCAL_TENANT_STATUS, idx_fiscal_doc_tenant_slug_status
key: IDX_FISCAL_TENANT_STATUS
```
`idx_fiscal_doc_tenant_status` ya no aparece en `possible_keys` (porque no existe) — el plan sigue siendo válido usando el índice que se conservó.

**B) `tenant_slug='angel' AND status='error' ORDER BY created_at DESC, id DESC`:**
```
possible_keys: IDX_FISCAL_TENANT_STATUS, idx_fiscal_doc_tenant_slug_status
key: idx_fiscal_doc_tenant_slug_status
```
Igual que antes de la migración — este patrón nunca dependió del índice eliminado.

**C) `countByStatus` sin tenant, `created_at >= CURDATE()`:**
```
key: IDX_FISCAL_CREATED
```
Sin cambios respecto a Fase 6.

**C2) `countByStatus` CON tenant, sin ORDER BY:**
```
possible_keys: IDX_FISCAL_TENANT_STATUS, idx_fiscal_doc_tenant_id, idx_fiscal_doc_tenant_slug_status
key: IDX_FISCAL_TENANT_STATUS
```
Confirma exactamente lo que predijo el análisis de Fase 6: este patrón (sin ORDER BY) seguía necesitando un índice de 2 columnas viable, y `IDX_FISCAL_TENANT_STATUS` (el que se conservó) lo sigue resolviendo.

**D) Listado/paginación sin filtro de tenant:**
```
type: ALL (full scan) — igual de "razonable" que antes; a 59 filas locales el optimizador prefiere
scan completo sobre cualquier índice, algo esperado a esta escala y no relacionado con la migración
(en producción, con 38k filas, Fase 6 ya documentó que este patrón usa IDX_FISCAL_CREATED).
```

**Ningún EXPLAIN produjo error ni referenció un índice inexistente.** Los planes para los patrones que SÍ dependían de `IDX_FISCAL_TENANT_STATUS` siguen exactamente iguales a los documentados en Fase 6, porque nunca dependieron del duplicado eliminado.

---

## 5. Pruebas funcionales

```
facturador_lycet (PHPUnit, suite completa, ejecutada DESPUÉS de aplicar la migración localmente):
  171 tests, 444 assertions — OK, 0 fallos
```

Estas pruebas son unitarias con repositorios mockeados (no hacen I/O real contra la base de datos), por lo que estructuralmente no podían verse afectadas por un cambio de índice — se ejecutaron de todas formas, como pide la fase, y confirman que ninguna otra regresión ocurrió en paralelo. Se verificó explícitamente que las reglas de Fases 1-4 (accepted, business, manual_only, permanent, transient/agotado, force, 409, list/detail/queue, históricos) siguen cubiertas por esos mismos 171 tests, sin ningún archivo de esas fases modificado en Fase 7.

No se ejecutó typecheck/lint/build de `frontend_central` ni tests de `backend_go` — no aplica, ningún archivo de esos dos repos fue tocado en esta fase (confirmado por `git status`, sección 7).

---

## 6. Confirmaciones explícitas

- **No se tocó ningún otro índice.** Verificado con `SHOW INDEX` completo antes/después (sección 3) — los 8 índices restantes (`PRIMARY`, `UNIQ_FISCAL_DOC_UUID`, `UNIQ_FISCAL_FINGERPRINT`, `IDX_FISCAL_CREATED`, `IDX_FISCAL_SALE`, `idx_fiscal_doc_series_number`, `idx_fiscal_doc_cursor`, `idx_fiscal_doc_tenant_id`, `idx_fiscal_doc_tenant_slug_status`) están exactamente igual.
- **No se modificó ninguna lógica fiscal.** El único archivo de código nuevo es la migración (`migrations/Version20260920000000.php`); no se tocó ningún archivo de `src/Service/Fiscal/`, `src/Controller/`, `frontend_central` ni `backend_go` en esta fase.
- **No se ejecutó la migración contra producción.** Solo contra el MySQL local de desarrollo (`127.0.0.1:3306`, base `lycet`). El acceso SSH de solo lectura a producción (usado en fases anteriores para medir) **no se usó en absoluto en esta fase** — no hizo falta, ya que el entorno local resultó ser una réplica de schema válida.

---

## 7. Git status / diff --stat

```
facturador_lycet:
?? migrations/Version20260920000000.php   ← único archivo nuevo de esta fase
 M src/Controller/v1/FiscalController.php                          (de Fase 1/2, sin cambios esta fase)
 M src/Service/Fiscal/FiscalBulkActionService.php                  (de Fase 1, sin cambios esta fase)
 M src/Service/Fiscal/FiscalDocumentDetailService.php               (de Fase 5, sin cambios esta fase)
 M src/Service/Fiscal/Observability/FiscalAlertService.php          (de Fase 5, sin cambios esta fase)
 M src/Service/Fiscal/Observability/FiscalOperationsService.php     (de Fase 5, sin cambios esta fase)
 M tests/... (de Fases 1/2/4, sin cambios esta fase)
?? tests/... (de Fases 2/4, sin cambios esta fase)

backend_go:      sin cambios respecto al cierre de Fase 6 (más este documento nuevo)
frontend_central: sin cambios respecto al cierre de Fase 6
```

---

## 8. Confirmación de no commit/push

No se ejecutó ningún `git commit` ni `git push` en ningún repositorio durante esta fase.

---

## 9. Riesgos y pendientes

- **Despliegue a producción: pendiente, paso explícito separado.** La migración está creada y validada localmente (up/down probados, EXPLAIN confirmado), pero **no se ha aplicado a producción**. Cuando se autorice, el paso es: `git push` (tu flujo habitual) → en el VPS, `php bin/console doctrine:migrations:migrate --no-interaction` como parte del deploy normal de CloudPanel (mismo mecanismo ya usado para las migraciones de Fases anteriores esta semana).
- **Riesgo de la migración en sí:** bajo. `DROP INDEX`/`CREATE INDEX` sobre InnoDB en MySQL 8 es `ALGORITHM=INPLACE` (no reconstruye la tabla completa, no bloquea lecturas/escrituras salvo un lock de metadata breve). A 38k filas en producción, el tiempo de ejecución esperado es de milisegundos a pocos segundos.
- **Nada más pendiente de esta fase específica** — las propuestas B (fiscal_tenant_metrics) y C (semántica temporal) de Fase 6 siguen como estaban, sin tocar, tal como se pidió.

Con esto, el bloque de performance (Fases 5-7) queda cerrado técnicamente salvo nueva evidencia — y salvo la autorización explícita para desplegar esta migración a producción.
