package proxy

import (
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

func copyTrailerKeys(keys [][]byte) [][]byte {
	if len(keys) == 0 {
		return nil
	}
	copied := make([][]byte, len(keys))
	for i, key := range keys {
		copied[i] = append([]byte(nil), key...)
	}
	return copied
}

// reannounceTrailers re-registers the client's trailers for the Backend hop.
// The Trailer header is hop-by-hop and stripped by preReq, and fasthttp's Del of
// it also forgets which headers are trailers; without this they would be
// sent to the Backend as ordinary headers.
func reannounceTrailers(req *fasthttp.Request, trailerKeys [][]byte) {
	for _, key := range trailerKeys {
		if err := req.Header.AddTrailerBytes(key); err != nil {
			zap.S().Warnf("dropping forbidden trailer %q: %v", key, err)
		}
	}
}
