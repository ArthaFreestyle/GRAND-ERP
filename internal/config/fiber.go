package config

import (
	"errors"

	"Arthafreestyle/ERP/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// NewFiber builds the HTTP app. Routes are registered later, in
// internal/delivery/http/route.
func NewFiber(cfg *viper.Viper, log *logrus.Logger) *fiber.App {
	return fiber.New(fiber.Config{
		AppName:      cfg.GetString("app.name"),
		ErrorHandler: newErrorHandler(log),
	})
}

// newErrorHandler turns anything a handler returns into the standard envelope.
// Handlers therefore just `return err` instead of formatting responses.
func newErrorHandler(log *logrus.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		var validationErrs validator.ValidationErrors
		if errors.As(err, &validationErrs) {
			fields := make(map[string]string, len(validationErrs))
			for _, fieldErr := range validationErrs {
				fields[fieldErr.Field()] = fieldErr.Tag()
			}

			return c.Status(fiber.StatusBadRequest).JSON(model.WebResponse[any]{
				Errors:           "validation failed",
				ValidationErrors: fields,
			})
		}

		code := fiber.StatusInternalServerError
		message := "internal server error"

		var domainErr *model.Error
		if errors.As(err, &domainErr) {
			code = statusForKind(domainErr.Kind)
			if code < fiber.StatusInternalServerError {
				message = domainErr.Message
			}
		}

		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			code = fiberErr.Code
			message = fiberErr.Message
		}

		if code >= fiber.StatusInternalServerError {
			log.WithError(err).WithFields(logrus.Fields{
				"method": c.Method(),
				"path":   c.Path(),
			}).Error("unhandled error")
		}

		return c.Status(code).JSON(model.WebResponse[any]{Errors: message})
	}
}

// statusForKind is the only place domain error kinds become HTTP codes.
func statusForKind(kind model.ErrorKind) int {
	switch kind {
	case model.KindInvalid:
		return fiber.StatusBadRequest
	case model.KindUnauthorized:
		return fiber.StatusUnauthorized
	case model.KindForbidden:
		return fiber.StatusForbidden
	case model.KindNotFound:
		return fiber.StatusNotFound
	case model.KindConflict:
		return fiber.StatusConflict
	case model.KindInternal:
		return fiber.StatusInternalServerError
	default:
		return fiber.StatusInternalServerError
	}
}
