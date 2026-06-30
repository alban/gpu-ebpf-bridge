// SPDX-License-Identifier: Apache-2.0

package maps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cilium/ebpf"
)

// DefaultPinDir is where the four bridge maps are pinned by default.
// Matches the bpffs root, so consumers can declare LIBBPF_PIN_BY_NAME on
// matching maps without needing any non-default MapOptions.PinPath.
const DefaultPinDir = "/sys/fs/bpf"

// Pinned-map base names. These are the actual files created under PinDir.
const (
	MapNameMeta             = "gpu_meta"
	MapNameDevice           = "gpu_device"
	MapNamePerPid           = "gpu_per_pid"
	MapNamePerPidPerDevice  = "gpu_per_pid_per_device"
)

// Bridge owns the four pinned BPF maps published by gpu-ebpf-bridge.
//
// The lifecycle is:
//
//	b, err := maps.Open(maps.DefaultPinDir)   // load BPF object, pin maps
//	defer b.Close()                           // close FDs (does NOT unpin)
//	... b.UpdateMeta(&m)
//	... b.UpdateDevice(0, &d)
//	... b.UpdatePerPid(pid, &p)
//	... b.UpdatePerPidPerDevice(pid, devIdx, &p)
//	b.Unpin()                                 // remove pins from bpffs
//
// Close() vs Unpin() are deliberately separate: Close releases this
// process's file descriptors but leaves the pinned maps in place so
// consumer eBPF programs (gadgets, bpftool, etc.) can keep reading.
// Unpin removes the bpffs entries; only call it on clean bridge
// shutdown when no consumers should be expected to outlive the bridge.
type Bridge struct {
	pinDir string
	objs   gputypesObjects
}

// Open loads the embedded BPF object and creates+pins the four maps
// under pinDir. If a compatible pinned map already exists, libbpf
// reuses it (see Maps.PinPath semantics in github.com/cilium/ebpf).
// Mismatching specs return an error.
func Open(pinDir string) (*Bridge, error) {
	if pinDir == "" {
		pinDir = DefaultPinDir
	}
	if err := os.MkdirAll(pinDir, 0o755); err != nil {
		return nil, fmt.Errorf("ensuring pin dir %s: %w", pinDir, err)
	}

	b := &Bridge{pinDir: pinDir}
	opts := &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{PinPath: pinDir},
	}
	if err := loadGputypesObjects(&b.objs, opts); err != nil {
		return nil, fmt.Errorf("loading BPF object and pinning maps in %s: %w",
			pinDir, err)
	}
	return b, nil
}

// PinDir returns the directory under which the maps are pinned.
func (b *Bridge) PinDir() string { return b.pinDir }

// Close releases this process's file descriptors but leaves the pinned
// maps in place on bpffs. Consumers can keep reading until Unpin() is
// called (typically only at clean shutdown).
func (b *Bridge) Close() error {
	return b.objs.Close()
}

// Unpin removes the four bpffs entries. After Unpin, consumers attempting
// to open the maps will get ENOENT. Idempotent: missing entries are
// silently ignored.
func (b *Bridge) Unpin() error {
	var errs []error
	for _, name := range []string{
		MapNameMeta, MapNameDevice, MapNamePerPid, MapNamePerPidPerDevice,
	} {
		path := filepath.Join(b.pinDir, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// UpdateMeta writes the bridge metadata record (key 0 of the gpu_meta
// ARRAY[1] map).
func (b *Bridge) UpdateMeta(m *Meta) error {
	key := uint32(0)
	return b.objs.GpuMeta.Update(&key, (*gputypesGpuMeta)(m), ebpf.UpdateAny)
}

// UpdateDevice writes the per-device record at the given device index.
// idx must be in [0, GPU_MAX_DEVICES).
func (b *Bridge) UpdateDevice(idx uint32, d *DeviceMetrics) error {
	return b.objs.GpuDevice.Update(&idx, (*gputypesGpuDeviceMetrics)(d), ebpf.UpdateAny)
}

// UpdatePerPid writes the aggregated per-PID record (key = host PID).
func (b *Bridge) UpdatePerPid(pid uint32, p *PidMetricsAggregated) error {
	return b.objs.GpuPerPid.Update(&pid, (*gputypesGpuPidMetricsAggregated)(p), ebpf.UpdateAny)
}

// UpdatePerPidPerDevice writes the per-(PID, device) detailed record.
// Key encoding: u64 = (pid << 32) | devIdx.
func (b *Bridge) UpdatePerPidPerDevice(pid uint32, devIdx uint32, p *PidMetrics) error {
	key := PerPidPerDeviceKey(pid, devIdx)
	return b.objs.GpuPerPidPerDevice.Update(&key, (*gputypesGpuPidMetrics)(p), ebpf.UpdateAny)
}

// DeletePerPid removes the aggregated entry for a PID. Returns nil if
// the entry did not exist.
func (b *Bridge) DeletePerPid(pid uint32) error {
	if err := b.objs.GpuPerPid.Delete(&pid); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	return nil
}

// DeletePerPidPerDevice removes a per-(PID, device) entry. Returns nil
// if the entry did not exist.
func (b *Bridge) DeletePerPidPerDevice(pid uint32, devIdx uint32) error {
	key := PerPidPerDeviceKey(pid, devIdx)
	if err := b.objs.GpuPerPidPerDevice.Delete(&key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	return nil
}

// PerPidPerDeviceKey builds the composite u64 key used by the
// gpu_per_pid_per_device map: (pid << 32) | devIdx.
func PerPidPerDeviceKey(pid uint32, devIdx uint32) uint64 {
	return uint64(pid)<<32 | uint64(devIdx)
}
