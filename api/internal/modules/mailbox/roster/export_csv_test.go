package roster

import (
	"bytes"
	"encoding/csv"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestCSVCellFormulaInjectionSafety(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "=SUM(A1:A2)", want: "'=SUM(A1:A2)"},
		{input: "+cmd|' /C calc'!A0", want: "'+cmd|' /C calc'!A0"},
		{input: "-1+1", want: "'-1+1"},
		{input: "@SUM(1+1)", want: "'@SUM(1+1)"},
		{input: "Normal Name", want: "Normal Name"},
		{input: "", want: ""},
	}

	for _, tc := range tests {
		if got := CSVCell(tc.input); got != tc.want {
			t.Fatalf("CSVCell(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestWriteMailboxCSV(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 6, 15, 10, 30, 0, 0, time.FixedZone("UTC+2", 2*3600))
	rows := []ExportRow{
		{
			Entry: &domain.MailboxDirectoryEntry{
				Mailbox: &domain.Mailbox{
					Address:     "support@example.com",
					DisplayName: "=HYPERLINK(\"http://evil\")",
					CreatedAt:   created,
				},
				MemberCount: 2,
				SentLast24h: 15,
			},
			Members: []ExportMember{
				{Email: "alice@example.com", Permission: domain.MailboxPermissionManage},
				{Email: "bob@example.com", Permission: domain.MailboxPermissionView},
			},
		},
		{
			Entry: &domain.MailboxDirectoryEntry{
				Mailbox: &domain.Mailbox{
					Address:     "carol@example.com",
					DisplayName: "Carol, Ops",
					CreatedAt:   created,
				},
				MemberCount: 1,
				SentLast24h: 0,
			},
			Members: []ExportMember{
				{Email: "carol@example.com"},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteMailboxCSV(&buf, rows); err != nil {
		t.Fatalf("WriteMailboxCSV failed: %v", err)
	}

	raw := buf.String()
	if !strings.HasPrefix(raw, "\xEF\xBB\xBF") {
		t.Fatalf("expected UTF-8 BOM prefix")
	}
	if !strings.Contains(raw, "\r\n") {
		t.Fatalf("expected CRLF line endings")
	}

	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatalf("csv.ReadAll failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 CSV rows (header + 2 data), got %d", len(records))
	}
	if !slices.Equal(records[0], MailboxCSVHeader) {
		t.Fatalf("header = %v, want %v", records[0], MailboxCSVHeader)
	}
	wantRow1 := []string{
		"support@example.com",
		"'=HYPERLINK(\"http://evil\")",
		"alice@example.com:manage;bob@example.com:view",
		"2",
		"15",
		"2026-06-15T08:30:00Z",
	}
	if !slices.Equal(records[1], wantRow1) {
		t.Fatalf("row 1 = %v, want %v", records[1], wantRow1)
	}
	wantRow2 := []string{
		"carol@example.com",
		"Carol, Ops",
		"carol@example.com",
		"1",
		"0",
		"2026-06-15T08:30:00Z",
	}
	if !slices.Equal(records[2], wantRow2) {
		t.Fatalf("row 2 = %v, want %v", records[2], wantRow2)
	}
}

func TestExportFilename(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 23, 30, 0, 0, time.FixedZone("UTC-5", -5*3600))
	if got := ExportFilename(domain.MailboxTypeShared, now); got != "shared-mailboxes-20260616.csv" {
		t.Fatalf("ExportFilename = %q, want shared-mailboxes-20260616.csv", got)
	}
}
