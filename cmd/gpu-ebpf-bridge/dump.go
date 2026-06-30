// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cilium/ebpf"

	"github.com/alban/gpu-ebpf-bridge/internal/maps"
)

// runDump implements --dump: open each bpffs-pinned bridge map
// read-only, iterate it, and print its contents. Bridge does not need
// to be running. Useful when bpftool is not installed.
func runDump(pinDir string) error {
	if err := dumpMeta(filepath.Join(pinDir, maps.MapNameMeta)); err != nil {
		return err
	}
	if err := dumpDevice(filepath.Join(pinDir, maps.MapNameDevice)); err != nil {
		return err
	}
	if err := dumpPerPid(filepath.Join(pinDir, maps.MapNamePerPid)); err != nil {
		return err
	}
	return dumpPerPidPerDevice(filepath.Join(pinDir, maps.MapNamePerPidPerDevice))
}

func dumpMeta(path string) error {
	m, err := ebpf.LoadPinnedMap(path, nil)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer m.Close()
	var v maps.Meta
	key := uint32(0)
	if err := m.Lookup(&key, &v); err != nil {
		return fmt.Errorf("lookup %s: %w", path, err)
	}
	fmt.Fprintln(os.Stdout, "# gpu_meta")
	fmt.Fprintf(os.Stdout, "  %+v\n\n", v)
	return nil
}

func dumpDevice(path string) error {
	m, err := ebpf.LoadPinnedMap(path, nil)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer m.Close()
	fmt.Fprintln(os.Stdout, "# gpu_device")
	var (
		key uint32
		val maps.DeviceMetrics
	)
	iter := m.Iterate()
	for iter.Next(&key, &val) {
		if val.TimestampNs == 0 {
			// ARRAY slots are zero-initialised; skip unwritten entries.
			continue
		}
		fmt.Fprintf(os.Stdout, "  [device %d] %+v\n", key, val)
	}
	fmt.Fprintln(os.Stdout)
	return iter.Err()
}

func dumpPerPid(path string) error {
	m, err := ebpf.LoadPinnedMap(path, nil)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer m.Close()
	fmt.Fprintln(os.Stdout, "# gpu_per_pid")
	var (
		key uint32
		val maps.PidMetricsAggregated
	)
	iter := m.Iterate()
	for iter.Next(&key, &val) {
		fmt.Fprintf(os.Stdout, "  [pid %d] %+v\n", key, val)
	}
	fmt.Fprintln(os.Stdout)
	return iter.Err()
}

func dumpPerPidPerDevice(path string) error {
	m, err := ebpf.LoadPinnedMap(path, nil)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer m.Close()
	fmt.Fprintln(os.Stdout, "# gpu_per_pid_per_device")
	var (
		key uint64
		val maps.PidMetrics
	)
	iter := m.Iterate()
	for iter.Next(&key, &val) {
		pid := uint32(key >> 32)
		dev := uint32(key & 0xFFFFFFFF)
		fmt.Fprintf(os.Stdout, "  [pid %d dev %d] %+v\n", pid, dev, val)
	}
	fmt.Fprintln(os.Stdout)
	return iter.Err()
}
