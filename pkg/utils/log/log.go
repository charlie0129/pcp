package log

import (
	"io"

	"github.com/rs/zerolog"
)

var (
	Verbosity int
)

func getLevel(verbosity int) zerolog.Level {
	level := zerolog.InfoLevel

	switch verbosity {
	case 0:
		level = zerolog.InfoLevel
	case 1:
		level = zerolog.DebugLevel
	default:
		level = zerolog.TraceLevel
	}

	return level
}

func GetLogger(out io.Writer, isTerminal bool) zerolog.Logger {
	level := getLevel(Verbosity)

	l := zerolog.New(out).With().Timestamp().Logger().Level(level)

	if isTerminal {
		l = l.Output(zerolog.ConsoleWriter{Out: out})
	}

	return l
}
