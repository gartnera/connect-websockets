package connect_websockets

// mostly copied from https://github.com/tmc/grpc-websocket-proxy/blob/master/wsproxy/websocket_proxy.go
// with some connect specific adjustments

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func isClosedConnError(err error) bool {
	str := err.Error()
	if strings.Contains(str, "use of closed network connection") {
		return true
	}
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway)
}

type inMemoryResponseWriter struct {
	io.Writer
	header http.Header
	code   int
	closed chan bool
}

func newInMemoryResponseWriter(w io.Writer) *inMemoryResponseWriter {
	return &inMemoryResponseWriter{
		Writer: w,
		header: http.Header{},
		closed: make(chan bool, 1),
	}
}

func (w *inMemoryResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}
func (w *inMemoryResponseWriter) Header() http.Header {
	return w.header
}
func (w *inMemoryResponseWriter) WriteHeader(code int) {
	w.code = code
}
func (w *inMemoryResponseWriter) CloseNotify() <-chan bool {
	return w.closed
}
func (w *inMemoryResponseWriter) Flush() {}

type Proxy struct {
	Errors chan error
	h      http.Handler
}

func NewProxy(handler http.Handler) http.Handler {
	return &Proxy{
		Errors: make(chan error, 20),
		h:      handler,
	}
}

func NewProxyPanic(handler http.Handler) http.Handler {
	p := &Proxy{
		Errors: make(chan error, 20),
		h:      handler,
	}
	go func() {
		for {
			err := <-p.Errors
			fmt.Println(err)
		}
	}()
	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		p.h.ServeHTTP(w, r)
		return
	}
	p.proxy(w, r)
}

func (p *Proxy) proxy(w http.ResponseWriter, r *http.Request) {
	var responseHeader http.Header
	conn, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		p.Errors <- fmt.Errorf("unable to upgrade request: %w", err)
		return
	}
	defer conn.Close()
	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	requestBodyR, requestBodyW := io.Pipe()
	request, err := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), requestBodyR)
	if err != nil {
		p.Errors <- fmt.Errorf("unable to make request: %w", err)
		return
	}
	for key, value := range r.Header {
		request.Header.Set(key, value[0])
	}
	// force HTTP2 simulation to make connect-go bidi code happy
	request.ProtoMajor = 2

	responseBodyR, responseBodyW := io.Pipe()
	response := newInMemoryResponseWriter(responseBodyW)
	go func() {
		<-ctx.Done()
		requestBodyW.CloseWithError(io.EOF)
		responseBodyW.CloseWithError(io.EOF)
		response.closed <- true
	}()

	go func() {
		defer cancelFn()
		p.h.ServeHTTP(response, request)
	}()

	// read loop
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			_, reader, err := conn.NextReader()
			if err != nil {
				if !isClosedConnError(err) {
					p.Errors <- fmt.Errorf("unable to read payload: %w", err)
				}
				return
			}
			_, err = io.Copy(requestBodyW, reader)
			if err != nil {
				panic(err)
			}
		}
	}()
	var flags byte
	var payloadLen uint32
	for {
		err = binary.Read(responseBodyR, binary.BigEndian, &flags)
		if err != nil {
			break
		}
		err = binary.Read(responseBodyR, binary.BigEndian, &payloadLen)
		if err != nil {
			break
		}
		var writer io.WriteCloser
		writer, err = conn.NextWriter(websocket.BinaryMessage)
		if err != nil {
			break
		}
		_, err = writer.Write([]byte{flags})
		if err != nil {
			break
		}
		err = binary.Write(writer, binary.BigEndian, payloadLen)
		if err != nil {
			break
		}
		var n, count int64
		for count < int64(payloadLen) {
			n, err = io.CopyN(writer, responseBodyR, int64(payloadLen)-count)
			if err != nil {
				break
			}
			count += n
		}

		err = writer.Close()
		if err != nil {
			break
		}
	}
	if err != nil {
		p.Errors <- fmt.Errorf("unable to read response fully: %w", err)
	}
}
