package verify

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
	"golang.org/x/time/rate"

	"github.com/charlie0129/pcp/pkg/hasher"
	"github.com/charlie0129/pcp/pkg/lister"
	"github.com/charlie0129/pcp/pkg/utils/log"
	"github.com/charlie0129/pcp/pkg/utils/progress"
	"github.com/charlie0129/pcp/pkg/utils/size"
)

// Lister config
var listerConfig = lister.Config{}

// Hasher config
var (
	maxConcurrentFiles = 16
	blockSize          = "256k"
)

var (
	transferRateLimitStr string
	fileRateLimitStr     string
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "verify [flags] SOURCE... DEST",
		Short:        "Verify files copied",
		Args:         cobra.MinimumNArgs(2),
		RunE:         runVerify,
		SilenceUsage: true,
	}

	f := cmd.Flags()

	f.StringVar(&blockSize, "block-size", blockSize, "Internal input and output block size (e.g., 32k, 1m)")
	f.IntVarP(&maxConcurrentFiles, "concurrent-files", "c", maxConcurrentFiles, "Maximum number of files to hashed concurrently, including both source and destination files")

	f.StringVar(&transferRateLimitStr, "transfer-rate-limit", "", "Limit bytes hashed per second (e.g., 1m, 500k), including both source and destination files")
	f.StringVar(&fileRateLimitStr, "file-rate-limit", "", "Limit files hashed per second (e.g., 10, 1k), including both source and destination files")

	return cmd
}

func runVerify(cmd *cobra.Command, args []string) error {
	var logger zerolog.Logger
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)

	var progressBar *progress.Progress
	progressDone := make(chan struct{})
	if term.IsTerminal(int(os.Stderr.Fd())) {
		progressBar = progress.New(os.Stderr, 100*time.Millisecond)
		defer func() {
			cancel()
			<-progressDone
		}()
		go func() {
			defer close(progressDone)
			progressBar.Start(ctx)
		}()
	}

	if progressBar == nil {
		logger = log.GetLogger(os.Stderr, false)
	} else {
		logger = log.GetLogger(progressBar, true)
	}

	listerConfig.SourcePaths = args[:len(args)-1]
	listerConfig.DestinationPath = args[len(args)-1]
	listerConfig.DoNotCreateDirs = true
	err := listerConfig.Validate()
	if err != nil {
		return err
	}

	l := lister.New(listerConfig, logger)
	files := make(chan lister.File, 4096)
	eg.Go(func() error {
		defer close(files)
		return l.Start(ctx, files)
	})

	hasherConfig := hasher.Config{
		MaxConcurrentFiles: maxConcurrentFiles,
		CopyBufferSize:     int(size.MustParse(blockSize)),
	}
	err = hasherConfig.Validate()
	if err != nil {
		return err
	}

	transferRateLimit := size.MustParse(transferRateLimitStr)
	fileRateLimit := size.MustParse(fileRateLimitStr)

	eg.Go(func() error {
		var transferRateLimiter *rate.Limiter
		var fileRateLimiter *rate.Limiter
		if transferRateLimit > 0 {
			// Each file is processed by a goroutine, and each goroutine copies one block at a time.
			// So we multiply them to get the burst to ensure they can get to transfer.
			transferRateLimiter = rate.NewLimiter(rate.Limit(transferRateLimit), int(size.MustParse(blockSize))*maxConcurrentFiles)
		}
		if fileRateLimit > 0 {
			fileRateLimiter = rate.NewLimiter(rate.Limit(fileRateLimit), 1*maxConcurrentFiles)
		}

		h := hasher.New(hasherConfig, logger)

		progressBar.SetStatsGetter(h.Stats)

		return h.Start(ctx, files, transferRateLimiter, fileRateLimiter)
	})

	err = eg.Wait()
	if err != nil {
		return err
	}

	return nil
}
