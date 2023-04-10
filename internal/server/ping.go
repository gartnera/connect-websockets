// Copyright 2021-2023 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/bufbuild/connect-go"
	pingv1 "github.com/gartnera/connect-websockets/internal/gen"
	"github.com/gartnera/connect-websockets/internal/gen/pingv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// The ping server implementation used in the tests returns errors if the
// client doesn't set a header, and the server sets headers and trailers on the
// response.
const (
	headerValue                 = "some header value"
	trailerValue                = "some trailer value"
	clientHeader                = "Connect-Client-Header"
	handlerHeader               = "Connect-Handler-Header"
	handlerTrailer              = "Connect-Handler-Trailer"
	clientMiddlewareErrorHeader = "Connect-Trigger-HTTP-Error"
)

const errorMessage = "oh no"

func expectClientHeader(check bool, req connect.AnyRequest) error {
	if !check {
		return nil
	}
	if err := expectMetadata(req.Header(), "header", clientHeader, headerValue); err != nil {
		return err
	}
	return nil
}

func expectMetadata(meta http.Header, metaType, key, value string) error {
	if got := meta.Get(key); got != value {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%s %q: got %q, expected %q",
			metaType,
			key,
			got,
			value,
		))
	}
	return nil
}

type PingServer struct {
	pingv1connect.UnimplementedPingServiceHandler

	CheckMetadata bool
}

func (p PingServer) Ping(ctx context.Context, request *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	if err := expectClientHeader(p.CheckMetadata, request); err != nil {
		return nil, err
	}
	if request.Peer().Addr == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no peer address"))
	}
	if request.Peer().Protocol == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no peer protocol"))
	}
	response := connect.NewResponse(
		&pingv1.PingResponse{
			Number: request.Msg.Number,
			Text:   request.Msg.Text,
		},
	)
	response.Header().Set(handlerHeader, headerValue)
	response.Trailer().Set(handlerTrailer, trailerValue)
	return response, nil
}

func (p PingServer) Fail(ctx context.Context, request *connect.Request[pingv1.FailRequest]) (*connect.Response[pingv1.FailResponse], error) {
	if err := expectClientHeader(p.CheckMetadata, request); err != nil {
		return nil, err
	}
	if request.Peer().Addr == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no peer address"))
	}
	if request.Peer().Protocol == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no peer protocol"))
	}
	err := connect.NewError(connect.Code(request.Msg.Code), errors.New(errorMessage))
	err.Meta().Set(handlerHeader, headerValue)
	err.Meta().Set(handlerTrailer, trailerValue)
	return nil, err
}

func (p PingServer) Sum(
	ctx context.Context,
	stream *connect.ClientStream[pingv1.SumRequest],
) (*connect.Response[pingv1.SumResponse], error) {
	if p.CheckMetadata {
		if err := expectMetadata(stream.RequestHeader(), "header", clientHeader, headerValue); err != nil {
			return nil, err
		}
	}
	if stream.Peer().Addr == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no peer address"))
	}
	if stream.Peer().Protocol == "" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no peer protocol"))
	}
	var sum int64
	for stream.Receive() {
		sum += stream.Msg().Number
	}
	if stream.Err() != nil {
		return nil, stream.Err()
	}
	response := connect.NewResponse(&pingv1.SumResponse{Sum: sum})
	response.Header().Set(handlerHeader, headerValue)
	response.Trailer().Set(handlerTrailer, trailerValue)
	return response, nil
}

func (p PingServer) CountUp(
	ctx context.Context,
	request *connect.Request[pingv1.CountUpRequest],
	stream *connect.ServerStream[pingv1.CountUpResponse],
) error {
	if err := expectClientHeader(p.CheckMetadata, request); err != nil {
		return err
	}
	/*
		if request.Peer().Addr == "" {
			return connect.NewError(connect.CodeInternal, errors.New("no peer address"))
		}
		if request.Peer().Protocol == "" {
			return connect.NewError(connect.CodeInternal, errors.New("no peer protocol"))
		}
	*/
	if request.Msg.Number <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"number must be positive: got %v",
			request.Msg.Number,
		))
	}
	stream.ResponseHeader().Set(handlerHeader, headerValue)
	stream.ResponseTrailer().Set(handlerTrailer, trailerValue)
	for i := int64(1); i <= request.Msg.Number; i++ {
		if err := stream.Send(&pingv1.CountUpResponse{Number: i}); err != nil {
			return err
		}
	}
	return nil
}

func (p PingServer) CumSum(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.CumSumRequest, pingv1.CumSumResponse],
) error {
	var sum int64
	if p.CheckMetadata {
		if err := expectMetadata(stream.RequestHeader(), "header", clientHeader, headerValue); err != nil {
			return err
		}
	}
	/*
		if stream.Peer().Addr == "" {
			return connect.NewError(connect.CodeInternal, errors.New("no peer address"))
		}
		if stream.Peer().Protocol == "" {
			return connect.NewError(connect.CodeInternal, errors.New("no peer address"))
		}
	*/
	stream.ResponseHeader().Set(handlerHeader, headerValue)
	stream.ResponseTrailer().Set(handlerTrailer, trailerValue)
	for {
		msg, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		sum += msg.Number
		if err := stream.Send(&pingv1.CumSumResponse{Sum: sum}); err != nil {
			return err
		}
	}
}

type PingServerWrapper struct {
	Cancel     func()
	Server     *PingServer
	Port       int
	Addr       net.Addr
	HttpServer *http.Server
	Done       chan error
	ctx        context.Context
}

func (e *PingServerWrapper) Cleanup() error {
	e.HttpServer.Shutdown(e.ctx)
	e.Cancel()
	return <-e.Done
}

func (e *PingServerWrapper) Wait() error {
	return <-e.Done
}

func (e *PingServerWrapper) BaseURLStr() string {
	return fmt.Sprintf("http://localhost:%d", e.Port)
}

type extraHandlersT func(h http.Handler) http.Handler

// NewPingServerWrapper creates and starts an example server
//
// provide port 0 for automatic port selection
func NewPingServerWrapper(port int, extraHandlers ...extraHandlersT) *PingServerWrapper {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		panic(err)
	}
	pingServer := &PingServer{}
	mux := http.NewServeMux()
	path, handler := pingv1connect.NewPingServiceHandler(pingServer)
	mux.Handle(path, handler)
	handler = h2c.NewHandler(mux, &http2.Server{})
	for _, extraHandler := range extraHandlers {
		handler = extraHandler(handler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &http.Server{Handler: handler, BaseContext: func(l net.Listener) context.Context { return ctx }}
	w := &PingServerWrapper{
		Cancel:     cancel,
		Server:     pingServer,
		Port:       l.Addr().(*net.TCPAddr).Port,
		Addr:       l.Addr(),
		HttpServer: srv,
		Done:       make(chan error),
		ctx:        ctx,
	}
	go func() {
		err := srv.Serve(l)
		w.Done <- err
	}()
	return w
}
