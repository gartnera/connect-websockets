package tests

import (
	"context"
	"net/http"
	"testing"

	"github.com/bufbuild/connect-go"
	pingv1 "github.com/gartnera/connect-websockets/internal/gen"
	"github.com/gartnera/connect-websockets/internal/gen/pingv1connect"
	"github.com/gartnera/connect-websockets/internal/server"

	"github.com/stretchr/testify/require"
)

func TestUnary(t *testing.T) {
	ctx := context.Background()
	exampleServer := server.NewPingServerWrapper(0)
	defer exampleServer.Cleanup()
	client := pingv1connect.NewPingServiceClient(http.DefaultClient, exampleServer.BaseURLStr())
	defer http.DefaultClient.CloseIdleConnections()

	payload := "asdf"
	req := connect.NewRequest(&pingv1.PingRequest{Text: payload})
	res, err := client.Ping(ctx, req)
	require.NoError(t, err)
	require.Equal(t, payload, res.Msg.Text)
}

func TestServerStream(t *testing.T) {
	ctx := context.Background()
	exampleServer := server.NewPingServerWrapper(0)
	defer exampleServer.Cleanup()
	client := pingv1connect.NewPingServiceClient(http.DefaultClient, exampleServer.BaseURLStr())
	defer http.DefaultClient.CloseIdleConnections()

	count := int64(10)
	req := connect.NewRequest(&pingv1.CountUpRequest{Number: count})
	stream, err := client.CountUp(ctx, req)
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		ok := stream.Receive()
		require.True(t, ok)
		msg := stream.Msg()
		require.Equal(t, msg.Number, int64(i+1))
	}
	ok := stream.Receive()
	require.False(t, ok)
}
