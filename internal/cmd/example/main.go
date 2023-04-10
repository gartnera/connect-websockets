package main

import (
	"fmt"

	"github.com/gartnera/connect-websockets/internal/server"
)

func main() {
	wrapper := server.NewPingServerWrapper(8000)
	fmt.Printf("Running on port %d\n", wrapper.Port)
	wrapper.Wait()
}
