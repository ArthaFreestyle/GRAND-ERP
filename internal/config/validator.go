package config

import (
	"reflect"

	"Arthafreestyle/ERP/internal/model"

	"github.com/go-playground/validator/v10"
)

// NewValidator is shared by every usecase. Validation runs in the usecase
// layer, not in handlers.
func NewValidator() *validator.Validate {
	validate := validator.New(validator.WithRequiredStructEnabled())

	// A tag on model.Optional[T] must apply to the value inside it, not to the
	// wrapper struct — without this, `max=64` on an Optional[string] is never
	// evaluated at all. Every Optional instantiation a DTO uses must be listed
	// here, otherwise its tags silently do nothing.
	validate.RegisterCustomTypeFunc(
		optionalValue,
		model.Optional[string]{},
		model.Optional[bool]{},
		model.Optional[[]int64]{},
		model.Optional[int64]{},
	)

	return validate
}

// optionalValue unwraps model.Optional[T] for the validator. An absent or
// explicitly null field yields nil, which the leading `omitempty` then skips —
// so PATCH tags must always start with omitempty, or "field not sent" is
// reported as a validation failure.
func optionalValue(field reflect.Value) any {
	value := field.FieldByName("Value")
	if !value.IsValid() || value.IsNil() {
		return nil
	}

	return value.Elem().Interface()
}
