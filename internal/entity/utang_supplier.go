package entity

import "time"

// UtangSupplier is a projection, not a table: one POSTED purchase invoice that a
// supplier is still owed money on.
//
// It follows the RiwayatBeli shape — a read answerable from documents that already
// exist, so there is no migration, no DTO to fill in, and nothing that can fall out of
// step. The working list for building a payment is exactly "which of this supplier's
// invoices are still open, and for how much", and every figure in that sentence is
// already recorded.
type UtangSupplier struct {
	IDPembelian      int64
	Nomor            string
	Tanggal          time.Time
	NoFakturSupplier *string
	TanggalFaktur    *time.Time
	JenisPembayaran  string
	StatusPembayaran string

	Total string
	// JumlahDialokasikan counts only effective allocations: POSTED payments, and for
	// giro only those that have actually cleared.
	JumlahDialokasikan string
	NilaiKreditRetur   string
	// SisaUtang = Total - JumlahDialokasikan - NilaiKreditRetur, computed in SQL so the
	// filter that selects open invoices and the figure reported for them cannot disagree.
	SisaUtang string
}
