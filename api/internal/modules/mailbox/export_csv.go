package mailbox

import (
	"io"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/roster"
)

var mailboxCSVHeader = roster.MailboxCSVHeader

func writeMailboxCSV(w io.Writer, rows []MailboxExportRow) error {
	converted := make([]roster.ExportRow, len(rows))
	for i, row := range rows {
		members := make([]roster.ExportMember, len(row.Members))
		for j, member := range row.Members {
			members[j] = roster.ExportMember{Email: member.Email, Permission: member.Permission}
		}
		converted[i] = roster.ExportRow{Entry: row.Entry, Members: members}
	}
	return roster.WriteMailboxCSV(w, converted)
}

func csvCell(value string) string {
	return roster.CSVCell(value)
}

func exportFilename(mailboxType domain.MailboxType, now time.Time) string {
	return roster.ExportFilename(mailboxType, now)
}
