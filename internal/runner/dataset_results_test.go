package runner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestParseRunResultAcceptsBoundedTypedDatasetWrites(t *testing.T) {
	raw := `{
  "status":"completed","summary":"Stored structured research.","files_changed":[],
  "verification":[{"name":"validated","status":"passed","details":"schema checked"}],"artifacts":[],"uncertainties":[],"approval_needed":null,
  "dataset_writes":[
    {"operation":"create_dataset","dataset":{"name":"Research","slug":"research","schema":[{"name":"source","type":"string","nullable":false}],"unique_key":["source"]}},
    {"operation":"upsert_rows","dataset_id":"dataset-1","rows":[{"source":"primary"}]}
  ]
}`
	result, err := ParseRunResult(raw)
	if err != nil || len(result.DatasetWrites) != 2 || result.DatasetWrites[0].Dataset.WorkspaceID != "" || result.DatasetWrites[1].DatasetID != "dataset-1" {
		t.Fatalf("dataset writes = %#v, err=%v", result.DatasetWrites, err)
	}
}

func TestParseRunResultAcceptsWorkspaceRelativeDatasetRowsFile(t *testing.T) {
	raw := `{"status":"completed","summary":"Prepared rows","files_changed":["research/rows.json"],"verification":[{"name":"rows","status":"passed","details":"100 rows validated"}],"artifacts":[],"uncertainties":[],"approval_needed":null,"dataset_writes":[{"operation":"upsert_rows","dataset_id":"dataset-1","rows_file":"research/rows.json"}]}`
	result, err := ParseRunResult(raw)
	if err != nil || len(result.DatasetWrites) != 1 || result.DatasetWrites[0].RowsFile != "research/rows.json" {
		t.Fatalf("rows file result = %#v, err=%v", result, err)
	}
	for _, invalid := range []string{"/tmp/rows.json", "../rows.json", "research/../../rows.json"} {
		bad := strings.ReplaceAll(raw, "research/rows.json", invalid)
		if _, err := ParseRunResult(bad); err == nil {
			t.Fatalf("accepted unsafe rows_file %q", invalid)
		}
	}
}

func TestParseRunResultAcceptsAtomicDatasetCreateFromRowsFile(t *testing.T) {
	raw := `{"status":"completed","summary":"Created populated research","files_changed":["research/rows.json"],"verification":[{"name":"rows","status":"passed","details":"100 rows validated"}],"artifacts":[],"uncertainties":[],"approval_needed":null,"dataset_writes":[{"operation":"create_dataset","dataset":{"name":"Research","slug":"research","schema":[{"name":"source","type":"string","nullable":false}],"unique_key":["source"]},"rows_file":"research/rows.json"}]}`
	result, err := ParseRunResult(raw)
	if err != nil || len(result.DatasetWrites) != 1 || result.DatasetWrites[0].RowsFile != "research/rows.json" {
		t.Fatalf("atomic create result = %#v, err=%v", result, err)
	}
}

func TestParseRunResultRejectsMalformedAndOversizedDatasetWrites(t *testing.T) {
	base := func(write string) string {
		return fmt.Sprintf(`{"status":"completed","summary":"Done","files_changed":[],"verification":[],"artifacts":[],"uncertainties":[],"approval_needed":null,"dataset_writes":[%s]}`, write)
	}
	for name, raw := range map[string]string{
		"unknown operation":   base(`{"operation":"sql","rows":[{"query":"DROP TABLE tasks"}]}`),
		"invalid schema type": base(`{"operation":"create_dataset","dataset":{"name":"Bad","schema":[{"name":"value","type":"blob"}]}}`),
		"missing ID":          base(`{"operation":"upsert_rows","rows":[{"value":1}]}`),
		"empty row":           base(`{"operation":"upsert_rows","dataset_id":"dataset-1","rows":[{}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRunResult(raw); err == nil {
				t.Fatal("malformed dataset write accepted")
			}
		})
	}
	rows := make([]string, MaxRunDatasetRowsPerWrite+1)
	for index := range rows {
		rows[index] = fmt.Sprintf(`{"id":%d}`, index)
	}
	if _, err := ParseRunResult(base(`{"operation":"upsert_rows","dataset_id":"dataset-1","rows":[` + strings.Join(rows, ",") + `]}`)); err == nil || !strings.Contains(err.Error(), "1-100") {
		t.Fatalf("row cap error = %v", err)
	}
}

func TestParseRunResultAcceptsExactRowUpdateAndDelete(t *testing.T) {
	raw := `{"status":"completed","summary":"Prepared exact row changes","files_changed":[],"verification":[],"artifacts":[],"uncertainties":[],"approval_needed":null,"dataset_writes":[{"operation":"update_row","dataset_id":"dataset-1","row_id":7,"values":{"score":9}},{"operation":"delete_row","dataset_id":"dataset-1","row_id":8}]}`
	result, err := ParseRunResult(raw)
	if err != nil || len(result.DatasetWrites) != 2 || result.DatasetWrites[0].RowID != 7 || result.DatasetWrites[1].Operation != domain.DatasetWriteDelete {
		t.Fatalf("exact row writes = %#v, err=%v", result.DatasetWrites, err)
	}
}
