package entity

import "time"

// Supplier maps the supplier table. Referenced by pembelian, so rows are
// retired with is_aktif rather than deleted.
//
// created_by is nullable here (unlike product.created_by), which is what lets
// this module ship before the auth module exists: it is written as NULL for now.
type Supplier struct {
	ID        int64
	Kode      *string
	Nama      string
	Telepon   *string
	Alamat    *string
	NPWP      *string
	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64

	// NamaPembuat is not a column of supplier. It comes from a LEFT JOIN on
	// users and is only filled by the read queries — resolving it per row would
	// be an N+1.
	NamaPembuat *string
}
