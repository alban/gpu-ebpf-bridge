# SPDX-License-Identifier: Apache-2.0
#
# Build the standalone CUDA workload (no Python deps).

NVCC ?= nvcc
NVCC_FLAGS ?= -O2

.PHONY: all clean
all: gpu_hold

gpu_hold: gpu_hold.cu
	$(NVCC) $(NVCC_FLAGS) -o $@ $<

clean:
	rm -f gpu_hold
