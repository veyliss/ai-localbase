#!/usr/bin/env sh
set -eu

tag_name="${1:-${GITHUB_REF_NAME:-}}"
notes_file="${2:-RELEASE_NOTES.md}"

if [ -z "$tag_name" ]; then
  echo "release check failed: tag name is required" >&2
  exit 1
fi

if [ ! -s "$notes_file" ]; then
  echo "release check failed: $notes_file is missing or empty" >&2
  exit 1
fi

tag_ref="refs/tags/$tag_name"
if ! git rev-parse -q --verify "$tag_ref" >/dev/null; then
  git fetch --force origin "$tag_ref:$tag_ref" >/dev/null 2>&1 || true
fi

if ! git rev-parse -q --verify "$tag_ref" >/dev/null; then
  echo "release check failed: tag $tag_name does not exist locally" >&2
  exit 1
fi

tag_type="$(git cat-file -t "$tag_ref")"
if [ "$tag_type" != "tag" ]; then
  git fetch --force origin "$tag_ref:$tag_ref" >/dev/null 2>&1 || true
  tag_type="$(git cat-file -t "$tag_ref")"
fi

if [ "$tag_type" != "tag" ]; then
  echo "release check failed: $tag_name is a lightweight tag; use an annotated tag with release notes" >&2
  exit 1
fi

tag_message_file="$(mktemp)"
git for-each-ref "$tag_ref" --format='%(contents)' > "$tag_message_file"

if [ ! -s "$tag_message_file" ]; then
  echo "release check failed: annotated tag $tag_name has an empty message" >&2
  rm -f "$tag_message_file"
  exit 1
fi

first_line="$(sed -n '/[^[:space:]]/p' "$tag_message_file" | sed -n '1p')"
if [ -z "$first_line" ]; then
  echo "release check failed: annotated tag $tag_name has no non-empty release title" >&2
  rm -f "$tag_message_file"
  exit 1
fi

if ! grep -F "$first_line" "$notes_file" >/dev/null; then
  echo "release check failed: $notes_file does not include annotated tag title: $first_line" >&2
  rm -f "$tag_message_file"
  exit 1
fi

if ! grep -F "## Docker Images" "$notes_file" >/dev/null; then
  echo "release check failed: $notes_file does not include Docker image section" >&2
  rm -f "$tag_message_file"
  exit 1
fi

expected_backend_image="ghcr.io/veyliss/ai-localbase-backend:$tag_name"
expected_frontend_image="ghcr.io/veyliss/ai-localbase-frontend:$tag_name"

if ! grep -F "$expected_backend_image" "$notes_file" >/dev/null; then
  echo "release check failed: $notes_file does not include backend image $expected_backend_image" >&2
  rm -f "$tag_message_file"
  exit 1
fi

if ! grep -F "$expected_frontend_image" "$notes_file" >/dev/null; then
  echo "release check failed: $notes_file does not include frontend image $expected_frontend_image" >&2
  rm -f "$tag_message_file"
  exit 1
fi

rm -f "$tag_message_file"
echo "release notes check passed for $tag_name"
