package entity

import "time"

// UnitKerja maps the unit_kerja table — the organizational location a ruang
// belongs to, and (from isu #12 fase 3 onward) what user_role grants will be
// scoped to. Referenced by ruang, so rows are retired with is_aktif rather
// than deleted, same as every other master table.
type UnitKerja struct {
	ID        int64
	Kode      *string
	Nama      string
	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64

	// NamaPembuat is not a column of unit_kerja. It comes from a LEFT JOIN on
	// users and is only filled by the read queries — resolving it per row would
	// be an N+1.
	NamaPembuat *string
}
