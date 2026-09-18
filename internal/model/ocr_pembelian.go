package model

import "io"

// OCRPembelianRequest carries one multipart photo through to the usecase (isu #39).
//
// Gambar is an io.Reader rather than a *multipart.FileHeader, the same reason
// UploadDokumenRequest's Berkas is: the usecase layer must not know it is fed by an
// HTTP form. UkuranDilaporkan is what the multipart header claims, used only to
// refuse an obviously oversized upload before a byte reaches Gemini — the limit that
// actually holds is enforced while streaming.
type OCRPembelianRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the form — isu #12 fase 5, the same check
	// CreatePembelianRequest carries. Checked before a single line is read off
	// the photo, so a caller who corrects forty lines is not refused 403 only
	// at submit.
	AktifIDUnitKerja *int64 `json:"-"`

	Gambar           io.Reader `json:"-" validate:"-"`
	UkuranDilaporkan int64     `json:"-"`

	IDSupplier int64  `json:"-" validate:"required,gt=0"`
	IDRuang    int64  `json:"-" validate:"required,gt=0"`
	Tanggal    string `json:"-" validate:"omitempty,datetime=2006-01-02"`
}

// OCRPembelianResponse is what an OCR endpoint answers. Nothing was written by the
// call that produced it — Usulan is shaped exactly like CreatePembelianRequest and
// may be sent to POST /pembelian unmodified once a human has checked it; every
// explanatory field lives in OCR instead.
type OCRPembelianResponse struct {
	Usulan CreatePembelianRequest `json:"usulan"`
	OCR    OCRInfoResponse        `json:"ocr"`
}

// OCRInfoResponse is the read-only half of an OCR response: what Gemini reported,
// what this endpoint computed from it, and every warning that fell out along the
// way. Nothing here blocks the response — a warning is not a 400, because the whole
// point is that an unmodified Usulan should still submit, or the caller should learn
// why it will not before correcting forty lines by hand.
type OCRInfoResponse struct {
	Model           string  `json:"model"`
	SupplierTerbaca *string `json:"supplier_terbaca"`
	TotalTerbaca    *string `json:"total_terbaca"`
	// TotalDihitung is Usulan's own total, summed the same way POST /pembelian
	// would compute it — so a mismatch against TotalTerbaca is comparing two
	// numbers this response already carries, not asking the caller to re-derive
	// one of them.
	TotalDihitung string             `json:"total_dihitung"`
	Peringatan    []string           `json:"peringatan"`
	Baris         []OCRBarisResponse `json:"baris"`
}

// OCRBarisResponse maps one line on the photo to its place in Usulan.Detail.
//
// IndeksUsulan nil means the line was not recognized and has no entry in
// Usulan.Detail at all — TeksAsli and whatever else was read is still reported here
// so the line does not disappear silently and can be mapped by hand.
type OCRBarisResponse struct {
	Urutan       int64   `json:"urutan"`
	IndeksUsulan *int    `json:"indeks_usulan"`
	TeksAsli     string  `json:"teks_asli"`
	KodeVendor   *string `json:"kode_vendor"`
	KodeBarang   *string `json:"kode_barang,omitempty"`
	NamaProduct  *string `json:"nama_product,omitempty"`
	Qty          *string `json:"qty,omitempty"`
	Harga        *string `json:"harga,omitempty"`
}
