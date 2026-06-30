// SPDX-License-Identifier: Apache-2.0

// Package poller drives the main bridge loop: every PollInterval, ask
// the nvml.Poller for the current device snapshot + per-PID samples
// and write the results into the bpffs-pinned BPF maps. It also
// maintains the gpu_meta freshness signal that consumers rely on.
package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/alban/gpu-ebpf-bridge/internal/maps"
	"github.com/alban/gpu-ebpf-bridge/internal/nvml"
)

// Config selects the poll cadence and the upstream NVML backend.
type Config struct {
	// PollInterval is the time between Poller.Devices() /
	// ProcessSamples() ticks. Defaults to 100 ms (10 Hz).
	PollInterval time.Duration

	// Source is the upstream telemetry provider (mock or real NVML).
	Source nvml.Poller

	// Bridge is the bpffs-pinned-map writer. Owned by the caller;
	// the poller will Update*() it but never Close/Unpin it.
	Bridge *maps.Bridge

	// Logger is used for warnings and per-tick info logs. Defaults to
	// slog.Default() if nil.
	Logger *slog.Logger
}

// Poller is the main bridge loop.
type Poller struct {
	cfg     Config
	logger  *slog.Logger
	helper  uint32 // os.Getpid() captured once

	mu          sync.Mutex
	// lastSeenPerDevice tracks the highest sample timestamp returned
	// by Source.ProcessSamples for each device, so the next call can
	// ask only for samples newer than that.
	lastSeenPerDevice map[uint32]uint64
}

// New constructs a Poller with sensible defaults applied.
func New(cfg Config) (*Poller, error) {
	if cfg.Source == nil {
		return nil, errors.New("poller: Config.Source is required")
	}
	if cfg.Bridge == nil {
		return nil, errors.New("poller: Config.Bridge is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 100 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Poller{
		cfg:               cfg,
		logger:            cfg.Logger,
		helper:            uint32(os.Getpid()),
		lastSeenPerDevice: make(map[uint32]uint64),
	}, nil
}

// Run drives the poll loop until ctx is cancelled. It returns nil on
// clean shutdown and the wrapped Init/loop error otherwise.
func (p *Poller) Run(ctx context.Context) error {
	if err := p.cfg.Source.Init(ctx); err != nil {
		return fmt.Errorf("nvml init: %w", err)
	}
	defer func() { _ = p.cfg.Source.Close() }()

	// Tick once immediately so consumers see fresh data within
	// ~PollInterval of bridge startup, then on every PollInterval.
	if err := p.tick(ctx); err != nil {
		p.logger.Warn("initial tick failed", "err", err)
	}

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.tick(ctx); err != nil {
				// Transient errors don't abort the loop; the bridge is
				// expected to be best-effort. The gpu_meta freshness
				// signal is what consumers should rely on.
				p.logger.Warn("tick failed", "err", err)
			}
		}
	}
}

func (p *Poller) tick(ctx context.Context) error {
	devs, devErr := p.cfg.Source.Devices(ctx)
	if devErr != nil {
		return fmt.Errorf("devices: %w", devErr)
	}

	// Write each device's metrics.
	for _, d := range devs {
		mapVal := deviceSnapshotToMap(d)
		if err := p.cfg.Bridge.UpdateDevice(d.Index, &mapVal); err != nil {
			p.logger.Warn("UpdateDevice failed", "idx", d.Index, "err", err)
		}
	}

	// Per-PID samples. Use the minimum lastSeen across devices to avoid
	// missing samples for newly-appearing devices; the real backend
	// keys its rolling window per device.
	lastSeen := p.minLastSeen()
	samples, sampErr := p.cfg.Source.ProcessSamples(ctx, lastSeen)
	if sampErr != nil {
		p.logger.Warn("ProcessSamples failed", "err", sampErr)
	}

	// Group samples by PID for the aggregated map; write detailed
	// entries one-by-one.
	type aggBuilder struct {
		ts         uint64
		usedTotal  uint64
		smMax      uint32
		memMax     uint32
		firstDev   uint8
		multi      bool
		count      uint8
	}
	agg := make(map[uint32]*aggBuilder)

	for _, s := range samples {
		// Detailed per-(pid, dev) entry.
		detail := pidSampleToMap(s)
		if err := p.cfg.Bridge.UpdatePerPidPerDevice(s.Pid, s.DeviceIndex, &detail); err != nil {
			p.logger.Warn("UpdatePerPidPerDevice failed",
				"pid", s.Pid, "dev", s.DeviceIndex, "err", err)
		}

		// Aggregated builder for this PID.
		b := agg[s.Pid]
		if b == nil {
			b = &aggBuilder{
				ts:       s.TimestampNs,
				firstDev: uint8(s.DeviceIndex),
			}
			agg[s.Pid] = b
		}
		b.usedTotal += s.UsedGpuMemory
		if s.SmUtilPct > b.smMax {
			b.smMax = s.SmUtilPct
		}
		if s.MemUtilPct > b.memMax {
			b.memMax = s.MemUtilPct
		}
		if uint8(s.DeviceIndex) != b.firstDev {
			b.multi = true
		}
		b.count++
		// Advance per-device watermark.
		p.advanceLastSeen(s.DeviceIndex, s.TimestampNs)
	}

	for pid, b := range agg {
		mapVal := maps.PidMetricsAggregated{
			TimestampNs:        b.ts,
			UsedGpuMemoryTotal: b.usedTotal,
			SmUtilPctMax:       b.smMax,
			MemUtilPctMax:      b.memMax,
			GpuDevicePrimary:   b.firstDev,
			DeviceCount:        b.count,
		}
		if b.multi {
			mapVal.GpuDevicePrimary = maps.DevicePrimaryMulti
		}
		if err := p.cfg.Bridge.UpdatePerPid(pid, &mapVal); err != nil {
			p.logger.Warn("UpdatePerPid failed", "pid", pid, "err", err)
		}
	}

	// gpu_meta last, so consumers that gate enrichment on
	// last_update_ns see the data first.
	meta := maps.Meta{
		SchemaVersion: maps.SchemaVersion,
		N_devices:     uint32(len(devs)),
		LastUpdateNs:  uint64(time.Now().UnixNano()),
		HelperPid:     p.helper,
	}
	if err := p.cfg.Bridge.UpdateMeta(&meta); err != nil {
		// Meta is essential for consumer freshness checks; this should
		// be loud.
		return fmt.Errorf("UpdateMeta: %w", err)
	}

	return nil
}

func (p *Poller) minLastSeen() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.lastSeenPerDevice) == 0 {
		return 0
	}
	var min uint64
	first := true
	for _, ts := range p.lastSeenPerDevice {
		if first || ts < min {
			min = ts
			first = false
		}
	}
	return min
}

func (p *Poller) advanceLastSeen(dev uint32, ts uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ts > p.lastSeenPerDevice[dev] {
		p.lastSeenPerDevice[dev] = ts
	}
}

func deviceSnapshotToMap(d nvml.DeviceSnapshot) maps.DeviceMetrics {
	return maps.DeviceMetrics{
		TimestampNs:         d.TimestampNs,
		SmUtilPct:           d.SmUtilPct,
		MemUtilPct:          d.MemUtilPct,
		MemTotal:            d.MemTotal,
		MemUsed:             d.MemUsed,
		MemReserved:         d.MemReserved,
		TempC:               d.TempC,
		PowerMw:             d.PowerMw,
		SmClockMhz:          d.SmClockMhz,
		MemClockMhz:         d.MemClockMhz,
		ThrottleReasons:     d.ThrottleReasons,
		PcieTxKbps:          d.PcieTxKbps,
		PcieRxKbps:          d.PcieRxKbps,
		EncUtilPct:          d.EncUtilPct,
		DecUtilPct:          d.DecUtilPct,
		NvlinkTxKbps:        d.NvlinkTxKbps,
		NvlinkRxKbps:        d.NvlinkRxKbps,
		EccCorrectedTotal:   d.EccCorrectedTotal,
		EccUncorrectedTotal: d.EccUncorrectedTotal,
		FanSpeedPct:         d.FanSpeedPct,
		ComputeMode:         d.ComputeMode,
	}
}

func pidSampleToMap(s nvml.ProcessSample) maps.PidMetrics {
	return maps.PidMetrics{
		TimestampNs:   s.TimestampNs,
		UsedGpuMemory: s.UsedGpuMemory,
		SmUtilPct:     s.SmUtilPct,
		MemUtilPct:    s.MemUtilPct,
		EncUtilPct:    s.EncUtilPct,
		DecUtilPct:    s.DecUtilPct,
		GpuDevice:     uint8(s.DeviceIndex),
		MigInstance:   s.MigInstance,
	}
}
