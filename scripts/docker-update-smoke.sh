#!/usr/bin/env bash
# Run after building pulso:smoke. All state is confined to disposable fixtures.
set -euo pipefail
base_image="${1:-pulso:smoke}"
test_prefix="pulso-update-smoke-$$"
fixture_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$fixture_dir"
  docker image rm "$test_prefix:new" "$test_prefix:next" "$test_prefix:bad" "$test_prefix:missing-runtime" >/dev/null 2>&1 || true
}
trap cleanup EXIT
for version in v0.0.2 v0.0.3; do
  tag=new
  if [[ "$version" == v0.0.3 ]]; then tag=next; fi
  docker buildx build --load --target runtime --build-arg "VERSION=$version" -t "$test_prefix:$tag" .
done
cat > "$fixture_dir/Dockerfile" <<EOF_BAD
FROM $test_prefix:next
USER root
RUN printf '#!/bin/sh\\nexit 9\\n' > /usr/local/bin/pulso && chmod 0755 /usr/local/bin/pulso
USER pulso:pulso
EOF_BAD
docker buildx build --load -t "$test_prefix:bad" "$fixture_dir"
cat > "$fixture_dir/Dockerfile" <<EOF_MISSING
FROM $test_prefix:next
USER root
RUN rm /usr/bin/sleep
USER pulso:pulso
EOF_MISSING
docker buildx build --load -t "$test_prefix:missing-runtime" "$fixture_dir"
PULSO_UPDATE_OLD_IMAGE="$base_image" \
PULSO_UPDATE_NEW_IMAGE="$test_prefix:new" \
PULSO_UPDATE_NEXT_IMAGE="$test_prefix:next" \
PULSO_UPDATE_BAD_IMAGE="$test_prefix:bad" \
PULSO_UPDATE_MISSING_RUNTIME_IMAGE="$test_prefix:missing-runtime" \
go test -race -count=1 -tags integration -timeout 240s ./internal/containerupdate -run TestDockerUpdateAndRollback -v
