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
//
// IDUnitKerja is where the grant applies — nil means every unit_kerja (isu #12
// fase 3). The same rule as IsAktif applies to a retired unit: the grant still
// needs to be visible so it can be revoked, so a retired unit's grant is not
// hidden. IsAktifUnitKerja is the unit's own version of IsAktif — nil exactly
// when IDUnitKerja is nil, since a global grant has no unit row to be retired.
//
// IDUserRole is the user_role row's own id — isu #12 fase 4's grant identity,
// what POST /auth/switch-context's id_user_role names. It is what makes each
// entry in a session's switcher menu addressable, since two grants can share
// the same role and even the same unit is never possible (the unique indexes
// forbid it) but nothing else here identifies the row itself.
type RoleRef struct {
	ID      int64  `json:"id"`
	Nama    string `json:"nama"`
	IsAktif bool   `json:"is_aktif"`

	IDUserRole int64 `json:"id_user_role"`

	IDUnitKerja      *int64  `json:"id_unit_kerja"`
	NamaUnitKerja    *string `json:"nama_unit_kerja"`
	IsAktifUnitKerja *bool   `json:"is_aktif_unit_kerja"`
}

// Nama is unique case-insensitively (role_nama_lower_uidx), so "Cashier" and
// "CASHIER" collide.
//
// ActorID is filled from the session by the controller, never from the body —
// the id comes from the verified token, never from anything a caller could set
// to someone else's. POST /api/v1/role is SUPERADMIN-only, so created_by is
// always present.
type CreateRoleRequest struct {
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Nama    string `json:"nama" validate:"required,max=64"`
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
	ActorID int64            `json:"-" validate:"required,gt=0"`
	Nama    Optional[string] `json:"nama" validate:"omitempty,max=64"`
	IsAktif Optional[bool]   `json:"is_aktif"`
}

type ListRoleRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every role; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
