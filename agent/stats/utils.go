// Copyright 2014-2015 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//	http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package stats

import (
	"math"
	"time"

	"github.com/docker/libcontainer"
)

// NaN32 returns a 32bit NaN.
func NaN32() float32 {
	return (float32)(math.NaN())
}

// IsNaN32 reports whether value is a “not-a-number” value.
func IsNaN32(value float32) bool {
	return math.IsNaN((float64)(value))
}

// Min32 returns the smaller of x or y.
func Min32(x, y float32) float32 {
	return (float32)(math.Min((float64)(x), (float64)(y)))
}

// Max32 returns the larger of the x or y.
func Max32(x, y float32) float32 {
	return (float32)(math.Max((float64)(x), (float64)(y)))
}

// ToContainerStats returns a new object of the ContainerStats object from libcontainer stats.
func ToContainerStats(containerStats libcontainer.ContainerStats) *ContainerStats {
	return &ContainerStats{
		cpuUsage:    containerStats.CgroupStats.CpuStats.CpuUsage.TotalUsage,
		memoryUsage: containerStats.CgroupStats.MemoryStats.Usage,
		timestamp:   time.Now(),
	}
}

// CreateContainerStats returns a new object of the ContainerStats object.
func CreateContainerStats(cpuTime uint64, memBytes uint64, ts time.Time) *ContainerStats {
	return &ContainerStats{
		cpuUsage:    cpuTime,
		memoryUsage: memBytes,
		timestamp:   ts,
	}
}

// ParseNanoTime returns the time object from a string formatted with RFC3339Nano layout.
func ParseNanoTime(value string) time.Time {
	ts, _ := time.Parse(time.RFC3339Nano, value)
	return ts
}
