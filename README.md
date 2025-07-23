# pcp - Parallelized Copy

`pcp` is a high-performance file copying tool that uses parallel processing to significantly speed up copying operations. It's designed as a drop-in replacement for `cp -r` with massive performance improvements for specific use cases.

## Use Cases

`pcp` excels in scenarios where latency is a bottleneck (i.e. not throughput), such as:

1. **Network filesystems**: Single-threaded operations are limited by connection speed and round-trip time is huge.
2. **Many small files**: Parallel (high-iodepth) I/O can fully utilize modern NVMe SSD performance

Do not use `pcp` for copying files on mechanical disks, as the main bottleneck will be head seeking latency, which `pcp` cannot overcome. Large concurrent IO will even slow it down. In such cases, traditional tools like `cp` or `rsync` are more suitable.

## Key Features

- **Parallel file copying**: Copy multiple files simultaneously
- **Chunk-based large file copying**: Split large files into chunks and copy them concurrently. All disk space is reserved before copying starts, if possible, so disk fragmentation is minimized.
- ~~**Resume capability**: (TODO) Automatically resume interrupted copies~~
- **Rate limiting**: Control transfer speed and file processing rate
- **Progress tracking**: Real-time progress display with speed statistics
- **Verification**: Optional SHA256 checksum verification

## How It Works

TBD

## Performance

TBD

## Limitations

- Does not preserve extended attributes (xattr)
- Only handles regular files, directories, and symlinks.
