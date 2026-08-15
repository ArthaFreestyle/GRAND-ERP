package usecase

import (
	"context"
	"database/sql"
	"errors"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// UserUseCase holds the business rules for users and their role grants. It owns
// the transaction boundary and returns models, never entities or Fiber types.
//
// It depends on RoleRepository and UnitKerjaRepository as well as UserRepository:
// granting a role at a unit has to check that both the role and the unit exist
// and are still active, and those questions belong to their own modules' SQL,
// not to a second copy of it here.
type UserUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	UserRepository      *repository.UserRepository
	RoleRepository      *repository.RoleRepository
	UnitKerjaRepository *repository.UnitKerjaRepository
}

func NewUserUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	userRepository *repository.UserRepository,
	roleRepository *repository.RoleRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *UserUseCase {
	return &UserUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		UserRepository:      userRepository,
		RoleRepository:      roleRepository,
		UnitKerjaRepository: unitKerjaRepository,
	}
}

// Create writes the user and its role grants in one transaction. Both or neither:
// a user that committed without its roles would silently have no permissions, and
// nothing in the response would say so.
func (c *UserUseCase) Create(ctx context.Context, request *model.CreateUserRequest) (*model.UserResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// A body repeating a pair is not an error worth a 409 — it plainly means
	// "grant that once". Deduplicating here is also what lets ReplaceRoles rely
	// on ON CONFLICT covering only the concurrent case.
	grants := toGrants(request.Grants)

	hash, err := hashPassword(request.Password)
	if err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	exists, err := c.UserRepository.ExistsByUsername(ctx, tx, request.Username, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, model.Conflict("username already used")
	}

	// email is nullable, and lower(NULL) never equals lower(NULL), so checking an
	// absent email would always answer false and prove nothing.
	if request.Email != nil {
		exists, err := c.UserRepository.ExistsByEmail(ctx, tx, *request.Email, 0)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("email already used")
		}
	}

	if err := c.requireActiveGrants(ctx, tx, grants); err != nil {
		return nil, err
	}

	user := &entity.User{
		Username:    request.Username,
		Email:       request.Email,
		Password:    hash,
		NamaLengkap: request.NamaLengkap,
		IsAktif:     true,
		// created_by stays NULL until the auth module can supply the actor.
	}

	if err := c.UserRepository.Create(ctx, tx, user); err != nil {
		return nil, conflictOnUnique(err, "username or email already used")
	}

	if err := c.UserRepository.ReplaceRoles(ctx, tx, user.ID, grants, nil); err != nil {
		return nil, invalidOnForeignKey(err, "grants contains a role or unit kerja that no longer exists")
	}

	if err := c.attachRoles(ctx, tx, []*entity.User{user}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.UserToResponse(user), nil
}

func (c *UserUseCase) Get(ctx context.Context, request *model.GetUserRequest) (*model.UserResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	user, err := c.UserRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "user not found")
	}

	if err := c.attachRoles(ctx, c.DB, []*entity.User{user}); err != nil {
		return nil, err
	}

	return converter.UserToResponse(user), nil
}

// Update patches the user and, when grants is present, replaces the whole grant
// set — both inside one transaction, so a failed role write cannot leave a renamed
// user holding the old grants.
func (c *UserUseCase) Update(ctx context.Context, request *model.UpdateUserRequest) (*model.UserResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// username, password, and is_aktif are NOT NULL: changeable, never clearable.
	if request.Username.Clears() {
		return nil, model.Invalid("username cannot be null")
	}
	if request.Password.Clears() {
		return nil, model.Invalid("password cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	// [] already means "revoke every grant", so null would be a second spelling
	// of the same instruction. Rejecting it keeps one meaning per body.
	if request.Grants.Clears() {
		return nil, model.Invalid("grants cannot be null; send [] to revoke every grant")
	}

	patch := repository.UserPatch{
		Username:       request.Username.Value,
		SetEmail:       request.Email.Present,
		Email:          request.Email.Value,
		SetNamaLengkap: request.NamaLengkap.Present,
		NamaLengkap:    request.NamaLengkap.Value,
		IsAktif:        request.IsAktif.Value,
	}

	// The stored column is a hash, so a new password is hashed before it ever
	// reaches the patch.
	if request.Password.Set() {
		hash, err := hashPassword(*request.Password.Value)
		if err != nil {
			return nil, err
		}

		patch.Password = &hash
	}

	touchesUser := patch.Username != nil || patch.SetEmail || patch.Password != nil ||
		patch.SetNamaLengkap || patch.IsAktif != nil
	replacesRoles := request.Grants.Present

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if !touchesUser && !replacesRoles {
		return nil, model.Invalid("no fields to update")
	}

	var grants []repository.Grant
	if replacesRoles {
		grants = toGrants(*request.Grants.Value)
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if patch.Username != nil {
		exists, err := c.UserRepository.ExistsByUsername(ctx, tx, *patch.Username, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("username already used")
		}
	}

	// Only a non-null email can collide; clearing it never can.
	if patch.Email != nil {
		exists, err := c.UserRepository.ExistsByEmail(ctx, tx, *patch.Email, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("email already used")
		}
	}

	if replacesRoles {
		if err := c.requireActiveGrants(ctx, tx, grants); err != nil {
			return nil, err
		}
	}

	// A roles-only patch changes no column of users, but it is still a change to
	// the user: Touch exists so updated_at and updated_by record it, and so an
	// unknown id still answers 404 instead of quietly writing nothing.
	var user *entity.User
	if touchesUser {
		user, err = c.UserRepository.Update(ctx, tx, request.ID, patch)
	} else {
		user, err = c.UserRepository.Touch(ctx, tx, request.ID, patch.SetUpdatedBy, patch.UpdatedBy)
	}
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "username or email already used"), "user not found",
		)
	}

	if replacesRoles {
		if err := c.UserRepository.ReplaceRoles(ctx, tx, user.ID, grants, nil); err != nil {
			return nil, invalidOnForeignKey(err, "grants contains a role or unit kerja that no longer exists")
		}
	}

	if err := c.attachRoles(ctx, tx, []*entity.User{user}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.UserToResponse(user), nil
}

func (c *UserUseCase) Search(ctx context.Context, request *model.ListUserRequest) ([]model.UserResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.UserRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.RoleID, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	// Three queries for a page, whatever the row count: count, rows, roles.
	if err := c.attachRoles(ctx, c.DB, pointersTo(list)); err != nil {
		return nil, nil, err
	}

	return converter.UserToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// attachRoles fills Roles on every given user with one query, instead of one query
// per user. It takes pointers so a page of users is filled in place.
func (c *UserUseCase) attachRoles(ctx context.Context, db repository.DBTX, users []*entity.User) error {
	if len(users) == 0 {
		return nil
	}

	userIDs := make([]int64, len(users))
	for i, user := range users {
		userIDs[i] = user.ID
	}

	assignments, err := c.UserRepository.FindRolesByUserIDs(ctx, db, userIDs)
	if err != nil {
		return err
	}

	byUser := make(map[int64][]entity.RoleGrant, len(users))
	for _, assignment := range assignments {
		byUser[assignment.UserID] = append(byUser[assignment.UserID], assignment.RoleGrant)
	}

	// A user with no grants keeps a nil slice, which the converter turns into [].
	for _, user := range users {
		user.Roles = byUser[user.ID]
	}

	return nil
}

// pointersTo adapts a page of users to attachRoles. Indexing rather than ranging by
// value matters: &user of a loop copy would fill the copy's Roles and drop them.
func pointersTo(list []entity.User) []*entity.User {
	users := make([]*entity.User, len(list))
	for i := range list {
		users[i] = &list[i]
	}

	return users
}

// requireActiveGrants rejects a grant naming a role or a unit_kerja that does not
// exist or has been retired, before any write happens.
//
// The foreign keys already refuse an unknown id, but neither can tell an
// inactive row from an active one, and their error messages name a constraint
// rather than the field the caller got wrong. The two checks are independent —
// a bad role and a bad unit are different problems — so each gets its own
// message.
func (c *UserUseCase) requireActiveGrants(ctx context.Context, db repository.DBTX, grants []repository.Grant) error {
	if len(grants) == 0 {
		return nil
	}

	roleIDs := uniqueRoleIDs(grants)

	count, err := c.RoleRepository.CountActiveByIDs(ctx, db, roleIDs)
	if err != nil {
		return err
	}
	if count != int64(len(roleIDs)) {
		return model.Invalid("grants contains a role that does not exist or is not active")
	}

	unitIDs := uniqueUnitIDs(grants)
	if len(unitIDs) == 0 {
		return nil
	}

	count, err = c.UnitKerjaRepository.CountActiveByIDs(ctx, db, unitIDs)
	if err != nil {
		return err
	}
	if count != int64(len(unitIDs)) {
		return model.Invalid("grants contains a unit_kerja that does not exist or is not active")
	}

	return nil
}

// hashPassword turns a plaintext password into a bcrypt hash. The plaintext is
// never stored, logged, or returned.
//
// bcrypt refuses input longer than 72 bytes. The DTO's max=72 catches the ordinary
// case, but it counts runes while bcrypt counts bytes, so a 72-character password
// of multibyte runes reaches here and fails — as a 400, not a 500.
func hashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return "", model.Invalid("password must be at most 72 bytes")
		}

		return "", err
	}

	return string(hash), nil
}

// toGrants converts request grants to repository.Grant, deduplicating by
// (IDRole, IDUnitKerja) while preserving order, and always returns a non-nil
// slice.
//
// A body repeating a pair plainly means "grant that once", not a conflict worth
// a 409. Non-nil matters too: ReplaceRoles' delete is an anti-join over
// unnest(...), and an empty non-nil slice is what makes "revoke every grant"
// actually revoke — unnest of a nil array behaves the same as an empty one here,
// but the explicit non-nil return keeps the contract obvious at the call site.
func toGrants(requests []model.GrantRequest) []repository.Grant {
	// unitID normalises a nil IDUnitKerja to 0 for the dedup key. Real unit ids
	// start at 1 (BIGINT GENERATED BY DEFAULT AS IDENTITY), so 0 is safe as the
	// "every unit" sentinel and can never collide with a real id.
	type key struct {
		roleID int64
		unitID int64
	}

	seen := make(map[key]struct{}, len(requests))
	grants := make([]repository.Grant, 0, len(requests))

	for _, request := range requests {
		k := key{roleID: request.IDRole}
		if request.IDUnitKerja != nil {
			k.unitID = *request.IDUnitKerja
		}

		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}

		grants = append(grants, repository.Grant{RoleID: request.IDRole, IDUnitKerja: request.IDUnitKerja})
	}

	return grants
}

// uniqueRoleIDs collects the distinct role ids named across a set of grants —
// the same role may appear more than once, granted at different units.
func uniqueRoleIDs(grants []repository.Grant) []int64 {
	seen := make(map[int64]struct{}, len(grants))
	ids := make([]int64, 0, len(grants))

	for _, grant := range grants {
		if _, ok := seen[grant.RoleID]; ok {
			continue
		}

		seen[grant.RoleID] = struct{}{}
		ids = append(ids, grant.RoleID)
	}

	return ids
}

// uniqueUnitIDs collects the distinct non-nil unit ids named across a set of
// grants. A nil IDUnitKerja (every unit) contributes nothing here — there is no
// row to validate.
func uniqueUnitIDs(grants []repository.Grant) []int64 {
	seen := make(map[int64]struct{}, len(grants))
	ids := make([]int64, 0, len(grants))

	for _, grant := range grants {
		if grant.IDUnitKerja == nil {
			continue
		}

		if _, ok := seen[*grant.IDUnitKerja]; ok {
			continue
		}

		seen[*grant.IDUnitKerja] = struct{}{}
		ids = append(ids, *grant.IDUnitKerja)
	}

	return ids
}
