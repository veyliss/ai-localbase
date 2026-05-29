package util

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

type StructuredTable struct {
	FileName string
	Sheet    string
	Headers  []string
	Rows     []StructuredTableRow
}

type StructuredTableRow struct {
	Number int
	Values []string
}

func ExtractStructuredTables(path string) ([]StructuredTable, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".csv":
		return extractStructuredCSV(path)
	case ".xlsx":
		return extractStructuredXLSX(path)
	default:
		return nil, fmt.Errorf("unsupported structured table type: %s", ext)
	}
}

func extractStructuredCSV(path string) ([]StructuredTable, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}

	table := buildStructuredTable(filepath.Base(path), "", records)
	if len(table.Headers) == 0 {
		return nil, nil
	}
	return []StructuredTable{table}, nil
}

func extractStructuredXLSX(path string) ([]StructuredTable, error) {
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = workbook.Close() }()

	tables := make([]StructuredTable, 0)
	for _, sheet := range workbook.GetSheetList() {
		rows, err := workbook.GetRows(sheet)
		if err != nil {
			return nil, fmt.Errorf("read xlsx sheet %s: %w", sheet, err)
		}
		table := buildStructuredTable(filepath.Base(path), sheet, rows)
		if len(table.Headers) == 0 {
			continue
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func buildStructuredTable(fileName, sheet string, rows [][]string) StructuredTable {
	nonEmptyRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		if !rowHasContent(row) {
			continue
		}
		nonEmptyRows = append(nonEmptyRows, trimTrailingEmptyCells(row))
	}
	if len(nonEmptyRows) == 0 {
		return StructuredTable{}
	}

	headers := normalizeTableHeaders(nonEmptyRows[0])
	tableRows := make([]StructuredTableRow, 0, len(nonEmptyRows)-1)
	for index, row := range nonEmptyRows[1:] {
		values := make([]string, len(headers))
		for cellIndex := range headers {
			if cellIndex < len(row) {
				values[cellIndex] = strings.TrimSpace(row[cellIndex])
			}
		}
		tableRows = append(tableRows, StructuredTableRow{
			Number: index + 2,
			Values: values,
		})
	}

	return StructuredTable{
		FileName: fileName,
		Sheet:    sheet,
		Headers:  headers,
		Rows:     tableRows,
	}
}
