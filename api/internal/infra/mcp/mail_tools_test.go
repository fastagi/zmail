package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubMailBackend struct {
	searchFn   func(context.Context, SearchMailInput) (SearchMailOutput, error)
	readFn     func(context.Context, ReadThreadInput) (ReadThreadOutput, error)
	triageFn   func(context.Context, TriageSummaryInput) (TriageSummaryOutput, error)
	draftFn    func(context.Context, DraftReplyInput) (DraftReplyOutput, error)
	organizeFn func(context.Context, OrganizeThreadInput) (OrganizeThreadOutput, error)
	sendFn     func(context.Context, SendMailInput) (SendMailOutput, error)
}

func (s *stubMailBackend) SearchMail(ctx context.Context, in SearchMailInput) (SearchMailOutput, error) {
	if s.searchFn != nil {
		return s.searchFn(ctx, in)
	}
	return SearchMailOutput{}, nil
}

func (s *stubMailBackend) ReadThread(ctx context.Context, in ReadThreadInput) (ReadThreadOutput, error) {
	if s.readFn != nil {
		return s.readFn(ctx, in)
	}
	return ReadThreadOutput{}, nil
}

func (s *stubMailBackend) TriageSummary(ctx context.Context, in TriageSummaryInput) (TriageSummaryOutput, error) {
	if s.triageFn != nil {
		return s.triageFn(ctx, in)
	}
	return TriageSummaryOutput{}, nil
}

func (s *stubMailBackend) DraftReply(ctx context.Context, in DraftReplyInput) (DraftReplyOutput, error) {
	if s.draftFn != nil {
		return s.draftFn(ctx, in)
	}
	return DraftReplyOutput{}, nil
}

func (s *stubMailBackend) OrganizeThread(ctx context.Context, in OrganizeThreadInput) (OrganizeThreadOutput, error) {
	if s.organizeFn != nil {
		return s.organizeFn(ctx, in)
	}
	return OrganizeThreadOutput{}, nil
}

func (s *stubMailBackend) SendMail(ctx context.Context, in SendMailInput) (SendMailOutput, error) {
	if s.sendFn != nil {
		return s.sendFn(ctx, in)
	}
	return SendMailOutput{}, nil
}

func toolCallLine(t *testing.T, id int, name string, args any) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	require.NoError(t, err)
	return string(payload)
}

func TestMailToolsListMetadata(t *testing.T) {
	server := NewServer("zmail", "1.0.0", "AI-native mail tools.", MailTools(&stubMailBackend{})...)
	responses := serve(t, server,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	require.Len(t, responses, 2)

	rawTools := responses[1]["result"].(map[string]any)["tools"].([]any)
	expected := []struct {
		name     string
		readOnly bool
	}{
		{name: "search_mail", readOnly: true},
		{name: "read_thread", readOnly: true},
		{name: "triage_summary", readOnly: true},
		{name: "draft_reply", readOnly: true},
		{name: "organize_thread", readOnly: false},
		{name: "send_mail", readOnly: false},
	}
	require.Len(t, rawTools, len(expected))

	for i, want := range expected {
		tool := rawTools[i].(map[string]any)
		assert.Equal(t, want.name, tool["name"])
		assert.NotEmpty(t, tool["title"])
		assert.NotEmpty(t, tool["description"])
		annotations := tool["annotations"].(map[string]any)
		assert.Equal(t, want.readOnly, annotations["readOnlyHint"], "tool %s readOnlyHint", want.name)

		schema := tool["inputSchema"].(map[string]any)
		assert.Equal(t, "object", schema["type"])
		assert.Equal(t, false, schema["additionalProperties"])
	}
}

func TestMailToolsHappyPaths(t *testing.T) {
	var (
		gotSearch   SearchMailInput
		gotRead     ReadThreadInput
		gotTriage   TriageSummaryInput
		gotDraft    DraftReplyInput
		gotOrganize OrganizeThreadInput
		gotSend     SendMailInput
	)
	backend := &stubMailBackend{
		searchFn: func(_ context.Context, in SearchMailInput) (SearchMailOutput, error) {
			gotSearch = in
			return SearchMailOutput{
				Matches: []SearchMailHit{{MessageID: "m1", ThreadID: "t1", Subject: "Invoice", From: "billing@example.com", Unread: true}},
				Total:   1,
			}, nil
		},
		readFn: func(_ context.Context, in ReadThreadInput) (ReadThreadOutput, error) {
			gotRead = in
			return ReadThreadOutput{
				ThreadID:      in.ThreadID,
				Subject:       "Invoice",
				Messages:      []ThreadMessage{{MessageID: "m1", From: "billing@example.com", To: []string{"me@example.com"}, BodyText: "Attached.", Attachments: []string{"inv.pdf"}}},
				TotalMessages: 1,
			}, nil
		},
		triageFn: func(_ context.Context, in TriageSummaryInput) (TriageSummaryOutput, error) {
			gotTriage = in
			return TriageSummaryOutput{
				Folder:      in.Folder,
				WindowHours: in.WindowHours,
				UnreadCount: 4,
				UrgentCount: 1,
				Items:       []TriageItem{{ThreadID: "t1", Subject: "Invoice", From: "billing@example.com", Priority: "high", Reason: "Due today"}},
			}, nil
		},
		draftFn: func(_ context.Context, in DraftReplyInput) (DraftReplyOutput, error) {
			gotDraft = in
			return DraftReplyOutput{
				ThreadID: in.ThreadID,
				To:       []string{"billing@example.com"},
				Subject:  "Re: Invoice",
				BodyText: "Thanks, we will pay today.",
			}, nil
		},
		organizeFn: func(_ context.Context, in OrganizeThreadInput) (OrganizeThreadOutput, error) {
			gotOrganize = in
			return OrganizeThreadOutput{ThreadID: in.ThreadID, Action: in.Action, Applied: true, Labels: []string{"finance"}}, nil
		},
		sendFn: func(_ context.Context, in SendMailInput) (SendMailOutput, error) {
			gotSend = in
			return SendMailOutput{MessageID: "m2", ThreadID: "t1", AcceptedRecipients: len(in.To) + len(in.Cc), QueuedAt: "2026-11-24T16:00:00Z"}, nil
		},
	}

	server := NewServer("zmail", "1.0.0", "Mail tools.", MailTools(backend)...)
	responses := serve(t, server,
		toolCallLine(t, 1, "search_mail", map[string]any{"query": "  invoice  ", "unread_only": true}),
		toolCallLine(t, 2, "read_thread", map[string]any{"thread_id": "t1", "include_attachments": true}),
		toolCallLine(t, 3, "triage_summary", map[string]any{}),
		toolCallLine(t, 4, "draft_reply", map[string]any{"thread_id": "t1", "instructions": "Confirm payment", "reply_all": true}),
		toolCallLine(t, 5, "organize_thread", map[string]any{"thread_id": "t1", "action": "label", "label": "finance"}),
		toolCallLine(t, 6, "send_mail", map[string]any{"to": []string{" billing@example.com "}, "cc": []string{"ops@example.com"}, "subject": "Re: Invoice", "body_text": "Paid.", "thread_id": "t1"}),
	)
	require.Len(t, responses, 6)
	for i, resp := range responses {
		assert.Nil(t, resp["error"], "request %d should not return rpc error", i+1)
		res := resp["result"].(map[string]any)
		assert.Nil(t, res["isError"], "request %d should not return tool error", i+1)
		assert.NotNil(t, res["structuredContent"])
	}

	assert.Equal(t, SearchMailInput{Query: "invoice", UnreadOnly: true, Limit: DefaultSearchMailLimit}, gotSearch)
	assert.Equal(t, ReadThreadInput{ThreadID: "t1", Limit: DefaultReadThreadLimit, IncludeAttachments: true}, gotRead)
	assert.Equal(t, TriageSummaryInput{Folder: "inbox", WindowHours: DefaultTriageWindowHrs, Limit: DefaultTriageLimit}, gotTriage)
	assert.Equal(t, DraftReplyInput{ThreadID: "t1", Instructions: "Confirm payment", Tone: "concise", ReplyAll: true}, gotDraft)
	assert.Equal(t, OrganizeThreadInput{ThreadID: "t1", Action: "label", Label: "finance"}, gotOrganize)
	assert.Equal(t, SendMailInput{To: []string{"billing@example.com"}, Cc: []string{"ops@example.com"}, Subject: "Re: Invoice", BodyText: "Paid.", ThreadID: "t1"}, gotSend)
}

func TestMailToolsLimitClamping(t *testing.T) {
	var searchLimit, readLimit, triageLimit int
	backend := &stubMailBackend{
		searchFn: func(_ context.Context, in SearchMailInput) (SearchMailOutput, error) {
			searchLimit = in.Limit
			hits := make([]SearchMailHit, MaxSearchMailLimit+10)
			for i := range hits {
				hits[i] = SearchMailHit{MessageID: fmt.Sprintf("m%d", i)}
			}
			return SearchMailOutput{Matches: hits, Total: len(hits)}, nil
		},
		readFn: func(_ context.Context, in ReadThreadInput) (ReadThreadOutput, error) {
			readLimit = in.Limit
			msgs := make([]ThreadMessage, MaxReadThreadLimit+5)
			for i := range msgs {
				msgs[i] = ThreadMessage{MessageID: fmt.Sprintf("m%d", i)}
			}
			return ReadThreadOutput{ThreadID: in.ThreadID, Messages: msgs, TotalMessages: len(msgs)}, nil
		},
		triageFn: func(_ context.Context, in TriageSummaryInput) (TriageSummaryOutput, error) {
			triageLimit = in.Limit
			items := make([]TriageItem, MaxTriageLimit+5)
			for i := range items {
				items[i] = TriageItem{ThreadID: fmt.Sprintf("t%d", i)}
			}
			return TriageSummaryOutput{Folder: in.Folder, WindowHours: in.WindowHours, Items: items}, nil
		},
	}

	server := NewServer("zmail", "1.0.0", "Mail tools.", MailTools(backend)...)
	responses := serve(t, server,
		toolCallLine(t, 1, "search_mail", map[string]any{"query": "status", "limit": 500}),
		toolCallLine(t, 2, "read_thread", map[string]any{"thread_id": "t1", "limit": 200}),
		toolCallLine(t, 3, "triage_summary", map[string]any{"limit": 999}),
	)
	require.Len(t, responses, 3)

	assert.Equal(t, MaxSearchMailLimit, searchLimit)
	assert.Equal(t, MaxReadThreadLimit, readLimit)
	assert.Equal(t, MaxTriageLimit, triageLimit)

	searchSC := responses[0]["result"].(map[string]any)["structuredContent"].(map[string]any)
	assert.Len(t, searchSC["matches"].([]any), MaxSearchMailLimit)
	assert.Equal(t, true, searchSC["truncated"])

	readSC := responses[1]["result"].(map[string]any)["structuredContent"].(map[string]any)
	assert.Len(t, readSC["messages"].([]any), MaxReadThreadLimit)
	assert.Equal(t, true, readSC["truncated"])

	triageSC := responses[2]["result"].(map[string]any)["structuredContent"].(map[string]any)
	assert.Len(t, triageSC["items"].([]any), MaxTriageLimit)
	assert.Equal(t, true, triageSC["truncated"])
}

func TestMailToolsValidationErrors(t *testing.T) {
	tooManyRecipients := make([]string, MaxSendRecipients+1)
	for i := range tooManyRecipients {
		tooManyRecipients[i] = fmt.Sprintf("u%d@example.com", i)
	}

	cases := []struct {
		name    string
		tool    string
		args    any
		wantErr string
	}{
		{name: "search_mail missing filter", tool: "search_mail", args: map[string]any{}, wantErr: "at least one search filter"},
		{name: "search_mail negative limit", tool: "search_mail", args: map[string]any{"query": "hi", "limit": -1}, wantErr: "limit must be >= 0"},
		{name: "search_mail unknown field", tool: "search_mail", args: map[string]any{"query": "hi", "unknown": true}, wantErr: "invalid arguments"},
		{name: "read_thread missing id", tool: "read_thread", args: map[string]any{}, wantErr: "thread_id is required"},
		{name: "read_thread negative limit", tool: "read_thread", args: map[string]any{"thread_id": "t1", "limit": -2}, wantErr: "limit must be >= 0"},
		{name: "triage_summary window negative", tool: "triage_summary", args: map[string]any{"window_hours": -1}, wantErr: "window_hours must be between 1 and 720"},
		{name: "triage_summary window too large", tool: "triage_summary", args: map[string]any{"window_hours": 721}, wantErr: "window_hours must be between 1 and 720"},
		{name: "triage_summary negative limit", tool: "triage_summary", args: map[string]any{"limit": -5}, wantErr: "limit must be >= 0"},
		{name: "draft_reply missing instructions", tool: "draft_reply", args: map[string]any{"thread_id": "t1"}, wantErr: "instructions is required"},
		{name: "draft_reply unsupported tone", tool: "draft_reply", args: map[string]any{"thread_id": "t1", "instructions": "Say yes", "tone": "sarcastic"}, wantErr: "unsupported tone"},
		{name: "organize_thread unknown action", tool: "organize_thread", args: map[string]any{"thread_id": "t1", "action": "delete_forever"}, wantErr: "unsupported action"},
		{name: "organize_thread label without name", tool: "organize_thread", args: map[string]any{"thread_id": "t1", "action": "label"}, wantErr: "label is required"},
		{name: "organize_thread snooze missing timestamp", tool: "organize_thread", args: map[string]any{"thread_id": "t1", "action": "snooze"}, wantErr: "snooze_until is required"},
		{name: "organize_thread snooze invalid timestamp", tool: "organize_thread", args: map[string]any{"thread_id": "t1", "action": "snooze", "snooze_until": "tomorrow"}, wantErr: "RFC3339"},
		{name: "send_mail empty recipients", tool: "send_mail", args: map[string]any{"to": []string{}, "subject": "Hi", "body_text": "Body"}, wantErr: "at least one recipient"},
		{name: "send_mail malformed recipient", tool: "send_mail", args: map[string]any{"to": []string{"not-an-email"}, "subject": "Hi", "body_text": "Body"}, wantErr: "invalid recipient"},
		{name: "send_mail too many recipients", tool: "send_mail", args: map[string]any{"to": tooManyRecipients, "subject": "Hi", "body_text": "Body"}, wantErr: "total recipients must not exceed"},
		{name: "send_mail missing subject", tool: "send_mail", args: map[string]any{"to": []string{"a@example.com"}, "body_text": "Body"}, wantErr: "subject is required"},
		{name: "send_mail missing body", tool: "send_mail", args: map[string]any{"to": []string{"a@example.com"}, "subject": "Hi"}, wantErr: "body_text is required"},
	}

	server := NewServer("zmail", "1.0.0", "Mail tools.", MailTools(&stubMailBackend{})...)
	lines := make([]string, 0, len(cases))
	for i, tc := range cases {
		lines = append(lines, toolCallLine(t, i+1, tc.tool, tc.args))
	}
	responses := serve(t, server, lines...)
	require.Len(t, responses, len(cases))

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Nil(t, responses[i]["error"])
			res := responses[i]["result"].(map[string]any)
			assert.Equal(t, true, res["isError"])
			text := res["content"].([]any)[0].(map[string]any)["text"].(string)
			assert.Contains(t, text, tc.wantErr)
		})
	}
}

func TestMailToolsNilBackendAndBackendErrorRecovery(t *testing.T) {
	nilServer := NewServer("zmail", "1.0.0", "Mail tools.", MailTools(nil)...)
	nilResp := serve(t, nilServer, toolCallLine(t, 1, "triage_summary", map[string]any{}))
	require.Len(t, nilResp, 1)
	assert.Equal(t, true, nilResp[0]["result"].(map[string]any)["isError"])
	assert.Contains(t, nilResp[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], "backend is not configured")

	backend := &stubMailBackend{
		searchFn: func(context.Context, SearchMailInput) (SearchMailOutput, error) {
			return SearchMailOutput{}, errors.New("imap index temporarily unavailable")
		},
		organizeFn: func(context.Context, OrganizeThreadInput) (OrganizeThreadOutput, error) {
			return OrganizeThreadOutput{}, errors.New("thread t99 not found")
		},
		sendFn: func(_ context.Context, in SendMailInput) (SendMailOutput, error) {
			return SendMailOutput{MessageID: "m-recovered", ThreadID: "t-new", AcceptedRecipients: len(in.To), QueuedAt: "2026-11-24T16:05:00Z"}, nil
		},
	}

	server := NewServer("zmail", "1.0.0", "Mail tools.", MailTools(backend)...)
	responses := serve(t, server,
		toolCallLine(t, 1, "search_mail", map[string]any{"query": "urgent"}),
		toolCallLine(t, 2, "organize_thread", map[string]any{"thread_id": "t99", "action": "snooze", "snooze_until": "2026-11-25T09:00:00Z"}),
		toolCallLine(t, 3, "send_mail", map[string]any{"to": []string{"alice@example.com"}, "subject": "Follow-up", "body_text": "All set."}),
	)
	require.Len(t, responses, 3)

	first := responses[0]["result"].(map[string]any)
	assert.Equal(t, true, first["isError"])
	assert.Contains(t, first["content"].([]any)[0].(map[string]any)["text"], "imap index temporarily unavailable")

	second := responses[1]["result"].(map[string]any)
	assert.Equal(t, true, second["isError"])
	assert.Contains(t, second["content"].([]any)[0].(map[string]any)["text"], "thread t99 not found")

	third := responses[2]["result"].(map[string]any)
	assert.Nil(t, third["isError"])
	sc := third["structuredContent"].(map[string]any)
	assert.Equal(t, "m-recovered", sc["message_id"])
}
