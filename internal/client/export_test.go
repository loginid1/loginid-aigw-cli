package client

import (
	"context"
	"net"
)

// SetLookupIPAddr swaps the resolver loopbackOnlyTransport uses and returns a
// function that restores it.
func SetLookupIPAddr(f func(context.Context, string) ([]net.IPAddr, error)) func() {
	old := lookupIPAddr
	lookupIPAddr = f

	return func() { lookupIPAddr = old }
}
