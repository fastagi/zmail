package domain

import "errors"

// Mail contact and list sentinel errors shared across the roster and broadcast modules.
var (
	ErrMailContactNotFound      = errors.New("mail contact not found")
	ErrMailContactExists        = errors.New("mail contact already exists")
	ErrMailContactInvalid       = errors.New("mail contact is invalid")
	ErrMailContactLimitReached  = errors.New("mail contact limit reached")
	ErrMailContactListNotFound  = errors.New("mail contact list not found")
	ErrMailContactListNameTaken = errors.New("mail contact list name is already taken")
	ErrMailContactListLimit     = errors.New("mail contact list limit reached")
	ErrMailContactImportInvalid = errors.New("mail contact import is invalid")
)
