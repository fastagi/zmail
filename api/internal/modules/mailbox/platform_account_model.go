package mailbox

import "github.com/fastagi/zmail/api/internal/modules/mailbox/roster"

var (
	// ErrPlatformAccountNotFound reports an account that does not exist, belongs to another
	// organization, or that the viewer may not see.
	ErrPlatformAccountNotFound = roster.ErrPlatformAccountNotFound
	// ErrPlatformAccountTaken reports another account of the organization with the same platform
	// and handle, ignoring case.
	ErrPlatformAccountTaken = roster.ErrPlatformAccountTaken
)

type (
	PlatformAccountPO      = roster.PlatformAccountPO
	PlatformAccountEventPO = roster.PlatformAccountEventPO
)

func uintText(id uint) string {
	return roster.UintText(id)
}

func optionalIDText(id *uint) string {
	return roster.OptionalIDText(id)
}

func optionalText(value string) *string {
	return roster.OptionalText(value)
}
