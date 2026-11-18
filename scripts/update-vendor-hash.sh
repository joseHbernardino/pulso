#!/bin/sh
# Recalcula o vendorHash de nix/package.nix.
#
#   scripts/update-vendor-hash.sh            # grava o hash correto no arquivo
#   scripts/update-vendor-hash.sh --check    # apenas verifica (usado no CI)
#
# Por que isso existe: buildGoModule baixa as dependências em uma derivação de
# saída fixa, cujo hash é declarado à mão em nix/package.nix. Qualquer mudança
# em go.mod ou go.sum o invalida — inclusive os bumps automáticos do Dependabot,
# que não conhecem Nix. O sintoma é um "hash mismatch in fixed-output
# derivation" que não diz o que precisa ser feito. Este script diz.
#
# O hash depende SOMENTE do conteúdo de go.mod e go.sum: a derivação é de saída
# fixa, então o resultado é o cache de módulos baixado, não a árvore inteira.
# Por isso é seguro calculá-lo a partir de uma cópia da árvore.

set -eu

# lib.fakeHash do nixpkgs. Um hash deliberadamente errado faz o Nix abortar
# informando o valor verdadeiro na linha "got:".
FAKE_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
PACKAGE_NIX="nix/package.nix"
FLAKE_ATTR="pulso.goModules"
NIX_FEATURES="nix-command flakes"

mode="write"
case "${1:-}" in
    --check) mode="check" ;;
    "") ;;
    -h|--help)
        sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
        exit 0
        ;;
    *)
        echo "uso: $0 [--check]" >&2
        exit 2
        ;;
esac

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH='' cd -- "$script_dir/.." && pwd)

if ! command -v nix >/dev/null 2>&1; then
    echo "erro: o Nix não está instalado." >&2
    echo "      Sem ele não é possível calcular o hash; instale-o ou rode" >&2
    echo "      este script no CI, onde o Nix está disponível." >&2
    exit 1
fi
if [ ! -f "$root/$PACKAGE_NIX" ]; then
    echo "erro: $PACKAGE_NIX não encontrado em $root." >&2
    exit 1
fi

current=$(sed -n 's/.*vendorHash = "\([^"]*\)".*/\1/p' "$root/$PACKAGE_NIX" | head -1)
if [ -z "$current" ]; then
    echo "erro: não encontrei a linha vendorHash em $PACKAGE_NIX." >&2
    exit 1
fi

work=$(mktemp -d)
cleanup() { rm -rf "$work"; }
trap cleanup EXIT HUP INT TERM

# A cópia exclui .git de propósito: num repositório git o flake ignora as
# alterações ainda não confirmadas, e o hash precisa refletir o go.mod do disco.
(cd "$root" && tar -cf - --exclude=./.git --exclude=./result .) | (cd "$work" && tar -xf -)

build_modules() {
    nix --extra-experimental-features "$NIX_FEATURES" \
        build "path:$work#$FLAKE_ATTR" --no-link 2>&1
}

# Primeiro tenta com o hash já gravado. Quando está correto não há nada a
# fazer, e a derivação baixada aproveita o cache do CI em vez de repetir o
# download dos módulos a cada execução.
if output=$(build_modules); then
    echo "vendorHash em dia: $current"
    exit 0
fi

echo "o vendorHash gravado não corresponde às dependências atuais; recalculando..." >&2

sed "s|vendorHash = \"[^\"]*\"|vendorHash = \"$FAKE_HASH\"|" \
    "$root/$PACKAGE_NIX" > "$work/$PACKAGE_NIX"
if ! grep -q "$FAKE_HASH" "$work/$PACKAGE_NIX"; then
    echo "erro: não consegui substituir o vendorHash na cópia de trabalho." >&2
    exit 1
fi

output=$(build_modules || true)
computed=$(printf '%s\n' "$output" \
    | sed -n 's|.*got: *\(sha256-[A-Za-z0-9+/=]\{10,\}\).*|\1|p' \
    | tail -1)

if [ -z "$computed" ] || [ "$computed" = "$FAKE_HASH" ]; then
    echo "erro: o Nix não informou o hash verdadeiro. Saída completa:" >&2
    printf '%s\n' "$output" >&2
    exit 1
fi

if [ "$mode" = "check" ]; then
    echo "" >&2
    echo "O vendorHash de $PACKAGE_NIX está desatualizado." >&2
    echo "  gravado: $current" >&2
    echo "  correto: $computed" >&2
    echo "" >&2
    echo "Corrija com um destes caminhos:" >&2
    echo "  scripts/update-vendor-hash.sh" >&2
    echo "  sed -i 's|$current|$computed|' $PACKAGE_NIX" >&2
    exit 1
fi

tmp_file="$work/package.nix.new"
sed "s|vendorHash = \"[^\"]*\"|vendorHash = \"$computed\"|" \
    "$root/$PACKAGE_NIX" > "$tmp_file"
cat "$tmp_file" > "$root/$PACKAGE_NIX"
echo "vendorHash atualizado"
echo "  antes:  $current"
echo "  depois: $computed"
