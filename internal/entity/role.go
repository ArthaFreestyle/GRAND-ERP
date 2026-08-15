package entity

import "time"

// Role maps the role table. Referenced by user_role, so rows are retired with
// is_aktif rather than deleted — dropping a role that was once granted would
// erase the record of who used to be allowed what.
type Role struct {
	ID        int64
	Nama      string
	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64
}

// RoleGrant is one grant a user holds: a role, and where it applies. A nil
// IDUnitKerja means the grant applies to every unit_kerja (isu #12 fase 3) —
// the shape SUPERADMIN's seeded grant takes, and any grant made before this
// column existed.
type RoleGrant struct {
	// ID is the user_role row's own id — what isu #12 fase 4's
	// POST /auth/switch-context targets, and what a token's grant list carries
	// so each choice in the switcher menu can be named unambiguously.
	ID   int64
	Role Role
	// IDUnitKerja is nil for a grant that applies everywhere. NamaUnitKerja and
	// IsAktifUnitKerja come from a JOIN and are only filled by the read
	// queries, the same way Supplier.NamaPembuat is. IsAktifUnitKerja is nil
	// exactly when IDUnitKerja is nil — a global grant has no unit_kerja row to
	// be active or not.
	IDUnitKerja      *int64
	NamaUnitKerja    *string
	IsAktifUnitKerja *bool
}

// RoleAssignment is one user_role row joined to its role and unit: the shape
// needed to attach grants to a page of users without querying per user.
type RoleAssignment struct {
	UserID int64
	RoleGrant
}
