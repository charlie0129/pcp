package progress

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charlie0129/pcp/pkg/utils/size"
)

var (
	SpeedSmoothingTime = 5 * time.Second
)

type Stats struct {
	FilesBeingProcessed  int64 // Number of files currently being processed.
	ChunksBeingProcessed int64 // Number of chunks currently being processed.
	FilesProcessed       int64 // Number of files that have been processed.
	BytesProcessed       int64 // Total number of bytes processed.
	IOCompleted          int64 // Total number of IO operations completed.
}

type Progress struct {
	mu  *sync.Mutex
	out io.Writer

	updatePeriod time.Duration

	statsGetter func() Stats // Function to get the current stats.

	stats       Stats
	lastStats   Stats // lastStats before screen is updated
	lastUpdated time.Time
}

func New(out io.Writer, updatePeriod time.Duration) *Progress {
	return &Progress{
		mu:           &sync.Mutex{},
		out:          out,
		updatePeriod: updatePeriod,
	}
}

// Write implements the io.Writer interface for Progress, so Progress can be
// used as an output for loggers, to avoid progress display issues when
// logs appear the same time as the progress bar is updated.
func (p *Progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Erase current progress text, and move to line start.
	_, _ = fmt.Fprint(p.out, "\033[2K\r")

	// Write the new text.
	n, err := p.out.Write(b)
	if err != nil {
		return n, err
	}

	// Make sure it ends in a newline so progress can be displayed on a new line.
	if n > 0 && b[n-1] != '\n' {
		_, _ = fmt.Fprintln(p.out)
	}

	return n, nil
}

func (p *Progress) Start(ctx context.Context) {
	startTime := time.Now()

	t := time.NewTicker(p.updatePeriod)
	defer t.Stop()

out:
	for {
		select {
		case <-ctx.Done():
			break out
		case <-t.C:
			p.mu.Lock()
			if p.statsGetter != nil {
				p.update(p.statsGetter())
			}
			_, _ = fmt.Fprint(p.out, "\033[2K\r"+p.formatStats())
			p.mu.Unlock()
		}
	}

	_, _ = fmt.Fprintln(p.out) // Last new line.

	// Summary time.
	runTime := time.Since(startTime)

	if p.stats.BytesProcessed > 0 && p.stats.FilesProcessed > 0 {
		p.mu.Lock()
		defer p.mu.Unlock()

		_, _ = fmt.Fprintf(p.out, `
Summary:
    Run time:        %s
    Bytes processed: %s
    Transfer speed:  %s/s
    Files processed: %s
    File proc speed: %s/s
    IO speed:        %s IOPS
`, runTime.Round(time.Millisecond).String(),
			size.FormatBytes(p.stats.BytesProcessed),
			size.FormatBytes(int64(float64(p.stats.BytesProcessed)/runTime.Seconds())),
			size.FormatNumber(p.stats.FilesProcessed),
			size.FormatNumber(int64(float64(p.stats.FilesProcessed)/runTime.Seconds())),
			size.FormatNumber(int64(float64(p.stats.IOCompleted)/runTime.Seconds())),
		)
	}
}

func (p *Progress) SetStatsGetter(f func() Stats) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statsGetter = f
}

func (p *Progress) update(stats Stats) {
	p.stats = stats
}

// lock before calling
func (p *Progress) formatStats() string {
	timeDiff := time.Since(p.lastUpdated)
	timeDiffSeconds := timeDiff.Seconds()

	ret := fmt.Sprintf("%s, at %s/s | %s %s, at %s/s | %s IOPS | Processing %s %s",
		size.FormatBytes(p.stats.BytesProcessed),
		size.FormatBytes(int64(float64(p.stats.BytesProcessed-p.lastStats.BytesProcessed)/timeDiffSeconds)),
		//
		size.FormatNumber(p.stats.FilesProcessed),
		singularOrPlural(p.stats.FilesProcessed, "file", "files"),
		size.FormatNumber(int64(float64(p.stats.FilesProcessed-p.lastStats.FilesProcessed)/timeDiffSeconds)),
		//
		size.FormatNumber(int64(float64(p.stats.IOCompleted-p.lastStats.IOCompleted)/timeDiffSeconds)),
		//
		size.FormatNumber(p.stats.FilesBeingProcessed),
		singularOrPlural(p.stats.FilesBeingProcessed, "file", "files"),
	)

	if p.stats.ChunksBeingProcessed > 0 {
		ret += fmt.Sprintf(" and %s chunks", size.FormatNumber(p.stats.ChunksBeingProcessed))
	}

	needSpeedRecalculation := timeDiff > SpeedSmoothingTime
	if needSpeedRecalculation {
		p.lastStats = p.stats
		p.lastUpdated = time.Now()
	}

	return ret
}

func singularOrPlural(n int64, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
