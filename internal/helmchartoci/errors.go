package helmchartoci

import "errors"

var (
	errEmptyImage   = errors.New("image reference is empty")
	errInvalidImage = errors.New("image reference has no repository name")
)
