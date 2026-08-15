package model

import "time"

// PembelianResponse is one purchase document.
//
// Every money and quantity figure is a decimal string, not a number: NUMERIC(20,2)
// through a JSON float loses cents, and these become a payable and an inventory
// valuation. Clients should treat them as strings and use a decimal type.
//
// biaya_angkut is reported but is not part of total. Total is what the supplier is
// owed; freight is the carrier's bill and reaches the books as alokasi_biaya on the
// lines instead.
type PembelianResponse struct {
	ID      int64     `json:"id"`
	Nomor   string    `json:"nomor"`
	Tanggal time.Time `json:"tanggal"`

	IDSupplier       int64   `json:"id_supplier"`
	NamaSupplier     string  `json:"nama_supplier,omitempty"`
	IDRuang          int64   `json:"id_ruang"`
	NamaRuang        string  `json:"nama_ruang,omitempty"`
	NoFakturSupplier *string `json:"no_faktur_supplier"`
	TanggalFaktur    *string `json:"tanggal_faktur"`

	Subtotal       string `json:"subtotal"`
	DiskonNota     string `json:"diskon_nota"`
	PPN            string `json:"ppn"`
	PPNDikreditkan bool   `json:"ppn_dikreditkan"`
	Pembulatan     string `json:"pembulatan"`
	Total          string `json:"total"`

	IDEkspedisi         *int64  `json:"id_ekspedisi"`
	NoResi              *string `json:"no_resi"`
	TotalKoli           *string `json:"total_koli"`
	TarifPerKoli        *string `json:"tarif_per_koli"`
	BiayaAngkut         string  `json:"biaya_angkut"`
	DitanggungSupplier  bool    `json:"ditanggung_supplier"`
	MetodeAlokasiAngkut string  `json:"metode_alokasi_angkut"`

	JenisPembayaran  string `json:"jenis_pembayaran"`
	StatusPembayaran string `json:"status_pembayaran"`
	StatusPenerimaan string `json:"status_penerimaan"`
	Status           string `json:"status"`

	// Three payable figures, none of them columns of pembelian:
	//
	//	jumlah_dialokasikan -- paid against this invoice by POSTED payments. An uncashed
	//	                       giro is not a payment, so it is not counted here.
	//	nilai_kredit_retur  -- credited back by POSTED returns. Not the same as a
	//	                       return's `total`, which is at cost and includes freight.
	//	sisa_utang          -- total - the two above: what the supplier is still owed.
	//
	// status_pembayaran is the cache over sisa_utang, and these are what it is computed
	// from — reported alongside it so a screen can show why it says what it says.
	JumlahDialokasikan string `json:"jumlah_dialokasikan"`
	NilaiKreditRetur   string `json:"nilai_kredit_retur"`
	SisaUtang          string `json:"sisa_utang"`

	// Detail is filled on detail reads only; a list would need a query per row.
	// Always an array when present, never null.
	Detail []PembelianDetailResponse `json:"detail,omitempty"`

	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`

	DiajukanOleh  *int64     `json:"diajukan_oleh"`
	DiajukanPada  *time.Time `json:"diajukan_pada"`
	DisetujuiOleh *int64     `json:"disetujui_oleh"`
	DisetujuiPada *time.Time `json:"disetujui_pada"`
	PostedAt      *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
	AlasanTolak    *string `json:"alasan_tolak"`
}

// PembelianDetailResponse is one invoice line.
//
// Four quantities, in two pairs. qty_faktur/qty_dasar is what the paper says and
// drives the payable; qty_diterima/qty_diterima_dasar is what came out of the box
// and drives stock. The *_dasar half of each pair is in the product's base unit.
//
// Five derived quantities, and they are not the same question:
//
//	selisih_dasar     = qty_dasar - qty_diterima_dasar   -- short on the first delivery
//	qty_susulan_dasar = sum of POSTED follow-up receipts -- turned up later
//	sisa_dasar        = selisih_dasar - qty_susulan_dasar -- still owed today
//	qty_retur_dasar   = sum of POSTED returns            -- went back to the supplier
//	qty_dapat_diretur = received + susulan - retur       -- may still go back
//
// selisih_dasar never changes once the document is posted; sisa_dasar shrinks as
// follow-up receipts arrive. Reported rather than left to the client to subtract,
// because the whole receiving screen is built around them.
//
// The last two are a separate axis from the first three, and mixing them is the
// mistake to avoid: goods returned were still received, so a return neither reopens
// what the supplier owes nor entitles anyone to a follow-up shipment. sisa_dasar and
// qty_dapat_diretur can be nonzero at the same time on the same line.
type PembelianDetailResponse struct {
	ID          int64  `json:"id"`
	IDProduct   int64  `json:"id_product"`
	KodeBarang  string `json:"kode_barang,omitempty"`
	NamaProduct string `json:"nama_product,omitempty"`

	QtyFaktur        string `json:"qty_faktur"`
	QtyDiterima      string `json:"qty_diterima"`
	IDSatuanInput    int64  `json:"id_satuan_input"`
	NamaSatuan       string `json:"nama_satuan,omitempty"`
	FaktorKonversi   int64  `json:"faktor_konversi"`
	QtyDasar         int64  `json:"qty_dasar"`
	QtyDiterimaDasar int64  `json:"qty_diterima_dasar"`
	QtySusulanDasar  int64  `json:"qty_susulan_dasar"`
	SelisihDasar     int64  `json:"selisih_dasar"`
	SisaDasar        int64  `json:"sisa_dasar"`
	QtyReturDasar    int64  `json:"qty_retur_dasar"`
	QtyDapatDiretur  int64  `json:"qty_dapat_diretur"`
	NamaSatuanDasar  string `json:"nama_satuan_dasar,omitempty"`

	HargaSatuanInput string `json:"harga_satuan_input"`
	DiskonBaris      string `json:"diskon_baris"`
	Subtotal         string `json:"subtotal"`
	JumlahKoli       string `json:"jumlah_koli"`
	AlokasiBiaya     string `json:"alokasi_biaya"`
	// Null until the document is posted — unknown during DRAFT, not missing.
	HargaPokokSatuanDasar *string `json:"harga_pokok_satuan_dasar"`
	KeteranganSelisih     *string `json:"keterangan_selisih"`
}

// SisaPembelianResponse lists the lines a supplier has not fully delivered.
//
// This is the working list for chasing a follow-up shipment over WhatsApp, and the
// input for the penerimaan_susulan document that isu #4 fase 3 adds.
type SisaPembelianResponse struct {
	ID               int64                   `json:"id"`
	Nomor            string                  `json:"nomor"`
	IDSupplier       int64                   `json:"id_supplier"`
	NamaSupplier     string                  `json:"nama_supplier,omitempty"`
	Status           string                  `json:"status"`
	StatusPenerimaan string                  `json:"status_penerimaan"`
	Baris            []SisaPembelianBarisRow `json:"baris"`
}

// SisaPembelianBarisRow is one under-delivered line.
type SisaPembelianBarisRow struct {
	IDPembelianDetail int64   `json:"id_pembelian_detail"`
	IDProduct         int64   `json:"id_product"`
	KodeBarang        string  `json:"kode_barang,omitempty"`
	NamaProduct       string  `json:"nama_product,omitempty"`
	QtyDasar          int64   `json:"qty_dasar"`
	QtyDiterimaDasar  int64   `json:"qty_diterima_dasar"`
	QtySusulanDasar   int64   `json:"qty_susulan_dasar"`
	SisaDasar         int64   `json:"sisa_dasar"`
	NamaSatuanDasar   string  `json:"nama_satuan_dasar,omitempty"`
	KeteranganSelisih *string `json:"keterangan_selisih"`
}

// CreatePembelianRequest opens a draft with its lines.
//
// The document is always created as DRAFT whatever the caller says — status is not
// in this DTO at all. Stock moves at posting and only at posting, and posting is a
// separate, differently authorized call.
//
// ActorID is filled from the session by the controller, never from the body:
// pembelian.created_by is NOT NULL and letting a caller name it would let anyone
// write history under someone else's id.
type CreatePembelianRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the body — isu #12 fase 5. Nil means the active
	// grant applies to every unit, in which case id_ruang is not restricted;
	// otherwise id_ruang must belong to this unit or the write is refused
	// 403, checked by periksaRuangUnitAktif.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal          string  `json:"tanggal" validate:"required,datetime=2006-01-02"`
	IDSupplier       int64   `json:"id_supplier" validate:"required,gt=0"`
	IDRuang          int64   `json:"id_ruang" validate:"required,gt=0"`
	NoFakturSupplier *string `json:"no_faktur_supplier" validate:"omitempty,max=64"`
	TanggalFaktur    *string `json:"tanggal_faktur" validate:"omitempty,datetime=2006-01-02"`

	// Money arrives as decimal strings. Empty means zero.
	DiskonNota     string `json:"diskon_nota" validate:"omitempty,numeric,max=21"`
	PPN            string `json:"ppn" validate:"omitempty,numeric,max=21"`
	PPNDikreditkan bool   `json:"ppn_dikreditkan"`
	// Pembulatan may be negative — it is the nota's rounding line, either way.
	Pembulatan string `json:"pembulatan" validate:"omitempty,numeric,max=21"`

	IDEkspedisi  *int64  `json:"id_ekspedisi" validate:"omitempty,gt=0"`
	NoResi       *string `json:"no_resi" validate:"omitempty,max=64"`
	TotalKoli    *string `json:"total_koli" validate:"omitempty,numeric,max=13"`
	TarifPerKoli *string `json:"tarif_per_koli" validate:"omitempty,numeric,max=21"`
	// DitanggungSupplier true means freight is already inside the nota and must not
	// be allocated a second time.
	DitanggungSupplier  bool   `json:"ditanggung_supplier"`
	MetodeAlokasiAngkut string `json:"metode_alokasi_angkut" validate:"omitempty,oneof=KOLI QTY"`
	JenisPembayaran     string `json:"jenis_pembayaran" validate:"omitempty,oneof=TUNAI KREDIT"`

	Detail []PembelianDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

// PembelianDetailRequest is one line as the receiving desk types it.
//
// QtyDiterima may be omitted, and then it equals QtyFaktur — the ordinary case
// where everything on the paper was in the box. Sending it explicitly is how a
// short delivery is recorded, and KeteranganSelisih becomes required when the two
// differ: that note is what the operator reads back when chasing the supplier.
//
// More than the invoice quantity is rejected. Goods that were never invoiced have
// no value, so the proportional nilai_masuk would exceed the invoice and the moving
// average would be permanently wrong.
type PembelianDetailRequest struct {
	IDProduct     int64 `json:"id_product" validate:"required,gt=0"`
	IDSatuanInput int64 `json:"id_satuan_input" validate:"required,gt=0"`

	QtyFaktur   string  `json:"qty_faktur" validate:"required,numeric,max=23"`
	QtyDiterima *string `json:"qty_diterima" validate:"omitempty,numeric,max=23"`

	HargaSatuanInput string `json:"harga_satuan_input" validate:"required,numeric,max=21"`
	DiskonBaris      string `json:"diskon_baris" validate:"omitempty,numeric,max=21"`
	// JumlahKoli is decimal: one line may occupy part of a koli. Filled by the
	// warehouse while unpacking, so it is normally zero at first entry.
	JumlahKoli        string  `json:"jumlah_koli" validate:"omitempty,numeric,max=13"`
	KeteranganSelisih *string `json:"keterangan_selisih" validate:"omitempty,max=500"`
}

// GetPembelianRequest backs both Get and Sisa — both are pure reads keyed on
// the same id.
type GetPembelianRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the request — isu #12 fase 6. Nil means the
	// active grant applies everywhere; otherwise a document whose id_ruang
	// falls outside this unit answers 404, the same as one that does not
	// exist.
	AktifIDUnitKerja *int64 `json:"-"`
}

// UpdatePembelianRequest patches the header of a DRAFT.
//
// nomor, id_supplier, and id_ruang are absent by design. The number identifies the
// document; the supplier is who the payable belongs to; the room is where every
// line lands, and changing it would point the whole document at a different stock
// balance. Cancel and re-enter instead.
//
// Nothing here may be patched once the document leaves DRAFT — the usecase enforces
// that, not the DTO.
type UpdatePembelianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal          Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	NoFakturSupplier Optional[string] `json:"no_faktur_supplier" validate:"omitempty,max=64"`
	TanggalFaktur    Optional[string] `json:"tanggal_faktur" validate:"omitempty,datetime=2006-01-02"`

	DiskonNota     Optional[string] `json:"diskon_nota" validate:"omitempty,numeric,max=21"`
	PPN            Optional[string] `json:"ppn" validate:"omitempty,numeric,max=21"`
	PPNDikreditkan Optional[bool]   `json:"ppn_dikreditkan"`
	Pembulatan     Optional[string] `json:"pembulatan" validate:"omitempty,numeric,max=21"`

	IDEkspedisi         Optional[int64]  `json:"id_ekspedisi" validate:"omitempty,gt=0"`
	NoResi              Optional[string] `json:"no_resi" validate:"omitempty,max=64"`
	TotalKoli           Optional[string] `json:"total_koli" validate:"omitempty,numeric,max=13"`
	TarifPerKoli        Optional[string] `json:"tarif_per_koli" validate:"omitempty,numeric,max=21"`
	DitanggungSupplier  Optional[bool]   `json:"ditanggung_supplier"`
	MetodeAlokasiAngkut Optional[string] `json:"metode_alokasi_angkut" validate:"omitempty,oneof=KOLI QTY"`
	JenisPembayaran     Optional[string] `json:"jenis_pembayaran" validate:"omitempty,oneof=TUNAI KREDIT"`
}

// ReplacePembelianDetailRequest swaps the whole line set of a DRAFT.
//
// Wholesale replacement rather than per-line CRUD: the lines of a document are one
// thing, retyped together off one piece of paper, and a partial edit would leave
// the header's totals disagreeing with the lines between requests.
type ReplacePembelianDetailRequest struct {
	ID      int64                    `json:"-" validate:"required,gt=0"`
	ActorID int64                    `json:"-" validate:"required,gt=0"`
	Detail  []PembelianDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

// AjukanPembelianRequest hands a draft over for approval. It carries no fields of
// its own: everything it needs is already on the document.
type AjukanPembelianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// TolakPembelianRequest sends a submission back to DRAFT.
//
// Alasan is required. A rejection with no reason gives the operator nothing to fix,
// and the note is the only channel back — the approver and the operator are not
// necessarily in the same room.
type TolakPembelianRequest struct {
	ID      int64  `json:"-" validate:"required,gt=0"`
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Alasan  string `json:"alasan" validate:"required,max=500"`
}

// PostingPembelianRequest approves a submission and writes it to kartu_stok.
type PostingPembelianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// BatalPembelianRequest voids a posted document by writing reversing kartu_stok
// rows. AlasanBatal is required: a reversal that nobody explained is indisting-
// uishable from a mistake, and kartu_stok keeps both forever.
type BatalPembelianRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

// BagiRataKoliRequest splits total_koli across the lines in proportion to
// qty_dasar, so the warehouse does not have to type a share per line when the
// shipment was packed evenly. The parts sum to exactly total_koli.
type BagiRataKoliRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// ListPembelianRequest filters the document list.
//
// TanggalDari and TanggalSampai are both inclusive whole days.
type ListPembelianRequest struct {
	PageRequest
	Search           string  `query:"search" validate:"omitempty,max=255"`
	Status           string  `query:"status" validate:"omitempty,oneof=DRAFT DIAJUKAN POSTED BATAL"`
	StatusPenerimaan string  `query:"status_penerimaan" validate:"omitempty,oneof=LENGKAP KURANG"`
	IDSupplier       int64   `query:"id_supplier" validate:"omitempty,gt=0"`
	TanggalDari      *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai    *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`

	// AktifIDUnitKerja, same rule as GetPembelianRequest.
	AktifIDUnitKerja *int64 `query:"-"`
}
