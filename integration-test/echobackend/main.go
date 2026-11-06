// Echo backend: the purpose-built test Backend for the divisor integration
// suite. It identifies which instance served a request, echoes the request
// back, and exposes knobs for delay, forced statuses, connection-level
// failures, and health toggling.
//
// Env:
//
//	BACKEND_ID     - instance identity, echoed as X-Backend-Id and in JSON (required)
//	START_HEALTHY  - "false" to boot with a failing /health (default healthy)
//	RESPONSE_DELAY - Go duration added to every echo response (not /health)
//
// Endpoints:
//
//	GET  /health           -> 200 "ok" when healthy, 503 otherwise (divisor's Probe target)
//	POST /health?ok=bool   -> toggle health state
//	GET  /counters?key=K   -> {"count": N} attempts seen for fail_key K, or
//	                          streams closed early for client_gone_key K
//	GET  /stream           -> text/event-stream, length-less: frames=N frames
//	                          "data: <i>\n\n" (default 3), each flushed, one
//	                          every interval=<dur> (default 1s);
//	                          client_gone_key=K counts the stream under K when
//	                          its client (divisor) closes it before the last frame
//	GET  /bytes?size=N&seed=S -> N pseudo-random bytes (ChaCha8 keyed by S) with a
//	                          Content-Length, X-Body-Seed: S and X-Body-Sha256: their hex SHA-256
//	*    /*                -> echo JSON, with query knobs:
//	     delay=<dur>            sleep before responding
//	     status=<code>          respond with this status code
//	     fail_key=K&fail_times=N  hijack-close the connection for the first N
//	                              requests carrying key K (counts every attempt)
//	     rh=Name:Value          set extra response header (repeatable)
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxInlineBody = 64 * 1024

type echoResponse struct {
	Backend       string              `json:"backend"`
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	RawQuery      string              `json:"raw_query"`
	Proto         string              `json:"proto"`
	RemoteAddr    string              `json:"remote_addr"`
	ContentLength int64               `json:"content_length"`
	BodyLen       int                 `json:"body_len"`
	BodySha256    string              `json:"body_sha256"`
	BodyB64       string              `json:"body_b64,omitempty"`
	Headers       map[string][]string `json:"headers"`
}

type server struct {
	id       string
	healthy  atomic.Bool
	delay    time.Duration
	counters sync.Map // fail_key -> *uint64
}

func (s *server) attempt(key string) uint64 {
	v, _ := s.counters.LoadOrStore(key, new(uint64))
	return atomic.AddUint64(v.(*uint64), 1)
}

func (s *server) count(key string) uint64 {
	v, ok := s.counters.Load(key)
	if !ok {
		return 0
	}
	return atomic.LoadUint64(v.(*uint64))
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		ok, err := strconv.ParseBool(r.URL.Query().Get("ok"))
		if err != nil {
			http.Error(w, "ok must be a bool", http.StatusBadRequest)
			return
		}
		s.healthy.Store(ok)
		w.WriteHeader(http.StatusOK)
		return
	}
	if s.healthy.Load() {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (s *server) handleCounters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]uint64{"count": s.count(r.URL.Query().Get("key"))})
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	frames := 3
	if n := q.Get("frames"); n != "" {
		parsed, err := strconv.Atoi(n)
		if err != nil || parsed < 0 {
			http.Error(w, "frames must be a non-negative integer", http.StatusBadRequest)
			return
		}
		frames = parsed
	}
	interval := time.Second
	if d := q.Get("interval"); d != "" {
		parsed, err := time.ParseDuration(d)
		if err != nil {
			http.Error(w, "bad interval", http.StatusBadRequest)
			return
		}
		interval = parsed
	}

	flusher := w.(http.Flusher)
	w.Header().Set("X-Backend-Id", s.id)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	for i := range frames {
		if i > 0 {
			select {
			case <-time.After(interval):
			case <-r.Context().Done():
				if key := q.Get("client_gone_key"); key != "" {
					s.attempt(key)
				}
				return
			}
		}
		io.WriteString(w, "data: "+strconv.Itoa(i)+"\n\n")
		flusher.Flush()
	}
}

func (s *server) handleBytes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size, err := strconv.Atoi(q.Get("size"))
	if err != nil || size < 0 {
		http.Error(w, "size must be a non-negative integer", http.StatusBadRequest)
		return
	}
	seed, err := strconv.ParseUint(q.Get("seed"), 10, 64)
	if err != nil {
		http.Error(w, "seed must be a non-negative integer", http.StatusBadRequest)
		return
	}

	var key [32]byte
	binary.LittleEndian.PutUint64(key[:], seed)
	body := make([]byte, size)
	_, _ = rand.NewChaCha8(key).Read(body)
	sum := sha256.Sum256(body)

	w.Header().Set("X-Backend-Id", s.id)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.Header().Set("X-Body-Seed", strconv.FormatUint(seed, 10))
	w.Header().Set("X-Body-Sha256", hex.EncodeToString(sum[:]))
	_, _ = w.Write(body)
}

func (s *server) handleEcho(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if key := q.Get("fail_key"); key != "" {
		failTimes, err := strconv.ParseUint(q.Get("fail_times"), 10, 64)
		if err != nil {
			http.Error(w, "fail_times must be a non-negative integer", http.StatusBadRequest)
			return
		}
		if s.attempt(key) <= failTimes {
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if d := q.Get("delay"); d != "" {
		dur, err := time.ParseDuration(d)
		if err != nil {
			http.Error(w, "bad delay", http.StatusBadRequest)
			return
		}
		time.Sleep(dur)
	}

	sum := sha256.Sum256(body)
	resp := echoResponse{
		Backend:       s.id,
		Method:        r.Method,
		Path:          r.URL.Path,
		RawQuery:      r.URL.RawQuery,
		Proto:         r.Proto,
		RemoteAddr:    r.RemoteAddr,
		ContentLength: r.ContentLength,
		BodyLen:       len(body),
		BodySha256:    hex.EncodeToString(sum[:]),
		Headers:       r.Header,
	}
	if len(body) <= maxInlineBody {
		resp.BodyB64 = base64.StdEncoding.EncodeToString(body)
	}

	w.Header().Set("X-Backend-Id", s.id)
	for _, rh := range q["rh"] {
		name, value, ok := strings.Cut(rh, ":")
		if ok {
			w.Header().Set(name, value)
		}
	}
	w.Header().Set("Content-Type", "application/json")

	status := http.StatusOK
	if st := q.Get("status"); st != "" {
		n, err := strconv.Atoi(st)
		if err != nil || n < 100 || n > 599 {
			http.Error(w, "bad status", http.StatusBadRequest)
			return
		}
		status = n
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

func main() {
	id := os.Getenv("BACKEND_ID")
	if id == "" {
		log.Fatal("BACKEND_ID is required")
	}

	s := &server{id: id}
	s.healthy.Store(os.Getenv("START_HEALTHY") != "false")
	if d := os.Getenv("RESPONSE_DELAY"); d != "" {
		dur, err := time.ParseDuration(d)
		if err != nil {
			log.Fatalf("bad RESPONSE_DELAY %q: %v", d, err)
		}
		s.delay = dur
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/counters", s.handleCounters)
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/bytes", s.handleBytes)
	mux.HandleFunc("/", s.handleEcho)

	// Port 8080 is the harness's fixed contract (containerPort); a knob here
	// would suggest configurability the harness does not have.
	log.Printf("echo backend %s listening on :8080", id)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
