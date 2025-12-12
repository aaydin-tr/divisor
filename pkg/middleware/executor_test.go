package middleware

import (
	"os"
	"testing"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

// Helper function to create a test context
func createTestContext() *middleware.Context {
	ctx := &fasthttp.RequestCtx{}
	return middleware.NewContext(ctx)
}

// Valid middleware code for testing
const validMiddlewareCode = `
package middleware

import (
	"github.com/aaydin-tr/divisor/middleware"
)

type TestMiddleware struct {
	config map[string]any
}

func (m *TestMiddleware) OnRequest(ctx *middleware.Context) error {
	return nil
}

func (m *TestMiddleware) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &TestMiddleware{config: config}
}
`

// Middleware that returns an error on request
const errorMiddlewareCode = `
package middleware

import (
	"errors"
	"github.com/aaydin-tr/divisor/middleware"
)

type ErrorMiddleware struct{}

func (m *ErrorMiddleware) OnRequest(ctx *middleware.Context) error {
	return errors.New("middleware error")
}

func (m *ErrorMiddleware) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &ErrorMiddleware{}
}
`

// Middleware without New function
const noNewFunctionCode = `
package middleware

import (
	"github.com/aaydin-tr/divisor/middleware"
)

type TestMiddleware struct{}

func (m *TestMiddleware) OnRequest(ctx *middleware.Context) error {
	return nil
}

func (m *TestMiddleware) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}
`

// Invalid Go code
const invalidGoCode = `
package middleware

this is not valid go code!!!
`

// Middleware that modifies request
const modifyingMiddlewareCode = `
package middleware

import (
	"github.com/aaydin-tr/divisor/middleware"
)

type ModifyingMiddleware struct {
	headerKey   string
	headerValue string
}

func (m *ModifyingMiddleware) OnRequest(ctx *middleware.Context) error {
	ctx.Request.Header.Set(m.headerKey, m.headerValue)
	return nil
}

func (m *ModifyingMiddleware) OnResponse(ctx *middleware.Context, err error) error {
	ctx.Response.Header.Set(m.headerKey, m.headerValue)
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &ModifyingMiddleware{
		headerKey:   config["key"].(string),
		headerValue: config["value"].(string),
	}
}
`

func TestNewExecutor(t *testing.T) {
	t.Run("valid middleware with inline code", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)
		assert.Len(t, executor.middlewares, 1)
	})

	t.Run("valid middleware with file reference", func(t *testing.T) {
		// Create a temporary file with middleware code
		tmpFile, err := os.CreateTemp("", "middleware-*.go")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(validMiddlewareCode)
		assert.NoError(t, err)
		tmpFile.Close()

		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				File:     tmpFile.Name(),
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)
		assert.Len(t, executor.middlewares, 1)
	})

	t.Run("multiple middlewares", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test1",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "test2",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "test3",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)
		assert.Len(t, executor.middlewares, 3)
	})

	t.Run("disabled middleware should be skipped", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "enabled",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "disabled",
				Disabled: true,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "enabled2",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)
		assert.Len(t, executor.middlewares, 2)
	})

	t.Run("empty middleware list", func(t *testing.T) {
		configs := []config.Middleware{}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.Nil(t, executor)
	})

	t.Run("middleware with config", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "modifying",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-Custom-Header",
					"value": "custom-value",
				},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)
		assert.Len(t, executor.middlewares, 1)
	})

	t.Run("missing New function", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				Code:     noNewFunctionCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.Equal(t, ErrNewFunctionNotFound, err)
	})

	t.Run("invalid Go code syntax", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				Code:     invalidGoCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
	})

	t.Run("non-existent file path", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				File:     "/non/existent/path/middleware.go",
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
	})

	t.Run("file with invalid content", func(t *testing.T) {
		// Create a temporary file with invalid code
		tmpFile, err := os.CreateTemp("", "invalid-middleware-*.go")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(invalidGoCode)
		assert.NoError(t, err)
		tmpFile.Close()

		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				File:     tmpFile.Name(),
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
	})

	t.Run("file without New function", func(t *testing.T) {
		// Create a temporary file without New function
		tmpFile, err := os.CreateTemp("", "no-new-middleware-*.go")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(noNewFunctionCode)
		assert.NoError(t, err)
		tmpFile.Close()

		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				File:     tmpFile.Name(),
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.Equal(t, ErrNewFunctionNotFound, err)
	})

	t.Run("mix of valid and invalid middlewares", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "valid",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "invalid",
				Disabled: false,
				Code:     invalidGoCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
	})

	t.Run("both code and file are empty", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "empty",
				Disabled: false,
				Code:     "",
				File:     "",
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.Equal(t, ErrCodeAndFileEmpty, err)
	})

	t.Run("both code and file are set", func(t *testing.T) {
		// Create a temporary file with middleware code
		tmpFile, err := os.CreateTemp("", "middleware-*.go")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(validMiddlewareCode)
		assert.NoError(t, err)
		tmpFile.Close()

		configs := []config.Middleware{
			{
				Name:     "both-set",
				Disabled: false,
				Code:     validMiddlewareCode,
				File:     tmpFile.Name(),
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.Equal(t, ErrCodeAndFileBothSet, err)
	})

	t.Run("empty code and file for disabled middleware should not error", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "disabled-empty",
				Disabled: true,
				Code:     "",
				File:     "",
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)
		assert.Len(t, executor.middlewares, 0)
	})

	t.Run("mix of valid and empty code/file middlewares", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "valid",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "empty",
				Disabled: false,
				Code:     "",
				File:     "",
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.Equal(t, ErrCodeAndFileEmpty, err)
	})

	t.Run("mix of valid and both-set middlewares", func(t *testing.T) {
		// Create a temporary file with middleware code
		tmpFile, err := os.CreateTemp("", "middleware-*.go")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(validMiddlewareCode)
		assert.NoError(t, err)
		tmpFile.Close()

		configs := []config.Middleware{
			{
				Name:     "valid",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "both-set",
				Disabled: false,
				Code:     validMiddlewareCode,
				File:     tmpFile.Name(),
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.Equal(t, ErrCodeAndFileBothSet, err)
	})
}

func TestRunOnRequest(t *testing.T) {
	t.Run("single middleware execution", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnRequest(ctx)
		assert.NoError(t, err)
	})

	t.Run("multiple middlewares execution chain", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test1",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "test2",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "test3",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnRequest(ctx)
		assert.NoError(t, err)
	})

	t.Run("middleware returns error", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "error",
				Disabled: false,
				Code:     errorMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnRequest(ctx)
		assert.Error(t, err)
		assert.Equal(t, "middleware error", err.Error())
	})

	t.Run("first middleware in chain fails", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "error",
				Disabled: false,
				Code:     errorMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "valid",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnRequest(ctx)
		assert.Error(t, err)
		assert.Equal(t, "middleware error", err.Error())
	})

	t.Run("middleware modifies request", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "modifying",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-Custom-Header",
					"value": "custom-value",
				},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnRequest(ctx)
		assert.NoError(t, err)

		// Verify the header was set
		headerValue := string(ctx.Request.Header.Peek("X-Custom-Header"))
		assert.Equal(t, "custom-value", headerValue)
	})

	t.Run("multiple middlewares modify request in sequence", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "first",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-First",
					"value": "first-value",
				},
			},
			{
				Name:     "second",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-Second",
					"value": "second-value",
				},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnRequest(ctx)
		assert.NoError(t, err)

		// Verify both headers were set
		firstHeader := string(ctx.Request.Header.Peek("X-First"))
		assert.Equal(t, "first-value", firstHeader)

		secondHeader := string(ctx.Request.Header.Peek("X-Second"))
		assert.Equal(t, "second-value", secondHeader)
	})
}

func TestRunOnResponse(t *testing.T) {
	t.Run("single middleware execution", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnResponse(ctx, nil)
		assert.NoError(t, err)
	})

	t.Run("multiple middlewares execution chain", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "test1",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "test2",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
			{
				Name:     "test3",
				Disabled: false,
				Code:     validMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnResponse(ctx, nil)
		assert.NoError(t, err)
	})

	t.Run("middleware modifies response", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "modifying",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-Custom-Response",
					"value": "response-value",
				},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnResponse(ctx, nil)
		assert.NoError(t, err)

		// Verify the response header was set
		headerValue := string(ctx.Response.Header.Peek("X-Custom-Response"))
		assert.Equal(t, "response-value", headerValue)
	})

	t.Run("multiple middlewares modify response in sequence", func(t *testing.T) {
		configs := []config.Middleware{
			{
				Name:     "first",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-Response-First",
					"value": "first-response",
				},
			},
			{
				Name:     "second",
				Disabled: false,
				Code:     modifyingMiddlewareCode,
				Config: map[string]any{
					"key":   "X-Response-Second",
					"value": "second-response",
				},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnResponse(ctx, nil)
		assert.NoError(t, err)

		// Verify both response headers were set
		firstHeader := string(ctx.Response.Header.Peek("X-Response-First"))
		assert.Equal(t, "first-response", firstHeader)

		secondHeader := string(ctx.Response.Header.Peek("X-Response-Second"))
		assert.Equal(t, "second-response", secondHeader)
	})

	t.Run("OnResponse with middleware that had error in OnRequest", func(t *testing.T) {
		// Even if middleware errors in OnRequest, OnResponse should still work
		configs := []config.Middleware{
			{
				Name:     "error",
				Disabled: false,
				Code:     errorMiddlewareCode,
				Config:   map[string]any{},
			},
		}

		executor, err := NewExecutor(configs)
		assert.NoError(t, err)
		assert.NotNil(t, executor)

		ctx := createTestContext()
		err = executor.RunOnResponse(ctx, nil)
		assert.NoError(t, err)
	})
}
