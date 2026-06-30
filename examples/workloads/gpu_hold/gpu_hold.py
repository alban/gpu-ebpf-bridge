#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""
gpu_hold.py -- minimal CUDA workload that populates the gpu-ebpf-bridge
per-PID maps (gpu_per_pid, gpu_per_pid_per_device).

Allocates a chunk of VRAM and (optionally) runs a periodic matrix
multiply to drive the per-PID utilization counter. While this script
is running you should see:

    - gpu_per_pid[<pid>].used_gpu_memory_total  > 0
    - gpu_per_pid[<pid>].sm_util_pct_max        > 0  (only if compute=1)
    - gpu_device[0].mem_used                    > 0
    - gpu_device[0].sm_util_pct                 > 0  (only if compute=1)

Configurable via environment variables (all optional):
    GPU_HOLD_MIB          VRAM to allocate (default: 2048)
    GPU_HOLD_SECONDS      Total runtime in seconds (default: 120)
    GPU_HOLD_COMPUTE      1 to do periodic matmul, 0 to just hold memory
                          (default: 1; set to 0 to test memory accounting only)
    GPU_HOLD_MATRIX_SIZE  matmul dimension (default: 4096; ~256 MiB per matrix)
    GPU_HOLD_INTERVAL     sleep between matmul ticks in seconds (default: 0.5)
    CUDA_VISIBLE_DEVICES  Which GPU to use (default: 0)

Usage:
    pip install --break-system-packages torch
    python3 examples/workloads/gpu_hold/gpu_hold.py
"""

import os
import sys
import time

try:
    import torch
except ImportError:
    print(
        "torch is not installed. Install it with one of:\n"
        "    pip install --break-system-packages torch\n"
        "    apt install python3-torch       # if your distro packages it\n",
        file=sys.stderr,
    )
    sys.exit(2)

if not torch.cuda.is_available():
    print(
        "torch.cuda.is_available() returned False. Check that nvidia-smi works\n"
        "and that this Python's torch package was built with CUDA support.",
        file=sys.stderr,
    )
    sys.exit(2)


def env_int(name: str, default: int) -> int:
    try:
        return int(os.environ[name])
    except KeyError:
        return default
    except ValueError:
        print(f"warning: {name}={os.environ[name]!r} is not an int; using {default}",
              file=sys.stderr)
        return default


def env_float(name: str, default: float) -> float:
    try:
        return float(os.environ[name])
    except KeyError:
        return default
    except ValueError:
        print(f"warning: {name}={os.environ[name]!r} is not a float; using {default}",
              file=sys.stderr)
        return default


def env_bool(name: str, default: bool) -> bool:
    return os.environ.get(name, "1" if default else "0") not in ("0", "false", "False", "")


mib       = env_int("GPU_HOLD_MIB", 2048)
duration  = env_int("GPU_HOLD_SECONDS", 120)
compute   = env_bool("GPU_HOLD_COMPUTE", True)
matsz     = env_int("GPU_HOLD_MATRIX_SIZE", 4096)
interval  = env_float("GPU_HOLD_INTERVAL", 0.5)

device = torch.device("cuda:0")
gpu_name = torch.cuda.get_device_name(0)
print(
    f"pid={os.getpid()} gpu={gpu_name!r} mib={mib} duration={duration}s "
    f"compute={'on' if compute else 'off'} matrix={matsz}x{matsz} interval={interval}s",
    flush=True,
)

# 1 MiB of float32 = 256K elements. Allocating as a single contiguous tensor.
hold_elems = mib * 256 * 1024
hold = torch.zeros(hold_elems, dtype=torch.float32, device=device)
gib = hold.element_size() * hold.nelement() / (1024 ** 3)
print(f"allocated {gib:.2f} GiB on {hold.device}", flush=True)

a = b = None
if compute:
    a = torch.randn(matsz, matsz, dtype=torch.float32, device=device)
    b = torch.randn(matsz, matsz, dtype=torch.float32, device=device)

start = time.time()
ticks = 0
try:
    while time.time() - start < duration:
        if compute:
            c = a @ b
            torch.cuda.synchronize()
            ticks += 1
            if interval > 0:
                time.sleep(interval)
        else:
            time.sleep(5)

        # Periodic heartbeat (~every 10s either way).
        elapsed = time.time() - start
        if compute:
            heartbeat = (ticks % max(1, int(10 / max(interval, 0.05))) == 0)
        else:
            heartbeat = (int(elapsed) % 10 < 5)  # rough
        if heartbeat:
            print(f"  ... t={elapsed:6.1f}s ticks={ticks}", flush=True)
except KeyboardInterrupt:
    print("interrupted", flush=True)

elapsed = time.time() - start
print(f"done after {elapsed:.1f}s, {ticks} matmul ticks", flush=True)

# Explicit free is optional (process exit reclaims), but kept for clarity.
del hold
if a is not None:
    del a, b
torch.cuda.empty_cache()
