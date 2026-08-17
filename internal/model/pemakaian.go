package model

import "time"

// PemakaianResponse is one internal-usage request: goods leaving for repair, office
// supplies, or a sample, with no supplier, no customer, and no destination room.
//
// disetujui_oleh, ts_disetujui, and catatan_persetujuan double as the reviewer, the
// review time, and the note for a rejection too, not only an approval — the schema
// carries no separate column for "who rejected it".
type PemakaianResponse struct {
	ID        int64     `json:"id"`
	Nomor     string    `json:"nomor"`
	Tanggal   time.Time `json:"tanggal"`
	IDRuang   int64     `json:"id_ruang"`
	NamaRuang string    `json:"nama_ruang,omitempty"`

	IDPemohon   int64  `json:"id_pemohon"`
	NamaPemohon string `json:"nama_pemohon,omitempty"`
	Keperluan   string `json:"keperluan"`
	Status      string `json:"status"`

	DisetujuiOleh      *int64     `json:"disetujui_oleh"`
	TsDisetujui        *time.Time `json:"ts_disetujui"`
	CatatanPersetujuan *string    `json:"catatan_persetujuan"`

	// Null until POSTED — unknown during every earlier state, not missing. It is the
	// sum of every line's hpp_total, which itself does not exist before posting.
	TotalHPP *string `json:"total_hpp"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []PemakaianDetailResponse `json:"detail,omitempty"`

	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	PostedAt  *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
}

// PemakaianDetailResponse is one line: a product, what was asked for, and — once
// reviewed — how much may actually leave.
//
// qty_dasar and qty_disetujui_dasar answer different questions. qty_dasar is
// planning data, frozen the moment the line is typed. qty_disetujui_dasar is nil
// until Setujui and is what posting actually reads; 0 means the line was refused on
// its own even though the document as a whole was approved.
type PemakaianDetailResponse struct {
	ID          int64  `json:"id"`
	IDProduct   int64  `json:"id_product"`
	KodeBarang  string `json:"kode_barang,omitempty"`
	NamaProduct string `json:"nama_product,omitempty"`

	QtyInput        string `json:"qty_input"`
	IDSatuanInput   int64  `json:"id_satuan_input"`
	NamaSatuan      string `json:"nama_satuan,omitempty"`
	FaktorKonversi  int64  `json:"faktor_konversi"`
	QtyDasar        int64  `json:"qty_dasar"`
	NamaSatuanDasar string `json:"nama_satuan_dasar,omitempty"`

	QtyDisetujuiDasar *int64 `json:"qty_disetujui_dasar"`
	// Both null until the document is posted.
	HPPSatuanDasar *string `json:"hpp_satuan_dasar"`
	HPPTotal       *string `json:"hpp_total"`
	Keterangan     *string `json:"keterangan"`
}

// CreatePemakaianRequest opens a DRAFT with its lines.
//
// The document is always created as DRAFT — status is not in this DTO. Stock moves
// only at posting, and posting is a separate, differently authorized call three
// transitions later.
type CreatePemakaianRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the body — isu #21 fase 2. Nil means the active
	// grant applies everywhere, so nothing is checked.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal   string `json:"tanggal" validate:"required,datetime=2006-01-02"`
	IDRuang   int64  `json:"id_ruang" validate:"required,gt=0"`
	IDPemohon int64  `json:"id_pemohon" validate:"required,gt=0"`
	Keperluan string `json:"keperluan" validate:"required,max=1000"`

	Detail []PemakaianDetailRequest `json:"detail" validate:"omitempty,max=500,dive"`
}

// PemakaianDetailRequest is one line: what is being asked for, and how much.
//
// Unlike mutasi there is no per-line quota drawn from a parent, so the same product
// may legitimately appear on more than one line — 1 DUS for the workshop, 3 PCS for
// the office, each with its own Keterangan.
type PemakaianDetailRequest struct {
	IDProduct     int64   `json:"id_product" validate:"required,gt=0"`
	IDSatuanInput int64   `json:"id_satuan_input" validate:"required,gt=0"`
	QtyInput      string  `json:"qty_input" validate:"required,numeric,max=23"`
	Keterangan    *string `json:"keterangan" validate:"omitempty,max=500"`
}

type GetPemakaianRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the request — isu #21 fase 2. Nil means
	// unrestricted, otherwise a document whose id_ruang falls outside this
	// unit answers 404.
	AktifIDUnitKerja *int64 `json:"-"`
}

// ListPemakaianRequest filters the document list.
//
// status=DIAJUKAN with terlama_dulu=true is the approval queue: it is what the
// approver works through, oldest request first, the same shape
// GET /supplier/{id}/utang uses for its own queue.
type ListPemakaianRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT DIAJUKAN DISETUJUI DITOLAK POSTED BATAL"`
	IDRuang       int64   `query:"id_ruang" validate:"omitempty,gt=0"`
	IDPemohon     int64   `query:"id_pemohon" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
	TerlamaDulu   bool    `query:"terlama_dulu"`

	// AktifIDUnitKerja, same rule as GetPemakaianRequest — isu #21 fase 2.
	AktifIDUnitKerja *int64 `query:"-"`
}

// UpdatePemakaianRequest patches the header of a DRAFT. Every field is a NOT NULL
// column, so an explicit null is rejected rather than silently ignored — same rule
// UpdateMutasiRequest follows.
type UpdatePemakaianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja, same rule as CreatePemakaianRequest: only checked when
	// id_ruang is actually being changed, and only against the new value.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal   Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	IDRuang   Optional[int64]  `json:"id_ruang" validate:"omitempty,gt=0"`
	IDPemohon Optional[int64]  `json:"id_pemohon" validate:"omitempty,gt=0"`
	Keperluan Optional[string] `json:"keperluan" validate:"omitempty,max=1000"`
}

// ReplacePemakaianDetailRequest swaps the whole line set of a DRAFT, for the same
// reason every other document does: the lines are one thing, typed and counted
// together, and a partial edit leaves the document disagreeing with itself between
// requests.
type ReplacePemakaianDetailRequest struct {
	ID      int64                    `json:"-" validate:"required,gt=0"`
	ActorID int64                    `json:"-" validate:"required,gt=0"`
	Detail  []PemakaianDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

// AjukanPemakaianRequest hands a draft to the approver. It carries no fields of its
// own: everything it needs is already on the document.
type AjukanPemakaianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// SetujuiPemakaianRequest approves a submission and decides how much of each line
// may actually leave.
//
// Detail is optional and per-line: a line the approver does not mention defaults to
// its own qty_dasar in full — the ordinary case, where everything asked for is
// granted. Naming a line is how the approver trims it, and 0 refuses that line on
// its own while the rest of the document is still approved.
type SetujuiPemakaianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Catatan *string                         `json:"catatan" validate:"omitempty,max=1000"`
	Detail  []SetujuiPemakaianDetailRequest `json:"detail" validate:"omitempty,max=500,dive"`
}

// SetujuiPemakaianDetailRequest trims one line. QtyDisetujuiDasar is validated
// against that line's own qty_dasar in the usecase, since the ceiling differs per
// row and cannot be expressed as a struct tag.
type SetujuiPemakaianDetailRequest struct {
	IDDetail          int64 `json:"id_detail" validate:"required,gt=0"`
	QtyDisetujuiDasar int64 `json:"qty_disetujui_dasar" validate:"gte=0"`
}

// TolakPemakaianRequest refuses a submission outright. Unlike pembelian this is
// terminal — see entity.StatusPemakaianDitolak — so there is no return to DRAFT.
// Alasan is required and is stored in catatan_persetujuan, the only note column the
// schema carries for a decision either way.
type TolakPemakaianRequest struct {
	ID      int64  `json:"-" validate:"required,gt=0"`
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Alasan  string `json:"alasan" validate:"required,max=1000"`
}

// PostingPemakaianRequest records that the approved goods actually left, and writes
// kartu_stok.
type PostingPemakaianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// BatalPemakaianRequest voids a posted document by writing reversing kartu_stok
// rows. AlasanBatal is required, same rule as every other document that reverses
// itself: a reversal nobody explained is indistinguishable from a mistake, and
// kartu_stok keeps both forever.
type BatalPemakaianRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}
