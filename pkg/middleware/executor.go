package middleware

import (
	"errors"
	"os"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

var (
	ErrNewFunctionNotFound = errors.New("new function not found")
	ErrCodeAndFileEmpty    = errors.New("middleware code and file cannot both be empty")
	ErrCodeAndFileBothSet  = errors.New("middleware code and file cannot both be set, choose one")
)

type Executor struct {
	middlewares []middleware.Middleware
}

func NewExecutor(configs []config.Middleware) (*Executor, error) {
	var middlewares []middleware.Middleware
	for _, cfg := range configs {
		if cfg.Disabled {
			continue
		}

		if cfg.Code == "" && cfg.File == "" {
			return nil, ErrCodeAndFileEmpty
		}

		if cfg.Code != "" && cfg.File != "" {
			return nil, ErrCodeAndFileBothSet
		}

		i := interp.New(interp.Options{})

		if err := i.Use(stdlib.Symbols); err != nil {
			return nil, err
		}

		if err := i.Use(Symbols); err != nil {
			return nil, err
		}

		code := cfg.Code
		if cfg.File != "" {
			fileContent, err := os.ReadFile(cfg.File)
			if err != nil {
				return nil, err
			}

			code = helper.B2s(fileContent)
		}

		if _, err := i.Eval(code); err != nil {
			return nil, err
		}

		v, err := i.Eval("middleware.New")
		if err != nil {
			return nil, ErrNewFunctionNotFound
		}

		newFunc := v.Interface().(func(map[string]any) middleware.Middleware)
		middlewares = append(middlewares, newFunc(cfg.Config))
	}

	return &Executor{middlewares: middlewares}, nil
}

func (e *Executor) RunOnRequest(ctx *middleware.Context) error {
	for _, mw := range e.middlewares {
		if err := mw.OnRequest(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (e *Executor) RunOnResponse(ctx *middleware.Context, err error) error {
	for _, mw := range e.middlewares {
		if err := mw.OnResponse(ctx, err); err != nil {
			return err
		}
	}
	return nil
}
