package model

import "fmt"

// ErrorKind classifies a failure without naming a transport. The usecase layer
// picks a kind; internal/config/fiber.go maps it to an HTTP status. This is why
// usecases never import Fiber.
type ErrorKind int

const (
	KindInternal ErrorKind = iota
	KindInvalid
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
)

// Error is the domain error carried across layers.
type Error struct {
	Kind    ErrorKind
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}

	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func NewError(kind ErrorKind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

func Invalid(message string) *Error      { return NewError(KindInvalid, message) }
func Unauthorized(message string) *Error { return NewError(KindUnauthorized, message) }
func Forbidden(message string) *Error    { return NewError(KindForbidden, message) }
func NotFound(message string) *Error     { return NewError(KindNotFound, message) }
func Conflict(message string) *Error     { return NewError(KindConflict, message) }
