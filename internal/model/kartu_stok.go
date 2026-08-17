package model

import "time"

// KartuStokResponse is one row of a product's movement history in one room — isu #22
// fase 1. Every quantity and money figure that comes off kartu_stok itself is exactly
// what the trigger recorded; nothing here is recomputed.
//
// NomorDokumen is ref_table + ref_id_transaksi translated into the document number an
// operator actually reads; ref_table and ref_id_transaksi ride along raw as well, for
// a client that wants to link straight to the source document's own endpoint.
//
// IDKartuStokAsal, non-nil, is what marks this row as a reversal rather than an
// ordinary posting — a client must be able to tell the two apart without guessing
// from JenisTransaksi, which an ordinary movement and whatever reverses it can share.
type KartuStokResponse struct {
	ID               int64     `json:"id"`
	TanggalTransaksi time.Time `json:"tanggal_transaksi"`
	JenisTransaksi   string    `json:"jenis_transaksi"`

	StokAwal   int64 `json:"stok_awal"`
	StokMasuk  int64 `json:"stok_masuk"`
	StokKeluar int64 `json:"stok_keluar"`
	StokAkhir  int64 `json:"stok_akhir"`

	// QtyInput and NamaSatuanInput are an audit trail of what the operator typed,
	// nothing more — every quantity above is already in the base unit. Both are nil
	// on a reversing row, which carries no operator input behind it.
	QtyInput        *string `json:"qty_input"`
	NamaSatuanInput *string `json:"nama_satuan_input"`

	HargaPokokSatuan string `json:"harga_pokok_satuan"`
	NilaiMasuk       string `json:"nilai_masuk"`
	NilaiKeluar      string `json:"nilai_keluar"`
	NilaiAkhir       string `json:"nilai_akhir"`

	RefTable       string  `json:"ref_table"`
	RefIDTransaksi int64   `json:"ref_id_transaksi"`
	NomorDokumen   *string `json:"nomor_dokumen"`

	IDKartuStokAsal *int64  `json:"id_kartu_stok_asal"`
	Keterangan      *string `json:"keterangan"`
}

// ListKartuStokRequest asks for one product's movement history in one room.
//
// IDRuang is required, unlike GET /product/{id}/stok: a balance chain is partitioned
// by (id_barang, id_ruang), and a "history" mixing several rooms would show a running
// balance that never existed on any single shelf. It is validated to exist (404 for
// an unknown room), and separately scoped by AktifIDUnitKerja: a room the caller has
// no authority over answers an empty page rather than 404, the same silent-omission
// rule every list-shaped read in this issue follows — there is no single resource
// identity here to 404 against, only a report that may come back empty.
//
// Dari/Sampai bound tanggal_transaksi, both inclusive whole days. They narrow which
// rows show; they never decide the order rows are shown in — see the ORDER BY
// comment on KartuStokRepository.Riwayat for why id, not date, is what the chain is
// actually built from.
type ListKartuStokRequest struct {
	PageRequest
	IDProduct int64   `json:"-" validate:"required,gt=0"`
	IDRuang   int64   `query:"id_ruang" validate:"required,gt=0"`
	Dari      *string `query:"dari" validate:"omitempty,datetime=2006-01-02"`
	Sampai    *string `query:"sampai" validate:"omitempty,datetime=2006-01-02"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the request — isu #12 fase 6 applied to this new read.
	AktifIDUnitKerja *int64 `query:"-"`
}
