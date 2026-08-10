// Package middleware holds Fiber middleware. It is part of the delivery layer: it
// parses HTTP, calls a usecase, and stores the result — it contains no business rule
// and never touches database/sql.
package middleware

import (
	"strings"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
)

// sessionKey is where the authenticated caller is stored on the request. Unexported
// with an accessor, so no handler can write a session it did not get from a token.
const sessionKey = "session"

// NewAuth rejects any request without a valid bearer token.
//
// It calls AuthUseCase.Authenticate rather than parsing the JWT itself, which is what
// keeps token handling in one place and out of the delivery layer.
func NewAuth(authUseCase *usecase.AuthUseCase) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		session, err := authUseCase.Authenticate(bearerToken(ctx))
		if err != nil {
			return err
		}

		ctx.Locals(sessionKey, session)

		return ctx.Next()
	}
}

// RequireRole rejects a caller lacking every one of the named roles. Holding any one
// of them is enough, because permissions are the union of a user's roles.
//
// It must be mounted after NewAuth. A missing session is treated as 401 rather than
// 403: without NewAuth there is no proof of who is asking, and answering 403 would
// claim the caller is known but unauthorized.
func RequireRole(names ...string) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		session, ok := SessionFrom(ctx)
		if !ok {
			return model.Unauthorized("authentication required")
		}

		if !session.HasAnyRole(names...) {
			return model.Forbidden("role tidak mencukupi untuk endpoint ini")
		}

		return ctx.Next()
	}
}

// SessionFrom returns the authenticated caller, if the request went through NewAuth.
//
// This is how a handler will fill created_by and updated_by once those columns start
// carrying an actor — the id comes from the token, never from the request body.
func SessionFrom(ctx fiber.Ctx) (*model.Session, bool) {
	session, ok := ctx.Locals(sessionKey).(*model.Session)

	return session, ok
}

// bearerToken pulls the credential out of the Authorization header.
//
// The scheme is compared case-insensitively because RFC 7235 defines it that way —
// "bearer" and "Bearer" are both valid and clients send both. An empty result is left
// for Authenticate to reject, so "missing" and "malformed" produce one answer.
func bearerToken(ctx fiber.Ctx) string {
	header := ctx.Get(fiber.HeaderAuthorization)

	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}

	return strings.TrimSpace(token)
}
