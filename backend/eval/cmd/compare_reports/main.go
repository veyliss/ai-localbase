package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"ai-localbase/eval/report"
)

func main() {
	baselinePath := flag.String("baseline", "", "基线报告 JSON 路径")
	candidatePath := flag.String("candidate", "", "候选报告 JSON 路径")
	outputPath := flag.String("output", "", "可选的 Markdown 对比报告输出路径")
	failOnRegression := flag.Bool("fail-on-regression", false, "存在指标退步或用例退步时返回非零退出码")
	flag.Parse()

	if strings.TrimSpace(*baselinePath) == "" || strings.TrimSpace(*candidatePath) == "" {
		fmt.Fprintln(os.Stderr, "用法: go run ./eval/cmd/compare_reports -baseline <baseline.json> -candidate <candidate.json>")
		os.Exit(2)
	}

	baseline, err := report.LoadJSON(*baselinePath)
	if err != nil {
		fatal(err)
	}
	candidate, err := report.LoadJSON(*candidatePath)
	if err != nil {
		fatal(err)
	}

	comparison := report.Compare(baseline, candidate)
	markdown := renderMarkdown(comparison, *baselinePath, *candidatePath)
	if strings.TrimSpace(*outputPath) != "" {
		if err := os.WriteFile(*outputPath, []byte(markdown), 0o644); err != nil {
			fatal(fmt.Errorf("写入对比报告失败: %w", err))
		}
	}
	fmt.Print(markdown)

	if *failOnRegression && hasRegression(comparison) {
		os.Exit(1)
	}
}

func renderMarkdown(comparison report.ReportComparison, baselinePath, candidatePath string) string {
	var builder strings.Builder
	builder.WriteString("# RAG Baseline 对比\n\n")
	builder.WriteString(fmt.Sprintf("- 基线：`%s`（%s）\n", baselinePath, comparison.BaselineRunID))
	builder.WriteString(fmt.Sprintf("- 候选：`%s`（%s）\n\n", candidatePath, comparison.CandidateRunID))
	builder.WriteString("## 指标差异\n\n")
	builder.WriteString("| 指标 | 基线 | 候选 | 差值 | 方向 |\n|---|---:|---:|---:|---|\n")
	for _, item := range comparison.Metrics {
		direction := "越低越好"
		if item.HigherWins {
			direction = "越高越好"
		}
		builder.WriteString(fmt.Sprintf("| %s | %.4f | %.4f | %+.4f | %s |\n", item.Name, item.Baseline, item.Candidate, item.Delta, direction))
	}

	builder.WriteString("\n## 用例变化\n\n")
	builder.WriteString(fmt.Sprintf("- 新命中：%d 条\n", len(comparison.NewHits)))
	builder.WriteString(fmt.Sprintf("- 退步：%d 条\n\n", len(comparison.Regressions)))
	if len(comparison.Regressions) > 0 {
		builder.WriteString("| 用例 ID | 分类 | 原因 |\n|---|---|---|\n")
		for _, item := range comparison.Regressions {
			builder.WriteString(fmt.Sprintf("| %s | %s | %s |\n", item.CaseID, item.Category, item.Reason))
		}
	}
	return builder.String()
}

func hasRegression(comparison report.ReportComparison) bool {
	if len(comparison.Regressions) > 0 {
		return true
	}
	for _, item := range comparison.Metrics {
		if item.HigherWins && item.Delta < 0 {
			return true
		}
		if !item.HigherWins && item.Delta > 0 {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
