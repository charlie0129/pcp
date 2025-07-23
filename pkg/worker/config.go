package worker

import (
	"fmt"
)

type Config struct {
	MaxConcurrentChunks   int
	MaxConcurrentSymlinks int
	ChunkSize             int64
	BlockSize             int
	Force                 bool
}

func (c *Config) Validate() error {
	if c.MaxConcurrentChunks <= 0 {
		return fmt.Errorf("MaxConcurrentChunks must be greater than 0")
	}
	if c.MaxConcurrentSymlinks <= 0 {
		return fmt.Errorf("MaxConcurrentSymlinks must be greater than 0")
	}
	if c.ChunkSize <= 0 {
		return fmt.Errorf("ChunkSize must be greater than 0")
	}
	if c.BlockSize <= 0 {
		return fmt.Errorf("BlockSize must be greater than 0")
	}
	if int64(c.BlockSize) > c.ChunkSize {
		return fmt.Errorf("BlockSize must be less than or equal to ChunkSize")
	}
	return nil
}
