// Internal test package: it calls the unexported issue so a token can be minted
// without a database. Login needs PostgreSQL; token handling does not, and these are
// the parts worth testing without one.
package usecase

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func newAuth(t *testing.T, ttl time.Duration) *AuthUseCase {
	t.Helper()

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	// nil DB: Authenticate and issue never touch it.
	return NewAuthUseCase(nil, log, nil, nil, testSecret, ttl, "grand-erp")
}

func testUser() *entity.User {
	return &entity.User{
		ID:       7,
		Username: "kasir_pagi",
		Roles: []entity.Role{
			{ID: 2, Nama: "CASHIER", IsAktif: true},
			{ID: 3, Nama: "INVENTARIS", IsAktif: true},
		},
	}
}

func TestTokenRoundTripCarriesUserAndRoles(t *testing.T) {
	auth := newAuth(t, time.Hour)

	token, expiresAt, err := auth.issue(testUser())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if !expiresAt.After(time.Now()) {
		t.Errorf("expiresAt = %v, want a future time", expiresAt)
	}

	session, err := auth.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if session.UserID != 7 {
		t.Errorf("UserID = %d, want 7", session.UserID)
	}

	if session.Username != "kasir_pagi" {
		t.Errorf("Username = %q, want kasir_pagi", session.Username)
	}

	if !session.HasRole("CASHIER") || !session.HasRole("INVENTARIS") {
		t.Errorf("Roles = %v, want both CASHIER and INVENTARIS", session.Roles)
	}

	if session.HasRole("SUPERADMIN") {
		t.Error("session claims SUPERADMIN, which was never granted")
	}
}

// An expired token has to be refused, and say so — it is the one failure a client can
// act on by logging in again.
func TestExpiredTokenIsRejected(t *testing.T) {
	auth := newAuth(t, -time.Minute) // already past

	token, _, err := auth.issue(testUser())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	_, err = auth.Authenticate(token)
	if err == nil {
		t.Fatal("expired token was accepted")
	}

	assertUnauthorized(t, err)

	if got := err.(*model.Error).Message; got != "token expired" {
		t.Errorf("message = %q, want %q", got, "token expired")
	}
}

// The signature is the only thing standing between a caller and any identity they
// care to claim.
func TestTokenSignedWithAnotherSecretIsRejected(t *testing.T) {
	forger := NewAuthUseCase(nil, logrus.New(), nil, nil,
		"ffffffffffffffffffffffffffffffff", time.Hour, "grand-erp")

	token, _, err := forger.issue(&entity.User{
		ID:       1,
		Username: "penyusup",
		Roles:    []entity.Role{{Nama: "SUPERADMIN", IsAktif: true}},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := newAuth(t, time.Hour).Authenticate(token); err == nil {
		t.Fatal("a token signed with a different secret was accepted")
	} else {
		assertUnauthorized(t, err)
	}
}

// The classic JWT attack: strip the signature and declare alg=none. Rejecting it is
// why Authenticate pins the accepted methods instead of trusting the token header.
func TestAlgNoneTokenIsRejected(t *testing.T) {
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, &claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    "grand-erp",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Username: "penyusup",
		Roles:    []string{"SUPERADMIN"},
	})

	token, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build alg=none token: %v", err)
	}

	if _, err := newAuth(t, time.Hour).Authenticate(token); err == nil {
		t.Fatal("alg=none token was accepted; the signing method is not pinned")
	} else {
		assertUnauthorized(t, err)
	}
}

// A token minted for another service must not open this one, even with the same key.
func TestTokenFromAnotherIssuerIsRejected(t *testing.T) {
	other := NewAuthUseCase(nil, logrus.New(), nil, nil, testSecret, time.Hour, "layanan-lain")

	token, _, err := other.issue(testUser())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := newAuth(t, time.Hour).Authenticate(token); err == nil {
		t.Fatal("a token from another issuer was accepted")
	} else {
		assertUnauthorized(t, err)
	}
}

func TestGarbageAndEmptyTokensAreRejected(t *testing.T) {
	auth := newAuth(t, time.Hour)

	for _, token := range []string{"", "not-a-token", "a.b.c", "Bearer x"} {
		if _, err := auth.Authenticate(token); err == nil {
			t.Errorf("token %q was accepted", token)
		} else {
			assertUnauthorized(t, err)
		}
	}
}

// A user with no roles must produce an empty slice, not nil: GET /auth/me serialises
// it and HasAnyRole is called on it.
func TestSessionRolesAreNeverNil(t *testing.T) {
	auth := newAuth(t, time.Hour)

	token, _, err := auth.issue(&entity.User{ID: 9, Username: "belum_punya_role"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	session, err := auth.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if session.Roles == nil {
		t.Error("Roles is nil; it must be an empty slice")
	}
}

// Every rejection has to be a 401, not a bare error that the handler would turn into
// a 500 with a generic message.
func assertUnauthorized(t *testing.T, err error) {
	t.Helper()

	domainErr, ok := err.(*model.Error)
	if !ok {
		t.Fatalf("error is %T, want *model.Error", err)
	}

	if domainErr.Kind != model.KindUnauthorized {
		t.Errorf("kind = %v, want KindUnauthorized", domainErr.Kind)
	}
}
