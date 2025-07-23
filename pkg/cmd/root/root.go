package root

import (
	"github.com/spf13/cobra"

	"github.com/charlie0129/pcp/pkg/lister"
	"github.com/charlie0129/pcp/pkg/utils/log"
)

// Lister config
var listerConfig = lister.Config{}

// Worker config
var (
	maxConcurrentChunks   = 32
	maxConcurrentSymlinks = 32
	chunkSize             = "4m"
	blockSize             = "256k"
)

var (
	transferRateLimitStr string
	fileRateLimitStr     string
	force                bool
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pcp [flags] SOURCE... DEST",
		Short: "Parallelized copy - cp with parallel processing",
		Long: `
Common source and destination file handling rules:
  - Source: one existing file (/a)
    Dest:   existing file (/b)
    Result: error, file exists, use --force to overwrite. If --force is set, 
            copy file onto destination itself with same name as 
            destination (/b).
  - Source: one existing file/dir or several existing files/dirs (/a, /b)
    Dest:   existing directory (/c)
    Result: copy file into destination with same name as 
            source (/c/a, /c/b).
  - Source: one existing file or dir (/a)
    Dest:   does not exist (/b)
    Result: copy file/dir onto destination itself with same name as 
            destination (/b).
  All other cases are invalid usages.


`,
		Args:         cobra.MinimumNArgs(2),
		RunE:         runCopy,
		SilenceUsage: true,
	}

	f := cmd.Flags()

	f.BoolVarP(&force, "force", "f", false, "Force copy even if file already exists")

	// Lister
	f.BoolVar(&listerConfig.FollowSymlinks, "follow-symlinks", false, "Copy files pointed to by symlinks instead of the symlinks themselves")
	f.BoolVar(&listerConfig.PreserveOwner, "preserve-owner", false, "Preserve file owner (requires root)")
	f.BoolVar(&listerConfig.IgnoreWalkErrors, "ignore-walk-errors", false, "Ignore errors while walking directories (e.g., permission denied)")

	// Worker
	f.IntVarP(&maxConcurrentChunks, "concurrent-chunks", "c", maxConcurrentChunks, "Maximum number of concurrently copied chunks")
	f.IntVar(&maxConcurrentSymlinks, "concurrent-symlinks", maxConcurrentSymlinks, "Maximum number of concurrently processed symlinks")
	f.StringVarP(&chunkSize, "chunk-size", "C", chunkSize, "Chunk size for splitting large files (e.g., 4m, 1g)")
	f.StringVar(&blockSize, "block-size", blockSize, "Internal input and output block size (e.g., 32k, 1m)")

	f.StringVar(&transferRateLimitStr, "transfer-rate-limit", "", "Limit bytes copied per second (e.g., 1m, 500k)")
	f.StringVar(&fileRateLimitStr, "file-rate-limit", "", "Limit files copied per second (e.g., 10, 1k)")

	pf := cmd.PersistentFlags()
	pf.CountVarP(&log.Verbosity, "verbose", "v", "Enable verbose output (-v for debug, -vv for trace)")

	return cmd
}
