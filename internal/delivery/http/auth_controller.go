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

	response, err := c.UseCase.Login(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.LoginResponse]{Data: response})
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
			Roles:    session.Roles,
		},
	})
}
