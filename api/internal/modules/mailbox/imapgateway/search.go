package imapgateway

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/fastagi/zmail/api/internal/domain"
)

// maxSearchDepth bounds how deeply NOT and OR may nest in one search.
const maxSearchDepth = 4

// errSearchTooDeep is a search nested past maxSearchDepth; it answers NO [LIMIT].
var errSearchTooDeep = errors.New("imapgateway: search nests too deeply")

// translateSearch maps an IMAP search to the store's. snapshot is the selected folder's UIDs by
// sequence number, which resolves sequence sets and "*".
func translateSearch(criteria *imap.SearchCriteria, snapshot []uint32) (domain.MailClientSearch, error) {
	return translateSearchAt(criteria, snapshot, 0)
}

func translateSearchAt(criteria *imap.SearchCriteria, snapshot []uint32, depth int) (domain.MailClientSearch, error) {
	var search domain.MailClientSearch
	if depth > maxSearchDepth {
		return search, errSearchTooDeep
	}
	if criteria.Larger != 0 || criteria.Smaller != 0 || criteria.ModSeq != nil {
		return search, fmt.Errorf("%w: size or mod-sequence", domain.ErrMailClientSearchUnsupported)
	}
	for _, set := range criteria.SeqNum {
		search.UIDSets = append(search.UIDSets, seqSetRanges(set, snapshot))
	}
	maxUID := uint32(0)
	if len(snapshot) > 0 {
		maxUID = snapshot[len(snapshot)-1]
	}
	for _, set := range criteria.UID {
		ranges := make([]domain.MailClientUIDRange, 0, len(set))
		for _, uidRange := range set {
			start, stop := staticRange(uint32(uidRange.Start), uint32(uidRange.Stop), maxUID)
			ranges = append(ranges, domain.MailClientUIDRange{Start: start, Stop: stop})
		}
		search.UIDSets = append(search.UIDSets, ranges)
	}
	// Only the date counts; the sent date is the stored date, as Zmail keeps no other.
	since := latest(criteria.Since, criteria.SentSince)
	before := earliest(criteria.Before, criteria.SentBefore)
	if !since.IsZero() {
		search.Since = &since
	}
	if !before.IsZero() {
		search.Before = &before
	}
	for _, field := range criteria.Header {
		switch strings.ToLower(field.Key) {
		case "from":
			search.From = append(search.From, field.Value)
		case "to":
			search.To = append(search.To, field.Value)
		case "cc":
			search.Cc = append(search.Cc, field.Value)
		case "subject":
			search.Subject = append(search.Subject, field.Value)
		case "message-id":
			search.MessageID = append(search.MessageID, field.Value)
		case "bcc":
			// Received mail keeps no Bcc.
			search.MatchNothing = true
		default:
			return search, fmt.Errorf("%w: header", domain.ErrMailClientSearchUnsupported)
		}
	}
	search.Body = append(search.Body, criteria.Body...)
	search.Text = append(search.Text, criteria.Text...)
	setFlag := func(target **bool, value bool) {
		if *target != nil && **target != value {
			search.MatchNothing = true
		}
		*target = &value
	}
	for _, flag := range criteria.Flag {
		switch canonicalFlag(flag) {
		case imap.FlagSeen:
			setFlag(&search.Seen, true)
		case imap.FlagFlagged:
			setFlag(&search.Flagged, true)
		case imap.FlagAnswered:
			setFlag(&search.Answered, true)
		default:
			// \Deleted, \Draft, and keywords are never set.
			search.MatchNothing = true
		}
	}
	for _, flag := range criteria.NotFlag {
		switch canonicalFlag(flag) {
		case imap.FlagSeen:
			setFlag(&search.Seen, false)
		case imap.FlagFlagged:
			setFlag(&search.Flagged, false)
		case imap.FlagAnswered:
			setFlag(&search.Answered, false)
		default:
			// \Deleted, \Draft, and keywords are never set, so every message lacks them.
		}
	}
	for i := range criteria.Not {
		not, err := translateSearchAt(&criteria.Not[i], snapshot, depth+1)
		if err != nil {
			return search, err
		}
		search.Not = append(search.Not, not)
	}
	for i := range criteria.Or {
		left, err := translateSearchAt(&criteria.Or[i][0], snapshot, depth+1)
		if err != nil {
			return search, err
		}
		right, err := translateSearchAt(&criteria.Or[i][1], snapshot, depth+1)
		if err != nil {
			return search, err
		}
		search.Or = append(search.Or, [2]domain.MailClientSearch{left, right})
	}
	return search, nil
}

// seqSetRanges turns sequence numbers into the UIDs they stand for in the snapshot.
func seqSetRanges(set imap.SeqSet, snapshot []uint32) []domain.MailClientUIDRange {
	ranges := make([]domain.MailClientUIDRange, 0, len(set))
	count := uint32(len(snapshot))
	for _, seqRange := range set {
		start, stop := staticRange(seqRange.Start, seqRange.Stop, count)
		if start == 0 || start > count {
			continue
		}
		stop = min(stop, count)
		ranges = append(ranges, domain.MailClientUIDRange{Start: snapshot[start-1], Stop: snapshot[stop-1]})
	}
	return ranges
}

// staticRange replaces "*" (zero) with largest and orders the bounds.
func staticRange(start, stop, largest uint32) (uint32, uint32) {
	if start == 0 {
		start = largest
	}
	if stop == 0 {
		stop = largest
	}
	if start > stop {
		start, stop = stop, start
	}
	return start, stop
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	default:
		return b
	}
}

// canonicalFlag folds a system flag's case.
func canonicalFlag(flag imap.Flag) imap.Flag {
	if strings.HasPrefix(string(flag), `\`) {
		return imap.Flag(`\` + strings.ToUpper(string(flag[1:2])) + strings.ToLower(string(flag[2:])))
	}
	return flag
}
