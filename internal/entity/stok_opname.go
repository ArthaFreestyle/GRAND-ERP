package entity

import "time"

// Status stok_opname, pinned by migration 000023's CHECK. Four values, following
// pembelian rather than pemakaian: DIAJUKAN -> DRAFT on rejection, never a
// terminal DITOLAK. A rejection here means "recount, the figures do not add up" —
// a paper correction, not a business decision — and since the freeze exists, a
// terminal state that never released it would leave the room dead.
const (
	StatusStokOpnameDraft    = "DRAFT"
	StatusStokOpnameDiajukan = "DIAJUKAN"
	StatusStokOpnamePosted   = "POSTED"
	StatusStokOpnameBatal    = "BATAL"
)

// StokOpname maps the stok_opname header — a physical count session against one
// ruang, and the only document whose posting can write in both directions in one
// document without the two ever pairing up: some lines surplus, some deficit,
// each standing on its own.
//
// The primary key column is idstok_opname, not id — the one table in this project
// named that way. It already carries foreign keys pointing at it and there is no
// value in a rename migration to "tidy" it; the entity field stays ID like
// everywhere else, and only the repository's SQL has to know the column is
// spelled differently.
//
// While Status is DRAFT or DIAJUKAN, IDRuang is frozen: the kartu_stok trigger
// (migration 000023) refuses every posting naming this room from any module,
// except this document's own adjustment rows. See "Pembekuan ruang selama
// opname berjalan" in CLAUDE.md.
type StokOpname struct {
	ID       int64
	Nomor    string
	IDRuang  int64
	TglBuka  time.Time
	TglTutup *time.Time
	// TsCutoff is the instant TarikSaldo's snapshot is taken from, filled by the
	// server at Create from now() and never from the request body — a
	// client-supplied cutoff would be a selisih the client gets to choose.
	TsCutoff  time.Time
	UraianSO  *string
	Status    string
	CreatedBy int64
	CreatedAt time.Time

	// VerifiedBy and TsVerified are written by BOTH Posting and Tolak — the
	// schema carries no separate "who rejected it" column, the same reuse
	// entity.Pemakaian makes of DisetujuiOleh/TsDisetujui.
	VerifiedBy *int64
	TsVerified *time.Time
	PostedAt   *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string
	TsBatal        *time.Time

	// Not columns of stok_opname. Filled by the read queries.
	NamaRuang string
	// IDUnitKerjaRuang is IDRuang's own unit_kerja — isu #12 fase 6.
	IDUnitKerjaRuang int64
	Detail           []StokOpnameDetail
}

// StokOpnameDetail maps one counted line: one product in the header's one room,
// the system balance at cutoff, and the physical count against it.
//
// StokSO nil is not the same fact as StokSO pointing at zero. Nil means "not
// counted yet" and is skipped everywhere — at posting and in the selisih
// arithmetic alike; reading it as zero would erase that product's entire
// recorded stock the moment its shelf simply had not been reached yet.
type StokOpnameDetail struct {
	ID           int64
	IDStokOpname int64
	IDBarang     int64
	IDRuang      int64
	// StokAwal is the system balance at TsCutoff, copied once by TarikSaldo (or
	// by a manual add going through the same lookup) and never touched again.
	// The selisih below is always computed against this frozen figure, never
	// against whatever the balance happens to be at posting time — see
	// "Jebakan utama" in CLAUDE.md.
	StokAwal int64
	// StokSO is nil until counted. See the type comment.
	StokSO *int64
	// StokSelisihLebih and StokSelisihKurang are always recomputed from StokSO
	// and StokAwal, never written from a form — the same rule status_pembayaran
	// follows. stok_opname_detail_selisih_check forbids both being positive at
	// once: direction is a fact, not a sign that can get lost on a sum.
	StokSelisihLebih  int64
	StokSelisihKurang int64
	Keterangan        *string
	// IDKartuStokCutoff is NOT NULL: every line must point at a real kartu_stok
	// row as its system-balance reference. A (product, room) pair with no such
	// row has never moved through this room at all and cannot be counted here —
	// see "Barang yang sistem belum pernah lihat" in CLAUDE.md.
	IDKartuStokCutoff int64
	// IDKartuStokPenyesuaian is nil until posting, and stays nil forever for a
	// line whose selisih came out zero — kartu_stok must never carry a row that
	// moved nothing.
	IDKartuStokPenyesuaian *int64
	UpdatedBy              *int64
	TsUpdate               *time.Time

	// Not columns of stok_opname_detail. Filled by the read query.
	KodeBarang      string
	NamaProduct     string
	NamaSatuanDasar string
}
