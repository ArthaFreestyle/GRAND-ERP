package entity

import "time"

// Ekspedisi maps the ekspedisi table — freight carriers referenced by
// pembelian. Referenced by transactions, so rows are retired with is_aktif
// rather than deleted.
type Ekspedisi struct {
	ID        int64
	Nama      string
	Telepon   *string
	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64
}
