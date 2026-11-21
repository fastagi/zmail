package domain

import (
	"context"
	"time"
)

// ExternalMailSyncOutcome is how one fetch of a connection ended.
type ExternalMailSyncOutcome string

const (
	// ExternalMailSyncOK stored a page.
	ExternalMailSyncOK ExternalMailSyncOutcome = "ok"
	// ExternalMailSyncAuthFailed means the provider refused the grant; polling stops.
	ExternalMailSyncAuthFailed ExternalMailSyncOutcome = "auth_failed"
	// ExternalMailSyncFetchFailed is any other provider failure; the next attempt backs off.
	ExternalMailSyncFetchFailed ExternalMailSyncOutcome = "fetch_failed"
	// ExternalMailSyncCursorExpired means the provider no longer honored the cursor; the sync
	// starts over from an empty one.
	ExternalMailSyncCursorExpired ExternalMailSyncOutcome = "cursor_expired"
	// ExternalMailSyncProviderUnavailable means the instance no longer runs the connector.
	ExternalMailSyncProviderUnavailable ExternalMailSyncOutcome = "provider_unavailable"
	// ExternalMailSyncDropped means the connection stopped being active while the fetch ran, so
	// nothing fetched was stored.
	ExternalMailSyncDropped ExternalMailSyncOutcome = "dropped"
)

// Bounds of the sync history of one connection.
const (
	// MaxExternalMailSyncRuns is how many of a connection's latest runs are kept and listed.
	MaxExternalMailSyncRuns = 50
	// ExternalMailSyncRunRetention is how long a run is kept at most.
	ExternalMailSyncRunRetention = 14 * 24 * time.Hour
)

// ExternalMailSyncRun records one fetch of one connection: when, how it ended, and how much mail
// it moved. It never holds message data.
type ExternalMailSyncRun struct {
	ID           uint
	ConnectionID uint
	StartedAt    time.Time
	FinishedAt   time.Time
	Outcome      ExternalMailSyncOutcome
	// Fetched is how many messages the provider returned; Stored how many were new.
	Fetched int
	Stored  int
	// More means the provider had another page ready.
	More bool
	// ErrorCode is the connection error code of a failed fetch; empty otherwise.
	ErrorCode string
	// TimedOut means the fetch hit its deadline; the outcome is fetch_failed.
	TimedOut bool
	// Throttled means the next page waits the spacing because the connection already fetched
	// the most pages in a row allowed at once.
	Throttled bool
}

// ExternalMailClaim asks for due connections, round-robin across providers.
type ExternalMailClaim struct {
	Now        time.Time
	LeaseUntil time.Time
	// Limit bounds the whole claim; PerProvider bounds each provider's share of it.
	Limit       int
	PerProvider int
}

// ExternalMailPurgeStep deletes a bounded batch of the copies of one revoked connection.
type ExternalMailPurgeStep struct {
	// RevokedBefore picks a connection revoked at or before it, oldest first; ignored when
	// ConnectionID names one.
	RevokedBefore time.Time
	ConnectionID  uint
	// Messages bounds the copies deleted in this step.
	Messages int
}

// ExternalMailPurge is what one purge step did.
type ExternalMailPurge struct {
	Connection *ExternalMailConnection
	// DeletedMessages counts the copies this step deleted.
	DeletedMessages int
	// Completed means no copy is left: the caller deletes the mailbox and the connection.
	Completed bool
}

// ExternalMailOpsRepository keeps the sync history and purges the copies of revoked connections.
type ExternalMailOpsRepository interface {
	// ListExternalMailSyncRuns returns the connection's latest runs, newest first.
	ListExternalMailSyncRuns(ctx context.Context, connectionID uint, limit int) ([]*ExternalMailSyncRun, error)
	// PruneExternalMailSyncRuns deletes up to limit runs that started before the time.
	PruneExternalMailSyncRuns(ctx context.Context, before time.Time, limit int) (int, error)
	// PurgeRevokedExternalMail locks one revoked connection whose copies are due, skipping one
	// another worker holds, and deletes a batch of its copies. It returns nil when none is due.
	// Call it inside a transaction: the lock holds until it ends.
	PurgeRevokedExternalMail(ctx context.Context, step ExternalMailPurgeStep) (*ExternalMailPurge, error)
	// DeleteExternalMailConnection deletes a connection with its runs and pending sign-ins.
	DeleteExternalMailConnection(ctx context.Context, id uint) error
	// ExpediteExternalMailPurge makes a revoked connection's copies due at the time. It answers
	// false when the connection is not revoked.
	ExpediteExternalMailPurge(ctx context.Context, id uint, revokedAt time.Time) (bool, error)
}

// MailboxAccessCopiesPurged records that the copies of a revoked connection were deleted, at the
// end of the retention or at once by the member who bound it. The mailbox is deleted with them.
const MailboxAccessCopiesPurged MailboxAccessEventType = "copies_purged"
