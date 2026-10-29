package middleware

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// Inline Middleware code in a shipped example must compile under yaegi: a
// broken snippet would teach users code divisor refuses at startup.
func TestExampleInlineMiddlewaresCompile(t *testing.T) {
	examples, err := filepath.Glob("../../examples/*.yaml")
	require.NoError(t, err)
	for _, example := range examples {
		cfg, err := config.ParseConfigFile(example)
		require.NoError(t, err, example)
		for _, declaredMiddleware := range cfg.Middlewares {
			if declaredMiddleware.Code == "" || declaredMiddleware.Disabled {
				continue
			}
			t.Run(filepath.Base(example)+"/"+declaredMiddleware.Name, func(t *testing.T) {
				_, err := NewExecutor([]config.Middleware{declaredMiddleware})
				assert.NoError(t, err)
			})
		}
	}
}

// The guide's Go blocks are copies, so each must still read verbatim in the
// compiled chain or the contract it quotes; only illustrative fragments are exempt.
func TestMiddlewareGuideGoBlocksAreCopiedFromCompiledCode(t *testing.T) {
	guide, err := os.ReadFile("../../docs/middleware.md")
	require.NoError(t, err)
	cfg, err := config.ParseConfigFile("../../examples/middleware-chain.config.yaml")
	require.NoError(t, err)
	contract, err := os.ReadFile("../../middleware/middleware.go")
	require.NoError(t, err)

	copiedFrom := []string{strings.ReplaceAll(normalizeLineEndings(string(contract)), "\t", "    ")}
	for _, declaredMiddleware := range cfg.Middlewares {
		copiedFrom = append(copiedFrom, declaredMiddleware.Code)
	}
	illustrativeFragments := []string{"// Wrong: the slice points into a buffer the next request overwrites."}

	goBlocks := fencedGoBlocks(normalizeLineEndings(string(guide)))
	require.NotEmpty(t, goBlocks)
	for _, goBlock := range goBlocks {
		firstLine, _, _ := strings.Cut(goBlock, "\n")
		if slices.Contains(illustrativeFragments, firstLine) {
			continue
		}
		isCopied := slices.ContainsFunc(copiedFrom, func(source string) bool { return strings.Contains(source, goBlock) })
		assert.True(t, isCopied, "docs/middleware.md Go block starting %q is not verbatim in examples/middleware-chain.config.yaml or middleware/middleware.go", firstLine)
	}
}

var fencedGoBlockOpening = regexp.MustCompile("^( *)```go$")

// fencedGoBlocks returns each ```go block's body, dedented by its fence's indent
// (a block nested in a list item is indented in the Markdown only).
func fencedGoBlocks(markdown string) []string {
	var goBlocks []string
	lines := strings.Split(markdown, "\n")
	for i := 0; i < len(lines); i++ {
		opening := fencedGoBlockOpening.FindStringSubmatch(lines[i])
		if opening == nil {
			continue
		}
		indent := opening[1]
		var body []string
		for i++; i < len(lines) && lines[i] != indent+"```"; i++ {
			body = append(body, strings.TrimPrefix(lines[i], indent))
		}
		goBlocks = append(goBlocks, strings.Join(body, "\n"))
	}
	return goBlocks
}

func normalizeLineEndings(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// docs/middleware.md walks through this chain; each subtest is a claim the guide makes.
func TestMiddlewareGuideExampleChain(t *testing.T) {
	cfg, err := config.ParseConfigFile("../../examples/middleware-chain.config.yaml")
	require.NoError(t, err)
	require.NoError(t, cfg.PrepareConfig())
	executor, err := NewExecutor(cfg.Middlewares)
	require.NoError(t, err)

	newRequest := func(headers map[string]string) *middleware.Context {
		ctx := &fasthttp.RequestCtx{}
		for name, value := range headers {
			ctx.Request.Header.Set(name, value)
		}
		return middleware.NewContext(ctx)
	}

	t.Run("a request without the API key is answered 401 before any Backend is asked", func(t *testing.T) {
		ctx := newRequest(nil)
		err := executor.RunOnRequest(ctx)
		assert.ErrorIs(t, err, middleware.ErrShortCircuit)
		assert.Equal(t, fasthttp.StatusUnauthorized, ctx.Response.StatusCode())
		assert.Empty(t, ctx.Request.Header.Peek("X-Request-Id"), "no Middleware after the guard runs")
	})

	t.Run("a request with the API key is forwarded without it, carrying a request id", func(t *testing.T) {
		ctx := newRequest(map[string]string{"X-Api-Key": "change-me"})
		require.NoError(t, executor.RunOnRequest(ctx))
		assert.Empty(t, ctx.Request.Header.Peek("X-Api-Key"))
		assert.Equal(t, "divisor-1", string(ctx.Request.Header.Peek("X-Request-Id")))

		next := newRequest(map[string]string{"X-Api-Key": "change-me"})
		require.NoError(t, executor.RunOnRequest(next))
		assert.Equal(t, "divisor-2", string(next.Request.Header.Peek("X-Request-Id")), "one instance serves every request")
	})

	t.Run("a request id the client sent is kept", func(t *testing.T) {
		ctx := newRequest(map[string]string{"X-Api-Key": "change-me", "X-Request-Id": "from-client"})
		require.NoError(t, executor.RunOnRequest(ctx))
		assert.Equal(t, "from-client", string(ctx.Request.Header.Peek("X-Request-Id")))
	})

	t.Run("a Backend failure is replaced by the fallback", func(t *testing.T) {
		ctx := newRequest(map[string]string{"X-Api-Key": "change-me"})
		require.NoError(t, executor.RunOnRequest(ctx))
		ctx.Response.Header.Set("X-Partial", "left by a Backend that failed mid-read")

		err := executor.RunOnResponse(ctx, errors.New("dial tcp: connection refused"))
		assert.ErrorIs(t, err, middleware.ErrShortCircuit)
		assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
		assert.Equal(t, "5", string(ctx.Response.Header.Peek("Retry-After")))
		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
		assert.JSONEq(t, `{"message":"service temporarily unavailable"}`, string(ctx.Response.Body()))
		assert.Empty(t, ctx.Response.Header.Peek("X-Partial"))
	})

	t.Run("a Backend response, even a 500, passes through untouched", func(t *testing.T) {
		ctx := newRequest(map[string]string{"X-Api-Key": "change-me"})
		require.NoError(t, executor.RunOnRequest(ctx))
		ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Response.SetBodyString("backend says no")

		require.NoError(t, executor.RunOnResponse(ctx, nil))
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		assert.Equal(t, "backend says no", string(ctx.Response.Body()))
	})
}
