package root

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
	"golang.org/x/time/rate"

	"github.com/charlie0129/pcp/pkg/lister"
	"github.com/charlie0129/pcp/pkg/utils/log"
	"github.com/charlie0129/pcp/pkg/utils/progress"
	"github.com/charlie0129/pcp/pkg/utils/size"
	"github.com/charlie0129/pcp/pkg/worker"
)

func runCopy(cmd *cobra.Command, args []string) error {
	var logger zerolog.Logger
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)

	var progressBar *progress.Progress
	progressDone := make(chan struct{})
	if term.IsTerminal(int(os.Stderr.Fd())) {
		progressBar = progress.New(os.Stderr, 100*time.Millisecond)

		// So we can print out summary.
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
	listerConfig.Force = force
	listerConfig.PreserveOwner = preserveOwner
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

	workerConfig := worker.Config{
		MaxConcurrentChunks:   maxConcurrentChunks,
		MaxConcurrentSymlinks: maxConcurrentSymlinks,
		ChunkSize:             size.MustParse(chunkSize),
		BlockSize:             int(size.MustParse(blockSize)),
		Force:                 force,
		PreserveOwner:         preserveOwner,
	}
	err = workerConfig.Validate()
	if err != nil {
		return err
	}

	transferRateLimit := size.MustParse(transferRateLimitStr)
	fileRateLimit := size.MustParse(fileRateLimitStr)

	eg.Go(func() error {
		var transferRateLimiter *rate.Limiter
		var fileRateLimiter *rate.Limiter
		if transferRateLimit > 0 {
			// Each chunk is processed by a goroutine, and each goroutine copies one block at a time.
			// So we multiply them to get the burst to ensure they can get to transfer.
			transferRateLimiter = rate.NewLimiter(rate.Limit(transferRateLimit), int(size.MustParse(blockSize))*maxConcurrentChunks)
		}
		if fileRateLimit > 0 {
			fileRateLimiter = rate.NewLimiter(rate.Limit(fileRateLimit), 1*maxConcurrentChunks) // Worst case: 1 chunk per file
		}

		w := worker.New(workerConfig, logger)
		if progressBar != nil {
			progressBar.SetStatsGetter(w.Stats)
		}

		return w.Start(ctx, files, transferRateLimiter, fileRateLimiter)
	})

	err = eg.Wait()
	if err != nil {
		return err
	}

	return nil
}
