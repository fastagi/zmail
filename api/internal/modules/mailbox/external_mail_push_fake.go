package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// defaultFakePushLife is how long a fake subscription lasts, like a Graph mail subscription.
const defaultFakePushLife = 70 * time.Hour

// fakePushState is the push side of FakeExternalMailSource. It behaves like Microsoft Graph: each
// subscription has an id and carries the secret it was created with, which notifications echo.
type fakePushState struct {
	subscriptions map[string]*fakeSubscription
	failing       map[string]error
	subscribes    map[string]int
	lastToken     map[string]ExternalMailToken
	unsubscribed  []string
	next          int
	// life overrides defaultFakePushLife when set.
	life time.Duration
}

type fakeSubscription struct {
	account string
	secret  string
	expires time.Time
}

func (p *fakePushState) init() {
	if p.subscriptions == nil {
		p.subscriptions, p.failing = map[string]*fakeSubscription{}, map[string]error{}
		p.subscribes, p.lastToken = map[string]int{}, map[string]ExternalMailToken{}
	}
}

var _ ExternalMailPushSource = (*FakeExternalMailSource)(nil)

// FailSubscribe makes every later subscribe or renewal for the account behind token fail with err;
// nil lets them succeed again.
func (f *FakeExternalMailSource) FailSubscribe(token string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	if err == nil {
		delete(f.push.failing, f.account(token))
		return
	}
	f.push.failing[f.account(token)] = err
}

// SetPushLife sets how long new and renewed fake subscriptions last.
func (f *FakeExternalMailSource) SetPushLife(life time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.life = life
}

// Subscribes reports how many subscribe or renewal calls the account behind token made.
func (f *FakeExternalMailSource) Subscribes(token string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	return f.push.subscribes[f.account(token)]
}

// LastPushToken returns the token of the account's latest subscribe call.
func (f *FakeExternalMailSource) LastPushToken(token string) ExternalMailToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	return f.push.lastToken[f.account(token)]
}

// PushSubscription returns the live subscription id and secret of the account behind token.
func (f *FakeExternalMailSource) PushSubscription(token string) (id, secret string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	account := f.account(token)
	for candidate, subscription := range f.push.subscriptions {
		if subscription.account == account {
			return candidate, subscription.secret, true
		}
	}
	return "", "", false
}

// Unsubscribed lists the subscription ids stopped at the fake, in order.
func (f *FakeExternalMailSource) Unsubscribed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.push.unsubscribed...)
}

// DropPushSubscription forgets a subscription, as a provider that removed it does.
func (f *FakeExternalMailSource) DropPushSubscription(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	delete(f.push.subscriptions, id)
}

func (f *FakeExternalMailSource) pushToken(token ExternalMailToken) string {
	if token.RefreshToken != "" {
		return token.RefreshToken
	}
	return token.AccessToken
}

// Subscribe renews the named subscription when it still exists for the account, or creates one
// with the request's secret.
func (f *FakeExternalMailSource) Subscribe(_ context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	token := f.pushToken(request.Token)
	account := f.account(token)
	f.push.subscribes[account]++
	f.push.lastToken[account] = request.Token
	if token == "" || f.spent[token] || f.revoked[account] || f.authFail[account] {
		return ExternalMailPushSubscription{}, ErrExternalMailAuth
	}
	if err := f.push.failing[account]; err != nil {
		return ExternalMailPushSubscription{}, err
	}
	life := f.push.life
	if life == 0 {
		life = defaultFakePushLife
	}
	expires := request.Now.Add(life)
	if existing, ok := f.push.subscriptions[request.SubscriptionID]; ok && existing.account == account {
		existing.expires = expires
		return ExternalMailPushSubscription{LookupKey: request.SubscriptionID, SubscriptionID: request.SubscriptionID, ExpiresAt: expires}, nil
	}
	if request.Secret == "" {
		return ExternalMailPushSubscription{}, errors.New("fake push: a new subscription needs a secret")
	}
	f.push.next++
	id := "fake-sub-" + strconv.Itoa(f.push.next)
	f.push.subscriptions[id] = &fakeSubscription{account: account, secret: request.Secret, expires: expires}
	return ExternalMailPushSubscription{LookupKey: id, SubscriptionID: id, Created: true, ExpiresAt: expires}, nil
}

// Unsubscribe forgets the subscription; an unknown one counts as stopped.
func (f *FakeExternalMailSource) Unsubscribe(_ context.Context, request ExternalMailPushRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.push.init()
	delete(f.push.subscriptions, request.SubscriptionID)
	f.push.unsubscribed = append(f.push.unsubscribed, request.SubscriptionID)
	return nil
}

// fakeNotification is the body the fake reads, shaped like a Graph notification collection.
type fakeNotification struct {
	Value []struct {
		SubscriptionID string `json:"subscriptionId"`
		ClientState    string `json:"clientState"`
		LifecycleEvent string `json:"lifecycleEvent"`
	} `json:"value"`
}

// ParseNotification echoes a validation token, or reads the body's notifications as hints whose
// secret the service checks.
func (f *FakeExternalMailSource) ParseNotification(_ context.Context, delivery ExternalMailPushDelivery) (ExternalMailPushParsed, error) {
	if token := delivery.Query.Get("validationToken"); token != "" {
		return ExternalMailPushParsed{Validation: token}, nil
	}
	var body fakeNotification
	if err := json.Unmarshal(delivery.Body, &body); err != nil {
		return ExternalMailPushParsed{}, fmt.Errorf("%w: fake notification is not JSON", domain.ErrInvalidInput)
	}
	hints := make([]ExternalMailPushHint, 0, len(body.Value))
	for _, item := range body.Value {
		hints = append(hints, ExternalMailPushHint{Key: item.SubscriptionID, Secret: item.ClientState, Lifecycle: item.LifecycleEvent})
	}
	return ExternalMailPushParsed{Hints: hints}, nil
}
