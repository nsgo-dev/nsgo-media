package main

import (
	"testing"

	"github.com/nsgo-dev/nsgo/nsgotest"
)

// TestMain gives the platform the main thread under nsgo test -target.
func TestMain(m *testing.M) { nsgotest.Main(m) }

func TestRootStarts(t *testing.T) {
	nsgotest.Start(t, Root)
}
