package model

import "encoding/json"

// Optional distinguishes the three states a field in a PATCH body can be in,
// which a plain pointer cannot:
//
//	absent           -> Present == false            : jangan ubah kolom ini
//	explicit null    -> Present, Value == nil       : kosongkan kolom ini
//	explicit value   -> Present, Value != nil       : set kolom ke nilai itu
//
// Without the middle state, `{"telepon": null}` is indistinguishable from
// omitting telepon, and COALESCE($n, telepon) keeps the old number forever — the
// operator can never clear a mistyped one.
//
// Present is only ever set by UnmarshalJSON, so a field missing from the body
// stays zero. This is the one mechanism used for PATCH across every module.
type Optional[T any] struct {
	Present bool
	Value   *T
}

// UnmarshalJSON records that the key appeared in the body at all. Fiber v3's
// default binder is encoding/json, which honours this interface.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Present = true

	if string(data) == "null" {
		o.Value = nil

		return nil
	}

	value := new(T)
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}

	o.Value = value

	return nil
}

// MarshalJSON keeps the type usable in responses, though DTOs normally expose a
// plain pointer there.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Value)
}

// Clears reports whether the caller explicitly asked to null the column.
func (o Optional[T]) Clears() bool {
	return o.Present && o.Value == nil
}

// Set reports whether the caller supplied an actual value.
func (o Optional[T]) Set() bool {
	return o.Present && o.Value != nil
}
