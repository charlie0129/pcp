package main

import (
	"os"

	"golang.org/x/term"

	"github.com/charlie0129/pcp/pkg/cmd/root"
	"github.com/charlie0129/pcp/pkg/cmd/verify"
	"github.com/charlie0129/pcp/pkg/utils/log"
)

var (
	logger = log.GetLogger(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())))
)

func main() {
	rootCmd := root.NewCommand()

	rootCmd.AddCommand(verify.NewCommand())

	if err := rootCmd.Execute(); err != nil {
		logger.Fatal().Err(err).Msg("Error executing pcp")
	}
}
