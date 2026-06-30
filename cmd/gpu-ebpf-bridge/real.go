// SPDX-License-Identifier: Apache-2.0
//
//go:build cgo && nvml

package main

import (
	"log/slog"

	"github.com/alban/gpu-ebpf-bridge/internal/nvml"
)

func newRealPoller() (nvml.Poller, error) {
	return nvml.NewReal(slog.Default()), nil
}
