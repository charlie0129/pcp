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

		if srcPath == dstPath {
			// If the source and destination paths are the same, skip it.
			// This can happen when the source is a single file and the destination
			// is a directory, or when the source is a directory, and we are copying
			// it onto itself.
			l.logger.Warn().Str("path", srcPath).Msg("Skipping source path that is the same as destination")
			return nil
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
			return l.createDir(ctx, srcPath, dstPath, info)
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
			Str("mode", info.Mode().String()).
			Msg("Discovered regular file")
		return nil
	}
}

func (l *Lister) createDir(ctx context.Context, src, dest string, info os.FileInfo) error {
	// We must create it immediately, so the workers can copy files into it.
	// Do not send it to the listedFiles channel, because the workers may
	// not create it in time.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	l.logger.Trace().Str("source", src).Str("destination", dest).
		Str("mode", info.Mode().String()).
		Msg("Discovered directory, creating it in destination")

	stat, err := os.Lstat(dest)
	dstExists := !os.IsNotExist(err)
	if err != nil && dstExists { // some other error than NotExist
		return errors.Wrapf(err, "failed to lstat destination %s", dest)
	}
	dstIsDir := dstExists && stat.IsDir()

	// NotForce:
	//    notexist: create with mode, chown
	//    exists&&isdir: error out
	//    exists&&other: error out
	// Force:
	//    notexist: create with mode, chown
	//    exists&&isdir: keep, chmod, chown
	//    exists&&other: delete, create with mode, chown
	if !l.conf.Force { // Not force
		if dstExists {
			return errors.Errorf("destination %s already exists, use --force to overwrite", dest)
		}
		if err := os.Mkdir(dest, info.Mode()); err != nil {
			return errors.Wrapf(err, "failed to create directory %s", dest)
		}
		l.logger.Debug().Str("path", dest).Msg("Created directory")
	} else { // l.conf.Force is true
		if dstExists && !dstIsDir {
			if err := os.Remove(dest); err != nil {
				return errors.Wrapf(err, "failed to remove existing file %s", dest)
			}
			dstExists = false // It's gone now
		}

		if !dstExists {
			if err := os.Mkdir(dest, info.Mode()); err != nil {
				return errors.Wrapf(err, "failed to create directory %s", dest)
			}
			l.logger.Debug().Str("path", dest).Msg("Created directory")
		} else { // dstExists && dstIsDir
			if err := os.Chmod(dest, info.Mode().Perm()); err != nil {
				return errors.Wrapf(err, "failed to chmod existing directory %s", dest)
			}
		}
	}

	if l.conf.PreserveOwner {
		statT, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("failed to preserve owner: no stat_t")
		}
		err := os.Chown(dest, int(statT.Uid), int(statT.Gid))
		if err != nil {
			return errors.Wrapf(err, "failed to preserve owner for directory %s", dest)
		}
		l.logger.Debug().Uint32("uid", statT.Uid).Uint32("gid", statT.Gid).Msg("Chowned directory to original owner")
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
		l.logger.Trace().Str("source", source).Str("target", target).
			Str("destination", dest).
			Str("mode", info.Mode().String()).
			Msg("Discovered symlink")
		return nil
	}
}
