package entity

import "time"

// Pelanggan maps the pelanggan table. Referenced by penjualan, so rows are
// retired with is_aktif rather than deleted.
type Pelanggan struct {
	ID      int64
	Kode    *string
	Nama    string
	Telepon *string
	Alamat  *string
	NPWP    *string

	// PlafonKredit is the credit limit, carried as a decimal string rather than a
	// float: binary rounding has no place in money, and this module only stores
	// and displays the value.
	//
	// nil means *no limit*, which is not the same as zero — normalising nil to 0
	// would turn a customer with unlimited credit into one that may not owe a
	// single rupiah.
	PlafonKredit *string

	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64

	// NamaPembuat is not a column of pelanggan. It comes from a LEFT JOIN on
	// users and is only filled by the read queries.
	NamaPembuat *string
}
