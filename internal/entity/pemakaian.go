package entity

import "time"

// Status pemakaian, aligned to POSTED/BATAL by migration 000021 — see
// entity.StatusMutasiPosted for the same vocabulary promise made by 000018 and kept
// there first.
//
// Two things set this state machine apart from every earlier one, and both are
// deliberate:
//
//   - DITOLAK is terminal, never returning to DRAFT. In pembelian a rejection means
//     "the paper was mistyped, fix it" — an editing note. Here a rejection is a
//     business decision: the goods are not being handed over. Routing it back to
//     DRAFT would blur that decision into a revision and erase the only trace that
//     the request was ever refused. If the requester still wants the goods, they
//     submit a new request — and that is exactly what should be visible.
//   - DISETUJUI sits between DIAJUKAN and POSTED, and it is not a wasted state.
//     Approval decides HOW MUCH may leave; posting records that it actually did.
//     TsDisetujui and PostedAt are two separate columns precisely because the two
//     can fall on different days — approved today, handed over from the warehouse
//     tomorrow.
const (
	StatusPemakaianDraft     = "DRAFT"
	StatusPemakaianDiajukan  = "DIAJUKAN"
	StatusPemakaianDisetujui = "DISETUJUI"
	StatusPemakaianDitolak   = "DITOLAK"
	StatusPemakaianPosted    = "POSTED"
	StatusPemakaianBatal     = "BATAL"
)

// Pemakaian maps the pemakaian header: goods leaving for internal use — repair,
// office supplies, samples — with no counterparty at all. That absence is the shape
// of the module: no supplier, no customer, no destination room. What is left is a
// record of who asked, who allowed it, and for what.
//
// IDPemohon is not CreatedBy. An administrative clerk may type the request on
// behalf of a mechanic who holds no account of their own — CreatedBy records who
// typed it, IDPemohon who asked for the goods. DisetujuiOleh is checked against
// IDPemohon, not CreatedBy, by pemakaian_penyetuju_check — a requester may not
// approve their own request, and the database enforces it independently of
// PemakaianUseCase.Setujui/Tolak.
//
// DisetujuiOleh and TsDisetujui double as the reviewer and the review time for a
// rejection too, not only an approval: the schema carries no separate ditolak_oleh
// column, and disetujui_oleh is the only actor column a decision — either way — has
// to record itself against.
type Pemakaian struct {
	ID        int64
	Nomor     string
	Tanggal   time.Time
	IDRuang   int64
	IDPemohon int64
	Keperluan string
	Status    string

	DisetujuiOleh      *int64
	TsDisetujui        *time.Time
	CatatanPersetujuan *string

	// TotalHPP is nullable and only ever answers something once POSTED: it sums
	// HPPTotal off every line, and HPPTotal itself does not exist until the trigger
	// reports it back through Insert's RETURNING. It is the one figure that answers
	// "this internal usage consumed how much" for the month.
	TotalHPP *string

	CreatedBy int64
	CreatedAt time.Time
	PostedAt  *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string

	// Not columns of pemakaian. Filled by the read queries.
	NamaRuang   string
	NamaPemohon string
	Detail      []PemakaianDetail
}

// PemakaianDetail maps one line: one product, what was asked for, and — once
// reviewed — how much of it may actually leave.
//
// QtyDasar and QtyDisetujuiDasar answer different questions and must not be
// collapsed. QtyDasar is planning data: what was requested, frozen the moment the
// line is typed and never touched again. QtyDisetujuiDasar is what posting actually
// reads, filled only at Setujui and never at Create — filling it earlier would let a
// requester approve their own request through the back door. Zero means the line
// was refused even though the document as a whole was approved; nil means no
// decision has been made yet.
type PemakaianDetail struct {
	ID             int64
	IDPemakaian    int64
	IDProduct      int64
	QtyInput       string
	IDSatuanInput  int64
	FaktorKonversi int64
	QtyDasar       int64

	// QtyDisetujuiDasar is nil until Setujui, and 0 afterwards means the line was
	// refused on its own — see PemakaianUseCase.Posting for how a zero line is
	// skipped rather than inserted as a movement of nothing.
	QtyDisetujuiDasar *int64

	// HPPSatuanDasar and HPPTotal are filled at posting from the trigger's own
	// RETURNING, the same rule mutasi_detail.HargaPokokSatuanDasar follows — the
	// application cannot know the moving average before the insert, so it copies
	// back what the database actually recorded rather than computing anything.
	HPPSatuanDasar *string
	HPPTotal       *string
	Keterangan     *string

	// Not columns of pemakaian_detail. Filled by the read query.
	KodeBarang      string
	NamaProduct     string
	NamaSatuan      string
	NamaSatuanDasar string
}
