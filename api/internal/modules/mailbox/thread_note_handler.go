package mailbox

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// CreateMailThreadNoteRequest is an internal note to leave on a thread.
type CreateMailThreadNoteRequest struct {
	Body string `json:"body"`
}

// MailThreadNoteResponse is one internal note on a thread.
type MailThreadNoteResponse struct {
	ID           uint      `json:"id"`
	ThreadID     uint      `json:"thread_id"`
	AuthorUserID *uint     `json:"author_user_id"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
}

// MailThreadNotesResponse is a thread's notes, oldest first.
type MailThreadNotesResponse struct {
	Items []MailThreadNoteResponse `json:"items"`
}

func toMailThreadNoteResponse(note domain.MailThreadNote) MailThreadNoteResponse {
	return MailThreadNoteResponse{
		ID: note.ID, ThreadID: note.ThreadID, AuthorUserID: note.AuthorUserID, Body: note.Body, CreatedAt: note.CreatedAt,
	}
}

// ListMailThreadNotes reads the internal notes on a shared mailbox's thread.
func (h *Handler) ListMailThreadNotes(c *gin.Context) {
	viewer, mailboxID, threadID, ok := threadScope(c)
	if !ok {
		return
	}
	notes, err := h.service.MailThreadNotes(c.Request.Context(), viewer, mailboxID, threadID)
	if err != nil {
		response.HandleError(c, "Failed to read the notes", err)
		return
	}
	// fastbuild:bounded-list max=200 reason=a thread keeps at most domain.MailThreadNoteLimit notes
	items := make([]MailThreadNoteResponse, 0, len(notes))
	for _, note := range notes {
		items = append(items, toMailThreadNoteResponse(note))
	}
	privateResponse(c)
	response.Success(c, MailThreadNotesResponse{Items: items})
}

// CreateMailThreadNote leaves an internal note on a shared mailbox's thread.
func (h *Handler) CreateMailThreadNote(c *gin.Context) {
	viewer, mailboxID, threadID, ok := threadScope(c)
	if !ok {
		return
	}
	var req CreateMailThreadNoteRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	note, err := h.service.AddMailThreadNote(c.Request.Context(), viewer, mailboxID, threadID, req.Body)
	if err != nil {
		respondOrganizeError(c, "Failed to save the note", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailThreadNoteResponse(note))
}

// DeleteMailThreadNote removes one of the caller's own notes.
func (h *Handler) DeleteMailThreadNote(c *gin.Context) {
	viewer, mailboxID, threadID, ok := threadScope(c)
	if !ok {
		return
	}
	noteID, err := strconv.ParseUint(c.Param("note_id"), 10, 64)
	if err != nil || noteID == 0 {
		response.BadRequest(c, "Invalid note id")
		return
	}
	if err = h.service.DeleteMailThreadNote(c.Request.Context(), viewer, mailboxID, threadID, uint(noteID)); err != nil {
		response.HandleError(c, "Failed to delete the note", err)
		return
	}
	response.NoContent(c)
}
