// SPDX-License-Identifier: Apache-2.0

package maps

// Public Go struct types mirroring the BPF struct definitions in
// include/gpu_types.h. The bpf2go-generated types in
// internal/maps/bpf/gputypes_*_bpfel.go are package-private; this file
// re-exports them as Meta/DeviceMetrics/PidMetrics/PidMetricsAggregated
// so consumers of the maps package have a stable, capitalized API
// surface that doesn't change when bpf2go is re-run.
//
// The Update* methods in maps.go take pointers to these types and
// cast to the bpf2go types internally. The casts are safe because both
// pairs are declared from the exact same BPF struct layout (bpf2go
// reads the BPF object's BTF; we re-declare with matching field order
// and types).

// Meta mirrors struct gpu_meta in include/gpu_types.h.
type Meta = gputypesGpuMeta

// DeviceMetrics mirrors struct gpu_device_metrics in include/gpu_types.h.
type DeviceMetrics = gputypesGpuDeviceMetrics

// PidMetrics mirrors struct gpu_pid_metrics in include/gpu_types.h
// (detailed per-(pid, device) record).
type PidMetrics = gputypesGpuPidMetrics

// PidMetricsAggregated mirrors struct gpu_pid_metrics_aggregated in
// include/gpu_types.h (convenience per-pid aggregated record).
type PidMetricsAggregated = gputypesGpuPidMetricsAggregated

// SchemaVersion is the version the bridge writes into Meta.SchemaVersion.
// Matches GPU_SCHEMA_VERSION in include/gpu_types.h.
const SchemaVersion uint32 = 1

// MaxDevices matches GPU_MAX_DEVICES in include/gpu_types.h. The
// gpu_device ARRAY has this many slots.
const MaxDevices = 16

// DevicePrimaryMulti is the sentinel value written into
// PidMetricsAggregated.GpuDevicePrimary when a single PID holds contexts
// on more than one device.
const DevicePrimaryMulti uint8 = 0xFF
