package validation

import (
	"os"

	"github.com/pkg/errors"
)

var (
	ErrNoSourcesProvided     = errors.New("no sources provided")
	ErrUnsupportedFileType   = errors.New("only directories, regular files, and symlinks are supported")
	ErrDestinationFileExists = errors.New("destination file already exists, use --force to overwrite")
	ErrInvalidUsage          = errors.New("invalid usage: check the help message for valid source and destination rules")
)

// ValidateSourcesAndDestination validates the sources and destination paths
// against the common source and destination file handling rules:
//   - Source: one existing file (/a)
//     Dest:   existing file (/b)
//     Result: error, file exists, use --force to overwrite. If --force is set,
//     copy file onto destination itself with same name as
//     destination (/b).
//   - Source: one existing file/dir or several existing files/dirs (/a, /b)
//     Dest:   existing directory (/c)
//     Result: copy file into destination with same name as
//     source (/c/a, /c/b).
//   - Source: one existing file or dir (/a)
//     Dest:   does not exist (/b)
//     Result: copy file/dir onto destination itself with same name as
//     destination (/b).
//
// All other cases are invalid usages.
func ValidateSourcesAndDestination(sources []string, destination string, force bool) error {
	dstStat, err := os.Stat(destination)
	if err != nil {
		if !os.IsNotExist(err) {
			return errors.Wrapf(err, "failed to stat destination %s", destination)
		}
	}
	dstExists := err == nil // Can be NotExist
	dstIsDir := err == nil && dstStat.IsDir()
	dstIsRegular := err == nil && dstStat.Mode().IsRegular()

	if len(sources) == 0 {
		return ErrNoSourcesProvided
	}
	sourceStats := make([]os.FileInfo, len(sources))
	for i, source := range sources {
		sourceStat, err := os.Stat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return errors.Wrapf(err, "source does not exist %s", destination)
			}
			return errors.Wrapf(err, "failed to stat source %s", source)
		}
		sourceStats[i] = sourceStat

		if IsUnsupportedType(sourceStat) {
			return errors.Wrapf(ErrUnsupportedFileType, "source %s", source)
		}
	}

	// Case 1
	//   - Source: one existing file (/a)
	//     Dest:   existing file (/b)
	//     Result: error, file exists, use --force to overwrite. If --force is set,
	//     copy file onto destination itself with same name as
	//     destination (/b).
	if len(sourceStats) == 1 && sourceStats[0].Mode().IsRegular() && // one file
		dstIsRegular { // existing file
		if !force {
			return ErrDestinationFileExists
		}
		return nil
	}

	// Case 2
	//   - Source: one existing file/dir or several existing files/dirs (/a, /b)
	//     Dest:   existing directory (/c)
	//     Result: copy file into destination with same name as
	//     source (/c/a, /c/b).
	if dstIsDir { // existing directory
		return nil
	}

	// Case 3
	//   - Source: one existing file or dir (/a)
	//     Dest:   does not exist (/b)
	//     Result: copy file/dir onto destination itself with same name as
	//     destination (/b).
	if len(sourceStats) == 1 && // one file/dir
		!dstExists { // destination does not exist
		return nil
	}

	// All other cases are invalid usages.
	return ErrInvalidUsage
}

func IsUnsupportedType(info os.FileInfo) bool {
	mode := info.Mode()
	return !(mode.IsDir() || mode.IsRegular() || mode&os.ModeSymlink != 0)
}
