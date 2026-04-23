package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultInterceptors_ReturnsFour(t *testing.T) {
	mws := DefaultInterceptors()
	assert.Len(t, mws, 4, "DefaultInterceptors should return [Recover, LogContext, Tracing, Slog]")
}
