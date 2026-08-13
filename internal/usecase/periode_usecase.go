package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// PeriodeUseCase owns book closing: the act that makes a month refuse further stock
// movements.
//
// It is the module closest to master data rather than to a transaction document —
// no number, no lines, no posting — so it follows the supplier shape. What it borrows
// from the documents is the action endpoint (POST /{...}/tutup) and the row lock
// taken before deciding, and it borrows those because closing a month races against
// everything posting into it.
//
// Two things it deliberately does not enforce:
//
//   - Closing does not have to be sequential. August may be closed while July is
//     still open. Requiring an order would mean closing every month that was never
//     used before reaching the one that matters, and nothing can be corrupted by the
//     gap: enforcement is per-month, inside the kartu_stok trigger, not a running
//     total that a skipped month would break.
//   - Cancelling a document whose period has since closed is still allowed. The
//     reversing kartu_stok rows are dated today, so they land in the current period —
//     see Batal in pembelian_usecase.go for why that is the accounting treatment
//     rather than an oversight.
type PeriodeUseCase struct {
	DB                *sql.DB
	Log               *logrus.Logger
	Validate          *validator.Validate
	PeriodeRepository *repository.PeriodeRepository
}

func NewPeriodeUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	periodeRepository *repository.PeriodeRepository,
) *PeriodeUseCase {
	return &PeriodeUseCase{
		DB:                db,
		Log:               log,
		Validate:          validate,
		PeriodeRepository: periodeRepository,
	}
}

// Get answers for any month, whether or not it has a row.
//
// A month nobody has closed answers a synthetic BUKA rather than a 404. That is not
// politeness — it is the actual state: migration 000004 treats a missing row as open,
// so "no row" and "open" are the same fact, and answering 404 would invite a client
// to conclude the month does not exist.
func (c *PeriodeUseCase) Get(ctx context.Context, request *model.GetPeriodeRequest) (*model.PeriodeResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	periode, err := c.PeriodeRepository.FindByTahunBulan(ctx, c.DB, request.Tahun, request.Bulan)
	if errors.Is(err, sql.ErrNoRows) {
		return converter.PeriodeToResponse(&entity.Periode{
			Tahun:  request.Tahun,
			Bulan:  request.Bulan,
			Status: entity.StatusPeriodeBuka,
		}), nil
	}
	if err != nil {
		return nil, err
	}

	return converter.PeriodeToResponse(periode), nil
}

// Search pages over the stored rows — that is, over months somebody has acted on.
// Months that were never closed have no row and do not appear, however open they are.
func (c *PeriodeUseCase) Search(ctx context.Context, request *model.ListPeriodeRequest) ([]model.PeriodeResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PeriodeRepository.Search(
		ctx, c.DB, request.Tahun, request.Status, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PeriodeToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Tutup closes a month.
//
// The lock comes first and it is the reason this is a transaction at all. The
// kartu_stok trigger takes the shared side of the same advisory lock before reading
// periode.status, so this call waits behind every posting already in flight for that
// month, and every posting that starts after it waits here. Without that, a posting
// could read 'BUKA', have this commit underneath it, and then commit its own row into
// a month whose books are shut — which is not a hypothetical when book closing is run
// in the late afternoon while the receiving desk is still typing.
func (c *PeriodeUseCase) Tutup(ctx context.Context, request *model.TutupPeriodeRequest) (*model.PeriodeResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	if err := c.PeriodeRepository.Lock(ctx, tx, request.Tahun, request.Bulan); err != nil {
		return nil, err
	}

	// Read under the lock, so the state this answers about is the state that is
	// about to be written. A month with no row is open and closing it is the
	// ordinary case, so an absent row is not an error here.
	periode, err := c.PeriodeRepository.FindByTahunBulan(ctx, tx, request.Tahun, request.Bulan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if periode != nil && periode.Status == entity.StatusPeriodeTutup {
		return nil, model.Conflict(fmt.Sprintf(
			"periode %04d-%02d sudah TUTUP", request.Tahun, request.Bulan,
		))
	}

	ditutup, err := c.PeriodeRepository.Tutup(ctx, tx, request.Tahun, request.Bulan, request.ActorID)
	if err != nil {
		return nil, invalidOnForeignKey(err, "ditutup_oleh menunjuk user yang tidak ada")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the actors' names, which the write could not
	// join for.
	return c.Get(ctx, &model.GetPeriodeRequest{Tahun: ditutup.Tahun, Bulan: ditutup.Bulan})
}

// Buka reopens a closed month, and it is SUPERADMIN-only for the same reason closing
// is: a month that can be reopened by anyone was never really closed.
//
// A month that has no row, or has one saying 'BUKA', is refused rather than treated
// as a no-op. Both are already open, and answering 200 to "open this" would let a
// caller believe they had changed something.
func (c *PeriodeUseCase) Buka(ctx context.Context, request *model.BukaPeriodeRequest) (*model.PeriodeResponse, error) {
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

	if err := c.PeriodeRepository.Lock(ctx, tx, request.Tahun, request.Bulan); err != nil {
		return nil, err
	}

	// The UPDATE repeats status = 'TUTUP' in its WHERE, so a row that was reopened
	// between the lock and the write matches nothing and arrives here as
	// sql.ErrNoRows — the same answer as a month that was never closed at all, and
	// the same message fits both.
	dibuka, err := c.PeriodeRepository.Buka(ctx, tx, request.Tahun, request.Bulan, request.ActorID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Conflict(fmt.Sprintf(
			"periode %04d-%02d tidak sedang TUTUP", request.Tahun, request.Bulan,
		))
	}
	if err != nil {
		return nil, invalidOnForeignKey(err, "dibuka_oleh menunjuk user yang tidak ada")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.Get(ctx, &model.GetPeriodeRequest{Tahun: dibuka.Tahun, Bulan: dibuka.Bulan})
}
