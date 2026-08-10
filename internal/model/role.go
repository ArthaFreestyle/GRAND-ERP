package model

import "time"

type RoleResponse struct {
	ID      int64  `json:"id"`
	Nama    string `json:"nama"`
	IsAktif bool   `json:"is_aktif"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
}

// RoleRef is the slim shape embedded in a UserResponse. A full RoleResponse per
// role would repeat five audit columns on every row of a user list to say things
// the caller did not ask about.
//
// IsAktif is kept because a user's role list includes assignments to roles that
// were retired after being granted — the assignment is still real and still needs
// revoking. Without this flag such a row is indistinguishable from a live one.
type RoleRef struct {
	ID      int64  `json:"id"`
	Nama    string `json:"nama"`
	IsAktif bool   `json:"is_aktif"`
}

// Nama is unique case-insensitively (role_nama_lower_uidx), so "Cashier" and
// "CASHIER" collide.
type CreateRoleRequest struct {
	Nama string `json:"nama" validate:"required,max=64"`
}

type GetRoleRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateRoleRequest patches only the fields present in the body. id, created_at,
// and created_by are deliberately absent.
//
// Renaming a role that authorization code already checks by name breaks that
// code; retire the row with is_aktif = false and add a new role instead. Nothing
// in the database can enforce that, so it stays a documented rule.
type UpdateRoleRequest struct {
	ID      int64            `json:"-" validate:"required,gt=0"`
	Nama    Optional[string] `json:"nama" validate:"omitempty,max=64"`
	IsAktif Optional[bool]   `json:"is_aktif"`
}

type ListRoleRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every role; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
