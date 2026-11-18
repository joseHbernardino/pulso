#!/usr/bin/env bash
# Valida scripts/get-pulso.sh em imagens reais de várias distribuições.
#
# Executa o instalador em modo --dry-run dentro de cada imagem e confere:
#   1. que o script roda do início ao fim sem erro;
#   2. que a família de pacotes detectada é a esperada;
#   3. que o comando de instalação do Docker escolhido é o correto.
#
# O dry-run não altera nada e não precisa de Docker dentro do contêiner.
# Um curl/wget falso é injetado porque o dry-run não baixa nada, e imagens
# mínimas não traem curl.
#
# Uso: bash scripts/test-installer-distros.sh [imagem ...]

set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
installer="${script_dir}/get-pulso.sh"
[[ -f "$installer" ]] || { echo "instalador não encontrado: $installer" >&2; exit 1; }

# imagem|família esperada|trecho esperado no comando de instalação
CASES=(
    "debian:trixie-slim|debian|get.docker.com"
    "ubuntu:24.04|debian|get.docker.com"
    "rockylinux:9|rhel|get.docker.com"
    "almalinux:9|rhel|get.docker.com"
    "fedora:41|rhel|get.docker.com"
    "amazonlinux:2023|amzn|dnf -y install docker"
    "amazonlinux:2|amzn|amazon-linux-extras"
    "opensuse/leap:15|suse|zypper"
    "archlinux:latest|arch|pacman"
    "alpine:3|alpine|apk add"
)

if [[ $# -gt 0 ]]; then
    filtered=()
    for want in "$@"; do
        for case_line in "${CASES[@]}"; do
            [[ "${case_line%%|*}" == "$want" ]] && filtered+=("$case_line")
        done
    done
    CASES=("${filtered[@]}")
fi

pass=0
fail=0
declare -a failures=()

for case_line in "${CASES[@]}"; do
    IFS='|' read -r image family expected <<<"$case_line"
    printf '\n\033[1m=== %s ===\033[0m\n' "$image"

    if ! docker image inspect "$image" >/dev/null 2>&1; then
        echo "  baixando $image..."
        if ! docker pull --quiet "$image" >/dev/null 2>&1; then
            echo "  SKIP: não foi possível baixar $image"
            continue
        fi
    fi

    # Shell POSIX de cada imagem; curl falso para o dry-run.
    output="$(docker run --rm \
        -v "${installer}:/get-pulso.sh:ro" \
        -e PULSO_DRY_RUN=1 \
        -e NO_COLOR=1 \
        --entrypoint /bin/sh \
        "$image" -c '
            mkdir -p /usr/local/bin
            printf "#!/bin/sh\nexit 0\n" > /usr/local/bin/curl
            chmod +x /usr/local/bin/curl
            export PATH=/usr/local/bin:$PATH
            sh /get-pulso.sh --dry-run --yes 2>&1
        ' 2>&1)"
    status=$?

    ok=true
    if [[ $status -ne 0 ]]; then
        echo "  FALHA: instalador saiu com código $status"
        ok=false
    fi
    if ! grep -q "família: ${family}" <<<"$output"; then
        echo "  FALHA: família esperada '${family}' não detectada"
        ok=false
    fi
    if ! grep -qF "$expected" <<<"$output"; then
        echo "  FALHA: comando esperado '${expected}' ausente"
        ok=false
    fi
    if ! grep -q "Pulso instalado" <<<"$output"; then
        echo "  FALHA: o instalador não chegou ao fim"
        ok=false
    fi

    if $ok; then
        printf '  \033[0;32mOK\033[0m  família=%s  docker=%s\n' "$family" "$expected"
        grep -E "Sistema:|Arquitetura:|cgroups|SELinux" <<<"$output" | sed 's/^/      /'
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        failures+=("$image")
        echo "  --- saída ---"
        sed 's/^/      /' <<<"$output" | tail -30
    fi
done

printf '\n\033[1m=== resumo ===\033[0m\n'
printf 'aprovadas: %d   falhas: %d\n' "$pass" "$fail"
if (( fail > 0 )); then
    printf 'falharam: %s\n' "${failures[*]}"
    exit 1
fi
