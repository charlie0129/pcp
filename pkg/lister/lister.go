package lister

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"

	"github.com/charlie0129/pcp/pkg/utils/size"
	"github.com/charlie0129/pcp/pkg/validation"
)

// Lister lists files from source paths and sends them to the provided channel,
// so the workers can pick them up and copy them.
//
// Note that lister also creates directories in destination as soon as it finds
// them, so the workers can copy files into them.
type Lister struct {
	conf   Config
	logger zerolog.Logger
}

func New(config Config, logger zerolog.Logger) *Lister {
	if err := config.Validate(); err != nil {
		panic(err)
	}

	return &Lister{
		conf:   config,
		logger: logger.With().Str("component", "lister").Logger(),
	}
}

func (l *Lister) Start(ctx context.Context, listedFiles chan<- File) error {
	return l.handleSources(ctx, listedFiles)
}

func (l *Lister) handleSources(ctx context.Context, listedFiles chan<- File) error {
	dstStat, err := os.Stat(l.conf.DestinationPath)
	dstExists := err == nil // Can be NotExist
	dstIsDir := err == nil && dstStat.IsDir()
	dstIsRegular := err == nil && dstStat.Mode().IsRegular()

	// Since we have already validated the sources and destination,
	// we only do minimal checks to tell which case we are in.

	//  - Source: one existing file (/a)
	//    Dest:   existing file (/b)
	//    Result: error, file exists, use --force to overwrite. If --force is set,
	//            copy file onto destination itself with same name as
	//            destination (/b).
	if len(l.conf.SourcePaths) == 1 && dstIsRegular {
		source := l.conf.SourcePaths[0]
		stat, err := os.Stat(source)
		if err != nil {
			return err // should not happen, already validated
		}

		if !stat.Mode().IsRegular() {
			return validation.ErrInvalidUsage
		}

		return l.sendFileJob(ctx, listedFiles, source, l.conf.DestinationPath, stat)
	}

	//  - Source: one existing file/dir or several existing files/dirs (/a, /b)
	//    Dest:   existing directory (/c)
	//    Result: copy file into destination with same name as
	//            source (/c/a, /c/b).
	if dstIsDir {
		for _, source := range l.conf.SourcePaths {
			err := l.walkDir(ctx, listedFiles, source, false, l.conf.IgnoreWalkErrors)
			if err != nil {
				return errors.Wrapf(err, "failed to walk source %s", source)
			}
		}
		return nil
	}

	//  - Source: one existing file or dir (/a)
	//    Dest:   does not exist (/b)
	//    Result: copy file/dir onto destination itself with same name as
	//            destination (/b).
	if len(l.conf.SourcePaths) == 1 && !dstExists {
		source := l.conf.SourcePaths[0]
		sourceStat, err := os.Stat(source)
		if err != nil {
			return errors.Wrapf(err, "failed to stat source %s", source)
		}

		if sourceStat.IsDir() {
			// We want the dir to be the same as destination, so we
			// remove the base name from the source path.
			return l.walkDir(ctx, listedFiles, source, true, l.conf.IgnoreWalkErrors)
		}

		return l.sendFileJob(ctx, listedFiles, source, l.conf.DestinationPath, sourceStat)
	}

	return validation.ErrInvalidUsage
}

func (l *Lister) walkDir(
	ctx context.Context,
	listedFiles chan<- File,
	sourceDirPath string,
	doNotIncludeSourceBaseName bool,
	ignoreSourceWalkErrors bool,
) error {
	sourceBaseName := filepath.Base(sourceDirPath)

	return filepath.WalkDir(sourceDirPath, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			if ignoreSourceWalkErrors {
				l.logger.Warn().Str("path", srcPath).Err(err).Msg("Ignoring error while walking source directory")
			} else {
				return errors.Wrapf(err, "failed to access dir in source %s", srcPath)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		info, err := d.Info()
		if err != nil {
			return errors.Wrapf(err, "failed to get source file info for %s", srcPath)
		}

		if validation.IsUnsupportedType(info) {
			l.logger.Warn().Str("path", srcPath).Str("type", info.Mode().String()).Msg("Ignoring unsupported file type")
			return nil // Skip unsupported types
		}

		// Example:
		//   sourceDirPath: /a
		//   path: /a/b
		//   relPath: b
		relPath, err := filepath.Rel(sourceDirPath, srcPath)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Example:
		//   dstPath: /c
		dstPath := filepath.Join(l.conf.DestinationPath, sourceBaseName, relPath) // /c/a/b
		if doNotIncludeSourceBaseName {
			dstPath = filepath.Join(l.conf.DestinationPath, relPath) // /c/b
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			if !l.conf.FollowSymlinks {
				return l.sendSymlinkJob(ctx, listedFiles, srcPath, dstPath, info)
			}
			// For dereferenced symlinks, stat the target
			info, err = os.Stat(srcPath)
			if err != nil {
				return errors.Wrapf(err, "failed to stat symlink target for %s", srcPath)
			}
		}

		if info.IsDir() {
			if l.conf.DoNotCreateDirs {
				return nil
			}
			// Directory must be created as soon as possible, because we the
			// workers may first try to copy the file and then find out
			// that the directory does not exist.
			return l.createDir(ctx, dstPath, info)
		}

		return l.sendFileJob(ctx, listedFiles, srcPath, dstPath, info)
	})
}

func (l *Lister) sendFileJob(
	ctx context.Context,
	listedFiles chan<- File,
	source, dest string,
	info os.FileInfo,
) error {
	job := File{
		SourcePath:      source,
		DestinationPath: dest,
		FileInfo:        info,
		IsSymlink:       false,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case listedFiles <- job:
		l.logger.Trace().Str("source", source).Str("destination", dest).
			Int64("size", info.Size()).Str("sizeHuman", size.FormatBytes(info.Size())).
			Msg("Discovered regular file")
		return nil
	}
}

func (l *Lister) createDir(ctx context.Context, dest string, info os.FileInfo) error {
	// We must create it immediately, so the workers can copy files into it.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	l.logger.Debug().Str("path", dest).Msg("Creating directory")

	if l.conf.Force {
		_, err := os.Lstat(dest)
		if err == nil {
			// If the destination already exists, delete it.
			err = os.RemoveAll(dest)
			if err != nil {
				return errors.Wrapf(err, "failed to remove existing file or directory %s", dest)
			}
		}
	}

	if err := os.Mkdir(dest, info.Mode()); err != nil {
		return errors.Wrap(err, "failed to create directory")
	}

	if l.conf.PreserveOwner {
		stat_t, ok := info.Sys().(syscall.Stat_t)
		if !ok {
			return errors.New("failed to preserve owner: no stat_t")
		}
		err := os.Chown(dest, int(stat_t.Uid), int(stat_t.Gid))
		if err != nil {
			return errors.Wrapf(err, "failed to preserve owner for file %s", dest)
		}
	}

	return nil
}

func (l *Lister) sendSymlinkJob(
	ctx context.Context,
	listedFiles chan<- File,
	source, dest string,
	info os.FileInfo,
) error {
	target, err := os.Readlink(source)
	if err != nil {
		return fmt.Errorf("failed to read symlink: %w", err)
	}

	job := File{
		SourcePath:      source,
		DestinationPath: dest,
		FileInfo:        info,
		IsSymlink:       true,
		SymlinkTarget:   target,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case listedFiles <- job:
		l.logger.Trace().Str("source", source).Str("target", target).Str("destination", dest).
			Msg("Discovered symlink")
		return nil
	}
}
