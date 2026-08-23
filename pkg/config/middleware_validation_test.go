package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateMiddlewares(t *testing.T) {
	cases := []struct {
		name       string
		middleware Middleware
		wantErr    error
	}{
		{"name required", Middleware{Code: "x"}, ErrMiddlewareNameRequired},
		{"code or file required", Middleware{Name: "m"}, ErrMiddlewareCodeAndFileEmpty},
		{"not both code and file", Middleware{Name: "m", Code: "x", File: "y"}, ErrMiddlewareCodeAndFileBothSet},
		{"disabled entry needs neither", Middleware{Name: "m", Disabled: true}, nil},
		{"code alone", Middleware{Name: "m", Code: "x"}, nil},
		{"file alone", Middleware{Name: "m", File: "y"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Middlewares: []Middleware{tc.middleware}}
			err := cfg.validateMiddlewares()
			if tc.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}
