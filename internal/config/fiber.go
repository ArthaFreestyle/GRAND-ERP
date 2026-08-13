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
		BodyLimit:    bodyLimit(cfg),
		ErrorHandler: newErrorHandler(log),
	})
}

// bodyLimit sizes the request body Fiber will accept at all.
//
// It has to be derived from dokumen.max_size_mb rather than left alone: Fiber's
// default is 4 MB, and fasthttp refuses an oversized body before any handler runs —
// so a 10 MB limit configured for attachments would be silently capped at 4 MB, with
// the rejection arriving as a bare 413 that no usecase rule produced and no message
// explains.
//
// The extra megabyte is multipart's own overhead: the part headers, the boundaries,
// and any other field in the form all count towards this limit, while
// dokumen.max_size_mb is about the file's own bytes. Without the slack a file exactly
// at the limit is rejected by the transport instead of accepted by the rule that was
// supposed to decide.
func bodyLimit(cfg *viper.Viper) int {
	maxMB := cfg.GetInt("dokumen.max_size_mb")
	if maxMB <= 0 {
		return fiber.DefaultBodyLimit
	}

	return (maxMB + 1) * 1024 * 1024
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
