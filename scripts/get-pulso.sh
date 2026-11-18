#!/bin/sh
# Instalador do Pulso para qualquer distribuição Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/joseHbernardino/pulso/main/scripts/get-pulso.sh | sh
#
# Objetivos:
#   - Não exigir checkout do repositório, Go, Node.js, Python nem Docker Compose.
#   - Instalar o Docker Engine quando ausente, nas famílias Debian, RHEL, SUSE,
#     Arch, Alpine e Amazon Linux.
#   - Subir o painel e o agente com `docker run`, sem arquivos Compose.
#   - Ser idempotente: executar de novo atualiza a instalação existente.
#
# ATENÇÃO (acoplamento com o código): o agente se localiza pelos rótulos
# `com.docker.compose.project` e `com.docker.compose.service` (ver
# internal/containerupdate/job.go e agent.go). Este script os aplica à mão.
# A rede das aplicações é derivada como "<projeto>-applications". Alterar
# PULSO_PROJECT em uma instalação existente órfã a rede e os contêineres.

set -eu

PULSO_PROJECT="${PULSO_PROJECT:-pulso}"
PANEL_SERVICE="pulso"
AGENT_SERVICE="pulso-updater"
PANEL_NAME="${PULSO_PROJECT}-panel"
AGENT_NAME="${PULSO_PROJECT}-updater"
DATA_VOLUME="${PULSO_PROJECT}_data"
UPDATE_VOLUME="${PULSO_PROJECT}_updates"

IMAGE_REPO="${PULSO_IMAGE_REPO:-ghcr.io/josehbernardino/pulso}"
IMAGE_TAG="${PULSO_IMAGE_TAG:-latest}"
SOURCE_REPO="${PULSO_RELEASE_REPOSITORY:-joseHbernardino/pulso}"
SOURCE_REF="${PULSO_SOURCE_REF:-main}"

STATE_DIR="${PULSO_STATE_DIR:-/var/lib/pulso}"
APPS_DIR="${PULSO_APPS_DIR:-${STATE_DIR}/applications}"
CONF_DIR="${PULSO_CONF_DIR:-/etc/pulso}"
CONF_FILE=""   # definido após o parse, por projeto

PORT="${PULSO_PORT:-5011}"
BIND="${PULSO_BIND:-0.0.0.0}"
APP_UID=10001
APP_GID=10001

ACTION=install
IMAGE_MODE=auto          # auto | registry | local | build

# Marcadores do que veio explicitamente da linha de comando. A configuração
# salva na instalação só preenche o que não foi informado agora.
PORT_SET=0
BIND_SET=0
APPS_DIR_SET=0
IMAGE_REPO_SET=0
IMAGE_TAG_SET=0
INSTALL_DOCKER=yes
OPEN_FIREWALL=no
HOST_SOURCE=""
HOST_SOURCE_SET=0
HOST_PRIVILEGED=no
HOST_PRIVILEGED_SET=0
DRY_RUN="${PULSO_DRY_RUN:-0}"
ASSUME_YES="${PULSO_ASSUME_YES:-0}"

# ---------------------------------------------------------------- utilidades

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    C_OK='\033[0;32m'; C_WARN='\033[0;33m'; C_ERR='\033[0;31m'; C_DIM='\033[2m'; C_OFF='\033[0m'
else
    C_OK=''; C_WARN=''; C_ERR=''; C_DIM=''; C_OFF=''
fi

log()  { printf '%b==>%b %s\n' "$C_OK" "$C_OFF" "$*"; }
step() { printf '%b  ->%b %s\n' "$C_DIM" "$C_OFF" "$*"; }
warn() { printf '%baviso:%b %s\n' "$C_WARN" "$C_OFF" "$*" >&2; }
die()  { printf '%berro:%b %s\n' "$C_ERR" "$C_OFF" "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# run executa um comando, ou apenas o descreve quando DRY_RUN=1.
# O aviso do modo simulado vai para a saída de erro de propósito: várias
# chamadas redirecionam a saída padrão para /dev/null, e isso engolia justamente
# a criação dos contêineres — o que a simulação mais precisa mostrar.
run() {
    if [ "$DRY_RUN" = "1" ]; then
        printf '%b[dry-run]%b %s\n' "$C_DIM" "$C_OFF" "$*" >&2
        return 0
    fi
    "$@"
}

# run_sh executa uma linha de shell (para pipes e redirecionamentos).
run_sh() {
    if [ "$DRY_RUN" = "1" ]; then
        printf '%b[dry-run]%b sh -c %s\n' "$C_DIM" "$C_OFF" "$1" >&2
        return 0
    fi
    sh -c "$1"
}

usage() {
    cat <<'USAGE'
Instalador do Pulso.

Uso: get-pulso.sh [opções]

Opções:
  --port <n>          Porta publicada do painel (padrão 5011)
  --bind <endereço>   IP em que o painel escuta (padrão 0.0.0.0; use 127.0.0.1
  --ip <endereço>     para expor apenas localmente). As duas formas são a mesma
                      opção.
  --registry          Exige a imagem publicada; não compila
  --build             Compila a imagem a partir do código-fonte baixado
  --local             Usa uma imagem já presente no host; não acessa a rede
                      (instalação em rede fechada / air-gapped)
  --image <ref>       Repositório da imagem (padrão ghcr.io/josehbernardino/pulso)
  --tag <tag>         Tag da imagem (padrão latest)
  --no-docker-install Não instala o Docker; falha se estiver ausente
  --open-firewall     Libera a porta no firewalld/ufw quando ativo
  --apps-dir <path>   Pasta de projetos no host (padrão /var/lib/pulso/applications)
  --host-root <path>  Expõe uma pasta do servidor dentro do painel, somente
                      leitura, para implantar projetos que vivem fora da pasta
                      de projetos. Use / para o servidor inteiro.
  --host-root-privileged
                      Sobe o painel como root com DAC_OVERRIDE, para ler árvores
                      sem permissão de leitura a outros. Só em servidor de
                      confiança.
  --project <nome>    Nome do projeto/instância (padrão pulso)
  --yes               Não faz perguntas
  --status            Mostra o estado da instalação e sai
  --token             Mostra o código de primeiro acesso ainda pendente
  --uninstall         Remove os contêineres do Pulso (preserva volumes)
  --purge             Remove contêineres E volumes de dados (destrutivo)
  --dry-run           Mostra o que seria feito, sem executar
  -h, --help          Esta ajuda

Variáveis equivalentes: PULSO_PORT, PULSO_BIND, PULSO_IMAGE_REPO, PULSO_IMAGE_TAG,
PULSO_APPS_DIR, PULSO_PROJECT, PULSO_DRY_RUN, PULSO_ASSUME_YES.
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --port) [ $# -ge 2 ] || die "--port exige um valor"; PORT="$2"; PORT_SET=1; shift 2 ;;
        --bind|--ip) [ $# -ge 2 ] || die "$1 exige um valor"; BIND="$2"; BIND_SET=1; shift 2 ;;
        --image) [ $# -ge 2 ] || die "--image exige um valor"; IMAGE_REPO="$2"; IMAGE_REPO_SET=1; shift 2 ;;
        --tag) [ $# -ge 2 ] || die "--tag exige um valor"; IMAGE_TAG="$2"; IMAGE_TAG_SET=1; shift 2 ;;
        --host-root) [ $# -ge 2 ] || die "--host-root exige um valor"; HOST_SOURCE="$2"; HOST_SOURCE_SET=1; shift 2 ;;
        --host-root-privileged) HOST_PRIVILEGED=yes; HOST_PRIVILEGED_SET=1; shift ;;
        --apps-dir) [ $# -ge 2 ] || die "--apps-dir exige um valor"; APPS_DIR="$2"; APPS_DIR_SET=1; shift 2 ;;
        --project)
            [ $# -ge 2 ] || die "--project exige um valor"
            PULSO_PROJECT="$2"
            PANEL_NAME="${PULSO_PROJECT}-panel"
            AGENT_NAME="${PULSO_PROJECT}-updater"
            DATA_VOLUME="${PULSO_PROJECT}_data"
            UPDATE_VOLUME="${PULSO_PROJECT}_updates"
            shift 2 ;;
        --registry) IMAGE_MODE=registry; shift ;;
        --build) IMAGE_MODE=build; shift ;;
        --local) IMAGE_MODE=local; shift ;;
        --no-docker-install) INSTALL_DOCKER=no; shift ;;
        --open-firewall) OPEN_FIREWALL=yes; shift ;;
        --yes|-y) ASSUME_YES=1; shift ;;
        --status) ACTION=status; shift ;;
        --token) ACTION=token; shift ;;
        --uninstall) ACTION=uninstall; shift ;;
        --purge) ACTION=purge; shift ;;
        --dry-run) DRY_RUN=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "opção desconhecida: $1 (use --help)" ;;
    esac
done

case "$PORT" in
    ''|*[!0-9]*) die "porta inválida: $PORT" ;;
esac
[ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "porta fora da faixa: $PORT"
case "$PULSO_PROJECT" in
    ''|*[!a-z0-9_-]*) die "nome de projeto inválido: use apenas a-z, 0-9, _ e -" ;;
esac

# Uma configuração por projeto permite várias instâncias no mesmo host.
CONF_FILE="${CONF_DIR}/${PULSO_PROJECT}.conf"

# load_conf recupera a configuração gravada na instalação, para que --status,
# --uninstall e --purge atuem sobre a instalação real, e para que uma
# reinstalação preserve porta, endereço e pastas sem repetir as opções.
load_conf() {
    [ -r "$CONF_FILE" ] || return 0
    # O arquivo é gerado por este script e define variáveis PULSO_*, com
    # valores entre aspas simples. Os nomes gravados são diferentes dos nomes
    # internos, então cada um é mapeado à mão; um valor vindo da linha de
    # comando sempre vence o que está gravado.
    # shellcheck disable=SC1090
    . "$CONF_FILE"
    [ "$PORT_SET" = 1 ] || [ -z "${PULSO_PORT:-}" ] || PORT="$PULSO_PORT"
    [ "$BIND_SET" = 1 ] || [ -z "${PULSO_BIND:-}" ] || BIND="$PULSO_BIND"
    [ "$APPS_DIR_SET" = 1 ] || [ -z "${PULSO_APPS_DIR:-}" ] || APPS_DIR="$PULSO_APPS_DIR"
    [ "$HOST_SOURCE_SET" = 1 ] || [ -z "${PULSO_HOST_SOURCE:-}" ] || HOST_SOURCE="$PULSO_HOST_SOURCE"
    [ "$HOST_PRIVILEGED_SET" = 1 ] || [ -z "${PULSO_HOST_PRIVILEGED:-}" ] || HOST_PRIVILEGED="$PULSO_HOST_PRIVILEGED"
    [ "$IMAGE_REPO_SET" = 1 ] || [ -z "${PULSO_IMAGE_REPO:-}" ] || IMAGE_REPO="$PULSO_IMAGE_REPO"
    [ "$IMAGE_TAG_SET" = 1 ] || [ -z "${PULSO_IMAGE_TAG:-}" ] || IMAGE_TAG="$PULSO_IMAGE_TAG"
    step "Configuração anterior carregada de ${CONF_FILE}."
    return 0
}

# ------------------------------------------------------- privilégios e sistema

SUDO=""
need_root() {
    if [ "$(id -u)" = "0" ]; then
        return 0
    fi
    if have sudo; then
        SUDO="sudo"
        step "Usando sudo para as etapas privilegiadas."
        return 0
    fi
    die "Execute como root ou instale o sudo."
}

# priv executa com escalonamento quando necessário.
priv() {
    if [ -n "$SUDO" ]; then
        run "$SUDO" "$@"
    else
        run "$@"
    fi
}

priv_sh() {
    if [ -n "$SUDO" ]; then
        run_sh "$SUDO sh -c '$1'"
    else
        run_sh "$1"
    fi
}

OS_ID=""
OS_LIKE=""
OS_VERSION=""
OS_NAME=""

detect_os() {
    [ "$(uname -s)" = "Linux" ] || die "Este instalador é para Linux. Em macOS e Windows use o Docker Desktop com a imagem publicada."
    if [ -r /etc/os-release ]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        OS_ID="${ID:-}"
        OS_LIKE="${ID_LIKE:-}"
        OS_VERSION="${VERSION_ID:-}"
        OS_NAME="${PRETTY_NAME:-${NAME:-$OS_ID}}"
    elif [ -r /etc/redhat-release ]; then
        OS_ID=rhel
        OS_LIKE=rhel
        OS_NAME="$(cat /etc/redhat-release)"
    else
        OS_ID=unknown
        OS_NAME="Linux desconhecido"
    fi
    log "Sistema: ${OS_NAME}"
}

ARCH=""
detect_arch() {
    machine="$(uname -m)"
    case "$machine" in
        x86_64|amd64) ARCH=amd64 ;;
        aarch64|arm64) ARCH=arm64 ;;
        *) die "arquitetura não suportada: $machine (use x86_64 ou aarch64)" ;;
    esac
    step "Arquitetura: ${ARCH}"
}

# os_family normaliza a distribuição em uma família de gerenciador de pacotes.
os_family() {
    case "$OS_ID" in
        debian|ubuntu|linuxmint|pop|elementary|zorin|kali|raspbian|devuan) echo debian; return ;;
        rhel|centos|rocky|almalinux|ol|oracle|fedora|scientific|cloudlinux) echo rhel; return ;;
        amzn) echo amzn; return ;;
        sles|sled|opensuse|opensuse-leap|opensuse-tumbleweed) echo suse; return ;;
        arch|archarm|manjaro|endeavouros|garuda) echo arch; return ;;
        alpine) echo alpine; return ;;
    esac
    # Distribuições derivadas declaram a origem em ID_LIKE.
    for like in $OS_LIKE; do
        case "$like" in
            debian|ubuntu) echo debian; return ;;
            rhel|centos|fedora) echo rhel; return ;;
            suse|opensuse) echo suse; return ;;
            arch) echo arch; return ;;
            alpine) echo alpine; return ;;
        esac
    done
    echo unknown
}

# ------------------------------------------------------------- Docker Engine

docker_ready() {
    have docker || return 1
    docker info >/dev/null 2>&1
}

start_docker() {
    if docker_ready; then
        return 0
    fi
    step "Iniciando o serviço do Docker."
    if have systemctl && [ -d /run/systemd/system ]; then
        priv systemctl enable --now docker 2>/dev/null \
            || priv systemctl start docker 2>/dev/null \
            || warn "systemctl não iniciou o docker; verifique 'systemctl status docker'."
    elif have rc-service; then
        priv rc-update add docker default 2>/dev/null || true
        priv rc-service docker start 2>/dev/null || true
    elif have service; then
        priv service docker start 2>/dev/null || true
    else
        warn "Sem systemd, OpenRC ou SysV detectado. Inicie o daemon do Docker manualmente."
    fi

    [ "$DRY_RUN" = "1" ] && return 0

    waited=0
    while [ "$waited" -lt 60 ]; do
        if docker_ready; then
            return 0
        fi
        sleep 2
        waited=$((waited + 2))
    done
    return 1
}

# Em RHEL 8+ o pacote podman-docker fornece /usr/bin/docker e conflita com o
# docker-ce. Removê-lo é necessário; o podman em si é preservado.
clear_podman_shim() {
    if ! have rpm; then
        return 0
    fi
    if rpm -q podman-docker >/dev/null 2>&1; then
        warn "O pacote podman-docker ocupa /usr/bin/docker e impede o Docker Engine."
        step "Removendo apenas podman-docker (o podman continua instalado)."
        priv_sh "(dnf -y remove podman-docker || yum -y remove podman-docker)"
    fi
}

install_docker_official() {
    # Script oficial da Docker: cobre Debian, Ubuntu, RHEL, CentOS, Rocky,
    # AlmaLinux, Fedora, SLES e Raspbian, e instala os plugins buildx/compose.
    step "Instalando o Docker Engine pelo script oficial (get.docker.com)."
    if ! have curl && ! have wget; then
        die "Instale curl ou wget para baixar o instalador do Docker."
    fi
    if have curl; then
        priv_sh "curl -fsSL https://get.docker.com -o /tmp/pulso-get-docker.sh"
    else
        priv_sh "wget -qO /tmp/pulso-get-docker.sh https://get.docker.com"
    fi
    priv_sh "sh /tmp/pulso-get-docker.sh"
    priv_sh "rm -f /tmp/pulso-get-docker.sh"
}

install_docker() {
    family="$(os_family)"
    log "Instalando o Docker Engine (família: ${family})."
    case "$family" in
        debian|rhel)
            [ "$family" = rhel ] && clear_podman_shim
            install_docker_official
            ;;
        amzn)
            # A Docker não publica repositório para o Amazon Linux.
            if [ "${OS_VERSION%%.*}" = "2" ] && have amazon-linux-extras; then
                priv_sh "amazon-linux-extras install -y docker"
            elif have dnf; then
                priv_sh "dnf -y install docker"
            else
                priv_sh "yum -y install docker"
            fi
            warn "O Amazon Linux fornece o Docker sem os plugins buildx/compose. O modo --build precisa do buildx."
            ;;
        suse)
            priv_sh "zypper --non-interactive install docker docker-buildx || zypper --non-interactive install docker"
            ;;
        arch)
            priv_sh "pacman -Sy --noconfirm docker docker-buildx"
            ;;
        alpine)
            priv_sh "apk add --no-cache docker docker-cli-buildx"
            ;;
        *)
            die "Não sei instalar o Docker em '${OS_ID}'. Instale o Docker Engine e execute de novo com --no-docker-install. Referência: https://docs.docker.com/engine/install/"
            ;;
    esac
}

ensure_docker() {
    if docker_ready; then
        step "Docker Engine presente: $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo desconhecido)"
        return 0
    fi
    if have docker; then
        step "Cliente Docker presente, mas o daemon não responde."
        if start_docker; then
            step "Daemon do Docker ativo."
            return 0
        fi
        die "O daemon do Docker não respondeu. Verifique 'systemctl status docker' e as permissões do socket."
    fi
    if [ "$INSTALL_DOCKER" = no ]; then
        die "Docker não encontrado e --no-docker-install foi informado."
    fi
    need_root
    install_docker
    if [ "$DRY_RUN" = "1" ]; then
        step "(dry-run) pularia a espera pelo daemon."
        return 0
    fi
    start_docker || die "Instalei o Docker, mas o daemon não subiu. Verifique 'systemctl status docker'."
    step "Docker Engine instalado e ativo."
}

DOCKER_SOCKET=""
detect_socket() {
    endpoint=""
    if [ -n "${DOCKER_HOST:-}" ] && [ -z "${DOCKER_CONTEXT:-}" ]; then
        endpoint="$DOCKER_HOST"
    else
        endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || echo '')"
    fi
    if [ -z "$endpoint" ]; then
        endpoint="unix:///var/run/docker.sock"
    fi
    case "$endpoint" in
        unix://*) DOCKER_SOCKET="${endpoint#unix://}" ;;
        *) die "O agente do Pulso precisa do socket Unix local do Engine. Contexto atual: ${endpoint}. Execute este instalador no servidor do Docker." ;;
    esac
    if [ ! -S "$DOCKER_SOCKET" ] && [ "$DRY_RUN" != "1" ]; then
        die "Socket do Docker não encontrado em ${DOCKER_SOCKET}."
    fi
    step "Socket do Engine: ${DOCKER_SOCKET}"
}

# ----------------------------------------------------------------- SELinux

SELINUX_ON=no
MOUNT_SUFFIX=""
detect_selinux() {
    if have getenforce; then
        state="$(getenforce 2>/dev/null || echo Disabled)"
    elif [ -r /sys/fs/selinux/enforce ]; then
        if [ "$(cat /sys/fs/selinux/enforce 2>/dev/null || echo 0)" = "1" ]; then
            state=Enforcing
        else
            state=Permissive
        fi
    else
        state=Disabled
    fi
    case "$state" in
        Enforcing|Permissive)
            SELINUX_ON=yes
            # :Z rotula o conteúdo do bind como privado deste contêiner, o que
            # mantém o SELinux ativo. Não desabilitamos o SELinux.
            MOUNT_SUFFIX=":Z"
            step "SELinux ${state}: usando rótulo privado (:Z) nos binds."
            if have rpm && ! rpm -q container-selinux >/dev/null 2>&1; then
                warn "Pacote container-selinux ausente; instalando."
                priv_sh "(dnf -y install container-selinux || yum -y install container-selinux)" || warn "Não instalei container-selinux; siga com atenção aos negados de SELinux."
            fi
            ;;
        *) step "SELinux inativo." ;;
    esac
}

# ---------------------------------------------------------------- cgroups

check_cgroups() {
    # A presença de cgroup.controllers é o marcador confiável da hierarquia
    # unificada (v2). O `stat -fc %T` não serve: o busybox do Alpine responde
    # UNKNOWN para o magic do cgroup2fs.
    if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
        step "cgroups v2 (unificado): limites de CPU e memória por aplicação disponíveis."
    elif [ -d /sys/fs/cgroup/memory ] || [ -d /sys/fs/cgroup/cpu ] || [ -d /sys/fs/cgroup/cpuacct ]; then
        warn "Host em cgroups v1. Os limites por contêiner funcionam, mas a hierarquia unificada é recomendada."
        warn "Para migrar: adicione systemd.unified_cgroup_hierarchy=1 aos parâmetros do kernel e reinicie."
    else
        warn "Não identifiquei a hierarquia de cgroups em /sys/fs/cgroup."
    fi
}

# ------------------------------------------------------------------ imagem

IMAGE=""
image_exists_remote() {
    docker manifest inspect "$1" >/dev/null 2>&1
}

# O Dockerfile usa `--mount=type=cache`, que exige BuildKit. O construtor
# legado do Docker não suporta essas montagens, então o buildx é obrigatório
# para compilar. Instalações antigas do Engine costumam não ter o plugin.
ensure_buildx() {
    if docker buildx version >/dev/null 2>&1; then
        return 0
    fi
    warn "Plugin Docker Buildx ausente; ele é obrigatório para compilar a imagem."
    if [ "$INSTALL_DOCKER" = no ]; then
        die "Instale o docker-buildx-plugin, ou use --registry/--local com uma imagem pronta."
    fi
    need_root
    step "Instalando o plugin Buildx."
    # O nome do pacote depende de QUEM empacotou o Engine, não da família: os
    # repositórios da Docker publicam docker-buildx-plugin, enquanto Ubuntu,
    # Debian e Fedora, que empacotam o Engine por conta própria, publicam
    # docker-buildx. Quem instalou o Docker pela própria distribuição não tem o
    # repositório da Docker configurado, e tentar só o primeiro nome falha.
    case "$(os_family)" in
        debian) priv_sh "apt-get update && (apt-get install -y docker-buildx-plugin || apt-get install -y docker-buildx)" ;;
        rhel)   priv_sh "(dnf -y install docker-buildx-plugin || dnf -y install docker-buildx || yum -y install docker-buildx-plugin || yum -y install docker-buildx)" ;;
        suse)   priv_sh "zypper --non-interactive install docker-buildx" ;;
        arch)   priv_sh "pacman -Sy --noconfirm docker-buildx" ;;
        alpine) priv_sh "apk add --no-cache docker-cli-buildx" ;;
        *)      die "Não sei instalar o Buildx em '${OS_ID}'. Instale o docker-buildx-plugin (repositórios da Docker) ou docker-buildx (empacotado pela distribuição), ou use --registry/--local." ;;
    esac
    if [ "$DRY_RUN" = "1" ]; then
        return 0
    fi
    docker buildx version >/dev/null 2>&1 || die "O Buildx continua indisponível. Use --registry ou --local com uma imagem pronta."
}

# set_build_args preenche os parâmetros posicionais com os --build-arg que
# identificam a imagem. Sem eles o binário se declara "dev" e o painel não
# consegue dizer o que está rodando — o que atrapalha o diagnóstico e a
# comparação de versões na atualização. RELEASE_REPOSITORY fica de fora de
# propósito: é o repositório de onde o Pulso baixaria uma atualização, e essa
# confiança não se infere; quem injeta é o workflow de release.
set_build_args() {
    bversion="local-${SOURCE_REF}"
    brevision="unknown"
    if have git && git -C "$1" rev-parse --git-dir >/dev/null 2>&1; then
        bversion="$(git -C "$1" describe --tags --always --dirty 2>/dev/null || echo "$bversion")"
        brevision="$(git -C "$1" rev-parse HEAD 2>/dev/null || echo unknown)"
    fi
    step "Versão da imagem: ${bversion}"
    set -- --build-arg "VERSION=${bversion}" \
        --build-arg "REVISION=${brevision}" \
        --build-arg "BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    BUILD_ARG_1="$1"; BUILD_ARG_2="$2"; BUILD_ARG_3="$3"
    BUILD_ARG_4="$4"; BUILD_ARG_5="$5"; BUILD_ARG_6="$6"
}

build_image() {
    IMAGE="${PULSO_PROJECT}:local"
    log "Compilando a imagem a partir do código-fonte."
    ensure_buildx
    # Num `curl ... | sh` o $0 é "sh", então dirname aponta para o diretório
    # atual e o teste cairia no projeto que estiver por ali. Conferir o módulo
    # no go.mod evita compilar a árvore errada.
    context="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)" || context=""
    if [ -n "$context" ] && [ -f "$context/Dockerfile" ] &&
        grep -q '^module pulso.local/pulso' "$context/go.mod" 2>/dev/null; then
        step "Usando o checkout local: ${context}"
        set_build_args "$context"
        run docker buildx build --load \
            "$BUILD_ARG_1" "$BUILD_ARG_2" "$BUILD_ARG_3" \
            "$BUILD_ARG_4" "$BUILD_ARG_5" "$BUILD_ARG_6" \
            --tag "$IMAGE" "$context"
        return 0
    fi
    # Sem checkout: baixa o tarball do repositório, sem exigir git.
    tmp="$(mktemp -d)"
    step "Baixando o código de ${SOURCE_REPO}@${SOURCE_REF}."
    url="https://codeload.github.com/${SOURCE_REPO}/tar.gz/refs/heads/${SOURCE_REF}"
    if have curl; then
        run_sh "curl -fsSL '$url' -o '$tmp/src.tar.gz'"
    else
        run_sh "wget -qO '$tmp/src.tar.gz' '$url'"
    fi
    run_sh "tar -xzf '$tmp/src.tar.gz' -C '$tmp'"
    if [ "$DRY_RUN" = "1" ]; then
        step "(dry-run) compilaria a imagem do tarball baixado."
        rm -rf "$tmp"
        return 0
    fi
    context="$(find "$tmp" -maxdepth 1 -mindepth 1 -type d | head -1)"
    [ -n "$context" ] && [ -f "$context/Dockerfile" ] || { rm -rf "$tmp"; die "O código baixado não contém um Dockerfile."; }
    set_build_args "$context"
    run docker buildx build --load \
        "$BUILD_ARG_1" "$BUILD_ARG_2" "$BUILD_ARG_3" \
        "$BUILD_ARG_4" "$BUILD_ARG_5" "$BUILD_ARG_6" \
        --tag "$IMAGE" "$context"
    rm -rf "$tmp"
}

image_exists_local() {
    docker image inspect "$1" >/dev/null 2>&1
}

ensure_image() {
    candidate="${IMAGE_REPO}:${IMAGE_TAG}"
    case "$IMAGE_MODE" in
        build)
            build_image
            ;;
        local)
            image_exists_local "$candidate" || die "Imagem local ${candidate} não encontrada. Carregue-a com 'docker load' ou use --registry/--build."
            IMAGE="$candidate"
            step "Usando a imagem local ${candidate} (sem acesso à rede)."
            ;;
        registry)
            log "Baixando a imagem publicada ${candidate}."
            run docker pull "$candidate" || die "Não foi possível baixar ${candidate}. Confira se a release foi publicada e o pacote é público."
            IMAGE="$candidate"
            ;;
        auto)
            if [ "$DRY_RUN" = "1" ]; then
                step "(dry-run) tentaria baixar ${candidate}, depois a imagem local, depois compilaria."
                IMAGE="$candidate"
                return 0
            fi
            # Precedência: imagem publicada (mais recente), imagem já presente
            # no host (rede fechada, air-gapped), compilação local.
            log "Procurando a imagem ${candidate}."
            if image_exists_remote "$candidate" && docker pull "$candidate" >/dev/null 2>&1; then
                IMAGE="$candidate"
                step "Imagem publicada obtida."
            elif image_exists_local "$candidate"; then
                IMAGE="$candidate"
                warn "Registro inacessível; usando a imagem local ${candidate} já presente no host."
            else
                warn "Imagem publicada indisponível e nenhuma local; compilando a partir do código."
                build_image
            fi
            ;;
    esac
    step "Imagem em uso: ${IMAGE}"
}

# -------------------------------------------------------- volumes e pastas

ensure_storage() {
    for volume in "$DATA_VOLUME" "$UPDATE_VOLUME"; do
        if docker volume inspect "$volume" >/dev/null 2>&1; then
            step "Volume existente: ${volume}"
        else
            step "Criando volume ${volume}."
            # O rótulo protege o volume na gestão de recursos do painel,
            # independentemente do nome que ele tenha.
            run docker volume create \
                --label "io.pulso.infrastructure=1" \
                --label "com.docker.compose.project=${PULSO_PROJECT}" \
                "$volume" >/dev/null
        fi
    done

    if [ -d "$APPS_DIR" ]; then
        step "Pasta de projetos existente: ${APPS_DIR}"
        return 0
    fi
    step "Criando a pasta de projetos ${APPS_DIR}."
    priv_sh "mkdir -p '$APPS_DIR'"
    # A posse é ajustada de dentro de um contêiner para que o mapeamento de
    # usuário (userns-remap, Docker rootless) seja aplicado pelo Engine.
    run docker run --rm --user 0:0 \
        --cap-drop ALL --cap-add CHOWN --cap-add FOWNER \
        --security-opt no-new-privileges:true \
        -v "${APPS_DIR}:/apps${MOUNT_SUFFIX}" \
        --entrypoint /bin/sh "$IMAGE" \
        -c "chown ${APP_UID}:${APP_GID} /apps && chmod 0750 /apps"
}

# ---------------------------------------------------------------- contêineres

container_exists() {
    docker container inspect "$1" >/dev/null 2>&1
}

remove_container() {
    if container_exists "$1"; then
        step "Removendo o contêiner anterior ${1}."
        run docker rm -f "$1" >/dev/null
    fi
}

start_agent() {
    log "Subindo o agente privilegiado (${AGENT_NAME})."
    remove_container "$AGENT_NAME"
    # O agente é o único componente com acesso ao socket do Engine.
    # Não definimos --hostname: o agente usa o hostname (ID curto do
    # contêiner) para se inspecionar (internal/containerupdate/agent.go).
    run docker run -d \
        --name "$AGENT_NAME" \
        --label "com.docker.compose.project=${PULSO_PROJECT}" \
        --label "com.docker.compose.service=${AGENT_SERVICE}" \
        --label "com.docker.compose.oneoff=False" \
        --label "io.pulso.infrastructure=1" \
        --restart unless-stopped \
        --stop-timeout 30 \
        --user 0:0 \
        --read-only \
        --tmpfs /tmp:rw,nosuid,size=256m \
        --cap-drop ALL \
        --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
        --security-opt no-new-privileges:true \
        --security-opt label:disable \
        --log-driver json-file --log-opt max-size=10m --log-opt max-file=3 \
        -e PULSO_UPDATE_DIR=/var/run/pulso-update \
        -e "PULSO_UPDATE_IMAGE=${IMAGE_REPO}" \
        -e "PULSO_RELEASE_REPOSITORY=${SOURCE_REPO}" \
        -e PULSO_APPLICATIONS_ENABLED=1 \
        -v "${UPDATE_VOLUME}:/var/run/pulso-update" \
        -v "${DOCKER_SOCKET}:/var/run/docker.sock" \
        --health-cmd "/usr/local/bin/pulso container-updater health" \
        --health-interval 10s --health-timeout 5s --health-start-period 10s --health-retries 3 \
        --entrypoint /usr/bin/tini \
        "$IMAGE" \
        -- /usr/local/bin/pulso container-updater serve >/dev/null
}

# host_target devolve o caminho dentro do contêiner que espelha o caminho do
# servidor. O painel traduz a raiz do servidor para PULSO_HOST_ROOT (/host) por
# prefixo, então montar uma subpasta na posição espelhada mantém a tradução
# correta sem expor o resto do sistema de arquivos.
host_target() {
    case "$1" in
        /) echo "/host" ;;
        *) echo "/host$1" ;;
    esac
}

validate_host_root() {
    [ -n "$HOST_SOURCE" ] || return 0
    case "$HOST_SOURCE" in
        /*) ;;
        *) die "--host-root exige um caminho absoluto do servidor, começando com /." ;;
    esac
    # Remove a barra final para o espelhamento não gerar "//".
    [ "$HOST_SOURCE" = "/" ] || HOST_SOURCE="${HOST_SOURCE%/}"
    if [ "$DRY_RUN" != "1" ] && [ ! -d "$HOST_SOURCE" ]; then
        die "A pasta ${HOST_SOURCE} não existe neste servidor."
    fi
    step "Pasta do servidor exposta ao painel: ${HOST_SOURCE} -> $(host_target "$HOST_SOURCE") (somente leitura)."
}

# check_host_root_readable prova, antes de subir o painel, que o usuário sem
# privilégios do contêiner consegue ler a pasta. Sem isto o problema só
# apareceria no painel, como "caminho indisponível" — a mesma mensagem de
# quando a pasta não foi montada, confundindo duas causas diferentes.
check_host_root_readable() {
    [ -n "$HOST_SOURCE" ] || return 0
    [ "$DRY_RUN" = "1" ] && return 0
    [ "$HOST_PRIVILEGED" = yes ] && return 0
    if docker run --rm --user "${APP_UID}:${APP_GID}" --cap-drop ALL \
        --security-opt no-new-privileges:true \
        -v "${HOST_SOURCE}:/probe:ro" --entrypoint /bin/sh "$IMAGE" \
        -c 'ls /probe >/dev/null 2>&1' >/dev/null 2>&1; then
        step "A pasta é legível pelo painel, que segue rodando sem privilégios."
        return 0
    fi
    warn "O painel roda como uid ${APP_UID} e não consegue ler ${HOST_SOURCE}."
    warn "Dê leitura a outros nessa árvore (chmod o+rX), ou repita com --host-root-privileged,"
    die "que sobe o painel como root com DAC_OVERRIDE — use apenas em servidor de confiança."
}

start_panel() {
    log "Subindo o painel (${PANEL_NAME}) em ${BIND}:${PORT}."
    remove_container "$PANEL_NAME"
    # O painel não recebe o socket do Docker. Ele fala com o agente por um
    # socket Unix no volume compartilhado, montado somente para leitura.
    #
    # Os argumentos vão em `set --` porque o acesso ao servidor é condicional.
    # Em sh, uma função tem seus próprios parâmetros posicionais, então isto não
    # afeta o resto do script, e cada caminho continua entre aspas.
    set -- -d \
        --name "$PANEL_NAME" \
        --label "com.docker.compose.project=${PULSO_PROJECT}" \
        --label "com.docker.compose.service=${PANEL_SERVICE}" \
        --label "com.docker.compose.oneoff=False" \
        --label "io.pulso.infrastructure=1" \
        --restart unless-stopped \
        --stop-timeout 120 \
        --read-only \
        --tmpfs /tmp:rw,nosuid,size=128m \
        --cap-drop ALL \
        --security-opt no-new-privileges:true \
        --log-driver json-file --log-opt max-size=10m --log-opt max-file=3 \
        -p "${BIND}:${PORT}:9615" \
        -e PULSO_UPDATE_DIR=/var/run/pulso-update \
        -e PULSO_APPLICATIONS_ENABLED=1 \
        -v "${DATA_VOLUME}:/var/lib/pulso" \
        -v "${UPDATE_VOLUME}:/var/run/pulso-update:ro" \
        -v "${APPS_DIR}:/apps${MOUNT_SUFFIX}"
    if [ -n "$HOST_SOURCE" ]; then
        # Somente leitura: o painel apenas lê o código para empacotá-lo. E
        # nunca com :Z — relabelar uma árvore do servidor, ou a raiz, altera
        # arquivos que não são nossos. Com SELinux ativo, desabilitar o rótulo
        # para este contêiner é o caminho seguro.
        set -- "$@" -e PULSO_HOST_ROOT=/host \
            -v "${HOST_SOURCE}:$(host_target "$HOST_SOURCE"):ro"
        if [ "$SELINUX_ON" = yes ]; then
            set -- "$@" --security-opt label:disable
        fi
        if [ "$HOST_PRIVILEGED" = yes ]; then
            warn "O painel subirá como root com DAC_OVERRIDE, para ler ${HOST_SOURCE}."
            set -- "$@" --user 0:0 --cap-add DAC_OVERRIDE
        fi
    fi
    run docker run "$@" "$IMAGE" >/dev/null
}

wait_healthy() {
    [ "$DRY_RUN" = "1" ] && return 0
    name="$1"
    waited=0
    while [ "$waited" -lt 180 ]; do
        state="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$name" 2>/dev/null || echo missing)"
        case "$state" in
            healthy|running) return 0 ;;
            exited|dead)
                warn "O contêiner ${name} encerrou. Últimas linhas do log:"
                docker logs --tail 40 "$name" 2>&1 | sed 's/^/    /' >&2
                return 1 ;;
        esac
        sleep 2
        waited=$((waited + 2))
    done
    warn "Tempo esgotado esperando ${name} (estado: ${state})."
    return 1
}

open_firewall() {
    [ "$OPEN_FIREWALL" = yes ] || return 0
    if have firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
        step "Liberando a porta ${PORT}/tcp no firewalld."
        priv firewall-cmd --permanent --add-port="${PORT}/tcp" >/dev/null
        priv firewall-cmd --reload >/dev/null
    elif have ufw && ufw status 2>/dev/null | head -1 | grep -qi active; then
        step "Liberando a porta ${PORT}/tcp no ufw."
        priv ufw allow "${PORT}/tcp" >/dev/null
    else
        step "Nenhum firewall ativo reconhecido (firewalld/ufw)."
    fi
}

firewall_hint() {
    [ "$OPEN_FIREWALL" = yes ] && return 0
    if have firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
        warn "firewalld ativo. Para liberar o painel: firewall-cmd --permanent --add-port=${PORT}/tcp && firewall-cmd --reload"
    elif have ufw && ufw status 2>/dev/null | head -1 | grep -qi active; then
        warn "ufw ativo. Para liberar o painel: ufw allow ${PORT}/tcp"
    fi
}

save_conf() {
    priv_sh "mkdir -p '$CONF_DIR'"
    conf="PULSO_PROJECT='${PULSO_PROJECT}'
PULSO_IMAGE_REPO='${IMAGE_REPO}'
PULSO_IMAGE_TAG='${IMAGE_TAG}'
PULSO_IMAGE='${IMAGE}'
PULSO_PORT='${PORT}'
PULSO_BIND='${BIND}'
PULSO_APPS_DIR='${APPS_DIR}'
PULSO_HOST_SOURCE='${HOST_SOURCE}'
PULSO_HOST_PRIVILEGED='${HOST_PRIVILEGED}'
PULSO_DOCKER_SOCKET='${DOCKER_SOCKET}'
PULSO_SELINUX='${SELINUX_ON}'
PULSO_INSTALLED_AT='$(date -u +%Y-%m-%dT%H:%M:%SZ)'"
    if [ "$DRY_RUN" = "1" ]; then
        step "(dry-run) gravaria ${CONF_FILE}."
        return 0
    fi
    if [ -n "$SUDO" ]; then
        printf '%s\n' "$conf" | $SUDO tee "$CONF_FILE" >/dev/null
        $SUDO chmod 0640 "$CONF_FILE" 2>/dev/null || true
    else
        printf '%s\n' "$conf" > "$CONF_FILE"
        chmod 0640 "$CONF_FILE" 2>/dev/null || true
    fi
}

setup_token() {
    [ "$DRY_RUN" = "1" ] && { echo "(dry-run)"; return 0; }
    docker exec "$PANEL_NAME" cat /var/lib/pulso/security/setup-token 2>/dev/null || echo ""
}

host_address() {
    addr=""
    # Quando o painel escuta em um endereço específico, é esse o endereço de
    # acesso. Descobrir o IP da rota padrão só faz sentido com 0.0.0.0.
    case "$BIND" in
        0.0.0.0|'' ) ;;
        '::'|'[::]') ;;
        *) echo "$BIND"; return 0 ;;
    esac
    if have hostname; then
        addr="$(hostname -I 2>/dev/null | awk '{print $1}')"
    fi
    if [ -z "$addr" ] && have ip; then
        addr="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1)"
    fi
    [ -n "$addr" ] || addr="IP_DO_SERVIDOR"
    echo "$addr"
}

do_status() {
    detect_os
    if ! docker_ready; then
        die "Docker indisponível."
    fi
    printf 'Projeto: %s\n' "$PULSO_PROJECT"
    [ -r "$CONF_FILE" ] && { printf 'Configuração: %s\n' "$CONF_FILE"; sed 's/^/  /' "$CONF_FILE"; }
    for name in "$PANEL_NAME" "$AGENT_NAME"; do
        if container_exists "$name"; then
            # .Name vem com barra inicial; o slice remove o primeiro caractere.
            docker inspect --format "{{slice .Name 1}}: {{.State.Status}}{{if .State.Health}} ({{.State.Health.Status}}){{end}} | imagem {{.Config.Image}}" "$name"
        else
            printf '%s: ausente\n' "$name"
        fi
    done
    token="$(setup_token 2>/dev/null)"
    if [ -n "$token" ] && [ "$token" != "(dry-run)" ]; then
        printf '\nCódigo de primeiro acesso (ainda pendente): %s\n' "$token"
    fi
    printf '\nAplicações gerenciadas:\n'
    docker ps -a --filter "label=io.pulso.application.owner=${PULSO_PROJECT}" \
        --format '  {{.Names}}\t{{.Status}}\t{{.Image}}' 2>/dev/null || true
}

# do_token recupera o código de primeiro acesso depois da instalação. Ele existe
# porque o código é impresso uma única vez, no fim da instalação, e sai do
# histórico do terminal com facilidade — sem isto, só restaria decorar o caminho
# do arquivo dentro do contêiner.
do_token() {
    # O diagnóstico vai para a saída de erro para que a saída padrão contenha
    # somente o código, e `TOKEN=$(... --token)` funcione.
    detect_os >&2
    docker_ready || die "Docker indisponível."
    container_exists "$PANEL_NAME" || die "O painel ${PANEL_NAME} não existe. Instale o Pulso primeiro."
    # Um contêiner parado faria o exec falhar e devolver vazio, o que seria lido
    # como "administrador já criado" — uma conclusão errada.
    if [ "$(docker inspect --format '{{.State.Running}}' "$PANEL_NAME" 2>/dev/null || echo false)" != "true" ]; then
        die "O painel ${PANEL_NAME} não está em execução. Inicie-o com: docker start ${PANEL_NAME}"
    fi
    token="$(setup_token)"
    if [ -n "$token" ] && [ "$token" != "(dry-run)" ]; then
        printf '%s\n' "$token"
        return 0
    fi
    die "Não há código pendente: a conta de administrador já foi criada. Entre com ela no painel."
}

do_uninstall() {
    log "Removendo os contêineres do Pulso."
    remove_container "$PANEL_NAME"
    remove_container "$AGENT_NAME"
    log "Contêineres removidos. Volumes preservados: ${DATA_VOLUME}, ${UPDATE_VOLUME}."
    log "As aplicações implantadas continuam em execução."
    log "Para remover tudo, incluindo contas e cadastros: --purge"
}

do_purge() {
    if [ "$ASSUME_YES" != "1" ] && [ "$DRY_RUN" != "1" ]; then
        # Num `curl ... | sh` a entrada padrão é o próprio script: um read aqui
        # consumiria uma linha do código como se fosse a resposta. Exigir --yes
        # é mais honesto do que aceitar uma confirmação que o usuário não deu.
        if [ ! -t 0 ]; then
            die "O --purge apaga contas e cadastros, e aqui não há terminal para confirmar. Repita com --yes se é isso que você quer."
        fi
        printf 'Isto remove contas, cadastros e o histórico do Pulso em %s. Digite "remover" para confirmar: ' "$DATA_VOLUME"
        read -r answer
        [ "$answer" = "remover" ] || die "Cancelado."
    fi
    do_uninstall
    for volume in "$DATA_VOLUME" "$UPDATE_VOLUME"; do
        if docker volume inspect "$volume" >/dev/null 2>&1; then
            step "Removendo o volume ${volume}."
            run docker volume rm "$volume" >/dev/null || warn "Não removi ${volume}; pode estar em uso."
        fi
    done
    log "Remoção concluída. A pasta de projetos ${APPS_DIR} foi preservada."
}

do_install() {
    detect_os
    detect_arch
    ensure_docker
    detect_socket
    detect_selinux
    check_cgroups
    ensure_image
    validate_host_root
    check_host_root_readable
    ensure_storage
    start_agent
    if ! wait_healthy "$AGENT_NAME"; then
        die "O agente não ficou saudável. Veja 'docker logs ${AGENT_NAME}'."
    fi
    start_panel
    if ! wait_healthy "$PANEL_NAME"; then
        die "O painel não ficou saudável. Veja 'docker logs ${PANEL_NAME}'."
    fi
    open_firewall
    save_conf

    address="$(host_address)"
    token="$(setup_token)"
    printf '\n'
    log "Pulso instalado."
    printf '   Painel:  http://%s:%s\n' "$address" "$PORT"
    if [ -n "$token" ] && [ "$token" != "(dry-run)" ]; then
        printf '   Código de primeiro acesso: %s\n' "$token"
    else
        printf '   Código de primeiro acesso: sh get-pulso.sh --token\n'
    fi
    printf '   Projetos no host: %s\n' "$APPS_DIR"
    printf '\n   Código:    sh get-pulso.sh --token    (se perder o de cima)\n'
    printf '   Estado:    sh get-pulso.sh --status\n'
    printf '   Logs:      docker logs -f %s\n' "$PANEL_NAME"
    printf '   Atualizar: execute este instalador de novo\n'
    firewall_hint
}

load_conf

case "$ACTION" in
    install) do_install ;;
    status) do_status ;;
    token) do_token ;;
    uninstall) detect_os; do_uninstall ;;
    purge) detect_os; do_purge ;;
esac
