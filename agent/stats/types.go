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
	"time"

	"golang.org/x/net/context"
)

// ContainerStats encapsulates the raw CPU and memory utilization from cgroup fs.
type ContainerStats struct {
	cpuUsage    uint64
	memoryUsage uint64
	timestamp   time.Time
}

// UsageStats abstracts the format in which the queue stores data.
type UsageStats struct {
	CPUUsagePerc      float32   `json:"cpuUsagePerc"`
	MemoryUsageInMegs uint32    `json:"memoryUsageInMegs"`
	Timestamp         time.Time `json:"timestamp"`
	cpuUsage          uint64    `json:"-"`
}

// CPUStatsSet is the format in which CPU usage data is exported to the server.
type CPUStatsSet struct {
	Min         float32 `json:"min"`
	Max         float32 `json:"max"`
	SampleCount int     `json:"sampleCount"`
	Sum         float32 `json:"sum"`
	Unit        string  `json:"unit"`
	Timestamp   int64   `json:"timestamp"`
}

// MemoryStatsSet is the format in which memory usage data is exported to the server.
type MemoryStatsSet struct {
	Min         uint32 `json:"min"`
	Max         uint32 `json:"max"`
	SampleCount int    `json:"sampleCount"`
	Sum         uint32 `json:"sum"`
	Unit        string `json:"unit"`
	Timestamp   int64  `json:"timestamp"`
}

// ContainerMetadata contains meta-data information for a container.
type ContainerMetadata struct {
	DockerID string `json:"-"`
	Name     string `json:"name"`
}

// CronContainer abstracts methods to gather and aggregate utilization data for a container.
type CronContainer struct {
	containerMetadata *ContainerMetadata
	ctx               context.Context
	cancel            context.CancelFunc
	statePath         string
	statsQueue        *Queue
	statsCollector    ContainerStatsCollector
}

// InstanceMetadata contains meta-data information for the container instance.
// ecs container agent.
type InstanceMetadata struct {
	ClusterArn           string `json:"cluster"`
	ContainerInstanceArn string `json:"containerInstance"`
}

// ContainerMetric groups CPU and Memory usage metrics for a container.
type ContainerMetric struct {
	ContainerMetadata *ContainerMetadata `json:"metadata"`
	CPUStatsSet       *CPUStatsSet       `json:"cpuStatsSet"`
	MemoryStatsSet    *MemoryStatsSet    `json:"memoryStatsSet"`
}

// TaskMetric groups all containers belonging to a task and their CPU and Memory usage stats.
type TaskMetric struct {
	TaskArn          string            `json:"taskArn"`
	ContainerMetrics []ContainerMetric `json:"containerMetrics"`
}

// InstanceMetrics aggregates all task metrics to publish to backend.
type InstanceMetrics struct {
	Metadata    *InstanceMetadata `json:"metadata"`
	TaskMetrics []TaskMetric      `json:"taskMetrics"`
}
