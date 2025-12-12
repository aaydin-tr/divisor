package middleware

import (
	"errors"
	"fmt"
	"os"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"go.uber.org/zap"
)

var (
	ErrPackageNameEmpty           = errors.New("Package name is empty for middleware please provide a package name")
	ErrNewFunctionNotFound        = errors.New("New function not found")
	ErrOnRequestFunctionNotFound  = errors.New("OnRequest function not found")
	ErrOnResponseFunctionNotFound = errors.New("OnResponse function not found")
	ErrCodeAndFileEmpty           = errors.New("Middleware code and file cannot both be empty")
	ErrCodeAndFileBothSet         = errors.New("Middleware code and file cannot both be set, choose one")
	ErrNewFunctionNotValid        = errors.New("New function does not satisfy new function signature")
	ErrOnRequestFunctionNotValid  = errors.New("OnRequest function does not satisfy OnRequest function signature")
	ErrOnResponseFunctionNotValid = errors.New("OnResponse function does not satisfy OnResponse function signature")
)

type Executor struct {
	middlewares []middleware.Middleware
}

func NewExecutor(configs []config.Middleware) (*Executor, error) {
	var middlewares []middleware.Middleware

	if len(configs) == 0 {
		return nil, nil
	}

	zap.S().Info("Middlewares are being prepared.")
	for _, cfg := range configs {
		mw, err := func(cfg config.Middleware) (mw middleware.Middleware, err error) {
			zap.S().Infof("Parsing middleware `%s`", cfg.Name)
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("Middleware `%s` parsing error: %v", cfg.Name, r)
				}
			}()

			if cfg.Disabled {
				return nil, nil
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
				b, err := os.ReadFile(cfg.File)
				if err != nil {
					return nil, err
				}
				code = helper.B2S(b)
			}

			program, err := i.Compile(code)
			if err != nil {
				return nil, err
			}

			if program.PackageName() == "" {
				return nil, ErrPackageNameEmpty
			}

			if _, err := i.Execute(program); err != nil {
				return nil, err
			}

			v, err := i.Eval(fmt.Sprintf("%s.New", program.PackageName()))
			if err != nil {
				return nil, ErrNewFunctionNotFound
			}

			newFunc, ok := v.Interface().(func(map[string]any) middleware.Middleware)
			if !ok {
				return nil, fmt.Errorf(
					"%s: use func New(config map[string]any) middleware.Middleware",
					ErrNewFunctionNotValid,
				)
			}

			mw = newFunc(cfg.Config)
			return mw, err
		}(cfg)

		if err != nil {
			return nil, err
		}

		if mw != nil {
			middlewares = append(middlewares, mw)
		}
	}

	zap.S().Info("Middlewares are prepared successfully.")
	zap.S().Infof("Prepared %d middlewares", len(middlewares))
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
