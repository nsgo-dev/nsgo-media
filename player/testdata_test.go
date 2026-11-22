package player

import (
	"os"
	"path/filepath"
)

// testdata is the repository's testdata directory, or the one
// NSGO_MEDIA_TESTDATA names where the tests run away from the checkout, as
// on a simulator: go tool nsgo test -target ios_simulator -env
// NSGO_MEDIA_TESTDATA=$PWD/testdata.
func testdata() string {
	if dir := os.Getenv("NSGO_MEDIA_TESTDATA"); dir != "" {
		return dir
	}
	return filepath.Join("..", "testdata")
}
