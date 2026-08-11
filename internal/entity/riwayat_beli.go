package entity

import "time"

// RiwayatBeli is one supplier's most recent posted purchase of a product.
//
// It is a projection, not a table: nothing is stored for it and nothing has to be
// maintained. Isu #4 fase 4 asks for the replacement of a purchase order, and the
// answer is that the data already exists — every POSTED pembelian_detail row is a
// price that was actually paid, which is worth more than a quotation because a
// quotation is only what was promised in a chat.
//
// Money and quantities are strings for the same reason they are on Pembelian: these
// are NUMERIC columns, and a float64 rounds money on the way out.
//
// Two prices, and they answer different questions:
//
//	HargaSatuanDasar      = subtotal / qty_dasar        -- the invoice, per base unit
//	HargaPokokSatuanDasar = what posting computed       -- after nota discount, PPN
//	                                                       share, and freight
//
// The first is what to compare against a supplier's next quote; the second is what
// the goods actually cost once they were on the shelf. Reporting only one of them
// would make one of those two questions unanswerable.
//
// HargaSatuanInput is deliberately not the comparable figure: it is per input unit,
// so 12.000/DUS and 1.000/PCS look nothing alike while being the same price. It is
// reported anyway because it is what is printed on the paper the operator is holding.
type RiwayatBeli struct {
	IDSupplier   int64
	NamaSupplier string

	IDPembelian      int64
	Nomor            string
	Tanggal          time.Time
	NoFakturSupplier *string

	IDPembelianDetail int64
	QtyFaktur         string
	IDSatuanInput     int64
	NamaSatuan        string
	FaktorKonversi    int64
	QtyDasar          int64
	NamaSatuanDasar   string

	HargaSatuanInput string
	DiskonBaris      string
	Subtotal         string
	AlokasiBiaya     string
	HargaSatuanDasar string
	// Nullable in the column, but never null here: only POSTED documents are read,
	// and posting is what fills it. Kept a pointer so a future change to that filter
	// cannot turn a NULL into a scan failure at runtime.
	HargaPokokSatuanDasar *string
}
