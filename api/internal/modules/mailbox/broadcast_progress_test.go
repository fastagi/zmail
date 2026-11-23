package mailbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
)

// slots answers slotFreesAt from attempt instants listed oldest first.
func slots(attempts ...time.Time) func(int64) (*time.Time, error) {
	return func(n int64) (*time.Time, error) {
		if n < 0 || n >= int64(len(attempts)) {
			return nil, nil
		}
		at := attempts[n].Add(sendLimitWindow)
		return &at, nil
	}
}

func rate(perMinute float64) *float64 { return &perMinute }

func TestEstimateWithRoomFinishesAtTheMeasuredRate(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	got, err := estimateProgress(progressInput{
		start: now, pending: 600, rate: rate(60), limit: 1000, used: 100, slotFreesAt: slots(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.finish == nil || !got.finish.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("finish = %v; want 10 minutes ahead", got.finish)
	}
	if got.limitPauses || got.resumesAt != nil {
		t.Fatalf("limit = %v, %v; want no pause with room for everyone", got.limitPauses, got.resumesAt)
	}
}

func TestEstimateWithoutARateHasNoFinishWhileTheLimitHasRoom(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	got, err := estimateProgress(progressInput{start: now, pending: 10, limit: 1000, slotFreesAt: slots()})
	if err != nil {
		t.Fatal(err)
	}
	if got.finish != nil || got.limitPauses {
		t.Fatalf("got %+v; want nothing to tell yet", got)
	}
}

func TestEstimateTellsWhenTheLimitPausesAndResumes(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	// 190 of 200 used: the oldest attempt was 20 hours ago, the next ones every minute after it.
	attempts := make([]time.Time, 190)
	for i := range attempts {
		attempts[i] = now.Add(-20*time.Hour + time.Duration(i)*time.Minute)
	}
	got, err := estimateProgress(progressInput{
		start: now, pending: 50, rate: rate(10), limit: 200, used: 190, slotFreesAt: slots(attempts...),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.limitPauses {
		t.Fatal("want the limit to pause the broadcast")
	}
	// Room for 10 now; the oldest attempt leaves the window in 4 hours.
	if want := now.Add(4 * time.Hour); got.resumesAt == nil || !got.resumesAt.Equal(want) {
		t.Fatalf("resumes = %v; want %v", got.resumesAt, want)
	}
	// The other 40 wait for the 40 oldest attempts to leave: the 40th leaves 39 minutes later.
	if want := now.Add(4*time.Hour + 39*time.Minute); got.finish == nil || !got.finish.Equal(want) {
		t.Fatalf("finish = %v; want %v", got.finish, want)
	}
}

func TestEstimateHeldByAFullLimitNeedsNoRate(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	// 205 used of 200 (requests in flight overshot): 6 must leave before one is sent again.
	attempts := make([]time.Time, 205)
	for i := range attempts {
		attempts[i] = now.Add(-23*time.Hour + time.Duration(i)*time.Second)
	}
	got, err := estimateProgress(progressInput{
		start: now, pending: 3, limit: 200, used: 205, slotFreesAt: slots(attempts...),
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(time.Hour + 5*time.Second); got.resumesAt == nil || !got.resumesAt.Equal(want) {
		t.Fatalf("resumes = %v; want %v", got.resumesAt, want)
	}
	if want := now.Add(time.Hour + 7*time.Second); got.finish == nil || !got.finish.Equal(want) {
		t.Fatalf("finish = %v; want %v", got.finish, want)
	}
}

func TestEstimateBeyondOneWindowAddsADayPerLimit(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	// Nothing used: 100 go today, then 100 per day; the 250th goes two days later.
	got, err := estimateProgress(progressInput{
		start: now, pending: 250, rate: rate(100), limit: 100, slotFreesAt: slots(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.limitPauses || got.resumesAt == nil || !got.resumesAt.Equal(now.Add(sendLimitWindow)) {
		t.Fatalf("limit = %v, %v; want a pause until a day later", got.limitPauses, got.resumesAt)
	}
	if want := now.Add(2*sendLimitWindow + 30*time.Second); got.finish == nil || !got.finish.Equal(want) {
		t.Fatalf("finish = %v; want %v", got.finish, want)
	}
}

func TestMeasuredRateUsesTheRecentWindow(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	started := now.Add(-2 * time.Minute)
	if got := measuredRate(50, &started, now); got == nil || *got != 25 {
		t.Fatalf("rate after 2 minutes = %v; want 25", got)
	}
	long := now.Add(-time.Hour)
	if got := measuredRate(300, &long, now); got == nil || *got != 30 {
		t.Fatalf("rate over the window = %v; want 30", got)
	}
	recent := now.Add(-10 * time.Second)
	if got := measuredRate(5, &recent, now); got == nil || *got != 5 {
		t.Fatalf("rate in the first minute = %v; want 5 (counted over one minute)", got)
	}
	if got := measuredRate(0, &long, now); got != nil {
		t.Fatalf("rate with nothing finished = %v; want none", *got)
	}
}

func TestBroadcastProgressReportsTheLimitFromStoredAttempts(t *testing.T) {
	fixture := newBroadcastFixture(t, 3)
	ctx := context.Background()
	fixture.contacts(t, 5, "eta")
	broadcast := fixture.start(t, 0)
	if processed, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil || processed != 3 {
		t.Fatalf("first round = %d, %v; want the limit of 3", processed, err)
	}
	loaded := fixture.load(t, broadcast.ID)
	progress, err := fixture.service.BroadcastProgress(ctx, loaded)
	if err != nil {
		t.Fatalf("BroadcastProgress: %v", err)
	}
	if !progress.SendLimitPauses || progress.SendLimitResumesAt == nil {
		t.Fatalf("progress = %+v; want the limit to hold it", progress)
	}
	// The oldest of the three attempts leaves the 24-hour window first.
	if wait := progress.SendLimitResumesAt.Sub(fixture.now); wait < sendLimitWindow-time.Minute || wait > sendLimitWindow {
		t.Fatalf("resumes in %v; want about a day", wait)
	}
	if progress.EstimatedFinishAt == nil || progress.EstimatedFinishAt.Before(*progress.SendLimitResumesAt) {
		t.Fatalf("finish = %v; want after the limit resumes", progress.EstimatedFinishAt)
	}
	if progress.RatePerMinute == nil || *progress.RatePerMinute <= 0 {
		t.Fatalf("rate = %v; want the three just sent", progress.RatePerMinute)
	}
}

func TestBroadcastProgressIsEmptyForPausedAndFinishedBroadcasts(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	ctx := context.Background()
	fixture.contacts(t, 2, "done")
	broadcast := fixture.start(t, 0)
	if _, _, err := fixture.service.PauseBroadcast(ctx, as(1), broadcast.ID); err != nil {
		t.Fatalf("PauseBroadcast: %v", err)
	}
	progress, err := fixture.service.BroadcastProgress(ctx, fixture.load(t, broadcast.ID))
	if err != nil || progress == nil || progress.EstimatedFinishAt != nil || progress.SendLimitPauses {
		t.Fatalf("paused progress = %+v, %v; want no estimate", progress, err)
	}
	if _, err := fixture.service.ResumeBroadcast(ctx, as(1), broadcast.ID); err != nil {
		t.Fatalf("ResumeBroadcast: %v", err)
	}
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	done := fixture.load(t, broadcast.ID)
	if done.Status != domain.MailBroadcastSent {
		t.Fatalf("status = %s", done.Status)
	}
	if progress, err := fixture.service.BroadcastProgress(ctx, done); err != nil || progress != nil {
		t.Fatalf("finished progress = %+v, %v; want none", progress, err)
	}
}

func TestReadingOneBroadcastIncludesItsProgressButTheListingDoesNot(t *testing.T) {
	fixture := newBroadcastFixture(t, 3)
	ctx := context.Background()
	fixture.contacts(t, 5, "http")
	broadcast := fixture.start(t, 0)
	if _, err := fixture.service.DispatchBroadcasts(ctx, 10); err != nil {
		t.Fatalf("DispatchBroadcasts: %v", err)
	}
	gin.SetMode(gin.TestMode)
	handler := NewHandler(fixture.service)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), as(1)))
		c.Next()
	})
	router.GET("/mail-broadcasts", handler.ListBroadcasts)
	router.GET("/mail-broadcasts/:broadcast_id", handler.GetBroadcast)
	get := func(path string) map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	one := get("/mail-broadcasts/" + strconv.FormatUint(uint64(broadcast.ID), 10))["data"].(map[string]any)
	progress, ok := one["progress"].(map[string]any)
	if !ok {
		t.Fatalf("progress = %#v; want an object", one["progress"])
	}
	if progress["send_limit_pauses"] != true || progress["send_limit_resumes_at"] == nil ||
		progress["estimated_finish_at"] == nil || progress["rate_window_minutes"] != float64(10) || progress["computed_at"] == nil {
		t.Fatalf("progress = %#v", progress)
	}
	if _, err := time.Parse(time.RFC3339, progress["send_limit_resumes_at"].(string)); err != nil {
		t.Fatalf("send_limit_resumes_at: %v", err)
	}

	listed := get("/mail-broadcasts")["data"].([]any)[0].(map[string]any)
	if value, present := listed["progress"]; !present || value != nil {
		t.Fatalf("listed progress = %#v (present %v); want null", value, present)
	}
}
