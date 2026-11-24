package mailbox

import "github.com/fastagi/zmail/api/internal/modules/mailbox/roster"

const maxDirectoryCursorLength = roster.MaxDirectoryCursorLength

func encodeDirectoryCursor(id uint) string {
	return roster.EncodeDirectoryCursor(id)
}

func decodeDirectoryCursor(token string) (uint, error) {
	return roster.DecodeDirectoryCursor(token)
}
