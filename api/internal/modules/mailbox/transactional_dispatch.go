package mailbox

import "context"

// DispatchTransactionalMessage sends one queued transactional message.
//
// Stub until the dispatcher lands: it calls no provider and leaves the message queued for the
// worker. Task 11 replaces this body with the lease, checks, and provider call.
func (s *service) DispatchTransactionalMessage(_ context.Context, _ uint) error {
	return nil
}
