package mailbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// txBase is the fixed instant the transactional tests measure from.
var txBase = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// openTransactionalRepository migrates a disposable schema with the real migrations, so the
// check constraints and partial indexes are the production ones, and adds organizations 10 and 11.
func openTransactionalRepository(t *testing.T) (*repository, *gorm.DB) {
	t.Helper()
	db := openMigratedPostgres(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'tx', 'tx@example.com', 'x', now(), now())`,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (11, 'Other', 'other', 901, now(), now())`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return NewRepository(db), db
}

var txSequence atomic.Int64

// newTransactional is a queued live message of organization 10, due at its creation time.
func newTransactional(createdAt time.Time) *domain.TransactionalMessage {
	n := txSequence.Add(1)
	next := createdAt
	return &domain.TransactionalMessage{
		OrganizationID:         10,
		Mode:                   domain.APIKeyModeLive,
		CreatedBy:              901,
		TemplateKey:            "signup-code",
		FromAddress:            "no-reply@acme.test",
		FromName:               "Acme",
		ToAddress:              "lilei@example.com",
		SubjectMasked:          "Acme verification code: ••••••",
		VariablesMasked:        map[string]string{"code": "••••••"},
		PayloadCiphertext:      []byte{0x01, 0x02},
		IdempotencyFingerprint: make([]byte, 32),
		Status:                 domain.TransactionalStatusQueued,
		NextAttemptAt:          &next,
		RFCMessageID:           fmt.Sprintf("tx%d@acme.test", n),
		CreatedAt:              createdAt,
		UpdatedAt:              createdAt,
	}
}

func createTransactional(t *testing.T, repo *repository, message *domain.TransactionalMessage) *domain.TransactionalMessage {
	t.Helper()
	created, err := repo.CreateTransactional(context.Background(), message)
	if err != nil || !created {
		t.Fatalf("CreateTransactional = %v, %v", created, err)
	}
	return message
}

func findTransactional(t *testing.T, repo *repository, id uint) *domain.TransactionalMessage {
	t.Helper()
	message, err := repo.FindTransactional(context.Background(), 10, id)
	if err != nil {
		t.Fatalf("FindTransactional(%d): %v", id, err)
	}
	return message
}

func keyOf(value string) *string { return &value }

func TestIdempotencyUniqueUnderConcurrency(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]bool, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message := newTransactional(txBase)
			message.IdempotencyKey = keyOf("order-1")
			results[i], errs[i] = repo.CreateTransactional(ctx, message)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("CreateTransactional: %v", err)
		}
	}
	if results[0] == results[1] {
		t.Fatalf("results = %v, want exactly one winner", results)
	}
	var rows int64
	if err := db.Raw(`SELECT count(*) FROM transactional_messages`).Scan(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}

	// The same key in the other mode is an independent message.
	test := newTransactional(txBase)
	test.Mode, test.IdempotencyKey = domain.APIKeyModeTest, keyOf("order-1")
	if created, err := repo.CreateTransactional(ctx, test); err != nil || !created {
		t.Fatalf("test-mode CreateTransactional = %v, %v", created, err)
	}
	live, err := repo.FindTransactionalByIdempotencyKey(ctx, 10, domain.APIKeyModeLive, "order-1")
	if err != nil || live.ID == test.ID {
		t.Fatalf("live lookup = %+v, %v", live, err)
	}
	if _, err := repo.FindTransactionalByIdempotencyKey(ctx, 11, domain.APIKeyModeLive, "order-1"); !errors.Is(err, ErrTransactionalMessageNotFound) {
		t.Fatalf("other organization lookup error = %v", err)
	}
}

func TestReleasedKeyCanBeReused(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	first := newTransactional(txBase)
	first.IdempotencyKey = keyOf("reset-1")
	createTransactional(t, repo, first)

	if err := repo.ReleaseIdempotencyKey(ctx, first.ID); err != nil {
		t.Fatalf("ReleaseIdempotencyKey: %v", err)
	}
	if got := findTransactional(t, repo, first.ID); got.IdempotencyKey != nil {
		t.Fatalf("released key = %v", *got.IdempotencyKey)
	}
	second := newTransactional(txBase.Add(25 * time.Hour))
	second.IdempotencyKey = keyOf("reset-1")
	createTransactional(t, repo, second)
	found, err := repo.FindTransactionalByIdempotencyKey(ctx, 10, domain.APIKeyModeLive, "reset-1")
	if err != nil || found.ID != second.ID {
		t.Fatalf("lookup = %+v, %v; want message %d", found, err, second.ID)
	}
}

func TestTransactionalRoundTripsEveryField(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	keyID := uint(7)
	message := newTransactional(txBase)
	message.APIKeyID = &keyID
	message.IdempotencyKey = keyOf("k")
	message.IdempotencyFingerprint = []byte("0123456789abcdef0123456789abcdef")
	createTransactional(t, repo, message)

	got := findTransactional(t, repo, message.ID)
	if got.Mode != domain.APIKeyModeLive || got.APIKeyID == nil || *got.APIKeyID != 7 || got.TemplateID != nil ||
		got.VariablesMasked["code"] != "••••••" || string(got.IdempotencyFingerprint) != string(message.IdempotencyFingerprint) ||
		string(got.PayloadCiphertext) != "\x01\x02" || got.Status != domain.TransactionalStatusQueued ||
		got.QueuedReason != nil || got.LastEvent != "" || !got.NextAttemptAt.Equal(txBase) || !got.CreatedAt.Equal(txBase) {
		t.Fatalf("round trip = %+v", got)
	}
	if _, err := repo.FindTransactional(ctx, 11, message.ID); !errors.Is(err, ErrTransactionalMessageNotFound) {
		t.Fatalf("other organization error = %v", err)
	}
}

// leaseAll leases organization 10's due messages with a 2-minute lease from now.
func leaseAll(t *testing.T, repo *repository, now time.Time, laneLimit int) []*domain.TransactionalMessage {
	t.Helper()
	leased, err := repo.LeaseTransactional(context.Background(), 10, nil, now, now.Add(domain.TransactionalLease),
		now.Add(-24*time.Hour), laneLimit, 50)
	if err != nil {
		t.Fatalf("LeaseTransactional: %v", err)
	}
	return leased
}

func TestLeaseRespectsLaneRoomAndSkipsLocked(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	now := txBase.Add(time.Hour)

	// One attempt already in the window: a sent live message.
	attempted := newTransactional(txBase)
	attempted.Status, attempted.PayloadCiphertext, attempted.NextAttemptAt = domain.TransactionalStatusSent, nil, nil
	attemptedAt := txBase.Add(10 * time.Minute)
	attempted.SendAttemptedAt = &attemptedAt
	createTransactional(t, repo, attempted)
	// An attempt from before the window does not count.
	old := newTransactional(txBase.Add(-48 * time.Hour))
	old.Status, old.PayloadCiphertext, old.NextAttemptAt = domain.TransactionalStatusSent, nil, nil
	oldAt := txBase.Add(-48 * time.Hour)
	old.SendAttemptedAt = &oldAt
	createTransactional(t, repo, old)

	var due []*domain.TransactionalMessage
	for i := range 3 {
		due = append(due, createTransactional(t, repo, newTransactional(txBase.Add(time.Duration(i)*time.Minute))))
	}
	// A test-mode and a simulated message lease whatever the lane says.
	test := newTransactional(txBase.Add(5 * time.Minute))
	test.Mode = domain.APIKeyModeTest
	createTransactional(t, repo, test)
	simulated := newTransactional(txBase.Add(6 * time.Minute))
	simulated.Simulated = true
	createTransactional(t, repo, simulated)
	// Not yet due.
	createTransactional(t, repo, newTransactional(now.Add(time.Minute)))

	leased := leaseAll(t, repo, now, 2)
	var ids []uint
	for _, message := range leased {
		ids = append(ids, message.ID)
		if message.LeaseUntil == nil || !message.LeaseUntil.Equal(now.Add(domain.TransactionalLease)) {
			t.Fatalf("lease of %d = %v", message.ID, message.LeaseUntil)
		}
	}
	slices.Sort(ids)
	if want := []uint{due[0].ID, test.ID, simulated.ID}; !slices.Equal(ids, want) {
		t.Fatalf("leased = %v, want %v", ids, want)
	}
	for _, message := range due[1:] {
		got := findTransactional(t, repo, message.ID)
		if got.QueuedReason == nil || *got.QueuedReason != domain.TransactionalQueuedSendLimit || got.LeaseUntil != nil {
			t.Fatalf("message %d = reason %v lease %v, want waiting for send_limit", message.ID, got.QueuedReason, got.LeaseUntil)
		}
	}
	if waiting, err := repo.CountTransactionalWaiting(ctx, 10); err != nil || waiting != 2 {
		t.Fatalf("CountTransactionalWaiting = %d, %v", waiting, err)
	}
	if attempts, err := repo.CountTransactionalAttemptsSince(ctx, 10, now.Add(-24*time.Hour)); err != nil || attempts != 1 {
		t.Fatalf("CountTransactionalAttemptsSince = %d, %v", attempts, err)
	}
	// The held lease takes the last room: nothing more leases until it is used or lost.
	if again := leaseAll(t, repo, now, 2); len(again) != 0 {
		t.Fatalf("second lease = %d messages, want 0", len(again))
	}
	// Once the holder starts its attempt, it is counted once (as an attempt, not as a lease too).
	if ok, err := repo.StartTransactionalAttempt(ctx, due[0].ID, now.Add(domain.TransactionalLease), now); err != nil || !ok {
		t.Fatalf("StartTransactionalAttempt = %v, %v", ok, err)
	}
	// The waiting messages were pushed back by TransactionalLaneWait.
	if again := leaseAll(t, repo, now.Add(domain.TransactionalLaneWait), 3); len(again) != 1 {
		t.Fatalf("lease with one more room = %d messages, want 1", len(again))
	}

	// A row locked by another transaction is skipped, not waited for.
	locked := createTransactional(t, repo, newTransactional(txBase.Add(20*time.Minute)))
	free := createTransactional(t, repo, newTransactional(txBase.Add(21*time.Minute)))
	tx := db.Begin()
	if err := tx.Exec(`SELECT id FROM transactional_messages WHERE id = ? FOR UPDATE`, locked.ID).Error; err != nil {
		t.Fatalf("lock row: %v", err)
	}
	skipped, err := repo.LeaseTransactional(ctx, 10, []uint{locked.ID, free.ID}, now, now.Add(domain.TransactionalLease),
		now.Add(-24*time.Hour), 100, 50)
	tx.Rollback()
	if err != nil || len(skipped) != 1 || skipped[0].ID != free.ID {
		t.Fatalf("lease with a locked row = %v, %v; want only %d", skipped, err, free.ID)
	}
}

func TestLeaseConcurrentLeasersNeverExceedLaneRoom(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	now := txBase.Add(time.Hour)
	for i := range 6 {
		createTransactional(t, repo, newTransactional(txBase.Add(time.Duration(i)*time.Minute)))
	}
	var wg sync.WaitGroup
	var total atomic.Int64
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leased, err := repo.LeaseTransactional(context.Background(), 10, nil, now, now.Add(domain.TransactionalLease),
				now.Add(-24*time.Hour), 2, 1)
			if err != nil {
				t.Errorf("LeaseTransactional: %v", err)
			}
			total.Add(int64(len(leased)))
		}()
	}
	wg.Wait()
	if got := total.Load(); got != 2 {
		t.Fatalf("leased in total = %d, want 2 (the lane limit)", got)
	}
}

func TestTransactionalDueOrganizations(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	now := txBase.Add(time.Hour)
	createTransactional(t, repo, newTransactional(txBase))
	other := newTransactional(txBase)
	other.OrganizationID = 11
	createTransactional(t, repo, other)
	later := newTransactional(now.Add(time.Minute))
	later.OrganizationID = 11
	createTransactional(t, repo, later)
	organizations, err := repo.TransactionalDueOrganizations(context.Background(), now, 10)
	if err != nil || !slices.Equal(organizations, []uint{10, 11}) {
		t.Fatalf("TransactionalDueOrganizations = %v, %v", organizations, err)
	}
	leaseAll(t, repo, now, 10)
	organizations, err = repo.TransactionalDueOrganizations(context.Background(), now, 10)
	if err != nil || !slices.Equal(organizations, []uint{11}) {
		t.Fatalf("after leasing organization 10 = %v, %v", organizations, err)
	}
}

// Organizations whose lane is full must not hold every slot of a dispatch pass: their waiting
// messages are pushed back by TransactionalLaneWait, and due organizations are ordered by their
// earliest due message, so a higher-id organization's retry still leases.
func TestFullLanesDoNotStarveOtherOrganizations(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	if err := db.Exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (12, 'Third', 'third', 901, now(), now())`).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := txBase.Add(10 * time.Minute)
	for _, organizationID := range []uint{10, 11} {
		for i := range 3 {
			message := newTransactional(txBase.Add(time.Duration(i) * time.Second))
			message.OrganizationID = organizationID
			createTransactional(t, repo, message)
		}
	}
	retry := newTransactional(txBase.Add(time.Minute))
	retry.OrganizationID = 12
	reason := domain.TransactionalQueuedRetry
	retry.QueuedReason = &reason
	retryAt := now.Add(-time.Second)
	retry.NextAttemptAt = &retryAt
	retry.Attempts = 1
	createTransactional(t, repo, retry)

	// laneLimit gives organizations 10 and 11 no room at all.
	laneLimit := map[uint]int{10: 0, 11: 0, 12: 5}
	pass := func(at time.Time) []uint {
		organizations, err := repo.TransactionalDueOrganizations(ctx, at, 2)
		if err != nil {
			t.Fatalf("TransactionalDueOrganizations: %v", err)
		}
		for _, organizationID := range organizations {
			if _, err := repo.LeaseTransactional(ctx, organizationID, nil, at, at.Add(domain.TransactionalLease),
				at.Add(-24*time.Hour), laneLimit[organizationID], 2); err != nil {
				t.Fatalf("LeaseTransactional(%d): %v", organizationID, err)
			}
		}
		return organizations
	}
	// The first pass reaches the two oldest organizations, which only mark their messages waiting.
	if first := pass(now); !slices.Equal(first, []uint{10, 11}) {
		t.Fatalf("first pass organizations = %v; want [10 11]", first)
	}
	for _, organizationID := range []uint{10, 11} {
		var pushed int64
		if err := db.Raw(`SELECT count(*) FROM transactional_messages WHERE organization_id = ? AND queued_reason = 'send_limit' AND next_attempt_at = ?`,
			organizationID, now.Add(domain.TransactionalLaneWait)).Scan(&pushed).Error; err != nil {
			t.Fatalf("query: %v", err)
		}
		if pushed != 3 {
			t.Fatalf("organization %d: %d waiting messages pushed back; want all 3, beyond the batch too", organizationID, pushed)
		}
	}
	// The next pass reaches organization 12, whose retry leases.
	second := pass(now.Add(time.Second))
	if !slices.Contains(second, 12) || second[0] != 12 {
		t.Fatalf("second pass organizations = %v; want organization 12 first", second)
	}
	got := findTransactional12(t, repo, retry.ID)
	if got.LeaseUntil == nil {
		t.Fatalf("organization 12's retry was not leased")
	}
	// Waiting messages expire by created_at, unaffected by the push.
	expired, err := repo.ExpireTransactional(ctx, txBase.Add(domain.TransactionalQueueTimeout+time.Minute), 50)
	if err != nil || len(expired) != 6 {
		t.Fatalf("ExpireTransactional = %d, %v; want the 6 waiting messages", len(expired), err)
	}
}

func findTransactional12(t *testing.T, repo *repository, id uint) *domain.TransactionalMessage {
	t.Helper()
	message, err := repo.FindTransactional(context.Background(), 12, id)
	if err != nil {
		t.Fatalf("FindTransactional(%d): %v", id, err)
	}
	return message
}

// A provider call that started inside a lease that then ran out may have been accepted: the
// message is never leased again (D33), only failed by FailUnknownTransactional.
func TestLostLeaseAttemptIsNeverLeasedAgain(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	start := txBase.Add(time.Minute)
	message := createTransactional(t, repo, newTransactional(txBase))
	leased := leaseAll(t, repo, start, 10)
	if len(leased) != 1 {
		t.Fatalf("leased = %d", len(leased))
	}
	if ok, err := repo.StartTransactionalAttempt(ctx, message.ID, *leased[0].LeaseUntil, start); err != nil || !ok {
		t.Fatalf("start = %v, %v", ok, err)
	}
	later := start.Add(domain.TransactionalLease + time.Second)
	if again := leaseAll(t, repo, later, 10); len(again) != 0 {
		t.Fatalf("a lost-lease attempt was leased again: %+v", again[0])
	}
	if again, err := repo.LeaseTransactional(ctx, 10, []uint{message.ID}, later, later.Add(domain.TransactionalLease),
		later.Add(-24*time.Hour), 10, 1); err != nil || len(again) != 0 {
		t.Fatalf("leasing by id = %v, %v; want nothing", again, err)
	}
	if organizations, err := repo.TransactionalDueOrganizations(ctx, later, 10); err != nil || len(organizations) != 0 {
		t.Fatalf("due organizations = %v, %v; want none", organizations, err)
	}
	failed, err := repo.FailUnknownTransactional(ctx, later, 10)
	if err != nil || len(failed) != 1 || failed[0].FailureCode != domain.TransactionalFailureUnknownOutcome {
		t.Fatalf("FailUnknownTransactional = %v, %v", failed, err)
	}
}

// Live messages waiting for lane room never starve test and simulated messages, however many of
// them are older than the batch.
func TestLaneExemptMessagesLeaseDespiteWaitingLiveMessages(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	now := txBase.Add(time.Hour)
	for i := range 4 {
		createTransactional(t, repo, newTransactional(txBase.Add(time.Duration(i)*time.Second)))
	}
	test := newTransactional(txBase.Add(time.Minute))
	test.Mode = domain.APIKeyModeTest
	test.Simulated = true
	test = createTransactional(t, repo, test)
	simulated := newTransactional(txBase.Add(2 * time.Minute))
	simulated.Simulated = true
	simulated = createTransactional(t, repo, simulated)

	leased, err := repo.LeaseTransactional(context.Background(), 10, nil, now, now.Add(domain.TransactionalLease),
		now.Add(-24*time.Hour), 0, 2)
	if err != nil {
		t.Fatalf("LeaseTransactional: %v", err)
	}
	ids := make([]uint, 0, len(leased))
	for _, message := range leased {
		ids = append(ids, message.ID)
	}
	if !slices.Equal(ids, []uint{test.ID, simulated.ID}) {
		t.Fatalf("leased = %v; want the test and simulated messages %d, %d", ids, test.ID, simulated.ID)
	}
	waiting, err := repo.CountTransactionalWaiting(context.Background(), 10)
	if err != nil || waiting != 4 {
		t.Fatalf("waiting = %d, %v; want every live message marked, beyond the batch too", waiting, err)
	}
}

func TestStaleLeaseHolderCannotFinish(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	message := createTransactional(t, repo, newTransactional(txBase))
	first := txBase.Add(time.Minute)
	stale := leaseAll(t, repo, first, 10)[0].LeaseUntil
	// The first holder loses its lease; a second dispatcher takes it over.
	second := first.Add(3 * time.Minute)
	current := leaseAll(t, repo, second, 10)
	if len(current) != 1 {
		t.Fatalf("lease after expiry = %d messages", len(current))
	}
	outcome := domain.TransactionalOutcome{Status: domain.TransactionalStatusSent, ProviderMessageID: "prov-1", SentAt: &second}
	if ok, err := repo.FinishTransactional(ctx, message.ID, *stale, outcome); err != nil || ok {
		t.Fatalf("stale FinishTransactional = %v, %v", ok, err)
	}
	if ok, err := repo.StartTransactionalAttempt(ctx, message.ID, *stale, second); err != nil || ok {
		t.Fatalf("stale StartTransactionalAttempt = %v, %v", ok, err)
	}
	if ok, err := repo.ScheduleTransactionalRetry(ctx, message.ID, *stale, second.Add(time.Minute), "provider.unavailable"); err != nil || ok {
		t.Fatalf("stale ScheduleTransactionalRetry = %v, %v", ok, err)
	}

	lease := *current[0].LeaseUntil
	if ok, err := repo.StartTransactionalAttempt(ctx, message.ID, lease, second); err != nil || !ok {
		t.Fatalf("StartTransactionalAttempt = %v, %v", ok, err)
	}
	if ok, err := repo.FinishTransactional(ctx, message.ID, lease, outcome); err != nil || !ok {
		t.Fatalf("FinishTransactional = %v, %v", ok, err)
	}
	got := findTransactional(t, repo, message.ID)
	if got.Status != domain.TransactionalStatusSent || got.PayloadCiphertext != nil || got.LeaseUntil != nil ||
		got.NextAttemptAt != nil || got.Attempts != 1 || got.ProviderMessageID == nil || *got.ProviderMessageID != "prov-1" ||
		got.SentAt == nil || !got.SentAt.Equal(second) {
		t.Fatalf("finished = %+v", got)
	}
	byProvider, err := repo.FindTransactionalByProviderID(ctx, "prov-1")
	if err != nil || byProvider.ID != message.ID {
		t.Fatalf("FindTransactionalByProviderID = %+v, %v", byProvider, err)
	}
	// A finished message cannot be finished again.
	if ok, err := repo.FinishTransactional(ctx, message.ID, lease, outcome); err != nil || ok {
		t.Fatalf("second FinishTransactional = %v, %v", ok, err)
	}
}

func TestScheduleTransactionalRetryQueuesAgain(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	message := createTransactional(t, repo, newTransactional(txBase))
	now := txBase.Add(time.Minute)
	lease := *leaseAll(t, repo, now, 10)[0].LeaseUntil
	if ok, err := repo.StartTransactionalAttempt(ctx, message.ID, lease, now); err != nil || !ok {
		t.Fatalf("StartTransactionalAttempt = %v, %v", ok, err)
	}
	next := now.Add(30 * time.Second)
	if ok, err := repo.ScheduleTransactionalRetry(ctx, message.ID, lease, next, "provider.unavailable"); err != nil || !ok {
		t.Fatalf("ScheduleTransactionalRetry = %v, %v", ok, err)
	}
	got := findTransactional(t, repo, message.ID)
	if got.Status != domain.TransactionalStatusQueued || got.QueuedReason == nil || *got.QueuedReason != domain.TransactionalQueuedRetry ||
		got.LeaseUntil != nil || !got.NextAttemptAt.Equal(next) || got.FailureCode != "provider.unavailable" || got.PayloadCiphertext == nil {
		t.Fatalf("retry = %+v", got)
	}
	// Not due before next; due after.
	if leased := leaseAll(t, repo, next.Add(-time.Second), 10); len(leased) != 0 {
		t.Fatalf("leased before the retry time: %d", len(leased))
	}
	if leased := leaseAll(t, repo, next, 10); len(leased) != 1 {
		t.Fatalf("leased at the retry time: %d", len(leased))
	}
	// A retry is not waiting for lane room.
	if waiting, _ := repo.CountTransactionalWaiting(ctx, 10); waiting != 0 {
		t.Fatalf("waiting = %d", waiting)
	}
}

func TestFailUnknownOnlyAfterAttemptInsideLostLease(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	start := txBase.Add(time.Minute)
	// lost: attempted inside its lease, which then ran out.
	lost := createTransactional(t, repo, newTransactional(txBase))
	// notStarted: its lease ran out before any attempt.
	notStarted := createTransactional(t, repo, newTransactional(txBase))
	leases := map[uint]time.Time{}
	for _, message := range leaseAll(t, repo, start, 10) {
		leases[message.ID] = *message.LeaseUntil
	}
	if ok, err := repo.StartTransactionalAttempt(ctx, lost.ID, leases[lost.ID], start.Add(time.Second)); err != nil || !ok {
		t.Fatal("start lost attempt")
	}
	// running: attempted inside a lease that is still held.
	running := createTransactional(t, repo, newTransactional(txBase))
	later := start.Add(2 * time.Minute)
	runningLeased, err := repo.LeaseTransactional(ctx, 10, []uint{running.ID}, later, later.Add(domain.TransactionalLease),
		later.Add(-24*time.Hour), 10, 10)
	if err != nil || len(runningLeased) != 1 {
		t.Fatalf("lease running = %v, %v", runningLeased, err)
	}
	runningLease := *runningLeased[0].LeaseUntil
	if ok, startErr := repo.StartTransactionalAttempt(ctx, running.ID, runningLease, later); startErr != nil || !ok {
		t.Fatal("start running attempt")
	}
	// retrying: attempted earlier, now waiting for its retry with no lease.
	retrying := createTransactional(t, repo, newTransactional(txBase))
	mustExec(t, db, `UPDATE transactional_messages SET send_attempted_at = ?, queued_reason = 'retry', next_attempt_at = ? WHERE id = ?`,
		start, later.Add(time.Hour), retrying.ID)

	now := later.Add(time.Minute) // lost's and notStarted's leases are gone, running's is held
	failed, err := repo.FailUnknownTransactional(ctx, now, 10)
	if err != nil || len(failed) != 1 || failed[0].ID != lost.ID {
		t.Fatalf("FailUnknownTransactional = %v, %v; want only %d", failed, err, lost.ID)
	}
	got := findTransactional(t, repo, lost.ID)
	if got.Status != domain.TransactionalStatusFailed || got.FailureCode != domain.TransactionalFailureUnknownOutcome ||
		got.PayloadCiphertext != nil || got.LeaseUntil != nil {
		t.Fatalf("lost = %+v", got)
	}
	for _, id := range []uint{notStarted.ID, running.ID, retrying.ID} {
		if got := findTransactional(t, repo, id); got.Status != domain.TransactionalStatusQueued {
			t.Fatalf("message %d = %s, want queued", id, got.Status)
		}
	}
	if again, err := repo.FailUnknownTransactional(ctx, now, 10); err != nil || len(again) != 0 {
		t.Fatalf("second FailUnknownTransactional = %v, %v", again, err)
	}
}

func TestExpireAfterOneHour(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	now := txBase.Add(2 * time.Hour)
	expired := createTransactional(t, repo, newTransactional(now.Add(-61*time.Minute)))
	fresh := createTransactional(t, repo, newTransactional(now.Add(-59*time.Minute)))
	held := newTransactional(now.Add(-90 * time.Minute))
	lease := now.Add(time.Minute)
	held.LeaseUntil = &lease
	createTransactional(t, repo, held)
	waiting := newTransactional(now.Add(-2 * time.Hour))
	reason := domain.TransactionalQueuedSendLimit
	waiting.QueuedReason = &reason
	createTransactional(t, repo, waiting)

	got, err := repo.ExpireTransactional(ctx, now, 10)
	if err != nil {
		t.Fatalf("ExpireTransactional: %v", err)
	}
	var ids []uint
	for _, message := range got {
		ids = append(ids, message.ID)
		if message.Status != domain.TransactionalStatusFailed || message.FailureCode != domain.TransactionalFailureExpired ||
			message.PayloadCiphertext != nil || message.QueuedReason != nil || message.NextAttemptAt != nil {
			t.Fatalf("expired = %+v", message)
		}
	}
	slices.Sort(ids)
	if want := []uint{expired.ID, waiting.ID}; !slices.Equal(ids, want) {
		t.Fatalf("expired = %v, want %v", ids, want)
	}
	for _, id := range []uint{fresh.ID, held.ID} {
		if got := findTransactional(t, repo, id); got.Status != domain.TransactionalStatusQueued {
			t.Fatalf("message %d = %s", id, got.Status)
		}
	}
}

func TestPurgeDeletesEventsAfterThirtyDays(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	now := txBase.Add(40 * 24 * time.Hour)
	before := now.Add(-domain.TransactionalRetention)
	var old []*domain.TransactionalMessage
	for i := range 3 {
		message := newTransactional(txBase.Add(time.Duration(i) * time.Minute))
		message.Status, message.PayloadCiphertext, message.NextAttemptAt = domain.TransactionalStatusFailed, nil, nil
		old = append(old, createTransactional(t, repo, message))
		if _, _, err := repo.AddTransactionalEvents(ctx, message.ID, []domain.MailEvent{
			{Type: domain.MailEventAccepted, OccurredAt: txBase},
		}); err != nil {
			t.Fatalf("AddTransactionalEvents: %v", err)
		}
	}
	recent := createTransactional(t, repo, newTransactional(now.Add(-time.Hour)))
	if _, _, err := repo.AddTransactionalEvents(ctx, recent.ID, []domain.MailEvent{{Type: domain.MailEventAccepted, OccurredAt: now}}); err != nil {
		t.Fatalf("AddTransactionalEvents: %v", err)
	}

	for _, want := range []int{2, 1, 0} {
		deleted, err := repo.PurgeTransactional(ctx, before, 2)
		if err != nil || deleted != want {
			t.Fatalf("PurgeTransactional = %d, %v; want %d", deleted, err, want)
		}
	}
	var messages, events int64
	if err := db.Raw(`SELECT count(*) FROM transactional_messages`).Scan(&messages).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if err := db.Raw(`SELECT count(*) FROM transactional_message_events`).Scan(&events).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if messages != 1 || events != 1 {
		t.Fatalf("left = %d messages, %d events; want the recent one", messages, events)
	}
	if _, err := repo.FindTransactional(ctx, 10, old[0].ID); !errors.Is(err, ErrTransactionalMessageNotFound) {
		t.Fatalf("purged lookup error = %v", err)
	}
}

func TestTransactionalEventsDeduplicateAndSummarize(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()
	message := createTransactional(t, repo, newTransactional(txBase))
	added, summary, err := repo.AddTransactionalEvents(ctx, message.ID, []domain.MailEvent{
		{Type: domain.MailEventAccepted, OccurredAt: txBase},
	})
	if err != nil || added != 1 || summary != domain.MailEventAccepted {
		t.Fatalf("accepted = %d, %q, %v", added, summary, err)
	}
	// Out of order: the bounce happened after the delivery, but arrives first.
	added, summary, err = repo.AddTransactionalEvents(ctx, message.ID, []domain.MailEvent{
		{Type: domain.MailEventBounced, Detail: "5.1.1", ProviderEventID: "evt-2", OccurredAt: txBase.Add(2 * time.Minute)},
		{Type: domain.MailEventDelivered, ProviderEventID: "evt-1", OccurredAt: txBase.Add(time.Minute)},
	})
	if err != nil || added != 2 || summary != domain.MailEventBounced {
		t.Fatalf("bounce = %d, %q, %v", added, summary, err)
	}
	// A repeated provider event is stored once.
	added, summary, err = repo.AddTransactionalEvents(ctx, message.ID, []domain.MailEvent{
		{Type: domain.MailEventBounced, ProviderEventID: "evt-2", OccurredAt: txBase.Add(2 * time.Minute)},
	})
	if err != nil || added != 0 || summary != domain.MailEventBounced {
		t.Fatalf("duplicate = %d, %q, %v", added, summary, err)
	}
	if got := findTransactional(t, repo, message.ID); got.LastEvent != domain.MailEventBounced {
		t.Fatalf("last_event = %q", got.LastEvent)
	}
	events, err := repo.ListTransactionalEvents(ctx, message.ID, 10)
	if err != nil || len(events) != 3 || events[0].Type != domain.MailEventAccepted || events[2].Type != domain.MailEventBounced ||
		events[2].Detail != "5.1.1" || events[2].ProviderEventID != "evt-2" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if limited, _ := repo.ListTransactionalEvents(ctx, message.ID, 2); len(limited) != 2 {
		t.Fatalf("limited events = %d", len(limited))
	}
	if _, _, err := repo.AddTransactionalEvents(ctx, 999999, nil); !errors.Is(err, ErrTransactionalMessageNotFound) {
		t.Fatalf("unknown message error = %v", err)
	}
}

func logIDs(t *testing.T, repo *repository, filter domain.TransactionalLogFilter) []uint {
	t.Helper()
	messages, err := repo.ListTransactionalLog(context.Background(), 10, filter)
	if err != nil {
		t.Fatalf("ListTransactionalLog: %v", err)
	}
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func TestLogKeysetPaginationStable(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	var ids []uint
	for i := range 5 {
		ids = append(ids, createTransactional(t, repo, newTransactional(txBase.Add(time.Duration(i)*time.Minute))).ID)
	}
	window := domain.TransactionalLogFilter{Mode: domain.APIKeyModeLive, Since: txBase.Add(-time.Hour), Until: txBase.Add(24 * time.Hour), PerPage: 2}

	// Each page returns one extra row so the caller knows another page exists.
	first := logIDs(t, repo, window)
	if want := []uint{ids[4], ids[3], ids[2]}; !slices.Equal(first, want) {
		t.Fatalf("first page = %v, want %v", first, want)
	}
	// A message stored between pages neither repeats nor shifts the next page.
	createTransactional(t, repo, newTransactional(txBase.Add(10*time.Minute)))
	window.Cursor = first[1]
	second := logIDs(t, repo, window)
	if want := []uint{ids[2], ids[1], ids[0]}; !slices.Equal(second, want) {
		t.Fatalf("second page = %v, want %v", second, want)
	}
	window.Cursor = second[1]
	if third := logIDs(t, repo, window); !slices.Equal(third, []uint{ids[0]}) {
		t.Fatalf("third page = %v", third)
	}
}

func TestTransactionalLogFilters(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	delivered := createTransactional(t, repo, newTransactional(txBase))
	mustExec(t, db, `UPDATE transactional_messages SET last_event = 'delivered', status = 'sent', payload_ciphertext = NULL, next_attempt_at = NULL WHERE id = ?`, delivered.ID)
	bounced := newTransactional(txBase.Add(time.Minute))
	bounced.ToAddress, bounced.TemplateKey = "han@example.com", "reset"
	createTransactional(t, repo, bounced)
	mustExec(t, db, `UPDATE transactional_messages SET last_event = 'bounced', status = 'sent', payload_ciphertext = NULL, next_attempt_at = NULL WHERE id = ?`, bounced.ID)
	suppressed := newTransactional(txBase.Add(2 * time.Minute))
	suppressed.Status, suppressed.PayloadCiphertext, suppressed.NextAttemptAt = domain.TransactionalStatusSuppressed, nil, nil
	createTransactional(t, repo, suppressed)
	test := newTransactional(txBase.Add(3 * time.Minute))
	test.Mode = domain.APIKeyModeTest
	createTransactional(t, repo, test)
	old := createTransactional(t, repo, newTransactional(txBase.Add(-48*time.Hour)))

	base := domain.TransactionalLogFilter{Mode: domain.APIKeyModeLive, Since: txBase.Add(-time.Hour), Until: txBase.Add(time.Hour), PerPage: 50}
	cases := []struct {
		name   string
		change func(*domain.TransactionalLogFilter)
		want   []uint
	}{
		{"live in window", func(*domain.TransactionalLogFilter) {}, []uint{suppressed.ID, bounced.ID, delivered.ID}},
		{"test mode", func(f *domain.TransactionalLogFilter) { f.Mode = domain.APIKeyModeTest }, []uint{test.ID}},
		{"status", func(f *domain.TransactionalLogFilter) { f.Status = "sent" }, []uint{bounced.ID, delivered.ID}},
		{"last event", func(f *domain.TransactionalLogFilter) { f.LastEvent = "delivered" }, []uint{delivered.ID}},
		{"outcome delivered", func(f *domain.TransactionalLogFilter) { f.Outcome = domain.TransactionalOutcomeDelivered }, []uint{delivered.ID}},
		{"outcome problem", func(f *domain.TransactionalLogFilter) { f.Outcome = domain.TransactionalOutcomeProblem }, []uint{suppressed.ID, bounced.ID}},
		{"outcome and status intersect", func(f *domain.TransactionalLogFilter) {
			f.Outcome, f.Status = domain.TransactionalOutcomeProblem, "suppressed"
		}, []uint{suppressed.ID}},
		{"template", func(f *domain.TransactionalLogFilter) { f.TemplateKey = "reset" }, []uint{bounced.ID}},
		{"to is exact and case-insensitive", func(f *domain.TransactionalLogFilter) { f.To = " Han@Example.COM " }, []uint{bounced.ID}},
		{"to never matches a part", func(f *domain.TransactionalLogFilter) { f.To = "example.com" }, []uint{}},
		{"since", func(f *domain.TransactionalLogFilter) { f.Since = txBase.Add(90 * time.Second) }, []uint{suppressed.ID}},
		{"until excludes its instant", func(f *domain.TransactionalLogFilter) { f.Until = txBase.Add(time.Minute) }, []uint{delivered.ID}},
		{"window", func(f *domain.TransactionalLogFilter) {
			f.Since, f.Until = txBase.Add(-72*time.Hour), txBase.Add(-24*time.Hour)
		}, []uint{old.ID}},
	}
	for _, tc := range cases {
		filter := base
		tc.change(&filter)
		if got := logIDs(t, repo, filter); !slices.Equal(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}

	count, err := repo.CountTransactionalSince(ctx, 10, domain.APIKeyModeLive, "", txBase.Add(-time.Hour), 100)
	if err != nil || count != 3 {
		t.Fatalf("CountTransactionalSince = %d, %v", count, err)
	}
	if count, _ := repo.CountTransactionalSince(ctx, 10, domain.APIKeyModeLive, "", txBase.Add(-time.Hour), 2); count != 2 {
		t.Fatalf("capped count = %d", count)
	}
	if count, _ := repo.CountTransactionalSince(ctx, 10, domain.APIKeyModeLive, "reset", txBase.Add(-time.Hour), 100); count != 1 {
		t.Fatalf("template count = %d", count)
	}
	if count, _ := repo.CountTransactionalSince(ctx, 10, domain.APIKeyModeTest, "", txBase.Add(-time.Hour), 100); count != 1 {
		t.Fatalf("test count = %d", count)
	}
}

func TestSuppressionUniquePerOrganization(t *testing.T) {
	repo, db := openTransactionalRepository(t)
	ctx := context.Background()
	add := func(organization uint, email, reason string) {
		t.Helper()
		if err := repo.AddSuppression(ctx, domain.MailSuppression{
			OrganizationID: organization, Email: email, Reason: reason, Source: domain.MailSuppressionSourceTransactional,
		}); err != nil {
			t.Fatalf("AddSuppression(%q): %v", email, err)
		}
	}
	add(10, " LiLei@Example.COM ", domain.MailSuppressionReasonBounced)
	add(10, "lilei@example.com", domain.MailSuppressionReasonComplained) // already there: kept as it was
	add(11, "lilei@example.com", domain.MailSuppressionReasonBounced)
	add(10, "han@example.com", domain.MailSuppressionReasonComplained)

	var rows []struct {
		OrganizationID uint
		Email          string
		Reason         string
	}
	if err := db.Raw(`SELECT organization_id, email, reason FROM mail_suppressions ORDER BY organization_id, email`).Scan(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 || rows[1].Email != "lilei@example.com" || rows[1].Reason != "bounced" {
		t.Fatalf("rows = %+v", rows)
	}
	for _, check := range []struct {
		organization uint
		email        string
		want         bool
	}{
		{10, "LILEI@example.com", true}, {10, " lilei@example.com", true}, {11, "lilei@example.com", true},
		{11, "han@example.com", false}, {10, "lilei@example.org", false},
	} {
		if got, err := repo.IsSuppressed(ctx, check.organization, check.email); err != nil || got != check.want {
			t.Fatalf("IsSuppressed(%d, %q) = %v, %v", check.organization, check.email, got, err)
		}
	}

	listed, err := repo.ListSuppressions(ctx, 10, domain.MailSuppressionFilter{PerPage: 1})
	if err != nil || len(listed) != 2 || listed[0].Email != "han@example.com" {
		t.Fatalf("ListSuppressions = %+v, %v", listed, err)
	}
	next, err := repo.ListSuppressions(ctx, 10, domain.MailSuppressionFilter{PerPage: 1, Cursor: listed[0].ID})
	if err != nil || len(next) != 1 || next[0].Email != "lilei@example.com" {
		t.Fatalf("second page = %+v, %v", next, err)
	}
	if filtered, _ := repo.ListSuppressions(ctx, 10, domain.MailSuppressionFilter{Email: "LiLei@example.com", PerPage: 10}); len(filtered) != 1 {
		t.Fatalf("email filter = %+v", filtered)
	}
	if filtered, _ := repo.ListSuppressions(ctx, 10, domain.MailSuppressionFilter{Reason: "complained", PerPage: 10}); len(filtered) != 1 || filtered[0].Email != "han@example.com" {
		t.Fatalf("reason filter = %+v", filtered)
	}

	// Another organization's entry cannot be removed through this one.
	other, _ := repo.ListSuppressions(ctx, 11, domain.MailSuppressionFilter{PerPage: 10})
	if crossRemoved, crossErr := repo.DeleteSuppression(ctx, 10, other[0].ID); crossErr != nil || crossRemoved != nil {
		t.Fatalf("cross-organization DeleteSuppression = %+v, %v", crossRemoved, crossErr)
	}
	removed, err := repo.DeleteSuppression(ctx, 10, listed[0].ID)
	if err != nil || removed == nil || removed.Email != "han@example.com" || removed.Reason != "complained" {
		t.Fatalf("DeleteSuppression = %+v, %v", removed, err)
	}
	if again, err := repo.DeleteSuppression(ctx, 10, listed[0].ID); err != nil || again != nil {
		t.Fatalf("second DeleteSuppression = %+v, %v", again, err)
	}
}

// mustExec runs a fixture statement and stops the test when it fails.
func mustExec(t *testing.T, db *gorm.DB, statement string, arguments ...any) {
	t.Helper()
	if err := db.Exec(statement, arguments...).Error; err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func TestOldestQueuedTransactionalIsNilWithoutQueuedMessages(t *testing.T) {
	repo, _ := openTransactionalRepository(t)
	ctx := context.Background()

	oldest, err := repo.OldestQueuedTransactional(ctx)
	if err != nil {
		t.Fatalf("empty queue: %v", err)
	}
	if oldest != nil {
		t.Fatalf("empty queue reported %v, want nil", *oldest)
	}

	createTransactional(t, repo, newTransactional(txBase.Add(time.Minute)))
	createTransactional(t, repo, newTransactional(txBase))
	oldest, err = repo.OldestQueuedTransactional(ctx)
	if err != nil {
		t.Fatalf("queued messages: %v", err)
	}
	if oldest == nil || !oldest.Equal(txBase) {
		t.Fatalf("oldest = %v, want %v", oldest, txBase)
	}
}
