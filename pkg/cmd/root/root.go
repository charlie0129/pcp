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
	maxConcurrentChunks   = 16
	maxConcurrentSymlinks = 16
	chunkSize             = "4m"
	blockSize             = "1m"
)

var (
	transferRateLimitStr string
	fileRateLimitStr     string
	force                bool
	preserveOwner        bool
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pcp [flags] SOURCE... DEST",
		Short: "Parallelized copy - cp with parallel processing",
		Long: `pcp is a file copying tool like cp -r that uses parallel processing to 
significantly speed up copying operations under certain conditions. It is 
particularly effective for copying many small files or large files in chunks, 
making it ideal for scenarios where latency is a bottleneck.

Use Cases:
  - High Latency Filesystems: typically network filesystems (e.g., NFS, SMB, 
    WebDAV) and distributed filesystems (e.g., CephFS, GlusterFS).
  - Many Small Files: copying a large number of small files can be slow with 
    traditional tools due to the overhead of opening and closing files.

Performance Tuning:
  - --block-size: Sets the number of bytes of each IO operation. Increasing it 
    to several megabytes can generally improve performance (the default value 
    of 1MiB is fine for most people), especially on mechanical disks. It 
    reduces the number of IO operations. Do not make it too large. Larger 
    values like several hundred megabytes does not make sense. This must be 
    smaller or equal to --chunk-size.
  - --chunk-size: Sets how large the chunks of a file are sliced into. If you 
    are using a network of very high latency (like several hundreds of 
    milliseconds), you can increase this so the there are fewer chunks to 
    reduce the number of new connections created.
  - --concurrent-chunks: Sets how many chunks can be copied concurrently. 
    Increasing this can improve performance on high latency filesystems, but 
    it can also increase the load on the filesystem and network.

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
		Example: `  # Simple copy
  $ pcp /path/to/source /path/to/destination

  # Copy with force overwrite
  $ pcp -f /path/to/source /path/to/destination

  # Copy with original UID and GID preserved (requires root)
  $ sudo pcp --preserve-owner /path/to/source /path/to/destination

  # Copy with debug logs (when each file is copied)
  $ pcp -v /path/to/source /path/to/destination

  # Copy with trace logs (detailed operation logs of each file and chunk)
  $ pcp -vv /path/to/source /path/to/destination`,
		Args:         cobra.MinimumNArgs(2),
		RunE:         runCopy,
		SilenceUsage: true,
	}

	f := cmd.Flags()

	f.BoolVarP(&force, "force", "f", false, "Force copy even if file already exists")

	// Lister
	f.BoolVar(&listerConfig.FollowSymlinks, "follow-symlinks", false, "Copy files pointed to by symlinks instead of the symlinks themselves")
	f.BoolVar(&listerConfig.IgnoreWalkErrors, "ignore-walk-errors", false, "Ignore errors while walking directories (e.g., permission denied)")

	// Worker
	f.IntVarP(&maxConcurrentChunks, "concurrent-chunks", "c", maxConcurrentChunks, "Maximum number of concurrently copied chunks")
	f.IntVar(&maxConcurrentSymlinks, "concurrent-symlinks", maxConcurrentSymlinks, "Maximum number of concurrently processed symlinks")
	f.StringVarP(&chunkSize, "chunk-size", "C", chunkSize, "Chunk size for splitting large files (e.g., 4m, 1g)")
	f.StringVar(&blockSize, "block-size", blockSize, "Internal input and output block size (e.g., 32k, 1m)")

	f.StringVar(&transferRateLimitStr, "transfer-rate-limit", "", "Limit bytes copied per second (e.g., 1m, 500k)")
	f.StringVar(&fileRateLimitStr, "file-rate-limit", "", "Limit files copied per second (e.g., 10, 1k)")

	f.BoolVar(&preserveOwner, "preserve-owner", preserveOwner, "Preserve original UID and GID (requires root)")

	pf := cmd.PersistentFlags()
	pf.CountVarP(&log.Verbosity, "verbose", "v", "Enable verbose output (-v for debug, -vv for trace)")

	return cmd
}
