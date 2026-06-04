#!/usr/bin/env bash
# Restaura la imagen anterior guardada en .deploy/previous-image
set -euo pipefail

BASE_DIR="${MISTIQ_BASE:-/opt/mistiq}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
ENV_FILE="${ENV_FILE:-.env}"
PREV_FILE="${BASE_DIR}/.deploy/previous-image"

cd "${BASE_DIR}"

if [[ ! -f "${PREV_FILE}" ]] || [[ ! -s "${PREV_FILE}" ]]; then
  echo "ERROR: no hay imagen anterior en ${PREV_FILE}"
  echo "Edite ${ENV_FILE} manualmente: MISTIQ_IMAGE=ghcr.io/ORG/repo:<sha-anterior>"
  exit 1
fi

PREV_IMAGE="$(tr -d '\r\n' < "${PREV_FILE}")"
if [[ -z "${PREV_IMAGE}" ]]; then
  echo "ERROR: ${PREV_FILE} está vacío"
  exit 1
fi

CURRENT="$(grep -E '^MISTIQ_IMAGE=' "${ENV_FILE}" | cut -d= -f2- | tr -d '\r\n' || true)"
echo "==> Rollback"
echo "    Actual:   ${CURRENT:-desconocido}"
echo "    Anterior: ${PREV_IMAGE}"

if [[ -f "${BASE_DIR}/.deploy/current-image" ]]; then
  echo "${CURRENT}" > "${BASE_DIR}/.deploy/previous-image" || true
fi

if grep -q '^MISTIQ_IMAGE=' "${ENV_FILE}"; then
  sed -i "s|^MISTIQ_IMAGE=.*|MISTIQ_IMAGE=${PREV_IMAGE}|" "${ENV_FILE}"
else
  echo "MISTIQ_IMAGE=${PREV_IMAGE}" >> "${ENV_FILE}"
fi

docker compose -f "${COMPOSE_FILE}" pull backend-go
docker compose -f "${COMPOSE_FILE}" up -d --no-deps backend-go

echo "==> Rollback aplicado. Verificando health..."
if [[ -x "${BASE_DIR}/deploy/scripts/health-check.sh" ]]; then
  bash "${BASE_DIR}/deploy/scripts/health-check.sh" || exit 1
else
  curl -sf http://127.0.0.1:3000/health >/dev/null || exit 1
fi

echo "OK: rollback completado con ${PREV_IMAGE}"
echo "Nota: si el rollback incluye cambios de esquema, puede requerir restore de BD."
