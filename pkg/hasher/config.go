package hasher

import (
	"github.com/pkg/errors"
)

type Config struct {
	MaxConcurrentFiles int
	CopyBufferSize     int
}

func (c Config) Validate() error {
	if c.MaxConcurrentFiles <= 0 {
		return errors.New("max concurrent files must be greater than 0")
	}
	if c.CopyBufferSize <= 0 {
		return errors.New("copy buffer size must be greater than 0")
	}
	return nil
}
