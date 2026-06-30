// SPDX-License-Identifier: Apache-2.0
//
// Package maps owns the four bpffs-pinned BPF maps that gpu-ebpf-bridge
// publishes as its API contract:
//
//	/sys/fs/bpf/gpu_meta
//	/sys/fs/bpf/gpu_device
//	/sys/fs/bpf/gpu_per_pid
//	/sys/fs/bpf/gpu_per_pid_per_device
//
// The bridge loads the small BPF object internal/maps/bpf/gpu_types.bpf.c
// (compiled by bpf2go and embedded at compile time) so the four maps come
// into the kernel with proper BTF for struct gpu_meta,
// gpu_device_metrics, gpu_pid_metrics, and gpu_pid_metrics_aggregated.
// Consumers can then CO-RE-read those maps by field name.
package maps

// Include paths (resolved from internal/maps/, where this gen.go lives):
//   - ./bpf            : the BPF source dir, holds gpu_types.bpf.c and vmlinux.h
//   - ../../include    : the public C header (gpu_types.h)
//   - /usr/include/bpf : libbpf headers (system-installed via libbpf-dev)
//
// bpf2go writes its Go bindings + .o objects into this package directory
// (internal/maps/). The BPF C source stays in internal/maps/bpf/, which
// is intentionally not a Go package — keeping .c files out of a Go
// package dir avoids "C source files not allowed when not using cgo".
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -no-strip -target amd64,arm64 -go-package maps gputypes ./bpf/gpu_types.bpf.c -- -I./bpf -I../../include -Wall -O2 -g
