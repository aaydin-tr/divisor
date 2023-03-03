package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync/atomic"

	"github.com/valyala/fasthttp"
)

var reqCount uint32

type response struct {
	Message    string            `json:"message"`
	Hostname   string            `json:"hostname"`
	ReqCounter uint32            `json:"req_counter"`
	Headers    map[string]string `json:"headers"`
}

func main() {
	if err := fasthttp.ListenAndServe(":8080", requestHandler); err != nil {
		log.Fatalf("Error in ListenAndServe: %v", err)
	}
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	atomic.AddUint32(&reqCount, 1)
	headers := make(map[string]string)
	ctx.Request.Header.VisitAll(func(key, value []byte) {
		headers[string(key)] = string(value)
	})
	res := response{
		Message:    fmt.Sprintf("This is a JSON response From %s counter %d", os.Getenv("hostname"), atomic.LoadUint32(&reqCount)),
		Hostname:   os.Getenv("hostname"),
		ReqCounter: atomic.LoadUint32(&reqCount),
		Headers:    headers,
	}
	ctx.SetContentType("application/json")
	b, _ := json.Marshal(res)
	ctx.SetBody(b)
}
