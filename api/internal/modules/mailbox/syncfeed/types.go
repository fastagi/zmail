package syncfeed

import "encoding/json"

// ChangeKind identifies a delta sync event variant matching @zmail/mail-core.
type ChangeKind string

const (
	ChangeKindUpsertMessage ChangeKind = "message.upserted"
	ChangeKindUpdateFlags   ChangeKind = "message.flags_updated"
	ChangeKindMoveMessage   ChangeKind = "message.moved"
	ChangeKindDeleteMessage ChangeKind = "message.deleted"
	ChangeKindThreadUpdated ChangeKind = "thread.updated"
)

// Valid reports whether k is one of the recognized delta change kinds.
func (k ChangeKind) Valid() bool {
	switch k {
	case ChangeKindUpsertMessage,
		ChangeKindUpdateFlags,
		ChangeKindMoveMessage,
		ChangeKindDeleteMessage,
		ChangeKindThreadUpdated:
		return true
	default:
		return false
	}
}

// Placement matches MailPlacement in @zmail/mail-core.
type Placement string

const (
	PlacementDefault Placement = "default"
	PlacementArchive Placement = "archive"
	PlacementFolder  Placement = "folder"
	PlacementTrash   Placement = "trash"
)

// TriageSummary matches MailTriage in @zmail/mail-core.
type TriageSummary struct {
	Category string  `json:"category"`
	Reason   string  `json:"reason"`
	DueAt    *string `json:"due_at"`
	Source   string  `json:"source"`
}

// ThreadSummary matches MailThreadSummary in @zmail/mail-core.
type ThreadSummary struct {
	MessageCount int    `json:"message_count"`
	MessageIDs   []uint `json:"message_ids"`
	Unread       bool   `json:"unread"`
	Starred      bool   `json:"starred"`
	LabelIDs     []uint `json:"label_ids"`
}

func normalizeThreadSummary(t *ThreadSummary) *ThreadSummary {
	if t == nil {
		return nil
	}
	cp := *t
	if cp.MessageIDs == nil {
		cp.MessageIDs = []uint{}
	}
	if cp.LabelIDs == nil {
		cp.LabelIDs = []uint{}
	}
	return &cp
}

// MessageSummary matches MailMessageSummary in @zmail/mail-core.
type MessageSummary struct {
	ID              uint           `json:"id"`
	MailboxID       uint           `json:"mailbox_id"`
	ThreadID        uint           `json:"thread_id"`
	Direction       string         `json:"direction"`
	Status          string         `json:"status"`
	From            string         `json:"from"`
	To              []string       `json:"to"`
	Cc              []string       `json:"cc"`
	Subject         string         `json:"subject"`
	Read            bool           `json:"read"`
	Starred         bool           `json:"starred"`
	Placement       Placement      `json:"placement"`
	FolderID        *uint          `json:"folder_id"`
	LabelIDs        []uint         `json:"label_ids"`
	AttachmentCount int            `json:"attachment_count"`
	TrashedAt       *string        `json:"trashed_at"`
	CreatedAt       string         `json:"created_at"`
	Thread          *ThreadSummary `json:"thread,omitempty"`
	Triage          *TriageSummary `json:"triage,omitempty"`
}

func normalizeMessageSummary(m *MessageSummary) *MessageSummary {
	if m == nil {
		return nil
	}
	cp := *m
	if cp.To == nil {
		cp.To = []string{}
	}
	if cp.Cc == nil {
		cp.Cc = []string{}
	}
	if cp.LabelIDs == nil {
		cp.LabelIDs = []uint{}
	}
	if cp.Placement == "" {
		cp.Placement = PlacementDefault
	}
	cp.Thread = normalizeThreadSummary(cp.Thread)
	return &cp
}

// ChangeEvent represents a single delta change event matching DeltaChangeEvent
// in @zmail/mail-core. Routing fields (OrganizationID, UserID, Seq) are omitted
// from JSON serialization.
type ChangeEvent struct {
	OrganizationID uint   `json:"-"`
	UserID         uint   `json:"-"`
	Seq            uint64 `json:"-"`

	Type       ChangeKind `json:"type"`
	EventID    string     `json:"event_id"`
	OccurredAt string     `json:"occurred_at"`

	Message   *MessageSummary `json:"message,omitempty"`
	MessageID uint            `json:"message_id,omitempty"`
	Read      *bool           `json:"read,omitempty"`
	Starred   *bool           `json:"starred,omitempty"`
	LabelIDs  []uint          `json:"label_ids,omitempty"`
	Placement Placement       `json:"placement,omitempty"`
	FolderID  *uint           `json:"folder_id,omitempty"`
	TrashedAt *string         `json:"trashed_at,omitempty"`
	ThreadID  uint            `json:"thread_id,omitempty"`
	Thread    *ThreadSummary  `json:"thread,omitempty"`
}

// MarshalJSON emits the exact discriminated-union wire shape expected by
// @zmail/mail-core for each ChangeKind.
func (e ChangeEvent) MarshalJSON() ([]byte, error) {
	switch e.Type {
	case ChangeKindUpsertMessage:
		return json.Marshal(struct {
			Type       ChangeKind      `json:"type"`
			EventID    string          `json:"event_id"`
			OccurredAt string          `json:"occurred_at"`
			Message    *MessageSummary `json:"message"`
		}{
			Type:       e.Type,
			EventID:    e.EventID,
			OccurredAt: e.OccurredAt,
			Message:    normalizeMessageSummary(e.Message),
		})
	case ChangeKindDeleteMessage:
		return json.Marshal(struct {
			Type       ChangeKind `json:"type"`
			EventID    string     `json:"event_id"`
			OccurredAt string     `json:"occurred_at"`
			MessageID  uint       `json:"message_id"`
		}{
			Type:       e.Type,
			EventID:    e.EventID,
			OccurredAt: e.OccurredAt,
			MessageID:  e.MessageID,
		})
	case ChangeKindUpdateFlags:
		return json.Marshal(struct {
			Type       ChangeKind `json:"type"`
			EventID    string     `json:"event_id"`
			OccurredAt string     `json:"occurred_at"`
			MessageID  uint       `json:"message_id"`
			Read       *bool      `json:"read,omitempty"`
			Starred    *bool      `json:"starred,omitempty"`
			LabelIDs   []uint     `json:"label_ids,omitempty"`
		}{
			Type:       e.Type,
			EventID:    e.EventID,
			OccurredAt: e.OccurredAt,
			MessageID:  e.MessageID,
			Read:       e.Read,
			Starred:    e.Starred,
			LabelIDs:   e.LabelIDs,
		})
	case ChangeKindMoveMessage:
		return json.Marshal(struct {
			Type       ChangeKind `json:"type"`
			EventID    string     `json:"event_id"`
			OccurredAt string     `json:"occurred_at"`
			MessageID  uint       `json:"message_id"`
			Placement  Placement  `json:"placement"`
			FolderID   *uint      `json:"folder_id"`
			TrashedAt  *string    `json:"trashed_at"`
		}{
			Type:       e.Type,
			EventID:    e.EventID,
			OccurredAt: e.OccurredAt,
			MessageID:  e.MessageID,
			Placement:  e.Placement,
			FolderID:   e.FolderID,
			TrashedAt:  e.TrashedAt,
		})
	case ChangeKindThreadUpdated:
		return json.Marshal(struct {
			Type       ChangeKind     `json:"type"`
			EventID    string         `json:"event_id"`
			OccurredAt string         `json:"occurred_at"`
			ThreadID   uint           `json:"thread_id"`
			Thread     *ThreadSummary `json:"thread"`
		}{
			Type:       e.Type,
			EventID:    e.EventID,
			OccurredAt: e.OccurredAt,
			ThreadID:   e.ThreadID,
			Thread:     normalizeThreadSummary(e.Thread),
		})
	default:
		type plain ChangeEvent
		return json.Marshal(plain(e))
	}
}

// SyncPage matches DeltaSyncPage in @zmail/mail-core.
type SyncPage struct {
	Changes       []ChangeEvent `json:"events"`
	NextCursor    *string       `json:"next_cursor"`
	HasMore       bool          `json:"has_more"`
	ResetRequired bool          `json:"reset_required,omitempty"`
	SyncedAt      string        `json:"synced_at"`
}

// MarshalJSON ensures empty Changes slices serialize as [] rather than null.
func (p SyncPage) MarshalJSON() ([]byte, error) {
	type alias SyncPage
	out := alias(p)
	if out.Changes == nil {
		out.Changes = []ChangeEvent{}
	}
	return json.Marshal(out)
}
