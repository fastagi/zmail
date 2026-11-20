package mailbox

import (
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/fastagi/zmail/api/internal/domain"
)

// maxDirectoryCursorLength bounds a directory cursor token; an encoded id is at most 28 characters.
const maxDirectoryCursorLength = 128

// encodeDirectoryCursor makes the opaque token of the page that starts below id.
func encodeDirectoryCursor(id uint) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(id), 10)))
}

// decodeDirectoryCursor reads a token made by encodeDirectoryCursor; anything else is invalid input.
func decodeDirectoryCursor(token string) (uint, error) {
	invalid := fmt.Errorf("%w: malformed cursor", domain.ErrInvalidInput)
	if len(token) > maxDirectoryCursorLength {
		return 0, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, invalid
	}
	id, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, invalid
	}
	return uint(id), nil
}
