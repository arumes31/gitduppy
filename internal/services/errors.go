package services

import "errors"

// Sentinel errors returned by the service layer. Handlers match them with
// errors.Is to select an HTTP status code without inspecting error strings.
// Services wrap them with fmt.Errorf("%w: detail", ErrX) so a human-readable,
// non-sensitive detail travels with the error while the sentinel identity is
// preserved for errors.Is. Raw gorm.ErrRecordNotFound (and other storage-layer
// errors) must be translated to one of these at the service boundary so they
// never leak out of the services package.
var (
	// ErrNotFound indicates a requested resource does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict indicates the request conflicts with existing state, e.g. a
	// uniqueness violation or a duplicate association.
	ErrConflict = errors.New("conflict")
	// ErrValidation indicates the request violated a domain validation rule.
	ErrValidation = errors.New("validation")
	// ErrForbidden indicates the caller is not permitted to perform the action.
	ErrForbidden = errors.New("forbidden")
	// ErrNotImplemented indicates the requested operation is not implemented.
	ErrNotImplemented = errors.New("not implemented")
)
