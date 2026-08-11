package model

import "time"

// RiwayatBeliResponse is what one supplier last charged for a product.
//
// Every money and quantity figure is a decimal string, not a number: NUMERIC through
// a JSON float loses cents, and this is the figure an operator compares a new quote
// against.
//
// The two prices are not interchangeable:
//
//	harga_satuan_dasar      -- the invoice, per base unit, after the line discount
//	harga_pokok_satuan_dasar -- what it cost on the shelf, after the nota discount,
//	                            the PPN share, and freight
//
// Compare a supplier's next offer against the first; judge margin against the second.
// harga_satuan_input is the number printed on the paper, per whatever unit was typed,
// so it is shown but is not comparable across suppliers who quote in different packings.
type RiwayatBeliResponse struct {
	IDSupplier   int64  `json:"id_supplier"`
	NamaSupplier string `json:"nama_supplier,omitempty"`

	IDPembelian      int64     `json:"id_pembelian"`
	Nomor            string    `json:"nomor"`
	Tanggal          time.Time `json:"tanggal"`
	NoFakturSupplier *string   `json:"no_faktur_supplier"`

	IDPembelianDetail int64  `json:"id_pembelian_detail"`
	QtyFaktur         string `json:"qty_faktur"`
	IDSatuanInput     int64  `json:"id_satuan_input"`
	NamaSatuan        string `json:"nama_satuan,omitempty"`
	FaktorKonversi    int64  `json:"faktor_konversi"`
	QtyDasar          int64  `json:"qty_dasar"`
	NamaSatuanDasar   string `json:"nama_satuan_dasar,omitempty"`

	HargaSatuanInput      string  `json:"harga_satuan_input"`
	DiskonBaris           string  `json:"diskon_baris"`
	Subtotal              string  `json:"subtotal"`
	AlokasiBiaya          string  `json:"alokasi_biaya"`
	HargaSatuanDasar      string  `json:"harga_satuan_dasar"`
	HargaPokokSatuanDasar *string `json:"harga_pokok_satuan_dasar"`
}

// ListRiwayatBeliRequest asks for a product's purchase history, one row per supplier.
//
// IDSupplier narrows it to a single supplier, which is what the purchase input screen
// wants: the header already names who the goods are coming from, so the useful
// question there is "what did I last pay this supplier for this item", not "what did
// everyone charge".
//
// IDProduct comes from the path, never the query string, so the two cannot disagree.
type ListRiwayatBeliRequest struct {
	PageRequest
	IDProduct  int64 `json:"-" validate:"required,gt=0"`
	IDSupplier int64 `query:"id_supplier" validate:"omitempty,gt=0"`
}
