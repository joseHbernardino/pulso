#!/usr/bin/env bash
# Manage a checkout without sourcing .env or changing existing data ownership.
set -euo pipefail
umask 077

root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$root_dir"

fail() { printf 'Pulso: %s\n' "$*" >&2; exit 1; }
help() {
  cat <<'EOF'
Uso: bash scripts/pulso-docker.sh COMANDO [OPÇÕES]

  install, start  Configurar e iniciar, preservando uma instalação existente
  update          Atualizar o código e recriar os serviços no mesmo modo
  restart         Reiniciar os serviços
  status          Mostrar o estado dos serviços
  logs            Acompanhar os logs
  token           Mostrar o código do primeiro acesso
  doctor          Verificar os requisitos e a configuração efetiva

Opções de install/start:
  --host          Usar projetos de qualquer pasta do servidor
  --build         Compilar a imagem neste checkout (padrão em instalação nova)
  --registry      Usar a imagem publicada em PULSO_IMAGE
  --updates       Ativar atualizações pelo portal (requer --registry)
  --port N        Escolher a porta do painel sem editar arquivos

Requer Linux, Docker Engine e Docker Compose 2.24.4 ou posterior.
EOF
}

command_name="${1:-help}"
if (( $# )); then shift; fi
case "$command_name" in
  help|-h|--help) help; exit 0 ;;
  install|start|update|restart|status|logs|token|doctor) ;;
  *) help >&2; fail "Comando desconhecido: $command_name" ;;
esac

requested_host=false
requested_image=""
requested_updates=false
requested_port=""
while (( $# )); do
  case "$1" in
    --host) requested_host=true ;;
    --build) requested_image=build ;;
    --registry) requested_image=registry ;;
    --updates) requested_updates=true ;;
    --port)
      (( $# >= 2 )) || fail "Informe a porta depois de --port."
      shift; requested_port="$1"
      [[ "$requested_port" =~ ^[0-9]{1,5}$ ]] && (( 10#$requested_port >= 1 && 10#$requested_port <= 65535 )) || fail "A porta deve estar entre 1 e 65535."
      requested_port="$((10#$requested_port))"
      ;;
    *) fail "Opção desconhecida: $1" ;;
  esac
  shift
done
if [[ "$command_name" != install && "$command_name" != start ]]; then
  [[ "$requested_host" == false && -z "$requested_image" && "$requested_updates" == false && -z "$requested_port" ]] || fail "As opções de configuração são usadas com install ou start."
fi

[[ "$(uname -s)" == Linux ]] || fail "Este instalador usa Docker Engine no Linux."
command -v docker >/dev/null || fail "Docker não encontrado. Instale o Docker Engine e o plugin Compose: https://docs.docker.com/engine/install/"
docker info >/dev/null || fail "Sem acesso ao Docker. Confira se o serviço está iniciado e se este usuário pode acessá-lo."
compose_version="$(docker compose version --short)" || fail "Instale o plugin Docker Compose."
compose_version="${compose_version#v}"
IFS=. read -r major minor patch <<<"${compose_version%%-*}"
[[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ && "$patch" =~ ^[0-9]+$ ]] || fail "Não foi possível identificar a versão do Compose: $compose_version"
(( major > 2 || (major == 2 && (minor > 24 || (minor == 24 && patch >= 4))) )) || fail "Atualize o Docker Compose para 2.24.4 ou posterior; encontrado $compose_version."

# Compose parses its own dotenv syntax. Never execute a user's .env as shell code.
compose_environment="$(docker compose -f compose.yaml config --environment)"
setting() { printf '%s\n' "$compose_environment" | sed -n "s/^$1=//p"; }
saved_files="$(setting COMPOSE_FILE)"
project_name="$(setting COMPOSE_PROJECT_NAME)"
if [[ -z "$project_name" ]]; then
  checkout_ids="$(docker ps -aq --filter "label=com.docker.compose.project.working_dir=$root_dir" --filter label=com.docker.compose.service=pulso --filter label=com.docker.compose.oneoff=False)"
  if [[ -n "$checkout_ids" && "$checkout_ids" != *$'\n'* ]]; then
    project_name="$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$checkout_ids")"
  elif [[ -n "$checkout_ids" ]]; then
    fail "Há mais de uma instalação nesta pasta. Informe COMPOSE_PROJECT_NAME para selecionar a instalação."
  else
    project_name="$(docker compose -f compose.yaml config | sed -n 's/^name: //p')"
  fi
fi
[[ -n "$project_name" ]] || fail "Não foi possível identificar o projeto Compose."
apps_dir="$(setting PULSO_APPS_DIR)"
apps_dir="${apps_dir:-./applications}"
existing_ids="$(docker ps -aq --filter "label=com.docker.compose.project=$project_name" --filter label=com.docker.compose.service=pulso --filter label=com.docker.compose.oneoff=False)"
existing=""
while IFS= read -r id; do
  [[ -n "$id" ]] || continue
  [[ -z "$existing" ]] || fail "Mais de um contêiner Pulso neste projeto. Use a configuração Compose existente para verificar a instalação."
  existing="$id"
done <<<"$existing_ids"

selected_files=()
if [[ -n "$saved_files" ]]; then
  IFS=: read -r -a selected_files <<<"$saved_files"
elif [[ -n "$existing" ]]; then
  previous_files="$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' "$existing")"
  if [[ -n "$previous_files" && "$previous_files" != '<no value>' ]]; then
    IFS=, read -r -a selected_files <<<"$previous_files"
  fi
fi
if (( ${#selected_files[@]} == 0 )); then
  selected_files=(compose.yaml compose.build.yaml)
fi

has_file() {
  local candidate
  for candidate in "${selected_files[@]}"; do
    [[ "${candidate##*/}" != "$1" ]] || return 0
  done
  return 1
}
remove_file() {
  local candidate retained=()
  for candidate in "${selected_files[@]}"; do
    [[ "${candidate##*/}" == "$1" ]] || retained+=("$candidate")
  done
  selected_files=("${retained[@]}")
}
if [[ "$requested_image" == build ]]; then
  remove_file compose.update.yaml
  has_file compose.build.yaml || selected_files+=(compose.build.yaml)
elif [[ "$requested_image" == registry ]]; then
  remove_file compose.build.yaml
fi
if [[ "$requested_host" == true || "$apps_dir" == / ]]; then
  has_file compose.host.yaml || selected_files+=(compose.host.yaml)
fi
if [[ "$requested_updates" == true ]]; then
  has_file compose.build.yaml && fail "Atualização pelo portal usa uma imagem publicada. Execute install --registry --updates depois de publicar a release."
  has_file compose.update.yaml || selected_files+=(compose.update.yaml)
fi
has_file compose.apps.yaml || selected_files+=(compose.apps.yaml)
# Host mode replaces the volume list; re-add the application socket afterwards.
remove_file compose.apps.yaml
selected_files+=(compose.apps.yaml)
# The update volume must be added after host mode's !override volumes.
if has_file compose.update.yaml; then
  remove_file compose.update.yaml
  selected_files+=(compose.update.yaml)
fi
for file in "${selected_files[@]}"; do
  [[ -f "$file" ]] || fail "Arquivo da instalação não encontrado: $file. A configuração existente foi preservada."
done
export COMPOSE_FILE
COMPOSE_FILE="$(IFS=:; printf '%s' "${selected_files[*]}")"
export COMPOSE_PROJECT_NAME="$project_name"
[[ -z "$requested_port" ]] || export PULSO_PORT="$requested_port"
docker_socket="$(setting PULSO_DOCKER_SOCKET)"
if [[ -z "$docker_socket" ]]; then
  if [[ -n "${DOCKER_HOST:-}" && -z "${DOCKER_CONTEXT:-}" ]]; then
    docker_endpoint="$DOCKER_HOST"
  else
    docker_endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}')"
  fi
  [[ "$docker_endpoint" == unix://* ]] || fail "Execute o instalador no Linux do Docker Engine, usando seu socket Unix local."
  docker_socket="${docker_endpoint#unix://}"
fi
[[ -S "$docker_socket" ]] || fail "Socket Docker não encontrado: $docker_socket"
export PULSO_DOCKER_SOCKET="$docker_socket"
export PULSO_AGENT_IMAGE
PULSO_AGENT_IMAGE="$(docker compose config --images pulso)"
docker compose config --quiet

save_setting() {
  local name="$1" value="$2" temporary
  [[ ! -L .env ]] || fail "O .env é um link simbólico. Use o Compose com os arquivos exibidos por doctor para preservar esse arquivo."
  [[ "$value" != *$'\n'* && "$value" != *"'"* && "$value" != *'\'* ]] || fail "Valor não suportado em $name."
  temporary="$(mktemp .env.pulso.XXXXXX)"
  if [[ -f .env ]]; then
    # Match only the key being changed; preserve all other settings and comments.
    awk -v key="$name" '$0 !~ "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" { print }' .env > "$temporary"
  fi
  printf "%s='%s'\n" "$name" "$value" >> "$temporary"
  mv -f -- "$temporary" .env
}
prepare() {
  if has_file compose.build.yaml; then
    docker buildx version >/dev/null || fail "Instale o plugin Docker Buildx para compilar a imagem."
    if command -v git >/dev/null && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
      export PULSO_BUILD_REVISION="$(git rev-parse HEAD)"
      export PULSO_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi
    docker compose build
  else
    docker compose pull || fail "Não foi possível baixar a imagem. Confira se a release foi publicada e o pacote está público. Para compilar localmente: bash scripts/pulso-docker.sh start --build"
  fi
  if [[ ! -d "$apps_dir" ]]; then
    mkdir -p -- "$apps_dir"
    if ! has_file compose.host.yaml; then
      # Only a newly created project directory is assigned to the image user.
      # Let the Engine map ownership correctly, including rootless/userns setups.
      docker compose run --rm --no-deps --user 0:0 --cap-add CHOWN --cap-add FOWNER \
        --entrypoint /bin/sh pulso -c 'chown 10001:10001 /apps && chmod 0750 /apps'
    fi
  fi
  save_setting COMPOSE_FILE "$COMPOSE_FILE"
  save_setting COMPOSE_PROJECT_NAME "$project_name"
  save_setting PULSO_AGENT_IMAGE "$PULSO_AGENT_IMAGE"
  save_setting PULSO_DOCKER_SOCKET "$PULSO_DOCKER_SOCKET"
  [[ -z "$requested_port" ]] || save_setting PULSO_PORT "$requested_port"
}

case "$command_name" in
  install|start|update)
    if [[ "$command_name" == update ]] && has_file compose.build.yaml; then
      command -v git >/dev/null || fail "Instale Git para atualizar o checkout."
      git diff --quiet && git diff --cached --quiet || fail "Existem alterações locais no código. Salve-as em um commit antes de atualizar."
      git pull --ff-only
    fi
    prepare
    docker compose up -d --wait --wait-timeout 180
    docker compose ps
    port="${requested_port:-$(setting PULSO_PORT)}"
    printf '\nPulso disponível em http://IP_DO_SERVIDOR:%s\n' "${port:-5011}"
    printf 'Configuração preservada. Próxima atualização: bash scripts/pulso-docker.sh update\n'
    ;;
  restart)
    docker compose restart
    docker compose up -d --no-recreate --wait --wait-timeout 180
    ;;
  status) docker compose ps ;;
  logs) docker compose logs --tail=100 -f ;;
  token) docker compose exec -T pulso cat /var/lib/pulso/security/setup-token ;;
  doctor)
    printf 'Docker Compose: %s\nProjeto: %s\nArquivos: %s\n' "$compose_version" "$project_name" "$COMPOSE_FILE"
    if has_file compose.build.yaml; then printf 'Imagem: compilação local\n'; else printf 'Imagem: registro\n'; fi
    if has_file compose.host.yaml; then printf 'Pastas: servidor completo (modo host)\n'; else printf 'Pastas: diretório de aplicações\n'; fi
    if [[ -n "$existing" ]]; then
      docker inspect --format 'Contêiner: {{.Name}} | usuário: {{.Config.User}} | estado: {{.State.Status}}' "$existing"
    fi
    docker compose ps
    ;;
esac
