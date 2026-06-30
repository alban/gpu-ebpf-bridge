// SPDX-License-Identifier: Apache-2.0
//
//go:build !cgo || !nvml

// This stub is built when:
//   - CGO is disabled (CGO_ENABLED=0), or
//   - The 'nvml' build tag is not set.
//
// In both cases the bridge cannot link against libnvidia-ml.so.1, so
// newRealPoller returns an error. The bridge's --mode=auto path
// translates that into "fall back to mock"; --mode=real treats it as
// a fatal startup error with a clear message.

package main

import (
	"errors"

	"github.com/alban/gpu-ebpf-bridge/internal/nvml"
)

func newRealPoller() (nvml.Poller, error) {
	return nil, errors.New("real NVML backend not compiled in (rebuild with -tags nvml and CGO_ENABLED=1)")
}

// Compile-time sanity: nvml.ErrNotAvailable is what callers check
// against to distinguish "no GPU on this node" from other errors.
var _ = nvml.ErrNotAvailable
