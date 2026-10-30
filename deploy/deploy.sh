#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

release_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
[[ $(basename "$(dirname "$release_dir")") == releases ]] || {
  echo 'Run this script from DEPLOY_DIR/releases/RELEASE_ID.' >&2; exit 1;
}
deploy_root=$(cd "$release_dir/../.." && pwd -P)
cd "$release_dir"
test -f .env && test -f docker-compose.yml
chmod 600 .env
export DOCKER_CONFIG="$release_dir/.docker-config"

# Also serialize deployments started manually on the server.
exec 9>"$deploy_root/.deploy.lock"
flock -w 600 9
compose() {
  docker compose --project-name cms-backend-v2 --env-file "$release_dir/.env" -f "$release_dir/docker-compose.yml" "$@"
}
cleanup() {
  local result=$?
  if (( result != 0 )); then
    echo 'Deployment failed; current release pointer was not advanced.' >&2
    compose ps || true
    compose logs --no-color --tail=100 server || true
  fi
  rm -f -- "$DOCKER_CONFIG/config.json"
  rmdir -- "$DOCKER_CONFIG" 2>/dev/null || true
  exit "$result"
}
trap cleanup EXIT

compose config --quiet
compose pull postgres server
compose up -d --wait --wait-timeout 180 postgres
compose up -d --wait --wait-timeout 180 --no-deps server

if [[ -L "$deploy_root/current" ]]; then
  readlink -f "$deploy_root/current" > "$release_dir/previous-release"
fi
ln -sfn -- "$release_dir" "$deploy_root/current.next"
mv -Tf -- "$deploy_root/current.next" "$deploy_root/current"
compose ps
echo 'Application is healthy. Verify the public HTTP tunnel separately.'
