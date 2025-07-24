# pcp - Parallelized Copy

`pcp` is a file copying tool like `cp -r` that uses parallel processing to significantly speed up copying operations
under certain conditions. It is particularly effective for copying many small files or large files in chunks, making it
ideal for scenarios where latency is a bottleneck.

## Use Cases

`pcp` excels in scenarios like:

1. **High Latency Filesystems**: typically network filesystems (e.g., NFS, SMB, WebDAV) and distributed filesystems (
   e.g., CephFS, GlusterFS).
    - Round-trip latency is a significant factor. When using single-threaded tools like `cp`, most of the time is spent
      on operations like opening files, creating directory entries, instead of actual data transfer.
    - Single-connection speed is limited. Some ISP limit the speed of a single connection, so using multiple connections
      can help to achieve higher throughput.
2. **Many Small Files**: copying a large number of small files can be slow with traditional tools due to the overhead of
   opening and closing files.
    - `pcp` can operate on multiple files concurrently, reducing the blocking time spent on file operations.
    - Parallelized writes can increase the IO depth submitted to the filesystem, improving throughput on modern SSDs
      that can handle very high IO depths.

Do not use `pcp` in these situations:

1. **Mechanical Disks**: mechanical disks (HDD) are very poor at random access and parallel IO. If you want to use `pcp`
   on HDDs, you should increase `--block-size` to a large value (e.g., 4M) to reduce the number of IO operations.

## Key Features

- **Parallel file copying**: Copy multiple files simultaneously
- **Chunk-based large file copying**: Split large files into chunks and copy them concurrently. All disk space is
  reserved before copying starts, if possible, so disk fragmentation is minimized.
- ~~**Resume capability**: (TODO) Automatically resume interrupted copies~~
- **Rate limiting**: Control transfer speed and file processing rate
- **Progress tracking**: Real-time progress display with speed statistics
- **Verification**: Optional SHA256 checksum verification
- **User-friendly logs**: Logs are structured and can easily be parsed by human or machines. If stderr is a terminal,
  logs are colorized to optimize human readability. Otherwise, logs are written in JSON format for machine parsing.

## Building

```shell
CGO_ENABLED=0 go build "-ldflags=-s -w" -o pcp ./cmd/pcp
```

## How It Works

`pcp` is separated into two main components:

- A `lister` thread that traverses the source directory and collects files to copy. Discovered files are sent to the
  `worker` for processing.
- A `worker` that processes the files in parallel, copying them to the destination. The `worker` has several threads:
    - A `slicer` that slices large files into chunks. Chunks are sent to the `copier`s for copying.
    - Several `copiers` (specified by `--concurrent-chunks`) that handle the actual copying of each chunk

Once `pcp` is started, `lister` and all `worker`s are immediately started. `worker`s do not wait for the `lister` to
finish, allowing them to start processing files as soon as they are available. This design minimizes idle time and
maximizes throughput.

This is how a single file gets copied:

1. The `lister` finds a file to copy and sends it to a `worker`.
2. The `worker` receives the file (let's assume the file is a regular file).
3. The `slicer` slices the file into chunks of a specified size (`--chunk-size`). If the file is smaller than
   `--chunk-size`, it is copied as a single chunk of the actual file size. All the chunks are sent to the `copier`s.
4. One or more `copier`s receive the chunks and copy them to the destination. Each `copier` works on a single chunk at a
   time, but multiple `copier`s can work on different chunks concurrently.
    1. The first `copier` that receives a chunk of this file will open the source file for reading and create the
       destination file.
       All other `copier`s copying the chunks of the same file shares the file descriptors to reduce the overhead of
       open and close syscalls. The destination file is created with the same permissions are the source file and file
       size is preallocated to the size of the source file to minimize fragmentation.
    2. Each `copier` calculates the start and end offsets of the chunk to copy. Each chunk can span across several
       `--block-size` blocks. It repeatedly reads `--block-size` bytes from the source file, and writes them to the
       destination file. This process continues until the entire chunk is copied, then this `copier` moves on to the
       next chunk.
    3. The `copier` to finish the very last chunk of this file will also close the source and destination
       file descriptors. If the user wants to `--preserve-owner`, it will also set the owner and group of the
       destination file to match the source file.

Copying multiple files works similarly, the only difference is that the `copier`s are working on chunks of different
files.

## Performance Tuning

- `--block-size`: Sets the number of bytes of each IO operation. Increasing it to several megabytes can generally
  improve performance (the default value of 1MiB is fine for most people), especially on
  mechanical disks. It reduces the number of IO operations. Do not make it too large. Larger values like several hundred
  megabytes does not make sense. This must be smaller or equal to `--chunk-size`.
- `--chunk-size`: Sets how large the chunks of a file are sliced into. If you are using a network of very high latency (
  like several hundreds of milliseconds), you can increase this so the there are fewer chunks to reduce the number of
  new connections
  created.
- `--concurrent-chunks`: Sets how many chunks can be copied concurrently. Increasing this can improve performance
  on high latency filesystems, but it can also increase the load on the filesystem and network.

## Limitations

- Does not preserve extended attributes (xattr)
- Only handles regular files, directories, and symlinks.
