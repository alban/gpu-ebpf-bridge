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
#   make build    - go build the bridge binary
#   make test     - run unit tests
#   make clean    - remove the built binary (does not touch bpf2go-generated
#                   files; use git clean if you need to wipe those)
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

test:
	$(GO) test -v ./...

clean:
	rm -rf bin/
