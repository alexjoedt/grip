package grip

import "errors"

var (
	ErrNoInstallPath  error = errors.New("no install path provided")
	ErrNoAbsolutePath error = errors.New("provided install path is not absolute")
)
