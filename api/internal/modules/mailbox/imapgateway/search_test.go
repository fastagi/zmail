package imapgateway

import (
	"errors"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestSearchCriteriaTranslation(t *testing.T) {
	snapshot := []uint32{3, 5, 9}
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	got, err := translateSearch(&imap.SearchCriteria{
		SeqNum:  []imap.SeqSet{{{Start: 2, Stop: 0}}},
		UID:     []imap.UIDSet{{{Start: 4, Stop: 9}}},
		Since:   since,
		Header:  []imap.SearchCriteriaHeaderField{{Key: "From", Value: "dana"}, {Key: "SUBJECT", Value: "plan"}, {Key: "message-id", Value: "<a@b>"}},
		Body:    []string{"code"},
		Flag:    []imap.Flag{imap.FlagSeen},
		NotFlag: []imap.Flag{imap.FlagFlagged, imap.FlagDeleted},
		Or:      [][2]imap.SearchCriteria{{{Text: []string{"x"}}, {Header: []imap.SearchCriteriaHeaderField{{Key: "To", Value: "y"}}}}},
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UIDSets) != 2 || got.UIDSets[0][0] != (domain.MailClientUIDRange{Start: 5, Stop: 9}) ||
		got.UIDSets[1][0] != (domain.MailClientUIDRange{Start: 4, Stop: 9}) {
		t.Fatalf("uid sets = %+v", got.UIDSets)
	}
	if got.Since == nil || !got.Since.Equal(since) || got.Seen == nil || !*got.Seen || got.Flagged == nil || *got.Flagged ||
		len(got.From) != 1 || len(got.Subject) != 1 || len(got.MessageID) != 1 || len(got.Body) != 1 || got.MatchNothing {
		t.Fatalf("translated = %+v", got)
	}
	if len(got.Or) != 1 || len(got.Or[0][0].Text) != 1 || len(got.Or[0][1].To) != 1 {
		t.Fatalf("or = %+v", got.Or)
	}

	if nothing, _ := translateSearch(&imap.SearchCriteria{Flag: []imap.Flag{imap.FlagDeleted}}, snapshot); !nothing.MatchNothing {
		t.Fatal("\\Deleted is never set, so searching for it matches nothing")
	}
	if nothing, _ := translateSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Bcc", Value: "z"}}}, snapshot); !nothing.MatchNothing {
		t.Fatal("inbound mail keeps no Bcc")
	}
	if _, err := translateSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "X-Spam", Value: "1"}}}, snapshot); !errors.Is(err, domain.ErrMailClientSearchUnsupported) {
		t.Fatalf("an arbitrary header = %v", err)
	}
	if _, err := translateSearch(&imap.SearchCriteria{Larger: 10}, snapshot); !errors.Is(err, domain.ErrMailClientSearchUnsupported) {
		t.Fatalf("LARGER = %v", err)
	}
	if empty, _ := translateSearch(&imap.SearchCriteria{SeqNum: []imap.SeqSet{{{Start: 7, Stop: 8}}}}, snapshot); len(empty.UIDSets) != 1 || len(empty.UIDSets[0]) != 0 {
		t.Fatalf("sequence numbers past the end = %+v", empty.UIDSets)
	}

	nested := imap.SearchCriteria{Body: []string{"deep"}}
	for range 4 {
		nested = imap.SearchCriteria{Not: []imap.SearchCriteria{nested}}
	}
	if _, err := translateSearch(&nested, snapshot); err != nil {
		t.Fatalf("four levels = %v", err)
	}
	nested = imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{nested, {}}}}
	if _, err := translateSearch(&nested, snapshot); !errors.Is(err, errSearchTooDeep) {
		t.Fatalf("five levels = %v", err)
	}
}
