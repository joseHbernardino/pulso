#!/bin/sh
# Roda localmente o que o CI roda, antes de publicar.
#
#   sh scripts/verify.sh            # verificação padrão (minutos)
#   sh scripts/verify.sh --full     # inclui os testes pesados com Docker
#   sh scripts/verify.sh --list     # só lista o que seria verificado
#
# Por que isso existe: o CI reprova um commit por motivos que um `go test`
# local não vê — formatação com -s, lint, build cruzado, varredura de
# vulnerabilidades, o vendorHash do Nix que qualquer mudança em go.mod
# invalida, a sintaxe POSIX do instalador. Cada um desses já reprovou uma
# publicação deste repositório. Aqui eles rodam juntos, na mesma ordem do CI,
# e o fim do relatório diz o que NÃO foi verificado, para que ninguém confunda
# "passou" com "está coberto".
#
# O que nunca é verificável aqui: a etapa de publicação (promover tags no
# GHCR e publicar o release) precisa de credenciais do repositório. O
# relatório sempre a lista como não verificada.
#
# Invoque pelo interpretador: este repositório está com core.fileMode=false,
# então nenhum script carrega o bit de execução no git.

set -u

mode="normal"
case "${1:-}" in
    --full) mode="full" ;;
    --list) mode="list" ;;
    -h|--help)
        sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
        exit 0
        ;;
    "") ;;
    *)
        echo "uso: sh $0 [--full|--list]" >&2
        exit 2
        ;;
esac

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cd "$root" || exit 1

work=$(mktemp -d)
results="$work/results"
: >"$results"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT HUP INT TERM

failed=0

ok() { printf 'ok\t%s\t%s\n' "$1" "${2:-}" >>"$results"; }
bad() {
    printf 'FALHOU\t%s\t%s\n' "$1" "${2:-}" >>"$results"
    failed=1
}
skipped() { printf 'pulado\t%s\t%s\n' "$1" "${2:-}" >>"$results"; }

announce() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

# check <nome> <comando...> — roda e registra o resultado, sem abortar o resto.
check() {
    name=$1
    shift
    if [ "$mode" = list ]; then
        printf '  %s\n' "$name"
        return 0
    fi
    announce "$name"
    if "$@"; then
        ok "$name"
    else
        bad "$name" "reproduza com: $*"
    fi
}

# ------------------------------------------------------------------ toolchain

want_go=$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' go.mod | head -1)
series=$(printf '%s' "$want_go" | cut -d. -f1-2)

find_go() {
    for candidate in \
        "${PULSO_GO:-}" \
        "/usr/local/go$series/bin/go" \
        "/usr/lib/go-$series/bin/go" \
        "$(command -v go 2>/dev/null || true)" \
        "/usr/local/go/bin/go"; do
        [ -n "$candidate" ] && [ -x "$candidate" ] || continue
        printf '%s' "$candidate"
        return 0
    done
    return 1
}

GO=$(find_go || true)
if [ -z "$GO" ]; then
    echo "erro: nenhum Go encontrado. Instale o Go $want_go ou aponte PULSO_GO." >&2
    exit 1
fi
GOFMT=$(dirname "$GO")/gofmt
have_go=$("$GO" env GOVERSION 2>/dev/null | sed 's/^go//')

if [ "$mode" != list ]; then
    printf 'Go:        %s (%s)\n' "$have_go" "$GO"
    printf 'go.mod:    %s\n' "$want_go"
    printf 'commit:    %s\n' "$(git rev-parse --short HEAD 2>/dev/null || echo '?')"
fi
case "$have_go" in
    "$series"*) ;;
    *) skipped "toolchain" "Go local $have_go, go.mod pede $series: formatação e lint podem divergir do CI" ;;
esac

# ------------------------------------------------------------- CI job "check"

# O CI compara com `gofmt -l -s .`, que também reprova quebra de linha CRLF.
# Num clone em Windows ou WSL o arquivo pode estar com CRLF no disco enquanto
# o git guarda LF — o CI vê LF e passa. Comparar o conteúdo normalizado evita
# esse falso negativo, sem deixar passar formatação de verdade.
format_check() {
    unformatted=""
    for file in $(git ls-files '*.go'); do
        sed 's/\r$//' "$file" >"$work/check.go"
        if ! "$GOFMT" -s "$work/check.go" >"$work/formatted.go" 2>"$work/gofmt.err"; then
            echo "gofmt não conseguiu ler $file:" >&2
            cat "$work/gofmt.err" >&2
            unformatted="$unformatted $file"
            continue
        fi
        cmp -s "$work/check.go" "$work/formatted.go" || unformatted="$unformatted $file"
    done
    if [ -n "$unformatted" ]; then
        echo "arquivos fora do formato (rode 'gofmt -s -w'):" >&2
        for file in $unformatted; do echo "  $file" >&2; done
        return 1
    fi
    echo "todos os arquivos .go estão formatados"
}

cross_build() {
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
        goos=${target%/*}
        goarch=${target#*/}
        printf '  %s/%s\n' "$goos" "$goarch"
        CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO" build \
            -buildvcs=false -trimpath \
            -ldflags "-s -w -X pulso.local/pulso/internal/version.Version=verify" \
            -o "$work/pulso" ./cmd/pulso || return 1
    done
}

# ------------------------------------------------------------ guarda do Nix

# O vendorHash de nix/package.nix é declarado à mão e qualquer mudança em
# go.mod ou go.sum o invalida. Sem Nix instalado não há como recalculá-lo,
# mas dá para exigir que ele tenha sido tocado junto — foi assim que uma
# publicação deste repositório quebrou.
vendor_hash_guard() {
    base=${PULSO_VERIFY_BASE:-origin/main}
    if ! git rev-parse --verify --quiet "$base" >/dev/null 2>&1; then
        echo "sem $base para comparar; nada a concluir"
        return 0
    fi
    modules=$(
        git diff --name-only "$base...HEAD" -- go.mod go.sum
        git diff --name-only HEAD -- go.mod go.sum
    )
    if [ -z "$modules" ]; then
        echo "go.mod e go.sum não mudaram em relação a $base"
        return 0
    fi
    pinned=$(
        git diff "$base...HEAD" -- nix/package.nix
        git diff HEAD -- nix/package.nix
    )
    if printf '%s' "$pinned" | grep -q 'vendorHash'; then
        echo "go.mod/go.sum mudaram e o vendorHash foi atualizado junto"
        return 0
    fi
    cat >&2 <<'AVISO'
go.mod ou go.sum mudaram, mas o vendorHash de nix/package.nix não.
O `nix build .` do CI vai falhar com "hash mismatch in fixed-output
derivation". Resolva de uma destas formas:

  sh scripts/update-vendor-hash.sh      # recalcula (precisa de Nix)

ou pegue o valor que o próprio Nix reporta na anotação do job Nix do CI
("To correct the hash mismatch ... use \"sha256-...\"") e grave em
nix/package.nix.
AVISO
    return 1
}

# ---------------------------------------------------------------- execução

if [ "$mode" = list ]; then
    echo "Verificações padrão:"
fi

check "testes da automação de release" python3 -m unittest discover -s scripts -p 'test_release_*.py'
check "go mod verify" "$GO" mod verify
check "build" "$GO" build -trimpath ./...
check "formatação (gofmt -s)" format_check
check "go vet" "$GO" vet ./...

if command -v golangci-lint >/dev/null 2>&1; then
    check "golangci-lint" golangci-lint run ./...
else
    skipped "golangci-lint" "não instalado; o CI roda a v2.15.0"
fi

check "testes (-race)" "$GO" test -count=1 -race -timeout 120s ./...
check "testes E2E" "$GO" test -count=1 -timeout 120s ./internal/e2e/...
check "build cruzado (5 alvos)" cross_build

if command -v govulncheck >/dev/null 2>&1; then
    check "govulncheck" govulncheck -test ./...
else
    skipped "govulncheck" "não instalado; 'go install golang.org/x/vuln/cmd/govulncheck@v1.9.0'"
fi

check "vendorHash acompanha go.mod" vendor_hash_guard

if command -v nix >/dev/null 2>&1; then
    check "vendorHash (recálculo real)" sh scripts/update-vendor-hash.sh --check
    check "nix flake check" nix flake check --all-systems
    check "nix build" nix build .
else
    skipped "Nix (flake check, build, vendorHash real)" "Nix não instalado; o CI roda estes três"
fi

if docker info >/dev/null 2>&1; then
    check "shellcheck do instalador" docker run --rm -v "$root/scripts:/mnt:ro" \
        koalaman/shellcheck:stable --shell=sh --severity=style /mnt/get-pulso.sh
    check "shellcheck dos testes de distribuição" docker run --rm -v "$root/scripts:/mnt:ro" \
        koalaman/shellcheck:stable --severity=warning /mnt/test-installer-distros.sh
    check "sintaxe POSIX do instalador (dash, bash)" docker run --rm -v "$root/scripts:/s:ro" \
        debian:trixie-slim sh -c 'dash -n /s/get-pulso.sh && bash -n /s/get-pulso.sh'
    check "sintaxe do instalador (busybox ash)" docker run --rm -v "$root/scripts:/s:ro" \
        alpine:3 sh -n /s/get-pulso.sh
    if [ "$mode" = full ] || [ "$mode" = list ]; then
        [ "$mode" = list ] && echo "Com --full, também:"
        check "Docker Engine de verdade (métricas e inventário)" \
            "$GO" test -tags integration -timeout 10m ./internal/containerupdate/ \
            -run 'TestContainerStatsAgainstRealDocker|TestInventoryAgainstRealDocker'
        check "dry-run do instalador (debian:trixie-slim)" bash scripts/test-installer-distros.sh debian:trixie-slim
    else
        skipped "testes pesados com Docker" "rode 'sh scripts/verify.sh --full'"
    fi
else
    skipped "tudo que depende de Docker" "Engine inacessível: shellcheck, sintaxe POSIX, integração e instalador"
fi

skipped "publicação (tags no GHCR e release)" "exige credenciais do repositório; só o CI verifica"

[ "$mode" = list ] && exit 0

# ----------------------------------------------------------------- relatório

printf '\n\033[1m================ resumo ================\033[0m\n'
while IFS="	" read -r status name detail; do
    case "$status" in
        ok) printf '  \033[32mok\033[0m       %s\n' "$name" ;;
        FALHOU) printf '  \033[31mFALHOU\033[0m   %s\n           %s\n' "$name" "$detail" ;;
        pulado) printf '  \033[33mpulado\033[0m   %s\n           %s\n' "$name" "$detail" ;;
    esac
done <"$results"

printf '\n'
if [ "$failed" -ne 0 ]; then
    printf '\033[31mNÃO publique: há verificações reprovadas acima.\033[0m\n'
    exit 1
fi
printf '\033[32mTudo que roda aqui passou.\033[0m Confira a lista de "pulado": o que está lá\n'
printf 'continua sendo verificado só pelo CI.\n'
