package tests

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/url"
	"testing"

	connect_websockets "github.com/gartnera/connect-websockets"
	pingv1 "github.com/gartnera/connect-websockets/internal/gen"
	"github.com/gartnera/connect-websockets/internal/server"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func generateConnectMessage(msg proto.Message) []byte {
	res, err := proto.Marshal(msg)
	if err != nil {
		panic(err)
	}
	flags := make([]byte, 1)
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(res)))
	return bytes.Join([][]byte{flags, size, res}, []byte{})
}

func decodeConnectMessage[T proto.Message](data []byte, msg T) T {
	err := proto.Unmarshal(data[5:], msg)
	if err != nil {
		panic(err)
	}
	return msg
}

func TestWebsocketServerStream(t *testing.T) {
	exampleServer := server.NewPingServerWrapper(0, connect_websockets.NewProxy)
	defer exampleServer.Cleanup()

	u := url.URL{Scheme: "ws", Host: exampleServer.Addr.String(), Path: "/connect.ping.v1.PingService/CountUp"}
	headers := http.Header{}
	headers.Add("Content-Type", "application/connect+proto")
	headers.Add("Response-Type", "application/connect+proto")
	c, _, err := websocket.DefaultDialer.Dial(u.String(), headers)
	defer func() {
		_ = c.Close()
	}()
	require.NoError(t, err)
	number := 10
	err = c.WriteMessage(websocket.BinaryMessage, generateConnectMessage(&pingv1.CountUpRequest{Number: int64(number)}))
	require.NoError(t, err)

	for i := 0; i < number; i++ {
		msgType, rawMsg, err := c.ReadMessage()
		require.NoError(t, err, i)
		require.Equal(t, websocket.BinaryMessage, msgType)
		require.NotEmpty(t, rawMsg)
		require.Zero(t, rawMsg[0], "flags should be unset on normal messages")
		msg := decodeConnectMessage(rawMsg, &pingv1.CountUpResponse{})
		require.Equal(t, int64(i+1), msg.Number)
	}
	// final message
	// TODO: propagate headers correctly
	msgType, rawMsg, err := c.ReadMessage()
	require.Equal(t, websocket.BinaryMessage, msgType)
	require.NotZero(t, rawMsg[0], "flags should be set on final message")
	require.NoError(t, err)

	// the connection should now be closed
	_, _, err = c.ReadMessage()
	require.Error(t, err)
}

// test that we can handle an error
func TestWebsocketServerStreamError(t *testing.T) {
	exampleServer := server.NewPingServerWrapper(0, connect_websockets.NewProxy)
	defer exampleServer.Cleanup()

	u := url.URL{Scheme: "ws", Host: exampleServer.Addr.String(), Path: "/connect.ping.v1.PingService/CountUp"}
	headers := http.Header{}
	headers.Add("Content-Type", "application/connect+proto")
	headers.Add("Response-Type", "application/connect+proto")
	c, _, err := websocket.DefaultDialer.Dial(u.String(), headers)
	defer func() {
		_ = c.Close()
	}()
	require.NoError(t, err)
	err = c.WriteMessage(websocket.BinaryMessage, generateConnectMessage(&pingv1.CountUpRequest{Number: 0}))
	require.NoError(t, err)

	// final message
	// TODO: propagate code correctly
	msgType, rawMsg, err := c.ReadMessage()
	require.Equal(t, websocket.BinaryMessage, msgType)
	require.NotZero(t, rawMsg[0], "flags should be set on final message")
	require.NoError(t, err)

	// the connection should now be closed
	_, _, err = c.ReadMessage()
	require.Error(t, err)
}
