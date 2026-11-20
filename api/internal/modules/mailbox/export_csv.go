package mailbox

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// mailboxCSVHeader is the first line of an export; its first three columns are the import columns.
var mailboxCSVHeader = []string{"address", "display_name", "members", "member_count", "sent_last_24h", "created_at"}

// writeMailboxCSV writes rows as a UTF-8 CSV file with a byte order mark and CRLF line ends, so
// spreadsheet applications open it with the right encoding.
func writeMailboxCSV(w io.Writer, rows []MailboxExportRow) error {
	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	if err := writer.Write(mailboxCSVHeader); err != nil {
		return err
	}
	for _, row := range rows {
		segments := make([]string, 0, len(row.Members))
		for _, member := range row.Members {
			if member.Permission == "" {
				segments = append(segments, member.Email)
				continue
			}
			segments = append(segments, member.Email+":"+string(member.Permission))
		}
		mailbox := row.Entry.Mailbox
		record := []string{
			csvCell(mailbox.Address),
			csvCell(mailbox.DisplayName),
			csvCell(strings.Join(segments, ";")),
			strconv.Itoa(row.Entry.MemberCount),
			strconv.FormatInt(row.Entry.SentLast24h, 10),
			mailbox.CreatedAt.UTC().Format(time.RFC3339),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// csvCell puts an apostrophe before a cell starting with =, +, -, or @, so a spreadsheet does not
// run it as a formula. Importing the file removes it again.
func csvCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}

// exportFilename names an export by type and UTC date.
func exportFilename(mailboxType domain.MailboxType, now time.Time) string {
	return string(mailboxType) + "-mailboxes-" + now.UTC().Format("20060102") + ".csv"
}
