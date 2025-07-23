package lister

import (
	"os"
)

type File struct {
	// SourcePath is the path to the source file.
	SourcePath string
	// DestinationPath is where the file will be copied to.
	DestinationPath string

	FileInfo os.FileInfo

	IsSymlink bool

	SymlinkTarget string
}
