#!/usr/bin/env bash
set -euo pipefail

# This runner only orchestrates local evaluation. The dataset and generated
# reports remain on the caller's machine and are never bundled by the repo.
if [[ $# -lt 1 ]]; then
  echo "用法: $0 <本地数据集.json> [输出目录]" >&2
  exit 2
fi

dataset=$1
output_dir=${2:-eval/results/strategy-matrix}
concurrency=${EVAL_CONCURRENCY:-1}

common_args=(
  -dataset "$dataset"
  -output "$output_dir"
  -mock=false
  -eval-concurrency "$concurrency"
  -run-prefix matrix
)

if [[ -n "${EVAL_KB_ID:-}" ]]; then common_args+=(-eval-kb-id "$EVAL_KB_ID"); fi
if [[ -n "${EVAL_EMBEDDING_BASE_URL:-}" ]]; then common_args+=(-eval-embedding-base-url "$EVAL_EMBEDDING_BASE_URL"); fi
if [[ -n "${EVAL_CHAT_BASE_URL:-}" ]]; then common_args+=(-eval-chat-base-url "$EVAL_CHAT_BASE_URL"); fi
if [[ -n "${EVAL_PATH_MAP:-}" ]]; then common_args+=(-eval-path-map "$EVAL_PATH_MAP"); fi

run_strategy() {
  local label=$1
  local search_mode=$2
  local rerank_strategy=$3
  local rewrite=$4
  go run ./eval/cmd/ "${common_args[@]}" \
    -run-label "$label" \
    -retrieval-search-mode "$search_mode" \
    -retrieval-rerank-strategy "$rerank_strategy" \
    -retrieval-query-rewrite "$rewrite"
}

cd "$(dirname "$0")/.."
run_strategy dense-keyword-no-rewrite dense keyword false
run_strategy hybrid-keyword-no-rewrite hybrid keyword false
run_strategy hybrid-semantic-no-rewrite hybrid semantic false
run_strategy hybrid-semantic-rewrite hybrid semantic true

echo "策略矩阵完成，报告保存在本地目录: $output_dir" >&2
