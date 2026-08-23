package middleware

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"go.uber.org/zap"
)

var (
	ErrPackageNameEmpty    = errors.New("Package name is empty for middleware please provide a package name")
	ErrNewFunctionNotFound = errors.New("New function not found")
	ErrNewFunctionNotValid = errors.New("New function does not satisfy new function signature")
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

			// code/file presence is validated by config.PrepareConfig.
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
				return nil, fmt.Errorf("%w: %v", ErrNewFunctionNotFound, err)
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
		if err := runProtected(func() error { return mw.OnRequest(ctx) }); err != nil {
			return err
		}
	}

	return nil
}

// RunOnResponse runs the hooks in reverse config order, so the Middleware
// that saw the request first sees the response last, after every later one
// has had its say (CONTEXT.md, Short-circuit).
func (e *Executor) RunOnResponse(ctx *middleware.Context, err error) error {
	for i := len(e.middlewares) - 1; i >= 0; i-- {
		mw := e.middlewares[i]
		if resErr := runProtected(func() error { return mw.OnResponse(ctx, err) }); resErr != nil {
			return resErr
		}
	}
	return nil
}

func runProtected(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			zap.S().Errorf("Recovered panic in middleware: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("middleware panic: %v", r)
		}
	}()
	return fn()
}
