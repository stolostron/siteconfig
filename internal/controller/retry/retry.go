package retry

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

var RetryBackoffTwoMinutes = wait.Backoff{
	Steps:    120,
	Duration: time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

var RetryBackoff30Seconds = wait.Backoff{
	Steps:    30,
	Duration: time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

func isRetriable(err error) bool {
	return apierrors.IsInternalError(err) || apierrors.IsServiceUnavailable(err) || net.IsConnectionRefused(err)
}

func RetryOnRetriable(backoff wait.Backoff, fn func() error) error {
	return retry.OnError(backoff, isRetriable, fn) //nolint:wrapcheck
}

func isConflictOrRetriable(err error) bool {
	return apierrors.IsConflict(err) || apierrors.IsInternalError(err) || apierrors.IsServiceUnavailable(err) || net.IsConnectionRefused(err)
}

func RetryOnConflictOrRetriable(backoff wait.Backoff, fn func() error) error {
	return retry.OnError(backoff, isConflictOrRetriable, fn) //nolint:wrapcheck
}
