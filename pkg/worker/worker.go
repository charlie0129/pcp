package worker

import (
	"context"
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/charlie0129/pcp/pkg/lister"
	"github.com/charlie0129/pcp/pkg/utils/progress"
	"github.com/charlie0129/pcp/pkg/utils/size"
)

// Worker copies a single file at a time. It slices files into chunks and
// copies them in parallel.
type Worker struct {
	conf   Config
	logger zerolog.Logger

	// Stats
	filesBeingCopied  atomic.Int64
	chunksBeingCopied atomic.Int64
	filesCopied       atomic.Int64
	bytesCopied       atomic.Int64
	ioCompleted       atomic.Int64
}

func New(conf Config, logger zerolog.Logger) *Worker {
	return &Worker{
		conf:   conf,
		logger: logger.With().Str("component", "worker").Logger(),
	}
}

func (w *Worker) Stats() progress.Stats {
	return progress.Stats{
		FilesBeingProcessed:  w.filesBeingCopied.Load(),
		ChunksBeingProcessed: w.chunksBeingCopied.Load(),
		FilesProcessed:       w.filesCopied.Load(),
		BytesProcessed:       w.bytesCopied.Load(),
		IOCompleted:          w.ioCompleted.Load(),
	}
}

// rateLimiter can be nil, in which case no rate limiting is applied.
func (w *Worker) Start(
	ctx context.Context,
	incomingFiles <-chan lister.File,
	transferRateLimiter *rate.Limiter,
	fileRateLimiter *rate.Limiter,
) error {
	chunks := make(chan Chunk, w.conf.MaxConcurrentChunks)
	symlinks := make(chan lister.File, w.conf.MaxConcurrentSymlinks)

	// Start goroutines to handle the concurrent copying of chunks.
	eg, ctx := errgroup.WithContext(ctx)

	// This goroutine will read files from the incomingFiles channel,
	// slice them into chunks, and send those chunks to the outChunks channel.
	eg.Go(func() error {
		defer close(chunks)   // Ensure outChunks is closed when done.
		defer close(symlinks) // Ensure symlinks channel is closed when done.

		if fileRateLimiter != nil {
			err := fileRateLimiter.Wait(ctx)
			if err != nil {
				return errors.Wrap(err, "failed to wait for file rate limiter")
			}
		}

		err := w.receiveFileAndFeedChunks(ctx, incomingFiles, chunks, symlinks)
		if err != nil {
			return errors.Wrap(err, "failed to slice chunks")
		}

		return nil
	})

	// Start multiple goroutines to copy chunks concurrently.
	for range w.conf.MaxConcurrentChunks {
		eg.Go(func() error {
			// Local copy buffer, avoid reallocations.
			copyBuffer := make([]byte, w.conf.BlockSize)
			var err error

			for chunk := range chunks {
				err = w.copyChunk(ctx, chunk, copyBuffer, transferRateLimiter)
				if err != nil {
					// Maybe we can log the error.
					return errors.Wrapf(err, "failed to copy chunk")
				}
			}

			return nil
		})
	}

	// Start multiple goroutines to handle symlinks concurrently.
	for range w.conf.MaxConcurrentSymlinks {
		eg.Go(func() error {
			var err error
			for l := range symlinks {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				err = os.Symlink(l.SymlinkTarget, l.DestinationPath)
				if err != nil {
					return errors.Wrapf(err, "failed to create symlink %s -> %s", l.DestinationPath, l.SymlinkTarget)
				}
			}
			return nil
		})
	}

	return eg.Wait()
}

func (w *Worker) updateBytesCopied(bytes, _ int64) {
	// Update the total bytes copied.
	w.bytesCopied.Add(bytes)
	// Update the total IO completed.
	w.ioCompleted.Add(1)
}

func (w *Worker) copyChunk(
	ctx context.Context,
	chunk Chunk,
	copyBuffer []byte,
	rateLimiter *rate.Limiter,
) error {
	needCloseFile := false

	defer func() {
		if needCloseFile {
			err, closed := chunk.File.TryClose()
			// We only close a file when it has been fully copied (or error).
			// Decrement the filesBeingCopied counter only if the file was closed.
			if closed {
				w.filesBeingCopied.Add(-1)
			}
			if err != nil {
				w.logger.Error().Err(err).Msg("failed to close file")
			}
		}
	}()

	select {
	case <-ctx.Done():
		needCloseFile = true
		return ctx.Err()
	default:
	}

	err, opened := chunk.File.Open()
	if opened {
		// This only happens when the file was not opened before.
		w.filesBeingCopied.Add(1)
	}
	if err != nil {
		needCloseFile = true
		return errors.Wrapf(err, "failed to open file for copying")
	}

	w.chunksBeingCopied.Add(1)
	defer w.chunksBeingCopied.Add(-1)

	err = chunk.File.CopyChunk(ctx, chunk.Index, copyBuffer, rateLimiter, w.updateBytesCopied)
	if err != nil {
		// Any error during copying means the whole copy failed, so we need to close the file.
		needCloseFile = true
		return errors.Wrapf(err, "failed to copy chunk %d of file %s", chunk.Index, chunk.File.info.SourcePath)
	}

	if chunk.File.AllChunksCopied() {
		// If all chunks are copied, we can close the file.
		needCloseFile = true
		// Consider this file as fully copied.
		w.filesCopied.Add(1)
		w.logger.Debug().
			Str("source", chunk.File.info.SourcePath).
			Str("destination", chunk.File.info.DestinationPath).
			Int64("size", chunk.File.info.FileInfo.Size()).
			Str("sizeHuman", size.FormatBytes(chunk.File.info.FileInfo.Size())).
			Msg("Copied file")
	}

	return nil
}

func (w *Worker) receiveFileAndFeedChunks(
	ctx context.Context,
	files <-chan lister.File,
	chunks chan<- Chunk,
	symlinks chan<- lister.File,
) error {
	handleRegularFile := func(file lister.File) {
		f := NewFile(w.logger, file, w.conf.ChunkSize, w.conf.Force)

		// Handle 0 byte files.
		if f.Chunks() == 0 {
			chunks <- Chunk{
				Index: -1, // pseudo index for 0 byte files
				File:  f,
			}
			return
		}

		// Generate chunks.
		for i := range f.Chunks() {
			chunks <- Chunk{
				Index: i,
				File:  f,
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case file, ok := <-files:
			if !ok {
				return nil // Channel closed, exit the loop.
			}

			if file.IsSymlink {
				symlinks <- file
				continue
			}

			handleRegularFile(file)
		}
	}
}
