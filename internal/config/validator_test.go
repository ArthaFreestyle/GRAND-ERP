package config

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// Tags on model.Optional[T] only work because NewValidator registers a custom
// type func for each instantiation. Without it, `max=4` below would be ignored
// and over-long values would reach the database — a silent failure, which is why
// it is worth a test.
func TestValidatorAppliesTagsInsideOptional(t *testing.T) {
	type request struct {
		Kode model.Optional[string] `validate:"omitempty,max=4"`
	}

	validate := NewValidator()

	cases := []struct {
		name    string
		kode    model.Optional[string]
		wantErr bool
	}{
		{
			name: "absent field passes",
			kode: model.Optional[string]{},
		},
		{
			name: "explicit null passes, because omitempty leads the tag",
			kode: model.Optional[string]{Present: true},
		},
		{
			name: "value within the limit passes",
			kode: model.Optional[string]{Present: true, Value: strPtr("ABCD")},
		},
		{
			name:    "value over the limit fails",
			kode:    model.Optional[string]{Present: true, Value: strPtr("ABCDE")},
			wantErr: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validate.Struct(&request{Kode: testCase.kode})

			if testCase.wantErr && err == nil {
				t.Error("expected a validation error, got nil")
			}

			if !testCase.wantErr && err != nil {
				t.Errorf("expected no validation error, got %v", err)
			}
		})
	}
}

// UpdateUserRequest.RoleIDs is an Optional[[]int64] tagged `dive,gt=0`, which
// assumes dive still reaches the elements of a slice handed back by a custom type
// func. That is not obvious — the validator sees a value the func returned, not
// the struct field — and if it did not hold, a body like {"role_ids": [0]} would
// reach the database and fail there instead.
func TestValidatorDivesIntoOptionalSlice(t *testing.T) {
	type request struct {
		RoleIDs model.Optional[[]int64] `validate:"omitempty,max=32,dive,gt=0"`
	}

	validate := NewValidator()

	cases := []struct {
		name    string
		present bool
		ids     []int64
		wantErr bool
	}{
		{
			name: "absent field passes",
		},
		{
			name:    "explicit null passes, because omitempty leads the tag",
			present: true,
		},
		{
			name:    "empty slice passes: it means revoke every role",
			present: true,
			ids:     []int64{},
		},
		{
			name:    "positive ids pass",
			present: true,
			ids:     []int64{1, 2},
		},
		{
			name:    "a zero id fails",
			present: true,
			ids:     []int64{1, 0},
			wantErr: true,
		},
		{
			name:    "a negative id fails",
			present: true,
			ids:     []int64{-3},
			wantErr: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			roleIDs := model.Optional[[]int64]{Present: testCase.present}
			if testCase.ids != nil {
				ids := testCase.ids
				roleIDs.Value = &ids
			}

			err := validate.Struct(&request{RoleIDs: roleIDs})

			if testCase.wantErr && err == nil {
				t.Error("expected a validation error, got nil")
			}

			if !testCase.wantErr && err != nil {
				t.Errorf("expected no validation error, got %v", err)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
