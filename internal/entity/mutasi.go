package entity

import "time"

// Status mutasi. Three values, not four: migration 000018 pins them in a CHECK, and
// the absent one is DIAJUKAN.
//
// Every other document that writes kartu_stok has an approval stage, because that
// table is append-only and a wrong posting can only be reversed — at whatever the
// moving average has become by then. Mutasi drops it because the stake is smaller than
// anywhere else: a wrong one records goods in the wrong room, and total stock and total
// inventory value do not move at all. No outside party, no money, and the correction is
// another mutasi the other way, which the same person is already allowed to write.
//
// That is a different argument from the one pembayaran_utang makes for having no
// DIAJUKAN. There, voiding leaves no residue and every cache recomputes exactly. Here
// it does leave residue — see MutasiUseCase.Batal. The justification is the size of the
// bet, not the tidiness of the undo.
//
// The two-person control survives entirely in the route table: INVENTARIS may reach
// DRAFT, SUPERADMIN posts and voids.
const (
	StatusMutasiDraft  = "DRAFT"
	StatusMutasiPosted = "POSTED"
	StatusMutasiBatal  = "BATAL"
)

// Mutasi maps the mutasi header: goods moving from one ruang to another.
//
// It is the fourth document to write kartu_stok and the first to write in both
// directions at once. MUTASI_KELUAR and MUTASI_MASUK are not two document types —
// migration 000007 says so plainly — they are the two rows one mutasi_detail line
// produces, in one transaction. Splitting them into separate documents would let goods
// leave a warehouse without ever entering a shop, with nothing holding the two halves
// together.
//
// Goods appearing with no origin are not a mutasi at all. A physical find or a counting
// correction is stok_opname, which has SO_SURPLUS and SO_DEFISIT waiting for it.
//
// There is no total and no money column of any kind. What a mutasi is worth is decided
// by the kartu_stok trigger at posting, per line, from the source room's moving
// average — the application cannot know that figure and must not try to. See
// MutasiDetail.HargaPokokSatuanDasar.
type Mutasi struct {
	ID      int64
	Nomor   string
	Tanggal time.Time
	// IDRuangAsal and IDRuangTujuan may both be changed while the document is DRAFT,
	// unlike pembayaran_utang, which keeps id_supplier out of its update DTO entirely.
	// The difference is that no detail row here points at a room — the rooms live on the
	// header alone — so changing them leaves nothing behind pointing at the wrong place.
	//
	// mutasi_ruang_check makes them different from each other.
	IDRuangAsal   int64
	IDRuangTujuan int64
	Keterangan    *string
	Status        string

	CreatedBy int64
	CreatedAt time.Time
	PostedAt  *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string

	// Not columns of mutasi. Filled by the read queries.
	NamaRuangAsal   string
	NamaRuangTujuan string
	// IDUnitKerjaRuangAsal is IDRuangAsal's own unit_kerja — isu #12 fase 6.
	// Only the source room's unit is ever checked, never the destination's:
	// cross-unit transfers are allowed by design (isu #12 fase 1), and
	// visibility mirrors the same write-side asymmetry (fase 5) rather than
	// showing a document to anyone touched by either end of it.
	IDUnitKerjaRuangAsal int64
	Detail               []MutasiDetail
}

// MutasiDetail maps one line: one product, one quantity, moving between the header's
// two rooms.
//
// Unlike penerimaan_susulan and retur_pembelian, this points at no other document's
// detail row. A mutasi has no parent — its quota is the source room's balance, and the
// kartu_stok trigger is what checks it, on every insert, under an advisory lock.
type MutasiDetail struct {
	ID        int64
	IDMutasi  int64
	IDProduct int64
	QtyInput  string
	// IDSatuanInput and FaktorKonversi are a snapshot from product_satuan taken when
	// the line is written, exactly as every other document does. Quantities move to base
	// units through it and never through the current master afterwards.
	IDSatuanInput  int64
	FaktorKonversi int64
	QtyDasar       int64
	// HargaPokokSatuanDasar is filled at POSTING, not when the line is typed, and this
	// is the one thing in the module that is expensive to get wrong.
	//
	// The rule from migration 000007 is that cost must follow the source room, or moving
	// goods would change the total value of inventory. But the application cannot compute
	// that figure: the source room's moving average is known only to
	// kartu_stok_hitung_saldo, which reads it inside a pg_advisory_xact_lock, and
	// anything Go reads before the insert may already be stale. The trigger also
	// overwrites harga_pokok_satuan and nilai_keluar on every outgoing row, so sending a
	// number of our own is discarded anyway.
	//
	// So this is a copy of what the outgoing row's RETURNING actually reported, taken
	// after the fact. Nullable in the schema precisely because a DRAFT has no answer for
	// it yet.
	HargaPokokSatuanDasar *string

	// Not columns of mutasi_detail. Filled by the read query.
	KodeBarang      string
	NamaProduct     string
	NamaSatuan      string
	NamaSatuanDasar string
}
