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

// GrantRequest is one role to grant, and where — isu #12 fase 3. A nil
// IDUnitKerja grants the role everywhere, the shape SUPERADMIN's seeded grant
// takes; a non-nil one scopes the grant to that unit_kerja alone, so the same
// role may be granted more than once for different units in one request (a
// cashier at outlet A and at outlet B is two GrantRequest entries, not one).
type GrantRequest struct {
	IDRole      int64  `json:"id_role" validate:"required,gt=0"`
	IDUnitKerja *int64 `json:"id_unit_kerja" validate:"omitempty,gt=0"`
}

// CreateUserRequest carries a plaintext password exactly once, on the way in. It
// is hashed in the usecase and never stored, logged, or returned.
//
// max=72 on Password mirrors bcrypt's input limit, which produces a validation
// error instead of a 500. It is not airtight: validator counts runes and bcrypt
// counts bytes, so a 72-character password of multibyte runes still passes here
// and fails in bcrypt — the usecase maps that error to a 400 as well.
//
// Grants may be omitted or empty; a user with no role can be created and granted
// roles later. Unknown or retired role or unit ids are rejected rather than
// skipped. A (id_role, id_unit_kerja) pair repeated in the body plainly means
// "grant that once" and is collapsed rather than rejected.
type CreateUserRequest struct {
	Username    string         `json:"username" validate:"required,max=64"`
	Email       *string        `json:"email" validate:"omitempty,email,max=255"`
	Password    string         `json:"password" validate:"required,min=8,max=72"`
	NamaLengkap *string        `json:"nama_lengkap" validate:"omitempty,max=255"`
	Grants      []GrantRequest `json:"grants" validate:"omitempty,max=32,dive"`
}

type GetUserRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateUserRequest patches only the fields present in the body. id, created_at,
// and created_by are deliberately absent.
//
// Grants replaces the whole set rather than adding to it, which is what makes
// the three Optional states meaningful here:
//
//	absent  -> leave the user's grants untouched
//	[]      -> revoke every grant
//	[...]   -> the user ends up with exactly those grants
//
// An explicit null is rejected: [] already says "no grants", so null would be a
// second spelling of the same thing.
type UpdateUserRequest struct {
	ID          int64                    `json:"-" validate:"required,gt=0"`
	Username    Optional[string]         `json:"username" validate:"omitempty,max=64"`
	Email       Optional[string]         `json:"email" validate:"omitempty,email,max=255"`
	Password    Optional[string]         `json:"password" validate:"omitempty,min=8,max=72"`
	NamaLengkap Optional[string]         `json:"nama_lengkap" validate:"omitempty,max=255"`
	IsAktif     Optional[bool]           `json:"is_aktif"`
	Grants      Optional[[]GrantRequest] `json:"grants" validate:"omitempty,max=32,dive"`
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
