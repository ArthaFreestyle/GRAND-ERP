package usecase

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
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
// The access tokens are stateless JWTs: nothing about THEM is stored server-side and
// no lookup happens per request to validate one. That half is unchanged and is not
// reopened by isu #24 — Authenticate still costs nothing but a signature check. What
// isu #24 adds is a second, deliberately stateful credential: the refresh token,
// stored in Redis via RefreshTokenRepository, which is what can actually be revoked
// (logout, password change, is_aktif = false, every grant revoked). An access token
// itself still cannot be revoked — TTL is the only bound on it, which is why
// jwt.ttl_minutes shrank from 60 to 15: that is now the entire residual window after
// a session is revoked, not the session's whole lifetime.
//
// Switching active context (isu #12 fase 4) mints a brand new access token and, on
// purpose, no refresh token — SwitchContext's shape is unchanged by isu #24. It still
// cannot revoke the access token it replaces either, the same limitation, stated
// again there because it is easy to assume otherwise specifically at that call site.
//
// It imports jwt but not Fiber: the middleware in the delivery layer calls
// Authenticate and receives a *model.Session, so HTTP stays on the far side.
type AuthUseCase struct {
	DB                      *sql.DB
	Log                     *logrus.Logger
	Validate                *validator.Validate
	UserRepository          *repository.UserRepository
	RefreshTokenRepository  *repository.RefreshTokenRepository
	LoginThrottleRepository *repository.LoginThrottleRepository

	secret         []byte
	ttl            time.Duration
	refreshTTL     time.Duration
	issuer         string
	maxAttempts    int
	throttleWindow time.Duration
}

func NewAuthUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	userRepository *repository.UserRepository,
	refreshTokenRepository *repository.RefreshTokenRepository,
	loginThrottleRepository *repository.LoginThrottleRepository,
	secret string,
	ttl time.Duration,
	refreshTTL time.Duration,
	issuer string,
	maxAttempts int,
	throttleWindow time.Duration,
) *AuthUseCase {
	return &AuthUseCase{
		DB:                      db,
		Log:                     log,
		Validate:                validate,
		UserRepository:          userRepository,
		RefreshTokenRepository:  refreshTokenRepository,
		LoginThrottleRepository: loginThrottleRepository,
		secret:                  []byte(secret),
		ttl:                     ttl,
		refreshTTL:              refreshTTL,
		issuer:                  issuer,
		maxAttempts:             maxAttempts,
		throttleWindow:          throttleWindow,
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
// Every failure answers the same "username or password is wrong" — unknown
// username, wrong password, disabled account, and now (isu #24 fase 4) a
// throttled (ip, username) pair too. Distinguishing any of them hands an
// attacker either a way to enumerate valid usernames or a way to tell
// "you're being rate-limited" from "you typed it wrong", and the operator
// gains nothing from any of those differences.
//
// A caller holding more than one usable grant gets a token with no active
// context (isu #12 fase 4): with grants now bound to a place, there is no
// default that would not risk someone acting under an authority they did not
// realize they had picked, so the token can only reach switch-context and
// auth/me until one is chosen. Holding exactly one grant has no such ambiguity
// and is selected automatically.
//
// ip is the caller's address as Fiber's own ctx.IP() reports it — no
// trusted-proxy list is configured anywhere in this codebase, so behind a
// reverse proxy every request can present the same address and the per-pair
// throttle degrades toward per-username. Documented, not solved here.
func (c *AuthUseCase) Login(ctx context.Context, ip string, request *model.LoginRequest) (*model.LoginResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	key := loginThrottleKey(ip, request.Username)

	throttled, err := c.isThrottled(ctx, key)
	if err != nil {
		return nil, err
	}
	if throttled {
		// Same timing shape as the unknown-username branch below: a real
		// bcrypt comparison, so a throttled reply costs the same wall-clock
		// time as a wrong-password one.
		bcryptDummyCompare()

		return nil, model.Unauthorized("username or password is wrong")
	}

	user, err := c.UserRepository.FindByUsernameWithPassword(ctx, c.DB, request.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Compare against a dummy hash anyway. Returning early here makes an
			// unknown username measurably faster to reject than a wrong password,
			// which leaks exactly what the identical message is hiding.
			bcryptDummyCompare()
			c.recordLoginFailure(ctx, key)

			return nil, model.Unauthorized("username or password is wrong")
		}

		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(request.Password)); err != nil {
		c.recordLoginFailure(ctx, key)

		return nil, model.Unauthorized("username or password is wrong")
	}

	// Checked after the password, deliberately: answering "account disabled" to
	// anyone who names a disabled user tells them the account exists.
	if !user.IsAktif {
		c.recordLoginFailure(ctx, key)

		return nil, model.Unauthorized("username or password is wrong")
	}

	// A legitimate caller who eventually gets it right should not go on
	// paying for earlier typos.
	if err := c.LoginThrottleRepository.Reset(ctx, key); err != nil {
		c.Log.WithError(err).Warn("gagal mereset penghitung login throttle")
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

	refreshToken, err := c.issueRefresh(ctx, user.ID, idUserRoleOf(modelAktif))
	if err != nil {
		return nil, err
	}

	return &model.LoginResponse{
		Token:        token,
		TokenType:    "Bearer",
		ExpiresAt:    expiresAt,
		RefreshToken: refreshToken,
		User:         converter.UserToResponse(user),
		Grants:       toGrantList(user.Roles),
		Aktif:        modelAktif,
	}, nil
}

// isThrottled reports whether key has already reached the attempt ceiling. It
// only reads — recordLoginFailure is what actually advances the count, and
// only on a genuine failure, never on a throttled attempt itself (that would
// let a caller extend their own lockout indefinitely just by retrying, which
// is a self-inflicted denial of service dressed up as a security feature).
func (c *AuthUseCase) isThrottled(ctx context.Context, key string) (bool, error) {
	count, err := c.LoginThrottleRepository.Peek(ctx, key)
	if err != nil {
		return false, err
	}

	return count >= int64(c.maxAttempts), nil
}

// recordLoginFailure advances the throttle counter. Errors are logged, not
// returned: a Redis hiccup must not turn into a 500 on top of an ordinary
// wrong-password response, and losing one increment only costs the caller one
// extra attempt before the ceiling bites.
func (c *AuthUseCase) recordLoginFailure(ctx context.Context, key string) {
	if _, err := c.LoginThrottleRepository.Increment(ctx, key, c.throttleWindow); err != nil {
		c.Log.WithError(err).Warn("gagal mencatat kegagalan login untuk throttle")
	}
}

// loginThrottleKey pairs ip and username (case-insensitively, mirroring every
// other username comparison in this codebase) — isu #24 fase 4's explicit
// requirement that the limit is computed per pair, not per username alone, so
// one attacker spamming one victim's username cannot lock that victim out
// from their own, different, IP.
func loginThrottleKey(ip, username string) string {
	return fmt.Sprintf("login_throttle:%s:%s", ip, strings.ToLower(username))
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

// issueRefresh mints and stores a new refresh token for userID, remembering
// idUserRole (the access token's active grant, if any) so Refresh can later
// reissue an access token that carries the same active context forward —
// isu #24 fase 2. Never called by SwitchContext, which mints an access token
// only, unchanged from before this issue.
func (c *AuthUseCase) issueRefresh(ctx context.Context, userID int64, idUserRole *int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}

	record := repository.RefreshRecord{UserID: userID, IDUserRole: idUserRole}
	if err := c.RefreshTokenRepository.Store(ctx, token, record, c.refreshTTL); err != nil {
		return "", err
	}

	return token, nil
}

// idUserRoleOf extracts the active grant's own id out of an ActiveContext, or
// nil when there is none — the shape issueRefresh stores and Refresh later
// re-resolves through FindGrantByID + grantUsableBy, the same re-check
// SwitchContext already performs against the database rather than trusting a
// stale claim.
func idUserRoleOf(aktif *model.ActiveContext) *int64 {
	if aktif == nil {
		return nil
	}

	id := aktif.IDUserRole

	return &id
}

// randomToken generates an opaque, unguessable refresh token — 256 bits from
// crypto/rand, base64url-encoded. It is deliberately not a JWT: Authenticate
// only ever tries to parse a bearer token as one, so a refresh token handed
// to a protected route fails to parse and is rejected the same way any
// garbage string would be — no special-casing needed anywhere to keep a
// refresh token out of routes that expect an access token.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ChangePassword is POST /api/v1/auth/me/password — isu #24 fase 1. userID
// comes from the caller's own session (the same way SwitchContext takes it as
// a parameter rather than a body field): this endpoint can only ever change
// the caller's own password, never anyone else's.
//
// PasswordLama is verified even though the caller already holds a valid
// session, and a wrong one answers the identical "username or password is
// wrong" used everywhere else in this module — a stolen access token must not
// be able to lock the real account holder out by changing the password out
// from under them, and the rejection must not read any differently than an
// ordinary login failure.
func (c *AuthUseCase) ChangePassword(ctx context.Context, userID int64, request *model.ChangePasswordRequest) (*model.UserResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	user, err := c.UserRepository.FindByIDWithPassword(ctx, c.DB, userID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(request.PasswordLama)); err != nil {
		return nil, model.Unauthorized("username or password is wrong")
	}

	hash, err := hashPassword(request.PasswordBaru)
	if err != nil {
		return nil, err
	}

	updated, err := c.UserRepository.Update(ctx, c.DB, userID, repository.UserPatch{
		Password:     &hash,
		SetUpdatedBy: true,
		UpdatedBy:    &userID,
	})
	if err != nil {
		return nil, notFoundOnNoRows(err, "user not found")
	}

	// isu #24 fase 3: a password change revokes every refresh token the user
	// holds. Best-effort — the password write already committed, so a Redis
	// failure here must not turn a successful change into an error response;
	// it only means the (already ≤15-minute-bounded) residual access window
	// does not shrink to zero immediately.
	if err := c.RefreshTokenRepository.RevokeAllForUser(ctx, userID); err != nil {
		c.Log.WithError(err).WithField("user_id", userID).
			Warn("gagal mencabut refresh token setelah ganti password")
	}

	return converter.UserToResponse(updated), nil
}

// Refresh exchanges a refresh token for a brand new access/refresh pair —
// isu #24 fase 2. The old refresh token is consumed (deleted) atomically
// before anything else happens, so it can never be presented again: a
// concurrent replay of the same token, whether from a legitimate second
// device racing a rotation or an attacker who intercepted it, meets
// ErrRefreshTokenNotFound and is refused.
//
// The active context carried by the old token is re-resolved against the
// database, not trusted from the stored record — the same reasoning
// SwitchContext already applies: a grant revoked or retired since the token
// was issued must not survive a refresh. If it is no longer usable, the new
// access token simply has no active context, same as a fresh Login would
// produce, and the caller must switch-context again.
func (c *AuthUseCase) Refresh(ctx context.Context, request *model.RefreshTokenRequest) (*model.LoginResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	const invalidMessage = "refresh token tidak valid atau sudah kedaluwarsa"

	record, err := c.RefreshTokenRepository.Consume(ctx, request.RefreshToken)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil, model.Unauthorized(invalidMessage)
		}

		return nil, err
	}

	user, err := c.UserRepository.FindByID(ctx, c.DB, record.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.Unauthorized(invalidMessage)
		}

		return nil, err
	}

	if !user.IsAktif {
		return nil, model.Unauthorized(invalidMessage)
	}

	if err := c.attachRolesForLogin(ctx, user); err != nil {
		return nil, err
	}

	var modelAktif *model.ActiveContext
	if record.IDUserRole != nil {
		assignment, err := c.UserRepository.FindGrantByID(ctx, c.DB, *record.IDUserRole)
		if err == nil && grantUsableBy(assignment, user.ID) {
			modelAktif = toActiveContext(&assignment.RoleGrant)
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		// Any other outcome — unknown grant, or one no longer usable — leaves
		// modelAktif nil, the same "not chosen yet" shape Login already
		// produces for an ambiguous set of grants.
	}

	token, expiresAt, err := c.issue(user, modelAktif)
	if err != nil {
		return nil, err
	}

	refreshToken, err := c.issueRefresh(ctx, user.ID, idUserRoleOf(modelAktif))
	if err != nil {
		return nil, err
	}

	return &model.LoginResponse{
		Token:        token,
		TokenType:    "Bearer",
		ExpiresAt:    expiresAt,
		RefreshToken: refreshToken,
		User:         converter.UserToResponse(user),
		Grants:       toGrantList(user.Roles),
		Aktif:        modelAktif,
	}, nil
}

// Logout revokes one refresh token — isu #24 fase 2. It answers success
// whether or not the token was still live: an already-expired or
// already-used token reaching logout has already achieved what logout wants,
// and treating that as an error would give a caller a way to probe whether a
// token value was ever valid.
func (c *AuthUseCase) Logout(ctx context.Context, request *model.LogoutRequest) error {
	if err := c.Validate.Struct(request); err != nil {
		return err
	}

	return c.RefreshTokenRepository.Delete(ctx, request.RefreshToken)
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
