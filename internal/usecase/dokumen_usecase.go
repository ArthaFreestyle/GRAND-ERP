package usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// maxLampiranPerDokumen caps how many files may hang off one document.
//
// A multi-page invoice photographed one page at a time is the case this has to
// leave room for; anything past ten is a stuck retry loop or a mistake, and an
// unbounded count turns one document into unbounded disk.
const maxLampiranPerDokumen = 10

// statusIndukBolehHapus is the parent status under which an attached file may still
// be removed. Anything past DRAFT has been submitted, and evidence attached to a
// document under review is not the uploader's to withdraw.
const statusIndukBolehHapus = "DRAFT"

// statusIndukBatal is the parent status that refuses new attachments.
const statusIndukBatal = "BATAL"

// mimeDiizinkan maps every accepted content type to the extension a stored file
// gets. Invoices are photographed or scanned, so there is no reason to accept
// anything else — and every type not on this list is one more thing a browser might
// be talked into executing.
//
// The extension comes from this map, never from the client's filename: the name
// "faktur.pdf" on a file whose bytes are HTML is exactly the trick the sniffing is
// there to defeat, and honouring its extension would hand the trick back.
var mimeDiizinkan = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"application/pdf": ".pdf",
}

// ukuranSniff is how many bytes http.DetectContentType looks at. Reading exactly
// this many keeps the sniff buffer small enough to be irrelevant next to the size
// limit — the file is never held in memory beyond it.
const ukuranSniff = 512

// batchPembersihan is how many orphans one sweep of the cleanup job claims at a
// time. Each file is its own unlink and its own transaction, so a batch bounds how
// long the job runs rather than how much it can eventually clean.
const batchPembersihan = 200

// DokumenUseCase holds the rules for file attachments: what may be uploaded, where
// it may be attached, who may take it away, and when an upload nobody claimed is
// swept up.
//
// It is the first usecase holding a non-SQL store. Storage is an interface from the
// repository layer for exactly the reason every SQL statement lives there too —
// swapping local disk for S3 must not reach past that boundary.
type DokumenUseCase struct {
	DB                *sql.DB
	Log               *logrus.Logger
	Validate          *validator.Validate
	DokumenRepository *repository.DokumenRepository
	Storage           repository.DokumenStorage

	// MaxUkuranByte is the upload limit, enforced while streaming rather than after
	// the fact. See Upload.
	MaxUkuranByte int64
	// OrphanTTL is how long an unattached upload is left alone before the cleanup job
	// treats it as abandoned. Long enough that a form left open overnight still finds
	// its photos.
	OrphanTTL time.Duration
}

func NewDokumenUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	dokumenRepository *repository.DokumenRepository,
	storage repository.DokumenStorage,
	maxUkuranByte int64,
	orphanTTL time.Duration,
) *DokumenUseCase {
	return &DokumenUseCase{
		DB:                db,
		Log:               log,
		Validate:          validate,
		DokumenRepository: dokumenRepository,
		Storage:           storage,
		MaxUkuranByte:     maxUkuranByte,
		OrphanTTL:         orphanTTL,
	}
}

// Upload stores one file and returns its metadata. The row is born an orphan:
// ref_table and ref_id stay NULL until something claims it.
//
// The order is deliberate and is the one thing here that cannot be swapped around:
// bytes to disk first, row second, and the file deleted again if the row fails. The
// other order leaves a row pointing at a file that is not there — which no cleanup
// can repair, because nothing knows what the file should have contained. A file with
// no row is the recoverable half of the pair, and it is deleted here anyway.
func (c *DokumenUseCase) Upload(ctx context.Context, request *model.UploadDokumenRequest) (*model.DokumenResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if request.Berkas == nil {
		return nil, model.Invalid("berkas is required")
	}

	// The multipart header's own size, checked first only to refuse an obviously
	// oversized upload before touching the disk. It is client-supplied, so it decides
	// nothing on its own — the limit that holds is the io.LimitReader below.
	if request.UkuranDilaporkan > c.MaxUkuranByte {
		return nil, model.Invalid(fmt.Sprintf(
			"berkas melebihi batas unggah %d byte", c.MaxUkuranByte,
		))
	}

	kepala, err := bacaKepala(request.Berkas)
	if err != nil {
		return nil, err
	}

	// The type comes from the bytes, never from the Content-Type header: that header
	// is entirely the caller's to write, and believing it means a .exe announced as
	// image/png is stored as one.
	mime := sniffMime(kepala)

	ekstensi, diizinkan := mimeDiizinkan[mime]
	if !diizinkan {
		return nil, model.Invalid("jenis berkas tidak didukung; hanya JPEG, PNG, dan PDF")
	}

	// Server-generated, so the client's filename never becomes a path. request.NamaAsli
	// is kept as display text only, and is free to be "../../config.json" without
	// meaning anything.
	namaSimpan := uuid.NewString() + ekstensi

	// The limit is enforced on the stream, not after the file is whole: reading first
	// and measuring second means an attacker picks how much memory or disk the server
	// spends. One byte over the limit is read on purpose — it is how "exactly at the
	// limit" is told apart from "truncated here".
	hasher := sha256.New()
	isi := io.TeeReader(
		io.LimitReader(io.MultiReader(bytes.NewReader(kepala), request.Berkas), c.MaxUkuranByte+1),
		hasher,
	)

	ukuran, err := c.Storage.Tulis(ctx, namaSimpan, isi)
	if err != nil {
		return nil, fmt.Errorf("simpan berkas dokumen: %w", err)
	}

	if ukuran > c.MaxUkuranByte {
		c.hapusBerkas(ctx, namaSimpan)

		return nil, model.Invalid(fmt.Sprintf(
			"berkas melebihi batas unggah %d byte", c.MaxUkuranByte,
		))
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))

	dokumen := &entity.Dokumen{
		NamaAsli:       request.NamaAsli,
		PathSimpan:     namaSimpan,
		Mime:           mime,
		UkuranByte:     ukuran,
		ChecksumSHA256: &checksum,
		CreatedBy:      request.ActorID,
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		c.hapusBerkas(ctx, namaSimpan)

		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := c.DokumenRepository.Create(ctx, tx, dokumen); err != nil {
		c.hapusBerkas(ctx, namaSimpan)

		return nil, invalidOnForeignKey(err, "pengunggah tidak ditemukan")
	}

	// Advisory, and read inside the same transaction so it cannot see a half-written
	// concurrent upload. A duplicate is never a reason to refuse: the same scan may
	// legitimately belong to two documents. It is reported so the receiving desk can
	// notice it has photographed one invoice twice.
	duplikat, err := c.DokumenRepository.FindDuplikat(ctx, tx, checksum, dokumen.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		c.hapusBerkas(ctx, namaSimpan)

		return nil, err
	}

	if err := tx.Commit(); err != nil {
		c.hapusBerkas(ctx, namaSimpan)

		return nil, err
	}

	response := converter.DokumenToResponse(dokumen)
	if duplikat != 0 {
		response.DuplikatDariID = &duplikat
	}

	return response, nil
}

// Isi opens an attachment for streaming, returning its metadata alongside. The
// caller closes the reader.
//
// It stays behind authentication like everything else: a photographed invoice
// carries purchase prices and the supplier's identity, which is precisely the
// information a competitor would like.
func (c *DokumenUseCase) Isi(ctx context.Context, request *model.GetDokumenRequest) (*model.DokumenResponse, io.ReadCloser, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	dokumen, err := c.DokumenRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, nil, notFoundOnNoRows(err, "dokumen not found")
	}

	// A soft-deleted row has no file behind it any more. 404 rather than 410: the
	// distinction buys the client nothing and "it was here yesterday" is not
	// information every caller should get.
	if dokumen.DeletedAt != nil {
		return nil, nil, model.NotFound("dokumen not found")
	}

	berkas, err := c.Storage.Buka(ctx, dokumen.PathSimpan)
	if err != nil {
		// The row says the file is there and it is not. That is the two halves having
		// drifted apart, which is a server fault and not the caller's problem to
		// interpret — so it is logged with the id and surfaces as a 500.
		c.Log.WithError(err).WithFields(logrus.Fields{
			"dokumen_id":  dokumen.ID,
			"path_simpan": dokumen.PathSimpan,
		}).Error("dokumen row has no file behind it")

		return nil, nil, fmt.Errorf("buka berkas dokumen: %w", err)
	}

	return converter.DokumenToResponse(dokumen), berkas, nil
}

// Tempel attaches an orphan to the document it belongs to.
//
// This is the second half of "upload first, attach later", and the only way a row
// stops being a cleanup candidate. It is one endpoint rather than a field on every
// document's create request, so a module that starts accepting attachments needs no
// DTO change of its own — only an entry in repository.RefTableDokumen.
func (c *DokumenUseCase) Tempel(ctx context.Context, request *model.TempelDokumenRequest) (*model.DokumenResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// Checked before the transaction opens: an unknown ref_table is a typo in the
	// request, not a state the database can settle. There is no foreign key behind a
	// polymorphic reference, so this map is the only thing that says which strings
	// name a document at all.
	if _, dikenal := repository.RefTableDokumen[request.RefTable]; !dikenal {
		return nil, model.Invalid("ref_table tidak dikenal")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Locked before it is read, so two requests attaching the same file to two
	// documents queue instead of racing. The guard repeated in Tempel's WHERE is the
	// backstop; this is what makes the error message the friendly one.
	dokumen, err := c.DokumenRepository.LockByID(ctx, tx, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "dokumen not found")
	}

	if dokumen.DeletedAt != nil {
		return nil, model.Conflict("dokumen sudah dihapus")
	}

	if dokumen.RefID != nil {
		return nil, model.Conflict("dokumen sudah tertempel ke dokumen lain")
	}

	status, err := c.DokumenRepository.StatusRef(ctx, tx, request.RefTable, request.RefID)
	if err != nil {
		// A ref_id naming no row is the same class of mistake as a foreign key that
		// does not resolve, and gets the same 400.
		return nil, notFoundOnNoRowsAsInvalid(err, "dokumen induk tidak ditemukan")
	}

	// A voided document is closed, and an attachment on one could never be removed
	// again — the removal rule only lets go of a DRAFT parent. Refusing here avoids
	// creating something permanent on a document that no longer counts.
	if status == statusIndukBatal {
		return nil, model.Conflict("dokumen induk sudah dibatalkan")
	}

	jumlah, err := c.DokumenRepository.CountLampiran(ctx, tx, request.RefTable, request.RefID)
	if err != nil {
		return nil, err
	}

	if jumlah >= maxLampiranPerDokumen {
		return nil, model.Conflict(fmt.Sprintf(
			"dokumen induk sudah memiliki %d lampiran", maxLampiranPerDokumen,
		))
	}

	tertempel, err := c.DokumenRepository.Tempel(ctx, tx, request.ID, request.RefTable, request.RefID)
	if err != nil {
		return nil, conflictOnTransisi(err, "dokumen sudah tertempel ke dokumen lain")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.DokumenToResponse(tertempel), nil
}

// Hapus removes an attachment: the file goes, the row stays with deleted_at set.
//
// Only while the file is still an orphan, or while the document holding it is a
// DRAFT. Past that the document has been submitted, and a photograph of the invoice
// it was approved against is part of the record rather than the uploader's to take
// back.
func (c *DokumenUseCase) Hapus(ctx context.Context, request *model.DeleteDokumenRequest) (*model.DokumenResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	dokumen, err := c.DokumenRepository.LockByID(ctx, tx, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "dokumen not found")
	}

	if dokumen.DeletedAt != nil {
		return nil, model.Conflict("dokumen sudah dihapus")
	}

	if dokumen.RefTable != nil && dokumen.RefID != nil {
		status, err := c.DokumenRepository.StatusRef(ctx, tx, *dokumen.RefTable, *dokumen.RefID)
		if err != nil {
			return nil, notFoundOnNoRowsAsInvalid(err, "dokumen induk tidak ditemukan")
		}

		if status != statusIndukBolehHapus {
			return nil, model.Conflict(
				"lampiran hanya bisa dihapus selama masih yatim atau dokumen induknya DRAFT",
			)
		}
	}

	if err := c.hapusBerkasDanTandai(ctx, tx, dokumen); err != nil {
		return nil, conflictOnTransisi(err, "dokumen sudah dihapus")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.DokumenToResponse(dokumen), nil
}

// Search lists one document's attachments, or the caller's own orphans when no
// reference is given — which is what a receiving screen needs to offer the photos
// taken minutes ago.
func (c *DokumenUseCase) Search(ctx context.Context, request *model.ListDokumenRequest) ([]model.DokumenResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	// Half a reference is not a filter: ref_table alone names a whole table, ref_id
	// alone names a row in no particular one.
	if (request.RefTable == "") != (request.RefID == nil) {
		return nil, nil, model.Invalid("ref_table dan ref_id harus diisi bersamaan")
	}

	if request.RefTable != "" {
		if _, dikenal := repository.RefTableDokumen[request.RefTable]; !dikenal {
			return nil, nil, model.Invalid("ref_table tidak dikenal")
		}
	}

	var refID int64
	if request.RefID != nil {
		refID = *request.RefID
	}

	list, total, err := c.DokumenRepository.Search(
		ctx, c.DB, request.RefTable, refID, request.ActorID, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.DokumenToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// BersihkanYatim deletes uploads that were never attached and are older than
// OrphanTTL, and reports how many it removed. This is the worker's only job.
//
// It works from rows, never from a directory listing. A listing would sweep up files
// whose row is written but not yet committed — the seconds between Storage.Tulis and
// tx.Commit in Upload — and those belong to a request that is still in flight.
//
// Concurrency-safe in two layers. An advisory lock keeps a second worker out
// entirely, and each row is handled in its own transaction under a row lock, so a
// file being attached at the same moment it is being swept blocks rather than losing
// its bytes.
func (c *DokumenUseCase) BersihkanYatim(ctx context.Context) (int, error) {
	// One connection for the whole job: a session-level advisory lock belongs to the
	// connection that took it, and releasing it from another pooled connection would
	// silently do nothing.
	conn, err := c.DB.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = conn.Close()
	}()

	locked, err := c.DokumenRepository.TryLockPembersihan(ctx, conn)
	if err != nil {
		return 0, err
	}

	if !locked {
		// Another worker is already sweeping. Nothing to do and nothing wrong — the
		// same pattern kartu_stok's trigger uses to serialise postings.
		c.Log.Debug("pembersihan dokumen: worker lain sedang berjalan")

		return 0, nil
	}
	defer func() {
		if err := c.DokumenRepository.UnlockPembersihan(ctx, conn); err != nil {
			c.Log.WithError(err).Error("pembersihan dokumen: gagal melepas advisory lock")
		}
	}()

	batas := time.Now().Add(-c.OrphanTTL)
	dibersihkan := 0

	for {
		if err := ctx.Err(); err != nil {
			return dibersihkan, err
		}

		list, err := c.DokumenRepository.FindYatim(ctx, conn, batas, batchPembersihan)
		if err != nil {
			return dibersihkan, err
		}

		if len(list) == 0 {
			return dibersihkan, nil
		}

		for i := range list {
			ok, err := c.bersihkanSatu(ctx, conn, &list[i])
			if err != nil {
				// One unreadable file or one row that moved must not stop the sweep: the
				// next run picks it up again, and everything behind it in this batch would
				// otherwise wait a full interval for no reason.
				c.Log.WithError(err).WithField("dokumen_id", list[i].ID).
					Warn("pembersihan dokumen: satu berkas yatim gagal dibersihkan")

				continue
			}

			if ok {
				dibersihkan++
			}
		}

		// A short batch means the candidate list is exhausted. Asking again would
		// return the same rows the failures above left behind, forever.
		if len(list) < batchPembersihan {
			return dibersihkan, nil
		}
	}
}

// bersihkanSatu removes one orphan's file and marks its row, in its own transaction
// under a row lock.
//
// The row is re-checked after the lock rather than trusted from the batch: between
// the SELECT and here it may have been attached, and attaching is exactly the thing
// that must win this race — the file is somebody's evidence now.
func (c *DokumenUseCase) bersihkanSatu(ctx context.Context, conn *sql.Conn, kandidat *entity.Dokumen) (bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	dokumen, err := c.DokumenRepository.LockByID(ctx, tx, kandidat.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}

		return false, err
	}

	if dokumen.RefID != nil || dokumen.DeletedAt != nil {
		return false, nil
	}

	if err := c.hapusBerkasDanTandai(ctx, tx, dokumen); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	return true, nil
}

// hapusBerkasDanTandai deletes the file and then marks the row, in that order.
//
// The order is the opposite of Upload's and for the same reason — whichever half is
// left over has to be the recoverable one. Here that is the row: if the mark or the
// commit fails after the file is gone, the row is still a live orphan and the next
// attempt finds it again, unlinks nothing (Hapus tolerates a missing file), and
// finishes the job. Marking first would leave a file that no query can ever name
// again, since the cleanup candidate list only holds rows that are not deleted yet.
//
// The cost is a window where a row points at a file that is not there, which
// downloads answer with a 500. That is a worse minute and a better week.
func (c *DokumenUseCase) hapusBerkasDanTandai(ctx context.Context, tx repository.DBTX, dokumen *entity.Dokumen) error {
	if err := c.Storage.Hapus(ctx, dokumen.PathSimpan); err != nil {
		return err
	}

	deletedAt, err := c.DokumenRepository.SoftDelete(ctx, tx, dokumen.ID)
	if err != nil {
		return err
	}

	// The caller holds the row it read before the lock; carry the mark onto it so the
	// response confirming a deletion does not report deleted_at as null.
	dokumen.DeletedAt = &deletedAt

	return nil
}

// hapusBerkas undoes a write whose row never landed. Failure is logged rather than
// returned: the caller is already returning an error, and replacing it with this one
// would hide why the upload actually failed. What is left behind is a file with no
// row — invisible to the cleanup job, which works from rows, so it is worth a loud
// line in the log.
func (c *DokumenUseCase) hapusBerkas(ctx context.Context, nama string) {
	if err := c.Storage.Hapus(ctx, nama); err != nil {
		c.Log.WithError(err).WithField("path_simpan", nama).
			Error("berkas dokumen tertinggal tanpa baris")
	}
}

// bacaKepala reads the first bytes of an upload, which is what the content sniffing
// looks at, and refuses an empty file.
//
// io.ReadFull rather than a single Read: a Read may legitimately return fewer bytes
// than asked for, and sniffing a 3-byte prefix of a JPEG gives a different answer
// than sniffing its first 512.
func bacaKepala(r io.Reader) ([]byte, error) {
	buf := make([]byte, ukuranSniff)

	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("baca berkas dokumen: %w", err)
	}

	if n == 0 {
		return nil, model.Invalid("berkas kosong")
	}

	return buf[:n], nil
}

// sniffMime reports the media type of a file from its own leading bytes, with any
// parameters stripped.
//
// http.DetectContentType answers "text/plain; charset=utf-8" for some inputs, and
// the parameter would make an otherwise exact whitelist lookup miss. Nothing here
// consults the Content-Type the client sent, which is the entire point.
func sniffMime(kepala []byte) string {
	tipe, _, _ := strings.Cut(http.DetectContentType(kepala), ";")

	return strings.TrimSpace(strings.ToLower(tipe))
}

// notFoundOnNoRowsAsInvalid maps an absent row to a 400 rather than a 404.
//
// It is for a row named in the request body instead of in the path: a ref_id
// pointing at no document is the same class of mistake as a foreign key that does
// not resolve, and invalidOnForeignKey already answers 400 for that. A 404 here
// would claim the attachment itself is missing, which it is not.
func notFoundOnNoRowsAsInvalid(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.Invalid(message)
	}

	if errors.Is(err, repository.ErrRefTableTidakDikenal) {
		return model.Invalid("ref_table tidak dikenal")
	}

	return err
}
