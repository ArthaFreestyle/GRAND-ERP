package http

import (
	"Arthafreestyle/ERP/internal/delivery/http/middleware"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// AuthController binds HTTP to the auth usecase — parse, call, write.
//
// Nothing here logs a request body: a login body is a plaintext password. The Fiber
// logger middleware records only method, path, and status.
type AuthController struct {
	Log     *logrus.Logger
	UseCase *usecase.AuthUseCase
}

func NewAuthController(log *logrus.Logger, useCase *usecase.AuthUseCase) *AuthController {
	return &AuthController{Log: log, UseCase: useCase}
}

// Login exchanges credentials for a bearer token.
func (c *AuthController) Login(ctx fiber.Ctx) error {
	request := new(model.LoginRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	response, err := c.UseCase.Login(ctx.Context(), ctx.IP(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.LoginResponse]{Data: response})
}

// ChangePassword is isu #24 fase 1: any authenticated caller may change their
// own password, no role guard — the same tier as Me and SwitchContext. It
// binds the body as-is; there is no ActorID field to overwrite, because the
// target is always session.UserID, never something the body could redirect.
func (c *AuthController) ChangePassword(ctx fiber.Ctx) error {
	session, ok := middleware.SessionFrom(ctx)
	if !ok {
		return model.Unauthorized("authentication required")
	}

	request := new(model.ChangePasswordRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	response, err := c.UseCase.ChangePassword(ctx.Context(), session.UserID, request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.UserResponse]{Data: response})
}

// Refresh exchanges a refresh token for a new access/refresh pair — isu #24
// fase 2. No session required: refresh exists precisely for the case where
// the access token has already expired, so requiring a valid one here would
// defeat the endpoint's own purpose.
func (c *AuthController) Refresh(ctx fiber.Ctx) error {
	request := new(model.RefreshTokenRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	response, err := c.UseCase.Refresh(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.LoginResponse]{Data: response})
}

// Logout revokes a refresh token — isu #24 fase 2. Same shape as Refresh: no
// session required, the refresh token itself is the credential this action
// needs.
func (c *AuthController) Logout(ctx fiber.Ctx) error {
	request := new(model.LogoutRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	if err := c.UseCase.Logout(ctx.Context(), request); err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.LogoutResponse]{
		Data: &model.LogoutResponse{Message: "logged out"},
	})
}

// Me returns the caller as the token describes them.
//
// It reads the session rather than the database on purpose: this is what the server
// will actually authorize with, so it is the useful thing to be able to inspect. With
// stateless tokens that can differ from the stored user — a role revoked after the
// token was issued still appears here until it expires.
func (c *AuthController) Me(ctx fiber.Ctx) error {
	session, ok := middleware.SessionFrom(ctx)
	if !ok {
		return model.Unauthorized("authentication required")
	}

	return ctx.JSON(model.WebResponse[*model.SessionResponse]{
		Data: &model.SessionResponse{
			UserID:   session.UserID,
			Username: session.Username,
			Grants:   session.Grants,
			Aktif:    session.Aktif,
		},
	})
}

// SwitchContext exchanges the caller's session for a new token acting as one
// specific grant — isu #12 fase 4. No role guard: a session with no active
// context authorizes nothing else, so this and Me are the only routes such a
// session can reach at all, and that falls out of Session.HasRole rather than
// anything special-cased here or in route.go.
func (c *AuthController) SwitchContext(ctx fiber.Ctx) error {
	session, ok := middleware.SessionFrom(ctx)
	if !ok {
		return model.Unauthorized("authentication required")
	}

	request := new(model.SwitchContextRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	response, err := c.UseCase.SwitchContext(ctx.Context(), session.UserID, request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.LoginResponse]{Data: response})
}
