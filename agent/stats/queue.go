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
	"errors"
	"math"
	"sync"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
)

const (
	// CPUUsageUnit is the unit of CPU usage being reported for a container.
	CPUUsageUnit = "Percent"

	// MemoryUsageUnit is the unit of memory usage being reported for a container.
	MemoryUsageUnit = "Megabytes"

	// BytesInMB is the number of bytes in a MegaByte. Using MB as it is one of the
	// units supported by Cloudwatch.
	// refer http://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_MetricDatum.html
	BytesInMB = 1000 * 1000
)

var cpuUsageUnit string
var memoryUsageUnit string

func init() {
	cpuUsageUnit = CPUUsageUnit
	memoryUsageUnit = MemoryUsageUnit
}

// Queue abstracts a queue using UsageStats slice.
type Queue struct {
	buffer     []UsageStats
	maxSize    int
	bufferLock sync.RWMutex
}

// NewQueue creates a queue.
func NewQueue(maxSize int) *Queue {
	return &Queue{
		buffer:  make([]UsageStats, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add adds a new set of container stats to the queue.
func (queue *Queue) Add(rawStat *ContainerStats) {
	queue.bufferLock.Lock()
	defer queue.bufferLock.Unlock()

	queueLength := len(queue.buffer)
	stat := UsageStats{
		CPUUsagePerc:      (float32)(NaN32()),
		MemoryUsageInMegs: (uint32)(rawStat.memoryUsage) / BytesInMB,
		Timestamp:         rawStat.timestamp,
		cpuUsage:          rawStat.cpuUsage,
	}
	if queueLength != 0 {
		// % utilization can be calculated only when queue is non-empty.
		lastStat := queue.buffer[queueLength-1]
		stat.CPUUsagePerc = 100 * (float32)(rawStat.cpuUsage-lastStat.cpuUsage) / (float32)(rawStat.timestamp.Sub(lastStat.Timestamp).Nanoseconds())
		if queue.maxSize == queueLength {
			// Remove first element if queue is full.
			queue.buffer = queue.buffer[1:queueLength]
		}
	}

	queue.buffer = append(queue.buffer, stat)
}

func getCPUUsagePerc(s *UsageStats) float64 {
	return float64(s.CPUUsagePerc)
}

func getMemoryUsagePerc(s *UsageStats) float64 {
	return float64(s.MemoryUsageInMegs)
}

type getUsageFunc func(*UsageStats) float64

// getCWStatsSet gets the stats set for either CPU or Memory based on the
// function pointer.
func (queue *Queue) getCWStatsSet(f getUsageFunc) (*ecstcs.CWStatsSet, error) {
	queue.bufferLock.Lock()
	defer queue.bufferLock.Unlock()

	queueLength := len(queue.buffer)
	if queueLength < 2 {
		// Need at least 2 data points to calculate this.
		return nil, errors.New("No data in the queue")
	}

	var min, max, sum float64
	var sampleCount int64
	min = -math.MaxFloat64
	max = math.MaxFloat64
	sum = 0
	sampleCount = 0

	for _, stat := range queue.buffer {
		perc := f(&stat)
		if math.IsNaN(perc) {
			continue
		}

		min = math.Min(min, perc)
		max = math.Max(max, perc)
		sampleCount++
		sum += perc
	}

	return &ecstcs.CWStatsSet{
		Max:         &max,
		Min:         &min,
		SampleCount: &sampleCount,
		Sum:         &sum,
	}, nil
}

// GetCPUStatsSet gets the stats set for CPU utilization.
func (queue *Queue) GetCPUStatsSet() (*ecstcs.CWStatsSet, error) {
	statsSet, err := queue.getCWStatsSet(getCPUUsagePerc)
	if err != nil {
		return nil, err
	}

	statsSet.Unit = &cpuUsageUnit

	return statsSet, nil
}

// GetMemoryStatsSet gets the stats set for memory utilization.
func (queue *Queue) GetMemoryStatsSet() (*ecstcs.CWStatsSet, error) {
	statsSet, err := queue.getCWStatsSet(getMemoryUsagePerc)
	if err != nil {
		return nil, err
	}

	statsSet.Unit = &memoryUsageUnit

	return statsSet, nil
}

// GetRawUsageStats gets the array of most recent raw UsageStats, in descending
// order of timestamps.
func (queue *Queue) GetRawUsageStats(numStats int) ([]UsageStats, error) {
	queue.bufferLock.Lock()
	defer queue.bufferLock.Unlock()

	queueLength := len(queue.buffer)
	if queueLength == 0 {
		return nil, errors.New("No data in the queue")
	}

	if numStats > queueLength {
		numStats = queueLength
	}

	usageStats := make([]UsageStats, numStats)
	for i := 0; i < numStats; i++ {
		// Order such that usageStats[i].timestamp > usageStats[i+1].timestamp
		rawUsageStat := queue.buffer[queueLength-i-1]
		usageStats[i] = UsageStats{
			CPUUsagePerc:      rawUsageStat.CPUUsagePerc,
			MemoryUsageInMegs: rawUsageStat.MemoryUsageInMegs,
			Timestamp:         rawUsageStat.Timestamp,
		}
	}

	return usageStats, nil
}
