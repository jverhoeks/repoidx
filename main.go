// Command repoidx indexes the git repositories on your machine and lets you
// search and filter them.
package main

import (
	"os"

	"github.com/jverhoeks/repoidx/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
