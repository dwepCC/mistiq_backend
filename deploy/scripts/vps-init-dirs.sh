#!/usr/bin/env bash
# Inicialización única del VPS backend. Ejecutar en el servidor:
#   sudo bash deploy/scripts/vps-init-dirs.sh
set -euo pipefail

BASE="${MISTIQ_BASE:-/opt/mistiq}"
DEPLOY_USER="${DEPLOY_USER:-$USER}"

echo "==> Creando estructura en ${BASE}"

mkdir -p "${BASE}/data/uploads" \
         "${BASE}/data/storage/invoices" \
         "${BASE}/.deploy"

chmod -R 755 "${BASE}/data"
touch "${BASE}/.deploy/previous-image" "${BASE}/.deploy/current-image" 2>/dev/null || true

if [[ "$(id -u)" -eq 0 ]] && [[ -n "${DEPLOY_USER}" ]] && [[ "${DEPLOY_USER}" != "root" ]]; then
  chown -R "${DEPLOY_USER}:${DEPLOY_USER}" "${BASE}"
fi

cat <<EOF

Estructura lista:

${BASE}/
├── docker-compose.production.yml   (copiar desde el repo)
├── .env                            (desde .env.production.example)
├── .deploy/
│   ├── current-image
│   └── previous-image              (rollback)
└── data/
    ├── uploads/                    → /app/uploads
    └── storage/
        └── invoices/               → /app/storage/invoices

Siguiente paso:
  1. Copiar docker-compose.production.yml y crear .env
  2. docker compose -f docker-compose.production.yml pull
  3. docker compose -f docker-compose.production.yml up -d
  4. docker exec mistiq-backend-go ./mistiq-api migrate

EOF
