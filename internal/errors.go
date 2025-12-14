package grip

import "errors"

var (
	ErrNoInstallPath  error = errors.New("no install path provided")
	ErrNoAbsolutePath error = errors.New("provided install path is not absolute")
	ErrInvalidAsset   error = errors.New("invalid asset")
	ErrInvalidRepo    error = errors.New("invalid repository path")
	ErrNotFound       error = errors.New("not found")
	ErrAlreadyExists  error = errors.New("already exists")
)
