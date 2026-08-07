package slogconfigurator

import "errors"

var (
	ErrInvalidLogLevel     = errors.New("invalid log level")
	ErrInvalidLogFormat    = errors.New("invalid log format")
	ErrInvalidLogAddSource = errors.New("invalid log add-source value")
)
