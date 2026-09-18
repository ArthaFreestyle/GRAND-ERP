package repository

import (
	"context"
	"errors"

	"Arthafreestyle/ERP/internal/entity"
)

// ErrModelGeminiTidakDitemukan reports that the configured gemini.model answered
// 404 — a model Google has retired. Wrapped rather than returned bare so the
// usecase can log the model name at Error level (isu #39): a plain 500 here would
// hide exactly the config key that needs to change.
var ErrModelGeminiTidakDitemukan = errors.New("model gemini tidak ditemukan")

// JenisOCRFaktur is which of the two OCR endpoints produced a photo (isu #39). It is
// the only thing that changes how a line's qty_diterima is decided:
// FAKTUR_KEDATANGAN reads the centang convention, NOTA does not.
type JenisOCRFaktur string

const (
	JenisOCRFakturKedatangan JenisOCRFaktur = "FAKTUR_KEDATANGAN"
	JenisOCRNota             JenisOCRFaktur = "NOTA"
)

// FakturReaderBaris is one line Gemini read off the photo, before any of the
// usecase's own checks run (id_product against the catalog, qty x faktor whole,
// over-delivery clipped, and so on). Every number is a string exactly as the model
// answered it — parsing into big.Rat happens in the usecase, never here, so this
// type carries no arithmetic opinion of its own.
type FakturReaderBaris struct {
	Urutan     int64
	TeksAsli   string
	KodeVendor *string
	// IDProduct nil means Gemini itself was not confident enough to name a
	// product from the catalog. That is the expected shape for an unrecognized
	// line, not an error.
	IDProduct     *int64
	SatuanTerbaca *string
	IDSatuan      *int64
	Qty           *string
	HargaSatuan   *string
	DiskonBaris   *string
	// Dicentang is only meaningful for JenisOCRFakturKedatangan: true, false, or
	// nil for "illegible" — the model is instructed to leave it nil for NOTA
	// rather than guess at a convention that document has no notion of.
	Dicentang        *bool
	QtyTulisanTangan *string
}

// FakturReaderHasil is Gemini's whole structured answer for one photo: the header
// figures it read plus every line, recognized or not.
type FakturReaderHasil struct {
	NoFaktur      *string
	TanggalFaktur *string
	NamaSupplier  *string
	DiskonNota    *string
	PPN           *string
	Total         *string
	TandaLunas    *string
	Baris         []FakturReaderBaris

	// Model, PromptTokenCount, and CandidatesTokenCount are for the log line and
	// the ocr.model field, never for a business decision — the fase 1 decision to
	// log token usage rather than budget it.
	Model                string
	PromptTokenCount     int32
	CandidatesTokenCount int32
}

// FakturReader is the OCR upstream — Gemini today, an interface for the same reason
// DokumenStorage is one: it is access to something outside this process, and it sits
// in the repository layer so no usecase test ever has to call it for real. Every
// usecase test hands in a fake.
type FakturReader interface {
	// Baca reads one photographed faktur or nota. katalog is the caller's active
	// product catalog, sent as the system prompt so Gemini answers every line
	// with an id_product from it rather than inventing one from a vendor's own
	// code.
	Baca(
		ctx context.Context, gambar []byte, mime string, jenis JenisOCRFaktur, katalog []entity.Product,
	) (*FakturReaderHasil, error)
}
