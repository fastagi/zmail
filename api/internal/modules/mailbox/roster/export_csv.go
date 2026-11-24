package roster

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ExportMember is one member of an exported mailbox. A personal mailbox's owner has no permission.
type ExportMember struct {
	Email      string
	Permission domain.MailboxPermission
}

// ExportRow is one exported mailbox with its counts and members ordered by email.
type ExportRow struct {
	Entry   *domain.MailboxDirectoryEntry
	Members []ExportMember
}

// MailboxCSVHeader is the first line of an export; its first three columns are the import columns.
var MailboxCSVHeader = []string{"address", "display_name", "members", "member_count", "sent_last_24h", "created_at"}

// WriteMailboxCSV writes rows as a UTF-8 CSV file with a byte order mark and CRLF line ends, so
// spreadsheet applications open it with the right encoding.
func WriteMailboxCSV(w io.Writer, rows []ExportRow) error {
	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	if err := writer.Write(MailboxCSVHeader); err != nil {
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
			CSVCell(mailbox.Address),
			CSVCell(mailbox.DisplayName),
			CSVCell(strings.Join(segments, ";")),
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

// CSVCell puts an apostrophe before a cell starting with =, +, -, or @, so a spreadsheet does not
// run it as a formula. Importing the file removes it again.
func CSVCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}

// ExportFilename names an export by type and UTC date.
func ExportFilename(mailboxType domain.MailboxType, now time.Time) string {
	return string(mailboxType) + "-mailboxes-" + now.UTC().Format("20060102") + ".csv"
}
