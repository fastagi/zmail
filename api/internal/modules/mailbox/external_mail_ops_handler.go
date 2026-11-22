package mailbox

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// ExternalMailSyncRunResponse is one fetch of a connection. It never carries message data.
type ExternalMailSyncRunResponse struct {
	ID           uint      `json:"id"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Outcome      string    `json:"outcome"`
	FetchedCount int       `json:"fetched_count"`
	StoredCount  int       `json:"stored_count"`
	More         bool      `json:"more"`
	ErrorCode    *string   `json:"error_code"`
	TimedOut     bool      `json:"timed_out"`
	Throttled    bool      `json:"throttled"`
	// Trigger is why the sync ran: poll, or push for a sync a provider notification asked for.
	Trigger string `json:"trigger"`
}

// ListExternalMailSyncRuns lists the latest sync runs of the caller's own connection, newest
// first; 404 for anyone else.
// fastbuild:bounded-list max=50 reason=the worker keeps at most MaxExternalMailSyncRuns runs per connection
func (h *Handler) ListExternalMailSyncRuns(c *gin.Context) {
	viewer, connectionID, ok := viewerScope(c, "connection_id")
	if !ok {
		return
	}
	runs, err := h.service.ListExternalMailSyncRuns(c.Request.Context(), viewer, connectionID)
	if err != nil {
		response.HandleError(c, "Failed to list external mail sync runs", err)
		return
	}
	items := make([]ExternalMailSyncRunResponse, 0, len(runs))
	for _, run := range runs {
		items = append(items, ExternalMailSyncRunResponse{
			ID: run.ID, StartedAt: run.StartedAt.UTC(), FinishedAt: run.FinishedAt.UTC(), Outcome: string(run.Outcome),
			FetchedCount: run.Fetched, StoredCount: run.Stored, More: run.More, ErrorCode: optionalText(run.ErrorCode),
			TimedOut: run.TimedOut, Throttled: run.Throttled, Trigger: string(externalMailSyncTrigger(run.Trigger)),
		})
	}
	privateResponse(c)
	response.Success(c, items)
}

// ExternalMailPurgeResponse is what deleting a revoked connection's copies did.
type ExternalMailPurgeResponse struct {
	DeletedMessages int  `json:"deleted_messages"`
	Completed       bool `json:"completed"`
}

// PurgeExternalMailCopies deletes the copies of the caller's own revoked connection at once: 200
// when every copy, the mailbox, and the connection are gone, 202 when the worker deletes the rest.
func (h *Handler) PurgeExternalMailCopies(c *gin.Context) {
	viewer, connectionID, ok := viewerScope(c, "connection_id")
	if !ok {
		return
	}
	result, err := h.service.PurgeExternalMailCopies(c.Request.Context(), viewer, connectionID)
	if err != nil {
		response.HandleError(c, "Failed to purge external mail copies", err)
		return
	}
	privateResponse(c)
	body := ExternalMailPurgeResponse(result)
	if result.Completed {
		response.Success(c, body)
		return
	}
	response.Accepted(c, body)
}

// externalMailSyncTrigger reads an empty trigger as a poll.
func externalMailSyncTrigger(trigger domain.ExternalMailSyncTrigger) domain.ExternalMailSyncTrigger {
	if trigger == "" {
		return domain.ExternalMailSyncTriggerPoll
	}
	return trigger
}
