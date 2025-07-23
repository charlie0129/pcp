package copier

import (
	"github.com/pkg/errors"

	"github.com/charlie0129/pcp/pkg/validation"
)

type Config struct {
	SourcePaths     []string
	DestinationPath string

	ChunkSize int64

	MaxConcurrentChunks int

	TransferRateLimit int64
	FileRateLimit     int64
	VerifyRateLimit   int64

	DropPermissions bool
	PreserveOwner   bool
	Force           bool
	FollowSymlinks  bool
	Verbose         bool
	ResumeDB        string
	NoResume        bool
}

func (c *Config) Validate() error {
	var err error

	err = validation.ValidateSourcesAndDestination(c.SourcePaths, c.DestinationPath, c.Force)
	if err != nil {
		return err
	}

	if c.ChunkSize < 0 {
		return errors.New("chunk size must be positive")
	}

	if c.MaxConcurrentChunks < 0 {
		return errors.New("max concurrent chunks must be positive")
	}

	if c.TransferRateLimit < 0 {
		return errors.New("transfer rate limit must be positive")
	}

	if c.FileRateLimit < 0 {
		return errors.New("file rate limit must be positive")
	}

	if c.VerifyRateLimit < 0 {
		return errors.New("verify rate limit must be positive")
	}

	return nil
}
