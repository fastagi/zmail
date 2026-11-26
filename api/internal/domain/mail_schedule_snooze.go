package domain

import (
	"context"
	"time"
)

// MailQueuedForSchedule is the QueuedReason of a single outbound message waiting until its
// ScheduledAt instant before being dispatched.
const MailQueuedForSchedule = "scheduled"

// MailSnoozeRepository wakes inbox messages whose snooze window has elapsed.
type MailSnoozeRepository interface {
	// WakeDueSnoozedMessages clears snoozed_until and marks unread every inbox message whose
	// snoozed_until is at or before now, returning how many messages woke up.
	WakeDueSnoozedMessages(ctx context.Context, now time.Time) (int, error)
}

// MailSnoozeWaker runs snooze wake-ups outside request paths on the mail worker tick.
type MailSnoozeWaker interface {
	WakeDueSnoozedMessages(ctx context.Context, now time.Time) (int, error)
}

// OrganizationSendPolicy is what an organization's own record says about its sending.
type OrganizationSendPolicy struct {
	// CreatedAt is when the organization was created; zero when it is unknown.
	CreatedAt time.Time
	// Suspended is true while a platform operator has suspended the organization; it sends nothing.
	Suspended bool
	// DailySendLimit, when set by a platform operator, replaces the instance limit of every lane and
	// the new-organization cap.
	DailySendLimit *int
}
