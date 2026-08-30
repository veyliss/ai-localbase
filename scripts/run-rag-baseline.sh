#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  printf '用法: %s <本地数据集 JSON> <知识库 ID> [输出目录]\n' "$0" >&2
  exit 2
fi

dataset_path=$1
knowledge_base_id=$2
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${3:-backend/eval/results/local-baseline}

if [[ "$dataset_path" != /* ]]; then
  dataset_path="$repo_root/$dataset_path"
fi
if [[ "$output_dir" != /* ]]; then
  output_dir="$repo_root/$output_dir"
fi

if [[ ! -f "$dataset_path" ]]; then
  printf '数据集不存在: %s\n' "$dataset_path" >&2
  exit 1
fi

cd "$repo_root/backend"

common_args=(
  -dataset "$dataset_path"
  -output "$output_dir"
  -mock=false
  -eval-kb-id "$knowledge_base_id"
  -eval-path-map "${EVAL_PATH_MAP:-/app=.}"
  -eval-allow-missing-sources=false
  -run-prefix baseline
)

# app-state usually stores the URL visible from the backend container. This
# wrapper runs on the host, so callers can provide host-visible endpoints
# without modifying the saved application configuration.
if [[ -n "${EVAL_EMBEDDING_BASE_URL:-}" ]]; then
  common_args+=(-eval-embedding-base-url "$EVAL_EMBEDDING_BASE_URL")
fi
if [[ -n "${EVAL_CHAT_BASE_URL:-}" ]]; then
  common_args+=(-eval-chat-base-url "$EVAL_CHAT_BASE_URL")
fi

fixture_manifest=${EVAL_FIXTURE_MANIFEST:-}
if [[ -z "$fixture_manifest" && -f "$repo_root/backend/eval/fixtures/public-v1/manifest.json" ]]; then
  fixture_manifest="$repo_root/backend/eval/fixtures/public-v1/manifest.json"
fi
if [[ -n "$fixture_manifest" && "$fixture_manifest" != /* ]]; then
  if [[ -f "$repo_root/$fixture_manifest" ]]; then
    fixture_manifest="$repo_root/$fixture_manifest"
  elif [[ -f "$repo_root/backend/$fixture_manifest" ]]; then
    fixture_manifest="$repo_root/backend/$fixture_manifest"
  fi
fi
if [[ -n "$fixture_manifest" && ! -f "$fixture_manifest" ]]; then
  printf 'fixture manifest 不存在: %s\n' "$fixture_manifest" >&2
  exit 1
fi
if [[ -n "$fixture_manifest" ]]; then
  common_args+=(-eval-fixture-manifest "$fixture_manifest")
fi

run_strategy() {
  local label=$1
  local search_mode=$2
  local rerank_strategy=$3
  local query_rewrite=$4
  local extra_args=()

  if [[ "$query_rewrite" == "true" ]]; then
    extra_args=(-retrieval-query-rewrite-max-variants 3)
  fi

  printf '\n运行策略: %s\n' "$label"
  go run ./eval/cmd/ "${common_args[@]}" \
    -run-label "$label" \
    -retrieval-search-mode "$search_mode" \
    -retrieval-rerank-strategy "$rerank_strategy" \
    -retrieval-query-rewrite "$query_rewrite" \
    "${extra_args[@]}"
}

printf '本地 baseline 仅读取已启动服务和本地数据集，不上传、不修改源数据。\n'
run_strategy dense-keyword-no-rewrite dense keyword false
run_strategy hybrid-keyword-no-rewrite hybrid keyword false
run_strategy hybrid-semantic-no-rewrite hybrid semantic false
run_strategy hybrid-semantic-rewrite hybrid semantic true

printf '\n四组策略已完成。使用 compare_reports 对任意两份 JSON 报告进行比较。\n'
