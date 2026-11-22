# Pruebas intermitentes conocidas

## `TestRunUserRoleMigration_ConcurrentRuns_OnlyOneExecutes` (`internal/superadmin/service/sa_user_role_migration_test.go`)

**Estado:** intermitente, **preexistente** (no relacionado con la release de performance de oct-2026).

**Qué prueba:** que dos ejecuciones simultáneas de `RunUserRoleMigration` no corran a la vez: la segunda debe abortar con
`ErrMigrationAlreadyRunning` porque la primera tiene el lock (`SAMigrationLock`, una fila con clave única).

**Por qué falla:** la prueba lanza dos goroutines y espera que **se solapen**. Si la primera termina toda la migración
(adquirir lock → trabajar → liberar lock) antes de que la segunda empiece, no hay contención: las dos tienen éxito y el
conteo de rechazos es 0 → `exactamente una ejecución debió rechazarse por lock, se rechazaron 0`. Depende solo de la
planificación de goroutines:

| Condición | Resultado |
|---|---|
| Equipo ocioso, GOMAXPROCS por defecto | pasa (30/30 corridas aisladas) |
| `GOMAXPROCS=1` (sin paralelismo real) | falla siempre |
| Suite completa en paralelo con otros paquetes | pasa o falla según la carga (falló 1 de 3 corridas completas el 07-oct-2026) |
| `-count=N` con N>1 | falla por otra razón: el DSN SQLite en memoria se comparte por nombre de test y los usuarios persisten (`UNIQUE constraint failed: super_admin_users.email`) |

**Evidencia de que es preexistente:** en un worktree de la versión ya desplegada (`ae5cdcc`) falla igual con
`GOMAXPROCS=1`; ningún commit posterior a `ae5cdcc` toca `internal/superadmin/service`.

**Riesgo para producción:** ninguno. La exclusión mutua real es la restricción única de `SAMigrationLock` en la base de
datos (en MySQL con conexiones concurrentes reales); la prueba solo falla cuando el planificador no solapa las goroutines,
no cuando el lock deja de funcionar. Es una migración puntual de roles del panel central.

**Arreglo sugerido (no aplicado):** forzar el solapamiento (que ambas goroutines esperen una barrera antes de llamar, o que
la primera retenga el lock hasta que la segunda haya intentado) y usar un DSN único por ejecución.

> `go test -race` no está disponible en esta máquina (cgo desactivado); no sirve como comprobación aquí.
