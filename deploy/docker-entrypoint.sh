#!/bin/sh
# Arranque en contenedor: migración central rápida y luego el proceso principal (API).
# El migrate completo (tenants) sigue en deploy: ./tukifac-api migrate
set -eu

case "${RUN_MIGRATE_ON_START:-1}" in
  0|false|FALSE|no|NO)
    ;;
  *)
    echo "[entrypoint] migrate-central (esquema central antes del API)..."
    ./tukifac-api migrate-central
    ;;
esac

exec "$@"
