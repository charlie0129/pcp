package worker

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/detailyang/go-fallocate"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/charlie0129/pcp/pkg/lister"
	"github.com/charlie0129/pcp/pkg/utils/cp"
	"github.com/charlie0129/pcp/pkg/utils/size"
)

// File is a file that is being copied.
type File struct {
	mu     *sync.Mutex
	logger zerolog.Logger

	info  lister.File
	force bool

	// srcFD is the file descriptor of the source file.
	srcFD *os.File
	// dstFD is the file descriptor of the destination file.
	dstFD *os.File

	chunkCount   int
	chunkSize    int64
	copiedChunks int
}

func NewFile(logger zerolog.Logger, info lister.File, chunkSize int64, force bool) *File {
	if info.IsSymlink {
		panic("symlink is not supported")
	}

	chunks := CalculateChunkCount(info.FileInfo.Size(), chunkSize)

	logger = logger.With().
		Str("source", info.SourcePath).
		Str("destination", info.DestinationPath).
		Int64("size", info.FileInfo.Size()).
		Str("sizeHuman", size.FormatBytes(info.FileInfo.Size())).
		Int("totalChunks", chunks).
		Logger()

	return &File{
		mu:           &sync.Mutex{},
		logger:       logger,
		info:         info,
		srcFD:        nil,
		dstFD:        nil,
		chunkCount:   chunks,
		chunkSize:    chunkSize,
		copiedChunks: 0,
		force:        force,
	}
}

func (f *File) close() (error, bool) {
	if f.srcFD != nil {
		if err := f.srcFD.Close(); err != nil {
			return errors.Wrap(err, "failed to close source fd"), false
		}
		f.srcFD = nil
		f.logger.Trace().Msg("Closed source fd")
	}

	if f.dstFD != nil {
		if err := f.dstFD.Close(); err != nil {
			return errors.Wrap(err, "failed to close destination fd"), false
		}
		f.dstFD = nil
		f.logger.Trace().Msg("Closed destination fd")
	}

	return nil, true
}

func (f *File) open() (error, bool) {
	if f.srcFD != nil && f.dstFD != nil {
		// If both file descriptors are already open, do nothing.
		return nil, false
	}

	hasError := false
	defer func() {
		if hasError {
			// If there was an error, close the file descriptors.
			_, _ = f.close()
		}
	}()

	if f.srcFD == nil {
		srcFD, err := os.Open(f.info.SourcePath) // RO
		if err != nil {
			hasError = true
			return errors.Wrapf(err, "failed to open for reading"), false
		}
		f.srcFD = srcFD
		f.logger.Trace().Msg("Opened source fd")
	}

	if f.dstFD == nil {
		flags := os.O_RDWR | os.O_CREATE
		if f.force {
			flags |= os.O_TRUNC
		} else {
			flags |= os.O_EXCL
		}

		dstFD, err := os.OpenFile(f.info.DestinationPath, flags, f.info.FileInfo.Mode()) // WO
		if err != nil {
			hasError = true
			return errors.Wrapf(err, "failed to open for writing"), false
		}

		f.dstFD = dstFD
		f.logger.Trace().Msg("Opened destination fd")

		// Preallocate the file to the expected size, if possible.
		fileSize := f.info.FileInfo.Size()
		if fileSize > 0 {
			if err := fallocate.Fallocate(f.dstFD, 0, fileSize); err != nil {
				// fallocate can fail on some filesystems, so we only log the error
				// and don't fail the whole operation. The copy will still work.
				f.logger.Warn().Err(err).Msg("Failed to preallocate disk space for destination file. Continuing anyway.")
			} else {
				f.logger.Trace().Msg("Preallocated disk space")
			}
		}
	}

	return nil, true
}

// TryClose tries to close the file descriptors for the source and destination
// files. It will only close if all chunks have been copied. If not, it returns
// nil and does not close the file descriptors.
//
// The second return value indicates whether the file descriptors were closed
// in this call or not. If they were already closed, it returns false.
func (f *File) TryClose() (error, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.copiedChunks < f.chunkCount {
		return nil, false
	}

	return f.close()
}

// Close tries to close the file descriptors for the source and destination
// files. It will close if the file descriptors are open.
//
// The second return value indicates whether the file descriptors were closed
// in this call or not. If they were already closed, it returns false.
func (f *File) Close() (error, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.close()
}

// Open tries to open the file descriptors for the source and destination
// files. If they are already open, it does nothing and returns nil.
//
// The second return value indicates whether the file descriptors were
// opened in this call or not. If they were already open, it returns false.
func (f *File) Open() (error, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.open()
}

func (f *File) Chunks() int {
	// No need to lock. It's never modified after creation.
	return f.chunkCount
}

func (f *File) AllChunksCopied() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	// If the number of copied chunks is equal to the total number of chunks,
	// then all chunks are copied.
	return f.copiedChunks == f.chunkCount
}

func (f *File) bumpCopiedChunks() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.copiedChunks++
	return f.copiedChunks
}

// CopyChunk copies a chunk of the file. You should handle file opening and
// closing yourself, as this function is designed to be called multiple times
// for different chunks of the same file.
//
// You MUST make sure you do not copy the same chunk multiple times. It will
// cause data corruption.
//
// rateLimiter can be nil, in which case no rate limiting is applied.
//
// progressListener can be nil, in which case no progress tracking is done.
// If provided, it will be called with the number of bytes copied so far
// after each successful write operation. You MUST NOT block in this function,
// as it will slow down the copy operation significantly.
func (f *File) CopyChunk(
	ctx context.Context,
	index int,
	buffer []byte,
	rateLimiter *rate.Limiter,
	progressListener func(int64, int64),
) error {
	// Early exit in case a context cancellation.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if f.chunkCount == 0 {
		return nil
	}

	// Calculate the offset and size of the chunk.
	offset, chunkSize := CalculateChunkOffsetAndSize(f.info.FileInfo.Size(), f.chunkSize, index)

	reader := io.NewSectionReader(f.srcFD, offset, chunkSize)
	writer := io.NewOffsetWriter(f.dstFD, offset)

	logger := f.logger.With().Int("chunk", index+1).Int64("offset", offset).Int64("chunkSize", chunkSize).Logger()

	_, err := cp.Copy(ctx, writer, reader, cp.WithRateLimiter(rateLimiter), cp.WithBuffer(buffer), cp.WithProgressTracker(progressListener))
	if err != nil {
		return err
	}

	copiedChunks := f.bumpCopiedChunks()
	logger.Trace().Int("copiedChunks", copiedChunks).Msg("Copied chunk")

	return nil
}

func CalculateChunkCount(fileSize, chunkSize int64) int {
	return int((fileSize + chunkSize - 1) / chunkSize)
}

func CalculateChunkOffsetAndSize(fileSize, chunkSize int64, index int) (int64, int64) {
	offset := int64(index) * chunkSize

	if offset+chunkSize > fileSize {
		chunkSize = fileSize - offset
	}

	if chunkSize < 0 {
		panic("invalid chunk size or index")
	}

	return offset, chunkSize
}
