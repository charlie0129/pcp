package lister

import (
	"github.com/charlie0129/pcp/pkg/validation"
)

type Config struct {
	SourcePaths      []string
	DestinationPath  string
	Force            bool
	FollowSymlinks   bool
	PreserveOwner    bool
	IgnoreWalkErrors bool
	DoNotCreateDirs  bool
}

// Validate checks the configuration for the lister. If you call Lister
// manually instead of using Copier, you must call this method before
// starting the lister.
func (c Config) Validate() error {
	var err error

	err = validation.ValidateSourcesAndDestination(c.SourcePaths, c.DestinationPath, c.Force)
	if err != nil {
		return err
	}

	return nil
}
