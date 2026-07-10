#!/usr/bin/env sh
set -eu

image="${1:-}"
ref_name="${2:-${GITHUB_REF_NAME:-}}"
ref_type="${3:-${GITHUB_REF_TYPE:-}}"

if [ -z "$image" ]; then
  echo "docker metadata check failed: image is required" >&2
  exit 1
fi

if [ -z "$ref_name" ]; then
  echo "docker metadata check failed: ref name is required" >&2
  exit 1
fi

tags_file="$(mktemp)"
cat > "$tags_file"

cleanup() {
  rm -f "$tags_file"
}
trap cleanup EXIT

if [ ! -s "$tags_file" ]; then
  echo "docker metadata check failed: no tags were provided for $image" >&2
  exit 1
fi

require_tag() {
  expected="$1"
  if ! grep -Fx "$expected" "$tags_file" >/dev/null; then
    echo "docker metadata check failed: missing expected tag $expected" >&2
    echo "generated tags:" >&2
    sed 's/^/  - /' "$tags_file" >&2
    exit 1
  fi
}

case "$ref_name" in
  v[0-9]*.[0-9]*.[0-9]*)
    version="${ref_name#v}"
    major_minor="$(printf '%s\n' "$version" | awk -F. '{ print $1 "." $2 }')"

    require_tag "$image:$ref_name"
    require_tag "$image:$version"
    require_tag "$image:$major_minor"
    ;;
  main)
    if [ "$ref_type" = "branch" ] || [ -z "$ref_type" ]; then
      require_tag "$image:main"
      require_tag "$image:latest"
    fi
    ;;
  develop)
    if [ "$ref_type" = "branch" ] || [ -z "$ref_type" ]; then
      require_tag "$image:develop"
    fi
    ;;
esac

echo "docker metadata check passed for $image ($ref_name)"
