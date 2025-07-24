package hasher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/charlie0129/pcp/pkg/lister"
	"github.com/charlie0129/pcp/pkg/utils/cp"
	"github.com/charlie0129/pcp/pkg/utils/progress"
)

// HashOne computes the SHA-256 hash of a single file.
func HashOne(
	ctx context.Context,
	copyBuffer []byte,
	file string,
	rateLimiter *rate.Limiter,
	progressTracker func(int64, int64),
) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open file for hashing")
	}
	defer f.Close()

	hash := sha256.New()

	_, err = cp.Copy(ctx, hash, f,
		cp.WithBuffer(copyBuffer),
		cp.WithRateLimiter(rateLimiter),
		cp.WithProgressTracker(progressTracker),
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open file for hashing")
	}

	return hash.Sum(nil), nil
}

type Hasher struct {
	logger zerolog.Logger

	conf Config

	// Stats
	filesBeingHashed atomic.Int64
	filesHashed      atomic.Int64
	bytesHashed      atomic.Int64
	ioCompleted      atomic.Int64
}

func New(config Config, logger zerolog.Logger) *Hasher {
	return &Hasher{
		conf:   config,
		logger: logger,
	}
}

// Start starts the hasher, which will hash files from the provided channel.
//
// It returns error if any of the files have mismatched hashes.
//
// fileRateLimiter is shared between src and dst hashers, so you need to
// double the limit if you want to limit the rate of both source and destination.
func (h *Hasher) Start(
	ctx context.Context,
	files <-chan lister.File,
	transferRateLimiter *rate.Limiter,
	fileRateLimiter *rate.Limiter,
) error {
	eg, ctx := errgroup.WithContext(ctx)

	failedFiles := atomic.Int64{}

	for range h.conf.MaxConcurrentFiles {
		eg.Go(func() error {
			f := h.hashSourceAndDestination(ctx, files, transferRateLimiter, fileRateLimiter)
			failedFiles.Add(int64(f))
			return nil // Never return error here, as we want to process all files.
		})
	}

	err := eg.Wait()
	if err != nil {
		return errors.Wrap(err, "failed to hash files")
	}

	if failedFiles.Load() > 0 {
		return errors.New("some files have mismatched hashes, see logs for details")
	}

	return nil
}

func (h *Hasher) Stats() progress.Stats {
	return progress.Stats{
		FilesBeingProcessed: h.filesBeingHashed.Load(),
		FilesProcessed:      h.filesHashed.Load(),
		BytesProcessed:      h.bytesHashed.Load(),
		IOCompleted:         h.ioCompleted.Load(),
	}
}

func (h *Hasher) hashSourceAndDestination(
	ctx context.Context,
	files <-chan lister.File,
	transferRateLimiter *rate.Limiter,
	fileRateLimiter *rate.Limiter,
) int {
	failedFiles := 0

	// Spawn two goroutines to hash the source and destination files.
	srcBuffer := make([]byte, h.conf.CopyBufferSize)
	srcFiles := make(chan string, 1)
	defer close(srcFiles)
	srcShaSums := make(chan []byte, 1)
	go h.hashFiles(ctx, srcFiles, srcShaSums, srcBuffer, transferRateLimiter)

	dstBuffer := make([]byte, h.conf.CopyBufferSize)
	dstFiles := make(chan string, 1)
	defer close(dstFiles)
	dstShaSums := make(chan []byte, 1)
	go h.hashFiles(ctx, dstFiles, dstShaSums, dstBuffer, transferRateLimiter)

	for file := range files {
		if fileRateLimiter != nil {
			err := fileRateLimiter.Wait(ctx)
			if err != nil {
				failedFiles++
				h.logger.Error().Err(err).Msg("Failed to wait for file rate limiter")
				continue
			}
		}

		// Check symlink targets are the same.
		if file.IsSymlink {
			srcTarget, dstTarget, err := h.checkSymlinks(file.SourcePath, file.DestinationPath)
			if err != nil {
				failedFiles++
				h.logger.Error().Err(err).Str("source", file.SourcePath).Str("destination", file.DestinationPath).Msg("failed to check symlink")
				continue
			}
			if srcTarget != dstTarget {
				failedFiles++
				h.logger.Warn().Str("source", file.SourcePath).Str("destination", file.DestinationPath).
					Str("sourceTarget", srcTarget).Str("destinationTarget", dstTarget).
					Msg("Source and destination symlink targets do not match")
				continue
			}
			continue
		}

		srcFiles <- file.SourcePath
		dstFiles <- file.DestinationPath

		// Wait for both hashes to be computed.
		srcHash := <-srcShaSums
		dstHash := <-dstShaSums

		if srcHash == nil || dstHash == nil {
			// Errors are printed in hashFiles function.
			failedFiles++
			continue
		}

		logger := h.logger.With().Str("source", file.SourcePath).Str("destination", file.DestinationPath).
			Str("sourceHash", hex.EncodeToString(srcHash)).Str("destinationHash", hex.EncodeToString(dstHash)).
			Logger()

		if !bytes.Equal(srcHash, dstHash) {
			failedFiles++
			logger.Warn().Msg("Source and destination hashes do not match")
			continue
		}

		logger.Debug().Msg("Source and destination hashes match")
	}

	return failedFiles
}

func (h *Hasher) checkSymlinks(
	src, dst string,
) (string, string, error) {
	srcTarget, err := os.Readlink(src)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to read symlink %s", src)
	}
	dstTarget, err := os.Readlink(dst)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to read symlink %s", dst)
	}
	return srcTarget, dstTarget, nil
}

func (h *Hasher) hashFiles(
	ctx context.Context,
	files <-chan string,
	shaSum chan<- []byte,
	copyBuffer []byte,
	transferRateLimiter *rate.Limiter,
) {
	for file := range files {
		hash, err := h.hashFile(ctx, file, copyBuffer, transferRateLimiter)
		if err != nil {
			h.logger.Error().Err(err).Str("file", file).Msg("Failed to hash file")
			shaSum <- nil
			continue
		}

		h.logger.Trace().Str("file", file).Str("hash", hex.EncodeToString(hash)).Msg("Hashed file")
		shaSum <- hash
	}
}

func (h *Hasher) updateBytesHashed(bytes, _ int64) {
	// Update the total bytes hashed.
	h.bytesHashed.Add(bytes)
	// Update the total IO completed.
	h.ioCompleted.Add(1)
}

func (h *Hasher) hashFile(
	ctx context.Context,
	file string,
	copyBuffer []byte,
	rateLimiter *rate.Limiter,
) ([]byte, error) {
	h.filesBeingHashed.Add(1)
	defer h.filesBeingHashed.Add(-1)

	hash, err := HashOne(ctx, copyBuffer, file, rateLimiter, h.updateBytesHashed)
	if err != nil {
		return nil, err
	}

	h.filesHashed.Add(1)

	return hash, nil
}
