package middleware

import "reflect"

// Symbols is the table handed to yaegi so a Middleware source can import
// github.com/aaydin-tr/divisor/middleware. The entries are generated into
// middleware_symbols.go (yaegi derives its own file name from the import
// path, hence the rename); rerun after any change to the middleware package.
//
//go:generate go run github.com/traefik/yaegi/cmd/yaegi extract github.com/aaydin-tr/divisor/middleware
//go:generate mv github_com-aaydin-tr-divisor-middleware.go middleware_symbols.go
var Symbols = map[string]map[string]reflect.Value{}
