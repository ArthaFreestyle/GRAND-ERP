package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"Arthafreestyle/ERP/internal/model"

	"github.com/gofiber/fiber/v3"
)

// newApp gives the test app an error handler that turns a domain error into its
// status, the way config.NewFiber does in production. Without it every rejection
// arrives as a 500 and the test cannot tell 401 from 403.
func newApp() *fiber.App {
	return fiber.New(fiber.Config{
		ErrorHandler: func(ctx fiber.Ctx, err error) error {
			var domainErr *model.Error
			if ok := asDomainError(err, &domainErr); ok {
				switch domainErr.Kind {
				case model.KindUnauthorized:
					return ctx.Status(http.StatusUnauthorized).SendString(domainErr.Message)
				case model.KindForbidden:
					return ctx.Status(http.StatusForbidden).SendString(domainErr.Message)
				default:
					return ctx.Status(http.StatusInternalServerError).SendString(domainErr.Message)
				}
			}

			return ctx.Status(http.StatusInternalServerError).SendString(err.Error())
		},
	})
}

func asDomainError(err error, target **model.Error) bool {
	domainErr, ok := err.(*model.Error)
	if ok {
		*target = domainErr
	}

	return ok
}

// withSession injects an authenticated caller, standing in for NewAuth.
func withSession(session *model.Session) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		ctx.Locals(sessionKey, session)

		return ctx.Next()
	}
}

// This is the test that matters most in this file.
//
// Fiber v3 registers routes as Get(path, handler, handlers...) and runs the chain in
// the order given, so a role guard passed AFTER the controller runs after the response
// is already written — that is, never, because a controller does not call Next(). The
// guard would look present in the route table and protect nothing.
func TestRouteGuardsRunBeforeHandler(t *testing.T) {
	cashier := &model.Session{UserID: 1, Username: "kasir", Roles: []string{"CASHIER"}}

	t.Run("guard first blocks the handler", func(t *testing.T) {
		app := newApp()
		reached := false

		app.Get("/x",
			withSession(cashier),
			RequireRole("SUPERADMIN"),
			func(ctx fiber.Ctx) error {
				reached = true

				return ctx.SendString("secret")
			},
		)

		response, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if reached {
			t.Error("handler ran despite the role guard rejecting the caller")
		}

		if response.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", response.StatusCode)
		}
	})

	// Documents the hazard rather than endorsing it: the same guard placed last does
	// not protect the route. If Fiber's ordering ever changes, this fails and the
	// comment in route.go needs revisiting.
	t.Run("guard last does not protect the route", func(t *testing.T) {
		app := newApp()
		reached := false

		app.Get("/y",
			withSession(cashier),
			func(ctx fiber.Ctx) error {
				reached = true

				return ctx.SendString("secret")
			},
			RequireRole("SUPERADMIN"),
		)

		if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/y", nil)); err != nil {
			t.Fatalf("request: %v", err)
		}

		if !reached {
			t.Error("handler did not run; Fiber's chain order may have changed, " +
				"so the ordering comment in route.go should be rechecked")
		}
	})
}

func TestRequireRoleAcceptsAnyOfTheNamedRoles(t *testing.T) {
	session := &model.Session{UserID: 1, Username: "gudang", Roles: []string{"INVENTARIS"}}

	app := newApp()
	app.Get("/x", withSession(session), RequireRole("SUPERADMIN", "INVENTARIS"),
		func(ctx fiber.Ctx) error { return ctx.SendString("ok") })

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — holding any one listed role is enough", response.StatusCode)
	}
}

// Role names are unique case-insensitively in the database, so "cashier" and "CASHIER"
// are one role and must not authorize differently.
func TestRequireRoleIgnoresCase(t *testing.T) {
	session := &model.Session{UserID: 1, Username: "kasir", Roles: []string{"cashier"}}

	app := newApp()
	app.Get("/x", withSession(session), RequireRole("CASHIER"),
		func(ctx fiber.Ctx) error { return ctx.SendString("ok") })

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
}

// Guard mounted without NewAuth: 401, not 403. There is no proof of who is asking, so
// claiming the caller is known but unauthorized would be wrong.
func TestRequireRoleWithoutSessionIsUnauthorized(t *testing.T) {
	app := newApp()
	app.Get("/x", RequireRole("SUPERADMIN"),
		func(ctx fiber.Ctx) error { return ctx.SendString("ok") })

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", response.StatusCode)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{name: "no header", header: "", want: ""},
		{name: "canonical scheme", header: "Bearer abc.def.ghi", want: "abc.def.ghi"},
		// RFC 7235 defines the scheme as case-insensitive, and clients send both.
		{name: "lowercase scheme", header: "bearer abc", want: "abc"},
		{name: "uppercase scheme", header: "BEARER abc", want: "abc"},
		{name: "wrong scheme", header: "Basic abc", want: ""},
		{name: "scheme only", header: "Bearer", want: ""},
		{name: "raw token without scheme", header: "abc.def.ghi", want: ""},
		{name: "padded token", header: "Bearer   abc  ", want: "abc"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			app := fiber.New()

			var got string
			app.Get("/x", func(ctx fiber.Ctx) error {
				got = bearerToken(ctx)

				return ctx.SendString("ok")
			})

			request := httptest.NewRequest(http.MethodGet, "/x", nil)
			if testCase.header != "" {
				request.Header.Set(fiber.HeaderAuthorization, testCase.header)
			}

			if _, err := app.Test(request); err != nil {
				t.Fatalf("request: %v", err)
			}

			if got != testCase.want {
				t.Errorf("bearerToken(%q) = %q, want %q", testCase.header, got, testCase.want)
			}
		})
	}
}
