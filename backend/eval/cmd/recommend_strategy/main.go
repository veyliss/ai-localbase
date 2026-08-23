package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"ai-localbase/eval/report"
)

type candidateFlag []string

func (values *candidateFlag) String() string { return strings.Join(*values, ",") }
func (values *candidateFlag) Set(value string) error {
	if !strings.Contains(value, "=") {
		return fmt.Errorf("候选策略格式必须是 name=report.json")
	}
	*values = append(*values, value)
	return nil
}

func main() {
	baselinePath := flag.String("baseline", "", "baseline 报告 JSON 路径")
	var candidatePaths candidateFlag
	flag.Var(&candidatePaths, "candidate", "候选策略，格式 name=report.json，可重复")
	outputPath := flag.String("output", "", "可选 Markdown 输出路径")
	flag.Parse()

	if strings.TrimSpace(*baselinePath) == "" || len(candidatePaths) == 0 {
		fatal(fmt.Errorf("用法: go run ./eval/cmd/recommend_strategy -baseline baseline.json -candidate hybrid=hybrid.json [-candidate semantic=semantic.json]"))
	}
	baseline, err := report.LoadJSON(*baselinePath)
	if err != nil {
		fatal(err)
	}
	candidates := make([]report.StrategyCandidate, 0, len(candidatePaths))
	for _, value := range candidatePaths {
		name, path, _ := strings.Cut(value, "=")
		candidate, err := report.LoadJSON(path)
		if err != nil {
			fatal(fmt.Errorf("加载候选策略 %s 失败: %w", name, err))
		}
		candidates = append(candidates, report.StrategyCandidate{Name: strings.TrimSpace(name), Report: candidate})
	}

	decision := report.RecommendStrategy(baseline, candidates, report.DefaultStrategyPolicy())
	markdown := renderMarkdown(decision, *baselinePath)
	if strings.TrimSpace(*outputPath) != "" {
		if err := os.WriteFile(*outputPath, []byte(markdown), 0o644); err != nil {
			fatal(fmt.Errorf("写入策略决策报告失败: %w", err))
		}
	}
	fmt.Print(markdown)
}

func renderMarkdown(decision report.StrategyDecision, baselinePath string) string {
	var builder strings.Builder
	builder.WriteString("# 默认检索策略决策\n\n")
	builder.WriteString(fmt.Sprintf("- Baseline：`%s`\n", baselinePath))
	builder.WriteString(fmt.Sprintf("- 推荐策略：**%s**\n", decision.RecommendedStrategy))
	builder.WriteString(fmt.Sprintf("- 是否通过：**%t**\n\n", decision.Approved))
	builder.WriteString("## 候选评估\n\n")
	builder.WriteString("| 策略 | 是否通过 | 综合分 | 原因 |\n|---|---|---:|---|\n")
	for _, evaluation := range decision.Evaluations {
		builder.WriteString(fmt.Sprintf("| %s | %t | %.4f | %s |\n", evaluation.Name, evaluation.Approved, evaluation.Score, strings.Join(evaluation.Reasons, "；")))
	}
	builder.WriteString("\n## 结论\n\n")
	builder.WriteString(strings.Join(decision.Reasons, "\n"))
	builder.WriteString("\n")
	return builder.String()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
