package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestSemanticChunkBasic(t *testing.T) {
	text := "第一段第一句。第一段第二句。\n\n第二段第一句。"
	cfg := DefaultSemanticChunkConfig()
	cfg.MaxChunkSize = 20
	cfg.MinChunkSize = 1
	cfg.OverlapSize = 0

	chunks := SemanticChunk(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if chunks[0] != "第一段第一句。第一段第二句。" {
		t.Fatalf("unexpected first chunk: %q", chunks[0])
	}
	if chunks[len(chunks)-1] != "第二段第一句。" {
		t.Fatalf("unexpected last chunk: %q", chunks[len(chunks)-1])
	}
}

func TestSemanticChunkOverlap(t *testing.T) {
	text := "句子一。句子二。句子三。"
	cfg := DefaultSemanticChunkConfig()
	cfg.MaxChunkSize = 6
	cfg.MinChunkSize = 1
	cfg.OverlapSize = 2

	chunks := SemanticChunk(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	prevTail := []rune(strings.TrimSpace(chunks[0]))
	if len(prevTail) < cfg.OverlapSize {
		t.Fatalf("expected chunk length >= overlap")
	}
	prefix := string(prevTail[len(prevTail)-cfg.OverlapSize:])
	if !strings.HasPrefix(chunks[1], prefix) {
		t.Fatalf("expected overlap prefix %q, got %q", prefix, chunks[1])
	}
}

func TestSemanticChunkLongSentence(t *testing.T) {
	text := strings.Repeat("很长", 40) + "。"
	cfg := DefaultSemanticChunkConfig()
	cfg.MaxChunkSize = 10
	cfg.MinChunkSize = 1
	cfg.OverlapSize = 0

	chunks := SemanticChunk(text, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected forced split, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len([]rune(chunk)) > cfg.MaxChunkSize {
			t.Fatalf("chunk %d too long: %d", i, len([]rune(chunk)))
		}
	}
}

func TestSemanticChunkMinSize(t *testing.T) {
	text := "短句。\n\n这是一个足够长的句子，用于验证最小长度过滤。"
	cfg := DefaultSemanticChunkConfig()
	cfg.MaxChunkSize = 50
	cfg.MinChunkSize = 10
	cfg.OverlapSize = 0

	chunks := SemanticChunk(text, cfg)
	if len(chunks) != 1 {
		t.Fatalf("expected short chunk to be filtered, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0], "这是一个足够长的句子") {
		t.Fatalf("unexpected chunk: %q", chunks[0])
	}
}

func TestExtractDocumentTextFromCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "customers.csv")
	content := "姓名,部门,城市\n张三,销售部,上海\n李四,技术部,杭州\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	text, err := ExtractDocumentText(path)
	if err != nil {
		t.Fatalf("extract csv: %v", err)
	}
	if !strings.Contains(text, "文件：customers.csv。字段：姓名、部门、城市。数据行数：2。") {
		t.Fatalf("expected csv summary, got %q", text)
	}
	if !strings.Contains(text, "第2行：姓名：张三。部门：销售部。城市：上海。") {
		t.Fatalf("expected first csv row, got %q", text)
	}
}

func TestExtractDocumentTextFromXLSX(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "finance.xlsx")
	workbook := excelize.NewFile()
	defer func() { _ = workbook.Close() }()
	workbook.SetSheetName("Sheet1", "付款记录")
	if err := workbook.SetSheetRow("付款记录", "A1", &[]string{"供应商", "金额", "状态"}); err != nil {
		t.Fatalf("set xlsx header: %v", err)
	}
	if err := workbook.SetSheetRow("付款记录", "A2", &[]string{"A公司", "120000", "已完成"}); err != nil {
		t.Fatalf("set xlsx row: %v", err)
	}
	if err := workbook.SaveAs(path); err != nil {
		t.Fatalf("save xlsx: %v", err)
	}

	text, err := ExtractDocumentText(path)
	if err != nil {
		t.Fatalf("extract xlsx: %v", err)
	}
	if !strings.Contains(text, "文件：finance.xlsx。工作表：付款记录。字段：供应商、金额、状态。数据行数：1。") {
		t.Fatalf("expected xlsx summary, got %q", text)
	}
	if !strings.Contains(text, "第2行：工作表：付款记录；供应商：A公司。金额：120000。状态：已完成。") {
		t.Fatalf("expected xlsx row, got %q", text)
	}
}

func TestExtractStructuredTableSummaryFromCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.csv")
	content := "城市,人数,状态\n武汉,120,活跃\n上海,80,活跃\n武汉,100,停用\n杭州,60,活跃\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	text, err := ExtractDocumentText(path)
	if err != nil {
		t.Fatalf("extract csv: %v", err)
	}
	if !strings.Contains(text, "统计摘要：文件《users.csv》共有4条数据记录。") {
		t.Fatalf("expected table-level summary, got %q", text)
	}
	if !strings.Contains(text, "统计摘要：字段“城市”为类别列，共4个非空值，主要分布为：武汉(2)、上海(1)、杭州(1)。") {
		t.Fatalf("expected category summary, got %q", text)
	}
	if !strings.Contains(text, "统计摘要：字段“人数”为数值列，非空值4个，最小值60.00，最大值120.00，平均值90.00。") {
		t.Fatalf("expected numeric summary, got %q", text)
	}
}
