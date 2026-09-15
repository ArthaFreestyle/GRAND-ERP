package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// ocrMimeDiizinkan is narrower than dokumen's own mimeDiizinkan (isu #16): an OCR
// call only ever looks at a photo, never a PDF, and accepts webp on top of
// jpeg/png — a format phone cameras commonly produce that the attachment module has
// no reason to allow.
var ocrMimeDiizinkan = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// OCRPembelianUseCase reads a photographed faktur or nota and proposes a
// CreatePembelianRequest for a human to check — isu #39. It writes nothing: no
// pembelian, no document_counter, no dokumen row, no kartu_stok. Its only side
// effect is the call to Gemini.
//
// ProductRepository supplies the active catalog Gemini is told to map every line
// against — FindKatalogOCR. PembelianRepository is borrowed for exactly one read,
// the duplicate no_faktur_supplier warning, the same narrow-borrow shape
// ProductUseCase already has of PembelianRepository for riwayat-beli. RuangRepository
// backs periksaRuangUnitAktif, the same isu #12 fase 5 check
// PembelianUseCase.Create itself goes through — checked here first so a caller who
// corrects forty lines is not refused 403 only at submit.
type OCRPembelianUseCase struct {
	Log                 *logrus.Logger
	DB                  *sql.DB
	Validate            *validator.Validate
	ProductRepository   *repository.ProductRepository
	PembelianRepository *repository.PembelianRepository
	RuangRepository     *repository.RuangRepository
	FakturReader        repository.FakturReader

	// MaxUkuranByte mirrors dokumen.max_size_mb — the issue's own decision to reuse
	// that limit rather than mint a second one.
	MaxUkuranByte int64
	// Timeout bounds the call to Gemini explicitly. Fiber v3's ctx.Context() is
	// never cancelled on client disconnect, so without this a slow or hung call
	// would run until Gemini itself gives up.
	Timeout time.Duration
}

func NewOCRPembelianUseCase(
	log *logrus.Logger,
	db *sql.DB,
	validate *validator.Validate,
	productRepository *repository.ProductRepository,
	pembelianRepository *repository.PembelianRepository,
	ruangRepository *repository.RuangRepository,
	fakturReader repository.FakturReader,
	maxUkuranByte int64,
	timeout time.Duration,
) *OCRPembelianUseCase {
	return &OCRPembelianUseCase{
		Log:                 log,
		DB:                  db,
		Validate:            validate,
		ProductRepository:   productRepository,
		PembelianRepository: pembelianRepository,
		RuangRepository:     ruangRepository,
		FakturReader:        fakturReader,
		MaxUkuranByte:       maxUkuranByte,
		Timeout:             timeout,
	}
}

// FakturKedatangan reads an arrival invoice a warehouse clerk has already checked
// off line by line against the physical delivery — see susunBarisOCR for how the
// centang convention decides qty_diterima.
func (c *OCRPembelianUseCase) FakturKedatangan(
	ctx context.Context, request *model.OCRPembelianRequest,
) (*model.OCRPembelianResponse, error) {
	return c.baca(ctx, request, repository.JenisOCRFakturKedatangan)
}

// Nota reads a vendor purchase nota, which carries no centang convention at all:
// every recognized line is read as fully received.
func (c *OCRPembelianUseCase) Nota(
	ctx context.Context, request *model.OCRPembelianRequest,
) (*model.OCRPembelianResponse, error) {
	return c.baca(ctx, request, repository.JenisOCRNota)
}

func (c *OCRPembelianUseCase) baca(
	ctx context.Context, request *model.OCRPembelianRequest, jenis repository.JenisOCRFaktur,
) (*model.OCRPembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// Checked before the photo is even opened: the whole point of validating
	// id_ruang up front (isu #12 fase 5) is that a caller with the wrong room
	// finds out before spending any effort on the correction screen, not only
	// once the corrected usulan reaches POST /pembelian.
	if err := periksaRuangUnitAktif(ctx, c.DB, c.RuangRepository, request.AktifIDUnitKerja, request.IDRuang); err != nil {
		return nil, err
	}

	if request.Gambar == nil {
		return nil, model.Invalid("file is required")
	}

	tanggal := request.Tanggal
	if tanggal == "" {
		tanggal = tanggalHargaJual(time.Now()).Format(dateOnly)
	}

	if request.UkuranDilaporkan > c.MaxUkuranByte {
		return nil, model.Invalid(fmt.Sprintf("file melebihi batas unggah %d byte", c.MaxUkuranByte))
	}

	gambar, mime, err := c.bacaGambar(request.Gambar)
	if err != nil {
		return nil, err
	}

	katalog, err := c.ProductRepository.FindKatalogOCR(ctx, c.DB)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	hasil, err := c.FakturReader.Baca(callCtx, gambar, mime, jenis, katalog)
	if err != nil {
		if errors.Is(err, repository.ErrModelGeminiTidakDitemukan) {
			c.Log.WithError(err).Error("ocr pembelian: model gemini tidak ditemukan, ganti gemini.model")
		} else {
			c.Log.WithError(err).Error("ocr pembelian: gemini gagal")
		}

		// No half-built usulan on a Gemini failure (timeout, quota, missing
		// model) — the issue's own fase 1 decision. A bare error becomes a
		// generic 500 via statusForKind; nothing here is the caller's mistake.
		return nil, fmt.Errorf("ocr pembelian: gagal membaca faktur: %w", err)
	}

	return c.susunUsulan(ctx, request, jenis, tanggal, katalog, hasil)
}

// bacaGambar reads the whole photo into memory and identifies its type from the
// bytes, never the multipart Content-Type header — the same discipline
// DokumenUseCase.Upload uses for an attachment, reusing that file's
// bacaKepala/sniffMime rather than re-implementing them.
//
// Unlike an attachment, a photo handed to this endpoint is never written to
// storage: it is read once, handed to Gemini, and discarded, so the whole limit is
// enforced with a single io.LimitReader rather than the tee-to-storage shape Upload
// needs.
func (c *OCRPembelianUseCase) bacaGambar(r io.Reader) ([]byte, string, error) {
	kepala, err := bacaKepala(r)
	if err != nil {
		return nil, "", err
	}

	mime := sniffMime(kepala)
	if !ocrMimeDiizinkan[mime] {
		return nil, "", model.Invalid("jenis berkas tidak didukung; hanya JPEG, PNG, dan WEBP")
	}

	// One byte past the limit is read on purpose, the same as Upload: it is how
	// "exactly at the limit" is told apart from "truncated here".
	sisa, err := io.ReadAll(io.LimitReader(r, c.MaxUkuranByte-int64(len(kepala))+1))
	if err != nil {
		return nil, "", fmt.Errorf("ocr pembelian: baca berkas: %w", err)
	}

	gambar := append(kepala, sisa...)
	if int64(len(gambar)) > c.MaxUkuranByte {
		return nil, "", model.Invalid(fmt.Sprintf("file melebihi batas unggah %d byte", c.MaxUkuranByte))
	}

	return gambar, mime, nil
}

// susunUsulan turns Gemini's raw answer into a CreatePembelianRequest plus every
// explanatory figure and warning. Nothing here rejects the call outright except a
// database error reading the duplicate-faktur check — every other problem becomes a
// peringatan, because the goal is that an unmodified usulan either submits cleanly
// or the caller already knows why it will not.
func (c *OCRPembelianUseCase) susunUsulan(
	ctx context.Context, request *model.OCRPembelianRequest, jenis repository.JenisOCRFaktur,
	tanggal string, katalog []entity.Product, hasil *repository.FakturReaderHasil,
) (*model.OCRPembelianResponse, error) {
	produk := make(map[int64]entity.Product, len(katalog))
	for _, p := range katalog {
		produk[p.ID] = p
	}

	var peringatan []string
	detail := make([]model.PembelianDetailRequest, 0, len(hasil.Baris))
	baris := make([]model.OCRBarisResponse, 0, len(hasil.Baris))
	subtotal := new(big.Rat)

	for _, b := range hasil.Baris {
		siap := susunBarisOCR(jenis, b, produk)
		peringatan = append(peringatan, siap.peringatan...)

		respon := model.OCRBarisResponse{Urutan: b.Urutan, TeksAsli: b.TeksAsli, KodeVendor: b.KodeVendor}

		if siap.detail == nil {
			// Unrecognized: qty/harga are passed through as raw text so the
			// screen has something to show while the caller maps the line by
			// hand, rather than the line vanishing with no trace.
			respon.Qty = b.Qty
			respon.Harga = b.HargaSatuan
			baris = append(baris, respon)

			continue
		}

		indeks := len(detail)
		respon.IndeksUsulan = &indeks

		if p, ok := produk[siap.detail.IDProduct]; ok {
			respon.KodeBarang = &p.KodeBarang
			respon.NamaProduct = &p.Nama
		}

		detail = append(detail, *siap.detail)
		baris = append(baris, respon)
		subtotal.Add(subtotal, siap.subtotalBaris)
	}

	diskonNota := angkaOpsional(hasil.DiskonNota, &peringatan, "diskon_nota")
	ppn := angkaOpsional(hasil.PPN, &peringatan, "ppn")

	total := new(big.Rat).Sub(subtotal, diskonNota)
	total.Add(total, ppn)
	total = roundNumeric(total, skalaUang)

	var totalTerbacaStr *string

	if hasil.Total != nil && strings.TrimSpace(*hasil.Total) != "" {
		if t, err := parseAngkaIndonesia(*hasil.Total); err == nil {
			t = roundNumeric(t, skalaUang)
			formatted := formatNumeric(t, skalaUang)
			totalTerbacaStr = &formatted

			if t.Cmp(total) != 0 {
				peringatan = append(peringatan, "total terbaca berbeda dari total dihitung")
			}
		} else {
			peringatan = append(peringatan, "total pada dokumen tidak terbaca sebagai angka yang valid")
		}
	}

	noFaktur := trimToNil(hasil.NoFaktur)

	if noFaktur != nil {
		nomor, err := c.PembelianRepository.FindNomorFakturSupplier(ctx, c.DB, request.IDSupplier, *noFaktur)
		if err != nil {
			return nil, err
		}

		if nomor != nil {
			peringatan = append(peringatan, fmt.Sprintf(
				"no_faktur_supplier %s sudah dipakai pembelian %s untuk supplier ini", *noFaktur, *nomor,
			))
		}
	}

	tanggalFaktur := tanggalFakturDariOCR(hasil.TanggalFaktur, &peringatan)

	var jenisPembayaran string

	if jenis == repository.JenisOCRNota {
		jenisPembayaran = jenisPembayaranDariTandaLunas(hasil.TandaLunas)
		if jenisPembayaran == "" {
			peringatan = append(peringatan, "jenis_pembayaran tidak terbaca dari nota, tidak ditebak")
		}
	}

	usulan := model.CreatePembelianRequest{
		Tanggal:          tanggal,
		IDSupplier:       request.IDSupplier,
		IDRuang:          request.IDRuang,
		NoFakturSupplier: noFaktur,
		TanggalFaktur:    tanggalFaktur,
		DiskonNota:       formatNumeric(diskonNota, skalaUang),
		PPN:              formatNumeric(ppn, skalaUang),
		Pembulatan:       "0",
		JenisPembayaran:  jenisPembayaran,
		Detail:           detail,
	}

	return &model.OCRPembelianResponse{
		Usulan: usulan,
		OCR: model.OCRInfoResponse{
			Model:           hasil.Model,
			SupplierTerbaca: trimToNil(hasil.NamaSupplier),
			TotalTerbaca:    totalTerbacaStr,
			TotalDihitung:   formatNumeric(total, skalaUang),
			Peringatan:      peringatan,
			Baris:           baris,
		},
	}, nil
}

// barisOCRSiap is one line's outcome: either a proposal detail line ready to append,
// or nil meaning the line stays unrecognized and out of usulan.detail entirely.
type barisOCRSiap struct {
	detail        *model.PembelianDetailRequest
	subtotalBaris *big.Rat
	peringatan    []string
}

// susunBarisOCR is the per-line decision: recognized or not, and — only for a
// recognized faktur-kedatangan line — how the centang convention decides
// qty_diterima. Kept apart from susunUsulan's bookkeeping (running subtotal, indeks
// assignment) so this piece, the one carrying every business rule, reads on its own.
func susunBarisOCR(
	jenis repository.JenisOCRFaktur, b repository.FakturReaderBaris, produk map[int64]entity.Product,
) barisOCRSiap {
	if b.IDProduct == nil {
		return barisOCRSiap{}
	}

	p, ada := produk[*b.IDProduct]
	if !ada {
		// A hallucinated id_product — one Gemini named that is not in the
		// catalog it was given. Treated as unrecognized rather than a 400: one
		// bad line must not throw out the whole faktur.
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: id_product hasil OCR tidak ada di katalog, baris tidak dikenali", b.Urutan,
		)}}
	}

	var faktor int64

	if b.IDSatuan != nil {
		for _, s := range p.Satuan {
			if s.IDSatuan == *b.IDSatuan {
				faktor = s.Faktor

				break
			}
		}
	}

	if faktor == 0 {
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: satuan hasil OCR tidak terdaftar untuk produk ini, baris tidak dikenali", b.Urutan,
		)}}
	}

	if b.Qty == nil {
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: qty tidak terbaca, baris tidak dikenali", b.Urutan,
		)}}
	}

	qtyFaktur, err := parseAngkaIndonesia(*b.Qty)
	if err != nil || qtyFaktur.Sign() <= 0 {
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: qty tidak terbaca sebagai angka yang valid, baris tidak dikenali", b.Urutan,
		)}}
	}

	if b.HargaSatuan == nil {
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: harga_satuan tidak terbaca, baris tidak dikenali", b.Urutan,
		)}}
	}

	harga, err := parseAngkaIndonesia(*b.HargaSatuan)
	if err != nil || harga.Sign() < 0 {
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: harga_satuan tidak terbaca sebagai angka yang valid, baris tidak dikenali", b.Urutan,
		)}}
	}

	qtyDasar := new(big.Rat).Mul(qtyFaktur, ratFromInt(faktor))
	if !isWholeNumber(qtyDasar) {
		return barisOCRSiap{peringatan: []string{fmt.Sprintf(
			"baris %d: qty x faktor menghasilkan pecahan satuan dasar, baris tidak dikenali", b.Urutan,
		)}}
	}

	var peringatan []string

	diskon := new(big.Rat)

	if b.DiskonBaris != nil && strings.TrimSpace(*b.DiskonBaris) != "" {
		if d, err := parseAngkaIndonesia(*b.DiskonBaris); err == nil && d.Sign() >= 0 {
			diskon = d
		} else {
			peringatan = append(peringatan, fmt.Sprintf("baris %d: diskon_baris tidak terbaca, diisi 0", b.Urutan))
		}
	}

	qtyDiterima, keterangan := qtyDiterimaOCR(jenis, b, qtyFaktur, &peringatan)

	if qtyDiterima.Cmp(qtyFaktur) > 0 {
		qtyDiterima = new(big.Rat).Set(qtyFaktur)
		peringatan = append(peringatan, fmt.Sprintf(
			"baris %d: qty_diterima melebihi qty_faktur, dipotong menjadi qty_faktur", b.Urutan,
		))
	}

	qtyFakturStr := formatNumeric(qtyFaktur, skalaQty)
	qtyDiterimaStr := formatNumeric(qtyDiterima, skalaQty)

	subtotalBaris := new(big.Rat).Mul(qtyFaktur, harga)
	subtotalBaris.Sub(subtotalBaris, diskon)
	subtotalBaris = roundNumeric(subtotalBaris, skalaUang)

	return barisOCRSiap{
		detail: &model.PembelianDetailRequest{
			IDProduct:         p.ID,
			IDSatuanInput:     *b.IDSatuan,
			QtyFaktur:         qtyFakturStr,
			QtyDiterima:       &qtyDiterimaStr,
			HargaSatuanInput:  formatNumeric(harga, skalaUang),
			DiskonBaris:       formatNumeric(diskon, skalaUang),
			JumlahKoli:        "0",
			KeteranganSelisih: keterangan,
		},
		subtotalBaris: subtotalBaris,
		peringatan:    peringatan,
	}
}

// qtyDiterimaOCR decides qty_diterima and, for a faktur-kedatangan line that was not
// checked off, the mandatory keterangan_selisih. NOTA ignores the centang
// convention entirely and always reads a line as fully received — that document has
// no such convention to read in the first place.
//
// A centang that is ragu (nil, illegible) is read exactly like an unchecked line —
// "Centang yang ragu dibaca sebagai tidak dicentang" — with one extra peringatan so
// a human knows to look again, since that ambiguity is exactly the kind of mistake
// that must never resolve toward "counted as arrived": stock recorded short is
// repaired with a penerimaan_susulan, stock recorded long is baked into the moving
// average and can only be reversed, never corrected.
func qtyDiterimaOCR(
	jenis repository.JenisOCRFaktur, b repository.FakturReaderBaris, qtyFaktur *big.Rat, peringatan *[]string,
) (*big.Rat, *string) {
	if jenis == repository.JenisOCRNota {
		return new(big.Rat).Set(qtyFaktur), nil
	}

	if b.Dicentang != nil && *b.Dicentang {
		return new(big.Rat).Set(qtyFaktur), nil
	}

	qtyDiterima := new(big.Rat)

	if b.QtyTulisanTangan != nil {
		if manual, err := parseAngkaIndonesia(*b.QtyTulisanTangan); err == nil && manual.Sign() >= 0 {
			qtyDiterima = manual
		}
	}

	if b.Dicentang == nil {
		*peringatan = append(*peringatan, fmt.Sprintf(
			"baris %d: centang tidak terbaca jelas, qty_diterima diisi %s",
			b.Urutan, formatNumeric(qtyDiterima, skalaQty),
		))
	}

	catatan := "OCR: baris tidak dicentang"

	return qtyDiterima, &catatan
}

// angkaOpsional parses a header figure Gemini may have left null or gotten wrong,
// falling back to zero with a peringatan rather than failing the whole call — a
// misread diskon_nota or ppn is exactly the kind of thing a human checks before
// submitting, not a reason to refuse the proposal outright.
func angkaOpsional(v *string, peringatan *[]string, nama string) *big.Rat {
	if v == nil || strings.TrimSpace(*v) == "" {
		return new(big.Rat)
	}

	n, err := parseAngkaIndonesia(*v)
	if err != nil {
		*peringatan = append(*peringatan, fmt.Sprintf("%s pada dokumen tidak terbaca sebagai angka, diisi 0", nama))

		return new(big.Rat)
	}

	return n
}

// trimToNil turns a blank or absent string into nil, so an empty answer from Gemini
// and no answer at all are the same thing to every caller downstream.
func trimToNil(v *string) *string {
	if v == nil {
		return nil
	}

	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}

	return &t
}

// tanggalFakturDariOCR accepts only an ISO date, exactly what CreatePembelianRequest
// itself requires — the system prompt asks Gemini for this shape directly rather
// than accepting a free-form date here and guessing at its layout, which would just
// move the ambiguity to Go without resolving it.
func tanggalFakturDariOCR(v *string, peringatan *[]string) *string {
	t := trimToNil(v)
	if t == nil {
		return nil
	}

	if _, err := time.Parse(dateOnly, *t); err != nil {
		*peringatan = append(*peringatan, "tanggal_faktur pada dokumen tidak terbaca dalam format YYYY-MM-DD, isi manual")

		return nil
	}

	return t
}

// jenisPembayaranDariTandaLunas reads a nota's own printed or handwritten mark
// ("LUNAS", "TUNAI", "KREDIT", ...) rather than guessing — the issue's own rule for
// this field: "Tidak ditebak." An empty result leaves usulan.jenis_pembayaran blank,
// which POST /pembelian itself already defaults to TUNAI.
func jenisPembayaranDariTandaLunas(v *string) string {
	if v == nil {
		return ""
	}

	tanda := strings.ToUpper(*v)

	switch {
	case strings.Contains(tanda, "LUNAS"), strings.Contains(tanda, "TUNAI"), strings.Contains(tanda, "CASH"):
		return entity.JenisPembayaranTunai
	case strings.Contains(tanda, "KREDIT"), strings.Contains(tanda, "BON"), strings.Contains(tanda, "UTANG"), strings.Contains(tanda, "HUTANG"):
		return entity.JenisPembayaranKredit
	default:
		return ""
	}
}
