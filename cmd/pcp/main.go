package main

import (
	"os"
	"runtime/pprof"

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

	// Only enable CPU profiling if explicitly requested via environment variable
	if os.Getenv("PCP_ENABLE_PROFILING") == "1" {
		f, err := os.Create("cpuprofile")
		if err != nil {
			logger.Fatal().Err(err).Msg("Failed to create cpuprofile file")
			return
		}

		err = pprof.StartCPUProfile(f)
		if err != nil {
			logger.Fatal().Err(err).Msg("Failed to start cpu profile")
		}
		defer f.Close()
		defer pprof.StopCPUProfile()
	}

	if err := rootCmd.Execute(); err != nil {
		logger.Fatal().Err(err).Msg("Error executing pcp")
	}
}
