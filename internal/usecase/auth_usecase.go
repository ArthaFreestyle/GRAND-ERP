package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// AuthUseCase issues and verifies bearer tokens. It holds the signing key, so it is
// the only place that can mint a session.
//
// The tokens are stateless JWTs: nothing is stored server-side and no lookup happens
// per request. The cost of that is stated plainly because it is a real limitation,
// not a detail — a session cannot be revoked. Setting is_aktif = false on a user, or
// taking away a role, does not reach a token that is already out. TTL is the only
// bound on it, which is why jwt.ttl_minutes defaults to 60 rather than to days.
//
// Switching active context (isu #12 fase 4) mints a brand new token; it does not and
// cannot revoke the old one either — the same limitation, stated again at
// SwitchContext because it is easy to assume otherwise there specifically.
//
// It imports jwt but not Fiber: the middleware in the delivery layer calls
// Authenticate and receives a *model.Session, so HTTP stays on the far side.
type AuthUseCase struct {
	DB             *sql.DB
	Log            *logrus.Logger
	Validate       *validator.Validate
	UserRepository *repository.UserRepository

	secret []byte
	ttl    time.Duration
	issuer string
}

func NewAuthUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	userRepository *repository.UserRepository,
	secret string,
	ttl time.Duration,
	issuer string,
) *AuthUseCase {
	return &AuthUseCase{
		DB:             db,
		Log:            log,
		Validate:       validate,
		UserRepository: userRepository,
		secret:         []byte(secret),
		ttl:            ttl,
		issuer:         issuer,
	}
}

// claims is the token payload. Grants ride along so authorization needs no
// database query, which is the whole point of the stateless choice — and the
// reason a grant made or revoked only takes effect at next login or
// switch-context. Grant and ActiveContext are model types, not something
// claims-private: the same shape goes into the token, comes back out of
// Authenticate into a Session, and is what LoginResponse/SessionResponse hand a
// client, so one pair of types serves all three rather than three that have to
// be kept in sync by hand.
//
// Aktif is nil when the caller has not chosen yet — see Login.
type claims struct {
	jwt.RegisteredClaims

	Username string               `json:"username"`
	Grants   []model.Grant        `json:"grants"`
	Aktif    *model.ActiveContext `json:"aktif,omitempty"`
}

// Login verifies a password and returns a token.
//
// Every failure answers the same "username or password is wrong". Distinguishing
// "no such user" from "wrong password" hands an attacker a way to enumerate valid
// usernames, and the operator gains nothing from the difference.
//
// A caller holding more than one usable grant gets a token with no active
// context (isu #12 fase 4): with grants now bound to a place, there is no
// default that would not risk someone acting under an authority they did not
// realize they had picked, so the token can only reach switch-context and
// auth/me until one is chosen. Holding exactly one grant has no such ambiguity
// and is selected automatically.
func (c *AuthUseCase) Login(ctx context.Context, request *model.LoginRequest) (*model.LoginResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	user, err := c.UserRepository.FindByUsernameWithPassword(ctx, c.DB, request.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Compare against a dummy hash anyway. Returning early here makes an
			// unknown username measurably faster to reject than a wrong password,
			// which leaks exactly what the identical message is hiding.
			bcryptDummyCompare()

			return nil, model.Unauthorized("username or password is wrong")
		}

		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(request.Password)); err != nil {
		return nil, model.Unauthorized("username or password is wrong")
	}

	// Checked after the password, deliberately: answering "account disabled" to
	// anyone who names a disabled user tells them the account exists.
	if !user.IsAktif {
		return nil, model.Unauthorized("username or password is wrong")
	}

	if err := c.attachRolesForLogin(ctx, user); err != nil {
		return nil, err
	}

	var aktif *entity.RoleGrant
	if len(user.Roles) == 1 {
		aktif = &user.Roles[0]
	}

	modelAktif := toActiveContext(aktif)

	token, expiresAt, err := c.issue(user, modelAktif)
	if err != nil {
		return nil, err
	}

	return &model.LoginResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresAt: expiresAt,
		User:      converter.UserToResponse(user),
		Grants:    toGrantList(user.Roles),
		Aktif:     modelAktif,
	}, nil
}

// SwitchContext issues a new token whose active context is the named grant —
// isu #12 fase 4. The grant is re-read from the database, never trusted from
// the caller's own claims: a stateless token cannot see a grant revoked, or a
// role or unit retired, after it was issued, and this endpoint is the one
// place in the whole design where that staleness is allowed to matter — every
// other read still authorizes purely from the token, on purpose.
//
// Every failure — the grant does not exist, belongs to another user, or its
// role or unit is no longer active — answers the same 403. Distinguishing them
// would let a caller probe which grant ids exist for other users, the same
// reasoning Login already applies to an unknown username.
//
// This mints a brand new token and cannot touch the old one: switching away
// from a grant does not revoke whatever authority the previous token still
// carries until it expires. jwt.ttl_minutes is the only bound on that, same as
// everywhere else stateless tokens are used in this codebase.
func (c *AuthUseCase) SwitchContext(ctx context.Context, userID int64, request *model.SwitchContextRequest) (*model.LoginResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	assignment, err := c.UserRepository.FindGrantByID(ctx, c.DB, request.IDUserRole)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.Forbidden("grant does not exist or is not usable")
		}

		return nil, err
	}

	if !grantUsableBy(assignment, userID) {
		return nil, model.Forbidden("grant does not exist or is not usable")
	}

	user, err := c.UserRepository.FindByID(ctx, c.DB, userID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "user not found")
	}

	if err := c.attachRolesForLogin(ctx, user); err != nil {
		return nil, err
	}

	modelAktif := toActiveContext(&assignment.RoleGrant)

	token, expiresAt, err := c.issue(user, modelAktif)
	if err != nil {
		return nil, err
	}

	return &model.LoginResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresAt: expiresAt,
		User:      converter.UserToResponse(user),
		Grants:    toGrantList(user.Roles),
		Aktif:     modelAktif,
	}, nil
}

// grantUsableBy reports whether assignment belongs to userID and is still
// live: its role active, and — if it is scoped to one — its unit active too. A
// global grant (nil IDUnitKerja) has no unit to check.
func grantUsableBy(assignment *entity.RoleAssignment, userID int64) bool {
	if assignment.UserID != userID {
		return false
	}

	if !assignment.Role.IsAktif {
		return false
	}

	if assignment.IDUnitKerja != nil {
		if assignment.IsAktifUnitKerja == nil || !*assignment.IsAktifUnitKerja {
			return false
		}
	}

	return true
}

// Authenticate decodes a bearer token into a session. The delivery middleware calls
// this and never parses a token itself.
func (c *AuthUseCase) Authenticate(token string) (*model.Session, error) {
	if token == "" {
		return nil, model.Unauthorized("missing bearer token")
	}

	parsed, err := jwt.ParseWithClaims(
		token,
		new(claims),
		func(*jwt.Token) (any, error) { return c.secret, nil },
		// Pinning the algorithm is what stops the alg=none and
		// RS256-verified-as-HS256 substitutions: without it, the parser accepts
		// whatever the token's own header claims it was signed with.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(c.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		// Expiry is the one failure a client can act on, so it says so. Everything
		// else — bad signature, wrong issuer, malformed — is one answer, because
		// the difference only helps someone probing the token format.
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, model.Unauthorized("token expired")
		}

		return nil, model.Unauthorized("invalid token")
	}

	payload, ok := parsed.Claims.(*claims)
	if !ok {
		return nil, model.Unauthorized("invalid token")
	}

	userID, err := parseUserID(payload.Subject)
	if err != nil {
		return nil, model.Unauthorized("invalid token")
	}

	// Never nil: a user with no grants must still serialise as [] in
	// GET /api/v1/auth/me, and HasAnyRole must be safe to call either way.
	grants := payload.Grants
	if grants == nil {
		grants = []model.Grant{}
	}

	return &model.Session{
		UserID:   userID,
		Username: payload.Username,
		Grants:   grants,
		Aktif:    payload.Aktif,
	}, nil
}

// issue mints a signed token for a user whose usable grants are already loaded
// (attachRolesForLogin), acting as aktif — which may be nil, meaning the
// caller has not chosen a context yet.
func (c *AuthUseCase) issue(user *entity.User, aktif *model.ActiveContext) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(c.ttl)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", user.ID),
			Issuer:    c.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Username: user.Username,
		Grants:   toGrantList(user.Roles),
		Aktif:    aktif,
	})

	signed, err := token.SignedString(c.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}

	return signed, expiresAt, nil
}

// attachRolesForLogin loads the grants that go into the token.
//
// Only usable grants are included: role active, and — if the grant is scoped
// to a unit — that unit active too. A retired role or a retired unit still
// shows in the user-management view (GET /api/v1/user) — the grant is real and
// needs revoking — but neither may authorize anything nor appear as a choice
// in the switcher menu, and this is the seam where that distinction is
// applied.
func (c *AuthUseCase) attachRolesForLogin(ctx context.Context, user *entity.User) error {
	assignments, err := c.UserRepository.FindRolesByUserIDs(ctx, c.DB, []int64{user.ID})
	if err != nil {
		return err
	}

	user.Roles = nil

	for _, assignment := range assignments {
		if grantUsableBy(&assignment, user.ID) {
			user.Roles = append(user.Roles, assignment.RoleGrant)
		}
	}

	return nil
}

// toGrantList converts a user's loaded grants to the model shape the token,
// LoginResponse, and SessionResponse all share. Never nil.
func toGrantList(list []entity.RoleGrant) []model.Grant {
	grants := make([]model.Grant, len(list))

	for i, grant := range list {
		grants[i] = model.Grant{
			IDUserRole:    grant.ID,
			IDRole:        grant.Role.ID,
			Role:          grant.Role.Nama,
			IDUnitKerja:   grant.IDUnitKerja,
			NamaUnitKerja: grant.NamaUnitKerja,
		}
	}

	return grants
}

// toActiveContext converts the chosen grant, if any, to the model shape the
// token, LoginResponse, and SessionResponse all share.
func toActiveContext(aktif *entity.RoleGrant) *model.ActiveContext {
	if aktif == nil {
		return nil
	}

	return &model.ActiveContext{
		IDUserRole:  aktif.ID,
		Role:        aktif.Role.Nama,
		IDUnitKerja: aktif.IDUnitKerja,
	}
}

// parseUserID reads the subject claim back into an id.
func parseUserID(subject string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(subject, "%d", &id); err != nil {
		return 0, err
	}

	if id <= 0 {
		return 0, errors.New("subject is not a positive id")
	}

	return id, nil
}

// dummyHash is a real bcrypt hash of a fixed string, used only to spend comparable
// time when the username does not exist. Its plaintext is irrelevant and it is never
// a valid credential for any account.
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

func bcryptDummyCompare() {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte("timing"))
}
