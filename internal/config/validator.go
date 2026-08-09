package config

import "github.com/go-playground/validator/v10"

// NewValidator is shared by every usecase. Validation runs in the usecase
// layer, not in handlers.
func NewValidator() *validator.Validate {
	return validator.New(validator.WithRequiredStructEnabled())
}
