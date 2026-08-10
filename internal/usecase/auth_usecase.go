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

// claims is the token payload. Roles ride along so authorization needs no database
// query, which is the whole point of the stateless choice — and the reason a role
// change only takes effect at next login.
type claims struct {
	jwt.RegisteredClaims

	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// Login verifies a password and returns a token.
//
// Every failure answers the same "username or password is wrong". Distinguishing
// "no such user" from "wrong password" hands an attacker a way to enumerate valid
// usernames, and the operator gains nothing from the difference.
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

	token, expiresAt, err := c.issue(user)
	if err != nil {
		return nil, err
	}

	return &model.LoginResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresAt: expiresAt,
		User:      converter.UserToResponse(user),
	}, nil
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

	// Never nil: a user with no roles must still serialise as [] in
	// GET /api/v1/auth/me, and HasAnyRole must be safe to call either way.
	roles := payload.Roles
	if roles == nil {
		roles = []string{}
	}

	return &model.Session{
		UserID:   userID,
		Username: payload.Username,
		Roles:    roles,
	}, nil
}

// issue mints a signed token for a user whose roles are already loaded.
func (c *AuthUseCase) issue(user *entity.User) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(c.ttl)

	roles := make([]string, len(user.Roles))
	for i := range user.Roles {
		roles[i] = user.Roles[i].Nama
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", user.ID),
			Issuer:    c.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Username: user.Username,
		Roles:    roles,
	})

	signed, err := token.SignedString(c.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}

	return signed, expiresAt, nil
}

// attachRolesForLogin loads the roles that go into the token.
//
// Only active roles are included. A retired role still shows in the user-management
// view — the grant is real and needs revoking — but it must not authorize anything,
// and this is the seam where that distinction is applied.
func (c *AuthUseCase) attachRolesForLogin(ctx context.Context, user *entity.User) error {
	assignments, err := c.UserRepository.FindRolesByUserIDs(ctx, c.DB, []int64{user.ID})
	if err != nil {
		return err
	}

	user.Roles = nil

	for _, assignment := range assignments {
		if assignment.Role.IsAktif {
			user.Roles = append(user.Roles, assignment.Role)
		}
	}

	return nil
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
