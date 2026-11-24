package triage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

type fakeTriageStore struct {
	domain.MailTriageRepository
	claims        []domain.MailTriageClaim
	claimErr      error
	reserveOK     bool
	reserveErr    error
	reservedLimit int
	reservedCalls int
	examples      []domain.MailTriageExample
	examplesErr   error
	saved         []domain.MailTriage
	saveErr       error
}

func (s *fakeTriageStore) ClaimTriage(_ context.Context, _ time.Time, _ int) ([]domain.MailTriageClaim, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.claims, nil
}

func (s *fakeTriageStore) ReserveTriageCall(_ context.Context, _ uint, limit int, _ time.Time) (bool, error) {
	s.reservedCalls++
	s.reservedLimit = limit
	if s.reserveErr != nil {
		return false, s.reserveErr
	}
	return s.reserveOK, nil
}

func (s *fakeTriageStore) RecentTriageExamples(_ context.Context, _ uint, _ int) ([]domain.MailTriageExample, error) {
	if s.examplesErr != nil {
		return nil, s.examplesErr
	}
	return s.examples, nil
}

func (s *fakeTriageStore) SaveTriage(_ context.Context, row domain.MailTriage) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, row)
	return nil
}

type stubModel struct {
	output Output
	err    error
	calls  int
}

func (m *stubModel) Classify(_ context.Context, _ Input) (Output, error) {
	m.calls++
	if m.err != nil {
		return Output{}, m.err
	}
	return m.output, nil
}

func (m *stubModel) Draft(_ context.Context, _ DraftInput) (DraftOutput, error) {
	return DraftOutput{}, ErrUnavailable
}

func availableConfig() config.AITriageConfig {
	return config.AITriageConfig{
		Provider: config.AITriageProviderFake,
		DailyCap: 100,
	}
}

func baseClaim() domain.MailTriageClaim {
	return domain.MailTriageClaim{
		Triage: domain.MailTriage{
			MessageID: 42,
			MailboxID: 7,
			Status:    domain.MailTriagePending,
		},
		OrganizationID:  9,
		OrganizationOn:  true,
		OrganizationCap: 50,
		MailboxOn:       true,
		MailboxRoleName: "support",
	}
}

func TestWorkerDispatchGuards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)

	nilWorker := NewWorker(nil, nil, errors.New("not found"), DisabledModel{}, availableConfig(), func() time.Time { return now }, newStubMasker())
	if n, err := nilWorker.DispatchTriage(ctx, 5); err != nil || n != 0 {
		t.Fatalf("nil store: got (%d, %v), want (0, nil)", n, err)
	}

	store := &fakeTriageStore{claims: []domain.MailTriageClaim{baseClaim()}}
	w := NewWorker(store, nil, errors.New("not found"), DisabledModel{}, availableConfig(), func() time.Time { return now }, newStubMasker())
	if n, err := w.DispatchTriage(ctx, 0); err != nil || n != 0 {
		t.Fatalf("limit=0: got (%d, %v), want (0, nil)", n, err)
	}
}

func TestWorkerDispatchSkipPaths(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	errNotFound := errors.New("message not found")

	cases := []struct {
		name  string
		claim func() domain.MailTriageClaim
	}{
		{
			name: "organization off",
			claim: func() domain.MailTriageClaim {
				c := baseClaim()
				c.OrganizationOn = false
				return c
			},
		},
		{
			name: "mailbox off",
			claim: func() domain.MailTriageClaim {
				c := baseClaim()
				c.MailboxOn = false
				return c
			},
		},
		{
			name: "personal mailbox owner left",
			claim: func() domain.MailTriageClaim {
				c := baseClaim()
				c.MailboxPersonal = true
				c.MailboxOwnerLeft = true
				return c
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeTriageStore{claims: []domain.MailTriageClaim{tc.claim()}, reserveOK: true}
			model := &stubModel{}
			w := NewWorker(store, nil, errNotFound, model, availableConfig(), func() time.Time { return now }, newStubMasker())

			n, err := w.DispatchTriage(context.Background(), 10)
			if err != nil || n != 1 {
				t.Fatalf("DispatchTriage = (%d, %v), want (1, nil)", n, err)
			}
			if len(store.saved) != 1 || store.saved[0].Status != domain.MailTriageSkipped {
				t.Fatalf("expected skipped status, got %+v", store.saved)
			}
			if store.reservedCalls != 0 || model.calls != 0 {
				t.Fatalf("expected no reservation or model call on skip")
			}
		})
	}

	t.Run("message not found skips", func(t *testing.T) {
		t.Parallel()
		store := &fakeTriageStore{claims: []domain.MailTriageClaim{baseClaim()}, reserveOK: true}
		w := NewWorker(
			store,
			func(context.Context, uint, uint) (*domain.MailMessage, error) { return nil, errNotFound },
			errNotFound,
			&stubModel{},
			availableConfig(),
			func() time.Time { return now },
			newStubMasker(),
		)

		n, err := w.DispatchTriage(context.Background(), 10)
		if err != nil || n != 1 {
			t.Fatalf("DispatchTriage = (%d, %v), want (1, nil)", n, err)
		}
		if len(store.saved) != 1 || store.saved[0].Status != domain.MailTriageSkipped {
			t.Fatalf("expected skipped status, got %+v", store.saved)
		}
	})
}

func TestWorkerDispatchUnavailableAndCap(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	errNotFound := errors.New("not found")

	t.Run("config unavailable defers by unavailableDelay", func(t *testing.T) {
		t.Parallel()
		store := &fakeTriageStore{claims: []domain.MailTriageClaim{baseClaim()}}
		cfg := config.AITriageConfig{Provider: config.AITriageProviderDisabled, DailyCap: 100}
		w := NewWorker(store, nil, errNotFound, &stubModel{}, cfg, func() time.Time { return now }, newStubMasker())

		if n, err := w.DispatchTriage(context.Background(), 5); err != nil || n != 1 {
			t.Fatalf("DispatchTriage = (%d, %v)", n, err)
		}
		if len(store.saved) != 1 || !store.saved[0].NextAttemptAt.Equal(now.Add(unavailableDelay)) {
			t.Fatalf("expected next attempt at %v, got %+v", now.Add(unavailableDelay), store.saved)
		}
	})

	t.Run("daily cap reached defers to next UTC day", func(t *testing.T) {
		t.Parallel()
		claim := baseClaim()
		claim.OrganizationCap = 250 // higher than cfg.DailyCap (100)
		store := &fakeTriageStore{claims: []domain.MailTriageClaim{claim}, reserveOK: false}
		w := NewWorker(store, nil, errNotFound, &stubModel{}, availableConfig(), func() time.Time { return now }, newStubMasker())

		if n, err := w.DispatchTriage(context.Background(), 5); err != nil || n != 1 {
			t.Fatalf("DispatchTriage = (%d, %v)", n, err)
		}
		if store.reservedLimit != 100 {
			t.Fatalf("expected min(orgCap, cfgCap) = 100, got %d", store.reservedLimit)
		}
		wantNext := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
		if len(store.saved) != 1 || !store.saved[0].NextAttemptAt.Equal(wantNext) {
			t.Fatalf("expected next attempt %v, got %+v", wantNext, store.saved)
		}
	})

	t.Run("model ErrUnavailable defers without incrementing attempts", func(t *testing.T) {
		t.Parallel()
		store := &fakeTriageStore{claims: []domain.MailTriageClaim{baseClaim()}, reserveOK: true}
		w := NewWorker(
			store,
			func(context.Context, uint, uint) (*domain.MailMessage, error) {
				return &domain.MailMessage{ID: 42, Subject: "Hi", TextBody: "Body"}, nil
			},
			errNotFound,
			&stubModel{err: ErrUnavailable},
			availableConfig(),
			func() time.Time { return now },
			newStubMasker(),
		)

		if n, err := w.DispatchTriage(context.Background(), 5); err != nil || n != 1 {
			t.Fatalf("DispatchTriage = (%d, %v)", n, err)
		}
		if len(store.saved) != 1 || store.saved[0].Attempts != 0 || !store.saved[0].NextAttemptAt.Equal(now.Add(unavailableDelay)) {
			t.Fatalf("unexpected deferred row: %+v", store.saved)
		}
	})
}

func TestWorkerDispatchRetryAndMaxAttempts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	errNotFound := errors.New("not found")
	findMsg := func(context.Context, uint, uint) (*domain.MailMessage, error) {
		return &domain.MailMessage{ID: 42, Subject: "Hi", TextBody: "Body"}, nil
	}

	tests := []struct {
		name           string
		initialAttempt int
		wantStatus     domain.MailTriageStatus
		wantCategory   domain.MailTriageCategory
		wantNext       time.Time
	}{
		{
			name:           "first failure schedules 1m retry",
			initialAttempt: 0,
			wantStatus:     domain.MailTriagePending,
			wantNext:       now.Add(time.Minute),
		},
		{
			name:           "second failure schedules 5m retry",
			initialAttempt: 1,
			wantStatus:     domain.MailTriagePending,
			wantNext:       now.Add(5 * time.Minute),
		},
		{
			name:           "third failure marks failed with review category",
			initialAttempt: 2,
			wantStatus:     domain.MailTriageFailed,
			wantCategory:   domain.MailTriageReview,
			wantNext:       now,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			claim := baseClaim()
			claim.Triage.Attempts = tc.initialAttempt
			store := &fakeTriageStore{claims: []domain.MailTriageClaim{claim}, reserveOK: true}
			w := NewWorker(store, findMsg, errNotFound, &stubModel{err: errors.New("upstream 500")}, availableConfig(), func() time.Time { return now }, newStubMasker())

			if n, err := w.DispatchTriage(context.Background(), 5); err != nil || n != 1 {
				t.Fatalf("DispatchTriage = (%d, %v)", n, err)
			}
			row := store.saved[0]
			if row.Attempts != tc.initialAttempt+1 || row.LastErrorCode != failureCode {
				t.Fatalf("unexpected attempts/error: %+v", row)
			}
			if row.Status != tc.wantStatus || row.Category != tc.wantCategory || !row.NextAttemptAt.Equal(tc.wantNext) {
				t.Fatalf("unexpected retry row: %+v", row)
			}
		})
	}
}

func TestWorkerDispatchSuccessAndContextCancel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	errNotFound := errors.New("not found")
	cfg := availableConfig()
	store := &fakeTriageStore{claims: []domain.MailTriageClaim{baseClaim()}, reserveOK: true}
	w := NewWorker(
		store,
		func(context.Context, uint, uint) (*domain.MailMessage, error) {
			return &domain.MailMessage{ID: 42, Subject: "Invoice", TextBody: "Please pay by 2026-07-10"}, nil
		},
		errNotFound,
		NewModel(cfg),
		cfg,
		func() time.Time { return now },
		newStubMasker(),
	)

	n, err := w.DispatchTriage(context.Background(), 5)
	if err != nil || n != 1 {
		t.Fatalf("DispatchTriage = (%d, %v)", n, err)
	}
	row := store.saved[0]
	if row.Status != domain.MailTriageDone || row.Category != domain.MailTriageDeadline || row.Attempts != 1 || row.LastErrorCode != "" {
		t.Fatalf("unexpected classified row: %+v", row)
	}
	if row.Model != cfg.ModelLabel() {
		t.Fatalf("expected model label %q, got %q", cfg.ModelLabel(), row.Model)
	}
	if row.DueAt == nil || !row.DueAt.Equal(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected due_at: %v", row.DueAt)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, err := w.DispatchTriage(canceledCtx, 5); !errors.Is(err, context.Canceled) || n != 1 {
		t.Fatalf("expected context.Canceled with count 1, got (%d, %v)", n, err)
	}
}
