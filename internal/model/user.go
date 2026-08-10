package model

import "time"

// UserResponse deliberately has no password field, not even the hash. The only
// way a hash reaches a client is if someone adds it here.
type UserResponse struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	Email       *string `json:"email"`
	NamaLengkap *string `json:"nama_lengkap"`
	IsAktif     bool    `json:"is_aktif"`

	// Roles is always an array, never null — a user with no roles yet serialises
	// as [] so a client can read roles.length on every row.
	Roles []RoleRef `json:"roles"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
}

// CreateUserRequest carries a plaintext password exactly once, on the way in. It
// is hashed in the usecase and never stored, logged, or returned.
//
// max=72 on Password mirrors bcrypt's input limit, which produces a validation
// error instead of a 500. It is not airtight: validator counts runes and bcrypt
// counts bytes, so a 72-character password of multibyte runes still passes here
// and fails in bcrypt — the usecase maps that error to a 400 as well.
//
// RoleIDs may be omitted or empty; a user with no role can be created and granted
// roles later. Unknown or retired ids are rejected rather than skipped.
type CreateUserRequest struct {
	Username    string  `json:"username" validate:"required,max=64"`
	Email       *string `json:"email" validate:"omitempty,email,max=255"`
	Password    string  `json:"password" validate:"required,min=8,max=72"`
	NamaLengkap *string `json:"nama_lengkap" validate:"omitempty,max=255"`
	RoleIDs     []int64 `json:"role_ids" validate:"omitempty,max=32,dive,gt=0"`
}

type GetUserRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateUserRequest patches only the fields present in the body. id, created_at,
// and created_by are deliberately absent.
//
// RoleIDs replaces the whole set rather than adding to it, which is what makes
// the three Optional states meaningful here:
//
//	absent  -> leave the user's roles untouched
//	[]      -> revoke every role
//	[1, 3]  -> the user ends up with exactly roles 1 and 3
//
// An explicit null is rejected: [] already says "no roles", so null would be a
// second spelling of the same thing.
type UpdateUserRequest struct {
	ID          int64             `json:"-" validate:"required,gt=0"`
	Username    Optional[string]  `json:"username" validate:"omitempty,max=64"`
	Email       Optional[string]  `json:"email" validate:"omitempty,email,max=255"`
	Password    Optional[string]  `json:"password" validate:"omitempty,min=8,max=72"`
	NamaLengkap Optional[string]  `json:"nama_lengkap" validate:"omitempty,max=255"`
	IsAktif     Optional[bool]    `json:"is_aktif"`
	RoleIDs     Optional[[]int64] `json:"role_ids" validate:"omitempty,max=32,dive,gt=0"`
}

type ListUserRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every user; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
	// RoleID narrows the list to holders of one role — "show me every cashier".
	// Nil means no role filter.
	RoleID *int64 `query:"role_id" validate:"omitempty,gt=0"`
}
