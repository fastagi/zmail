package mailbox

import (
	"context"
	"log/slog"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/events"
)

// RegisterEvents subscribes the mailbox starter to the organization starter's membership events.
// Handlers run synchronously after the membership change commits. A failure is logged and never
// fails the membership change: the mailbox then stays personal for an owner who cannot sign in,
// and a handover seals at its own time, which seals more, never less.
func (h *Handler) RegisterEvents(bus *events.EventBus) {
	bus.Subscribe(domain.EventOrganizationMemberRemoved, func(ctx context.Context, event events.Event) error {
		removed, ok := unwrap[domain.OrganizationMemberRemovedEvent](event)
		if !ok {
			return nil
		}
		if err := h.service.MemberRemoved(ctx, removed); err != nil {
			logMemberEventFailure(ctx, event.EventName(), removed.OrganizationID, removed.UserID, err)
		}
		return nil
	})
	bus.Subscribe(domain.EventOrganizationMemberJoined, func(ctx context.Context, event events.Event) error {
		joined, ok := unwrap[domain.OrganizationMemberJoinedEvent](event)
		if !ok {
			return nil
		}
		if err := h.service.MemberJoined(ctx, joined); err != nil {
			logMemberEventFailure(ctx, event.EventName(), joined.OrganizationID, joined.UserID, err)
		}
		return nil
	})
}

// unwrap finds the published value inside the metadata wrapper the bus adds.
func unwrap[T any](event events.Event) (T, bool) {
	if value, ok := event.(T); ok {
		return value, true
	}
	if wrapped, ok := event.(events.WrappedEvent); ok {
		value, ok := wrapped.BasicEvent.(T)
		return value, ok
	}
	var zero T
	return zero, false
}

func logMemberEventFailure(ctx context.Context, eventName string, organizationID, userID uint, err error) {
	slog.ErrorContext(ctx, "mailbox_member_event_failed",
		slog.String("event", eventName),
		slog.Uint64("organization_id", uint64(organizationID)),
		slog.Uint64("user_id", uint64(userID)),
		slog.String("error", err.Error()))
}
