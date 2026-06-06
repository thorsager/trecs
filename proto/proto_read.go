package proto

import (
	"bufio"
	"fmt"
	"strings"
	"sync"
)

var bufioReaderPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(nil, 4096)
	},
}

// UnmarshalError represents a SIP parsing failure and may wrap an underlying cause.
type UnmarshalError struct {
	Cause error
	Msg   string
}

func (e *UnmarshalError) Error() string {
	if e.Cause != nil {
		return e.Msg + ": " + e.Cause.Error()
	}
	return e.Msg
}

func (e *UnmarshalError) Unwrap() error {
	return e.Cause
}

func UnmarshalErrorf(msg string, args ...any) *UnmarshalError {
	return &UnmarshalError{Msg: fmt.Sprintf(msg, args...)}
}

func UnmarshalErrorWrap(cause error, msg string, args ...any) *UnmarshalError {
	return &UnmarshalError{Msg: fmt.Sprintf(msg, args...), Cause: cause}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSuffix(line, "\r\n"), nil
}

func readContinuedLine(r *bufio.Reader) (string, error) {
	line, err := readLine(r)
	if err != nil || line == "" {
		return line, err
	}
	for {
		b, peekErr := r.Peek(1)
		if peekErr != nil || (b[0] != ' ' && b[0] != '\t') {
			break
		}
		line = strings.TrimRight(line, " \t\r")
		cont, err := readLine(r)
		if err != nil {
			return line, err
		}
		line += cont
	}
	return line, nil //nolint:nilerr // Peek error (e.g. EOF) just means no continuation
}
