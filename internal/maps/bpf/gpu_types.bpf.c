// SPDX-License-Identifier: Apache-2.0
//
// gpu_types.bpf.c - establishes the gpu-ebpf-bridge maps in bpffs with BTF.
//
// This BPF object declares no programs and no entry points. Its sole
// purpose is to carry the canonical struct definitions for the four
// bridge maps into the kernel's BPF BTF, so that:
//
//   1. cilium/ebpf can NewMapWithOptions(... PinByName) and produce maps
//      whose value_type_id refers to the BTF type we want consumers to
//      CO-RE-read by name.
//
//   2. Tools like `bpftool map dump pinned <path>` automatically decode
//      every field by name without needing an external schema.
//
//   3. Consumer eBPF gadgets (in IG or elsewhere) that include
//      include/gpu_types.h can use BPF_CORE_READ and
//      bpf_core_field_exists() against the same struct layout.
//
// The bridge loads this object at startup, takes references to the four
// maps (which arrive pinned at /sys/fs/bpf/<name> thanks to
// LIBBPF_PIN_BY_NAME + cilium/ebpf MapOptions.PinPath), and then writes
// to them directly via the Map.Update() API. There is no need to attach
// or run any BPF program.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

#include "gpu_types.h"

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct gpu_meta);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} gpu_meta SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, GPU_MAX_DEVICES);
	__type(key, __u32);
	__type(value, struct gpu_device_metrics);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} gpu_device SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 4096);
	__type(key, __u32);                            /* host PID */
	__type(value, struct gpu_pid_metrics_aggregated);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} gpu_per_pid SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 4096);
	__type(key, __u64);                            /* (pid << 32) | device_idx */
	__type(value, struct gpu_pid_metrics);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} gpu_per_pid_per_device SEC(".maps");

char __license[] SEC("license") = "Dual BSD/GPL";
