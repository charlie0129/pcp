package cp

import (
	"context"
	"io"

	"github.com/pkg/errors"
	"golang.org/x/time/rate"
)

var DefaultBufferSize = 64 * 1024 // 64KB

type Options struct {
	rateLimiter     *rate.Limiter
	progressTracker func(int64, int64)
	buffer          []byte
}

type Option func(options *Options)

// WithRateLimiter sets a rate limiter for the copy operation.
func WithRateLimiter(limiter *rate.Limiter) Option {
	return func(options *Options) {
		options.rateLimiter = limiter
	}
}

// WithProgressTracker sets a progress tracker function that will be called
// after each successful write operation.
//
// The first argument is the number bytes written in this write operation,
// the second argument is the total number of bytes written so far.
//
// Note that it is called very frequently, depending on the size of the buffer
// and the rate of data transfer. You MUST NOT block in this function,
// as it will slow down the copy operation significantly.
func WithProgressTracker(tracker func(int64, int64)) Option {
	return func(options *Options) {
		options.progressTracker = tracker
	}
}

// WithBuffer sets a custom buffer for the copy operation. It determines
// the block size for reading and writing data. If not set, a default buffer
// size of DefaultBufferSize is used.
func WithBuffer(buffer []byte) Option {
	return func(options *Options) {
		options.buffer = buffer
	}
}

func Copy(
	ctx context.Context,
	dst io.Writer,
	src io.Reader,
	opts ...Option,
) (written int64, err error) {
	options := &Options{}

	for _, opt := range opts {
		opt(options)
	}

	if options.buffer == nil {
		options.buffer = make([]byte, DefaultBufferSize)
	}

	return copyBuffer(ctx, dst, src, options.buffer, options.rateLimiter, options.progressTracker)
}

// copyBuffer the similar to io.copyBuffer, but with context, rate limiter and
// progress tracker.
func copyBuffer(
	ctx context.Context,
	dst io.Writer,
	src io.Reader,
	buf []byte,
	rateLimiter *rate.Limiter,
	progressTracker func(int64, int64),
) (totalWritten int64, err error) {
	for {
		select {
		case <-ctx.Done():
			return totalWritten, ctx.Err()
		default:
		}

		bytesRead, readErr := src.Read(buf)
		if bytesRead > 0 {
			if rateLimiter != nil {
				if err := rateLimiter.WaitN(ctx, bytesRead); err != nil {
					return totalWritten, errors.Wrap(err, "rate limiter wait failed")
				}
			}

			bytesWritten, WriteErr := dst.Write(buf[0:bytesRead])
			if bytesWritten < 0 || bytesRead < bytesWritten {
				bytesWritten = 0
				if WriteErr == nil {
					WriteErr = errors.New("invalid write result")
				}
			}

			totalWritten += int64(bytesWritten)
			if progressTracker != nil {
				progressTracker(int64(bytesWritten), totalWritten)
			}

			if WriteErr != nil {
				err = errors.Wrap(WriteErr, "failed to write buffer")
				break
			}

			if bytesRead != bytesWritten {
				err = io.ErrShortWrite
				break
			}
		}

		if readErr != nil {
			if readErr != io.EOF {
				err = errors.Wrap(readErr, "read buffer failed")
			}
			break
		}
	}

	return totalWritten, err
}
