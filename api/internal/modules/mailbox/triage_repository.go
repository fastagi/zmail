package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// The service takes the triage seam from its repository by type assertion; this keeps the real
// repository from silently losing it.
var _ domain.MailTriageRepository = (*repository)(nil)

// MailTriagePO is one inbound message's AI triage row.
type MailTriagePO struct {
	MessageID     uint    `gorm:"primaryKey;autoIncrement:false"`
	MailboxID     uint    `gorm:"not null"`
	Category      *string `gorm:"size:16"`
	Reason        string  `gorm:"size:120;not null;default:''"`
	DueAt         *time.Time
	Source        string  `gorm:"size:16;not null;default:'ai'"`
	Status        string  `gorm:"size:16;not null;default:'pending'"`
	Attempts      int     `gorm:"not null;default:0"`
	LastErrorCode *string `gorm:"size:64"`
	Model         string  `gorm:"size:64;not null;default:''"`
	DecidedBy     *uint
	NextAttemptAt time.Time `gorm:"not null"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (MailTriagePO) TableName() string { return "mail_triage" }

func (po *MailTriagePO) toDomain() *domain.MailTriage {
	triage := &domain.MailTriage{
		MessageID: po.MessageID, MailboxID: po.MailboxID, Reason: po.Reason, DueAt: po.DueAt,
		Source: domain.MailTriageSource(po.Source), Status: domain.MailTriageStatus(po.Status),
		Attempts: po.Attempts, Model: po.Model, DecidedBy: po.DecidedBy,
		NextAttemptAt: po.NextAttemptAt, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
	if po.Category != nil {
		triage.Category = domain.MailTriageCategory(*po.Category)
	}
	if po.LastErrorCode != nil {
		triage.LastErrorCode = *po.LastErrorCode
	}
	return triage
}

// MailTriageSettingsPO is an organization's AI triage settings and its model calls of one day.
type MailTriageSettingsPO struct {
	OrganizationID  uint       `gorm:"primaryKey;autoIncrement:false"`
	Enabled         bool       `gorm:"not null;default:false"`
	DailyMessageCap int        `gorm:"not null;default:500"`
	DraftEnabled    bool       `gorm:"not null;default:false"`
	UsageDay        *time.Time `gorm:"type:date"`
	UsageCount      int        `gorm:"not null;default:0"`
	UpdatedBy       *uint
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (MailTriageSettingsPO) TableName() string { return "mail_triage_settings" }

// FindTriageSettings reads the organization's settings; a missing row is the defaults, unsaved.
func (r *repository) FindTriageSettings(ctx context.Context, organizationID uint) (domain.MailTriageSettings, error) {
	settings := domain.MailTriageSettings{OrganizationID: organizationID, DailyMessageCap: domain.DefaultMailTriageDailyMessageCap}
	if r.db == nil {
		return settings, domain.ErrServiceUnavailable
	}
	var po MailTriageSettingsPO
	err := r.conn(ctx).Where("organization_id = ?", organizationID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	return triageSettingsOf(po), nil
}

func triageSettingsOf(po MailTriageSettingsPO) domain.MailTriageSettings {
	return domain.MailTriageSettings{
		OrganizationID: po.OrganizationID, Enabled: po.Enabled, DailyMessageCap: po.DailyMessageCap,
		DraftEnabled: po.DraftEnabled, UpdatedBy: po.UpdatedBy, Saved: true, UpdatedAt: po.UpdatedAt,
	}
}

// SaveTriageSettings upserts the three switches; the day's usage is kept.
func (r *repository) SaveTriageSettings(ctx context.Context, settings domain.MailTriageSettings, now time.Time) (domain.MailTriageSettings, error) {
	if r.db == nil {
		return settings, domain.ErrServiceUnavailable
	}
	now = now.UTC()
	po := MailTriageSettingsPO{
		OrganizationID: settings.OrganizationID, Enabled: settings.Enabled, DailyMessageCap: settings.DailyMessageCap,
		DraftEnabled: settings.DraftEnabled, UpdatedBy: settings.UpdatedBy, CreatedAt: now, UpdatedAt: now,
	}
	err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "daily_message_cap", "draft_enabled", "updated_by", "updated_at"}),
	}).Create(&po).Error
	if err != nil {
		return settings, err
	}
	return r.FindTriageSettings(ctx, settings.OrganizationID)
}

// SetMailboxTriage changes a live mailbox's switch.
func (r *repository) SetMailboxTriage(ctx context.Context, mailboxID uint, enabled bool) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailboxPO{}).Where("id = ?", mailboxID).
		Updates(map[string]any{"ai_triage_enabled": enabled, "updated_at": time.Now().UTC()}).Error
}

// EnqueueTriage inserts a pending row when the mailbox is live with its switch on, the
// organization's switch is on, and a personal mailbox's owner is still a member. It reads both
// switches in the same statement, so the cost on the inbound path is one insert.
func (r *repository) EnqueueTriage(ctx context.Context, messageID, mailboxID uint, now time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	now = now.UTC()
	result := r.conn(ctx).Exec(`INSERT INTO mail_triage (message_id, mailbox_id, status, source, next_attempt_at, created_at, updated_at)
		SELECT @message, box.id, 'pending', 'ai', @now, @now, @now
		FROM mailboxes AS box
		JOIN mail_triage_settings AS settings ON settings.organization_id = box.organization_id
		WHERE box.id = @mailbox AND box.deleted_at IS NULL AND box.ai_triage_enabled AND settings.enabled
		  AND (box.type = 'shared' OR box.owner_left_at IS NULL)
		ON CONFLICT (message_id) DO NOTHING`, map[string]any{"message": messageID, "mailbox": mailboxID, "now": now})
	return result.RowsAffected == 1, result.Error
}

// triageClaimRow is one claimed row with its mailbox's and organization's switches.
type triageClaimRow struct {
	MailTriagePO
	OrganizationID   uint
	OrganizationOn   bool
	OrganizationCap  int
	MailboxOn        bool
	MailboxPersonal  bool
	MailboxOwnerLeft bool
	MailboxRoleName  string
}

// ClaimTriage locks up to limit due pending rows, skipping rows another worker holds, and moves
// their next attempt lease past now, so the caller may commit and call the model outside the
// transaction: a worker that dies leaves the rows to the next worker once the lease ends.
func (r *repository) ClaimTriage(ctx context.Context, now time.Time, limit int) ([]domain.MailTriageClaim, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if limit <= 0 {
		return []domain.MailTriageClaim{}, nil
	}
	now = now.UTC()
	var rows []triageClaimRow
	err := r.InTransaction(ctx, func(tx context.Context) error {
		db := r.conn(tx)
		if err := db.Raw(`SELECT mail_triage.*, box.organization_id,
			  COALESCE(settings.enabled, false) AS organization_on,
			  COALESCE(settings.daily_message_cap, 0) AS organization_cap,
			  (box.ai_triage_enabled AND box.deleted_at IS NULL) AS mailbox_on,
			  box.type = 'personal' AS mailbox_personal,
			  box.owner_left_at IS NOT NULL AS mailbox_owner_left,
			  COALESCE(role.name, '') AS mailbox_role_name
			FROM mail_triage
			JOIN mailboxes AS box ON box.id = mail_triage.mailbox_id
			LEFT JOIN mail_triage_settings AS settings ON settings.organization_id = box.organization_id
			LEFT JOIN mail_roles AS role ON role.id = box.role_id
			WHERE mail_triage.status = 'pending' AND mail_triage.next_attempt_at <= @now
			ORDER BY mail_triage.next_attempt_at, mail_triage.message_id
			LIMIT @limit
			FOR UPDATE OF mail_triage SKIP LOCKED`, map[string]any{"now": now, "limit": limit}).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uint, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.MessageID)
		}
		return db.Model(&MailTriagePO{}).Where("message_id IN ?", ids).
			Updates(map[string]any{"next_attempt_at": now.Add(triageClaimLease), "updated_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	claims := make([]domain.MailTriageClaim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, domain.MailTriageClaim{
			Triage: *row.toDomain(), OrganizationID: row.OrganizationID, OrganizationOn: row.OrganizationOn,
			OrganizationCap: row.OrganizationCap, MailboxOn: row.MailboxOn, MailboxPersonal: row.MailboxPersonal,
			MailboxOwnerLeft: row.MailboxOwnerLeft, MailboxRoleName: row.MailboxRoleName,
		})
	}
	return claims, nil
}

// ReserveTriageCall counts one model call on the organization's day when the switch is on and the
// day has room under limit; a new UTC day starts the count again.
func (r *repository) ReserveTriageCall(ctx context.Context, organizationID uint, limit int, now time.Time) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	day := now.UTC().Format(time.DateOnly)
	result := r.conn(ctx).Exec(`UPDATE mail_triage_settings
		SET usage_count = CASE WHEN usage_day = CAST(@day AS date) THEN usage_count + 1 ELSE 1 END,
		    usage_day = CAST(@day AS date)
		WHERE organization_id = @organization AND enabled
		  AND (usage_day IS DISTINCT FROM CAST(@day AS date) OR usage_count < @limit)`,
		map[string]any{"day": day, "organization": organizationID, "limit": limit})
	return result.RowsAffected == 1, result.Error
}

// SaveTriage writes the worker's outcome, unless a person decided the message meanwhile.
func (r *repository) SaveTriage(ctx context.Context, triage domain.MailTriage) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	var category, errorCode *string
	if triage.Category != "" {
		category = new(string(triage.Category))
	}
	if triage.LastErrorCode != "" {
		errorCode = new(triage.LastErrorCode)
	}
	return r.conn(ctx).Model(&MailTriagePO{}).
		Where("message_id = ? AND source = ? AND status = ?", triage.MessageID, string(domain.MailTriageSourceAI), string(domain.MailTriagePending)).
		Updates(map[string]any{
			"category": category, "reason": triage.Reason, "due_at": triage.DueAt, "status": string(triage.Status),
			"attempts": triage.Attempts, "last_error_code": errorCode, "model": triage.Model,
			"next_attempt_at": triage.NextAttemptAt.UTC(), "updated_at": time.Now().UTC(),
		}).Error
}

// OverrideTriage upserts a person's decision as a done manual result.
func (r *repository) OverrideTriage(ctx context.Context, override domain.MailTriageOverride) (*domain.MailTriage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	at := override.At.UTC()
	po := MailTriagePO{
		MessageID: override.MessageID, MailboxID: override.MailboxID, Category: new(string(override.Category)),
		DueAt: override.DueAt, Source: string(domain.MailTriageSourceManual), Status: string(domain.MailTriageDone),
		DecidedBy: new(override.DecidedBy), NextAttemptAt: at, CreatedAt: at, UpdatedAt: at,
	}
	err := r.conn(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"category", "reason", "due_at", "source", "status", "decided_by", "last_error_code", "updated_at",
		}),
	}).Create(&po).Error
	if err != nil {
		return nil, err
	}
	return r.FindTriage(ctx, override.MessageID)
}

// FindTriage reads a message's row; nil without one.
func (r *repository) FindTriage(ctx context.Context, messageID uint) (*domain.MailTriage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailTriagePO
	err := r.conn(ctx).Where("message_id = ?", messageID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil //nolint:nilnil // no row is a normal answer
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

// ListTriage returns the visible rows of the given messages.
func (r *repository) ListTriage(ctx context.Context, messageIDs []uint) (map[uint]*domain.MailTriage, error) {
	results := make(map[uint]*domain.MailTriage, len(messageIDs))
	if len(messageIDs) == 0 {
		return results, nil
	}
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailTriagePO
	if err := r.conn(ctx).Where("message_id IN ? AND status IN ?", messageIDs, visibleTriageStatuses()).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		results[rows[i].MessageID] = rows[i].toDomain()
	}
	return results, nil
}

func visibleTriageStatuses() []string {
	return []string{string(domain.MailTriageDone), string(domain.MailTriageFailed)}
}

// RecentTriageExamples returns the subjects and categories of the mailbox's newest overrides.
func (r *repository) RecentTriageExamples(ctx context.Context, mailboxID uint, limit int) ([]domain.MailTriageExample, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []struct {
		Subject  string
		Category string
	}
	err := r.conn(ctx).Raw(`SELECT mail_messages.subject, mail_triage.category
		FROM mail_triage JOIN mail_messages ON mail_messages.id = mail_triage.message_id
		WHERE mail_triage.mailbox_id = ? AND mail_triage.source = 'manual' AND mail_triage.category IS NOT NULL
		  AND `+unsealedMessage+`
		ORDER BY mail_triage.updated_at DESC, mail_triage.message_id DESC
		LIMIT ?`, mailboxID, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	examples := make([]domain.MailTriageExample, 0, len(rows))
	for _, row := range rows {
		examples = append(examples, domain.MailTriageExample{Subject: row.Subject, Category: domain.MailTriageCategory(row.Category)})
	}
	return examples, nil
}

// CountTriage counts the membership's unread, unsealed Inbox messages of the mailboxes per
// visible category.
func (r *repository) CountTriage(ctx context.Context, membershipID uint, mailboxIDs []uint) (domain.MailTriageCounts, error) {
	var counts domain.MailTriageCounts
	if len(mailboxIDs) == 0 {
		return counts, nil
	}
	if r.db == nil {
		return counts, domain.ErrServiceUnavailable
	}
	var rows []struct {
		Category string
		Total    int
	}
	err := r.conn(ctx).Raw(`SELECT mail_triage.category, COUNT(*) AS total
		FROM mail_triage JOIN mail_messages ON mail_messages.id = mail_triage.message_id
		WHERE mail_triage.mailbox_id IN @mailbox_ids AND mail_triage.status IN @visible
		  AND mail_messages.placement = @inbox AND `+unreadByMember+` AND `+unsealedMessage+`
		GROUP BY mail_triage.category`, map[string]any{
		"mailbox_ids": mailboxIDs, "visible": visibleTriageStatuses(), "m": membershipID,
		"inbox": string(domain.MailPlacementDefault),
	}).Scan(&rows).Error
	if err != nil {
		return counts, err
	}
	for _, row := range rows {
		switch domain.MailTriageCategory(row.Category) {
		case domain.MailTriageReply:
			counts.Reply = row.Total
		case domain.MailTriageDeadline:
			counts.Deadline = row.Total
		case domain.MailTriageFYI:
			counts.FYI = row.Total
		case domain.MailTriageReview:
			counts.Review = row.Total
		}
	}
	return counts, nil
}
