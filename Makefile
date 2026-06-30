# SPDX-License-Identifier: Apache-2.0
#
# Top-level Makefile for gpu-ebpf-bridge.
#
# Standard targets:
#   make vmlinux  - regenerate internal/maps/bpf/vmlinux.h from the kernel BTF.
#                   The committed copy is a reasonable default; regenerate
#                   only if you need fields from a newer kernel.
#   make generate - run "go generate" (bpf2go compiles the BPF object and
#                   emits Go bindings under internal/maps/bpf/). The
#                   resulting *.bpfel_*.go and *.bpfel_*.o files are
#                   committed to the repo so downstream users can `go get`
#                   without running this themselves.
#   make build      - go build (mock-only binary, no cgo, runs anywhere)
#   make build-nvml - go build with -tags nvml (real NVML backend, needs
#                     CGO + libnvidia-ml.so.1 at runtime; use on GPU nodes)
#   make test       - run unit tests (mock backend)
#   make test-nvml  - run unit tests against the nvml-tagged build
#   make clean      - remove the built binaries
#
# Build host requirements (only for `make generate` and `make vmlinux`,
# not for `make build` once generated files are in place):
#   - clang >= 11 (invoked by bpf2go via the //go:generate directive)
#   - bpftool (used only by `make vmlinux` to dump kernel BTF)
#   - libbpf headers at /usr/include/bpf/ (libbpf-dev on Debian/Ubuntu)
#   - go >= 1.21

BPFTOOL  ?= bpftool
GO       ?= go

BPF_DIR    := internal/maps/bpf
VMLINUX_H  := $(BPF_DIR)/vmlinux.h

.PHONY: all vmlinux generate build test clean

all: build

# Generate vmlinux.h from the running kernel's BTF. The repo ships with a
# committed copy as a sensible default; regenerate only if you need
# fields from a newer kernel. bpf2go consumes it via the -I./bpf flag
# baked into internal/maps/gen.go.
$(VMLINUX_H):
	@echo "Generating $@ from kernel BTF..."
	$(BPFTOOL) btf dump file /sys/kernel/btf/vmlinux format c > $@

vmlinux: $(VMLINUX_H)

# Invoke bpf2go via go generate. Writes:
#   internal/maps/bpf/gputypes_bpfel_x86.{go,o}
#   internal/maps/bpf/gputypes_bpfel_arm64.{go,o}
# Both Go files and .o objects are committed so consumers can build
# without the BPF toolchain.
generate:
	$(GO) generate ./...

build:
	$(GO) build -o bin/gpu-ebpf-bridge ./cmd/gpu-ebpf-bridge

# Build the variant that links the real NVML backend. Requires CGO and
# libnvidia-ml.so.1 at runtime. Use this on actual GPU nodes; the plain
# 'make build' produces a mock-only binary that runs anywhere.
build-nvml:
	CGO_ENABLED=1 $(GO) build -tags nvml -o bin/gpu-ebpf-bridge-nvml ./cmd/gpu-ebpf-bridge

test:
	$(GO) test -exec 'sudo -E' ./...

test-nvml:
	CGO_ENABLED=1 $(GO) test -tags nvml -exec 'sudo -E' ./...

clean:
	rm -rf bin/
