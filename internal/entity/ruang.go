package entity

// Ruang maps the ruang table — lokasi penyimpanan yang jadi partisi saldo di
// kartu_stok. Entities carry no JSON tags and no framework imports; they never
// leave the usecase layer.
//
// IDUnitKerja is NOT NULL since migration 000019 (isu #12 fase 2): a ruang
// with no unit_kerja is a ruang nobody can decide is theirs to use.
type Ruang struct {
	ID          int64
	Kode        *string
	NamaRuang   string
	IsAktif     bool
	IDUnitKerja int64

	// NamaUnitKerja is not a column of ruang. It comes from a JOIN on
	// unit_kerja and is only filled by the read queries — resolving it per row
	// would be an N+1.
	NamaUnitKerja string
	// NomorOpnameBeku is not a column of ruang either. It is the nomor of the
	// open (DRAFT/DIAJUKAN) stok_opname currently freezing this room, nil when
	// the room is free — isu #15. A LEFT JOIN against stok_opname's own
	// one-open-per-room index, not a second endpoint: a cashier whose posting
	// was refused has to be able to see the cause and who to chase without
	// searching for it.
	NomorOpnameBeku *string
}
