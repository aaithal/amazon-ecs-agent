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
	Min         float32   `json:"min"`
	Max         float32   `json:"max"`
	SampleCount int       `json:"sampleCount"`
	Sum         float32   `json:"sum"`
	Timestamp   time.Time `json:"timeStamp"`
}

// MemoryStatsSet is the format in which memory usage data is exported to the server.
type MemoryStatsSet struct {
	Min         uint32    `json:"min"`
	Max         uint32    `json:"max"`
	SampleCount int       `json:"sampleCount"`
	Sum         uint32    `json:"sum"`
	Timestamp   time.Time `json:"timeStamp"`
}

// CronContainer abstracts methods to gather and aggregate utilization data for a container.
type CronContainer struct {
	containerMetadata ContainerMetadata
	ctx               context.Context
	cancel            context.CancelFunc
	statePath         string
	statsQueue        *Queue
	statsCollector    ContainerStatsCollector
}

// ContainerStatsSet groups container meta-data and CPU and memory stats sets.
type ContainerStatsSet struct {
	InstanceMetadata  *InstanceMetadata `json:"instanceMetadata"`
	ContainerMetadata ContainerMetadata `json:"containerMetaData"`
	CPUStatsSet       *CPUStatsSet      `json:"cpuStatsSet"`
	MemoryStatsSet    *MemoryStatsSet   `json:"memoryStatsSet"`
}

// ContainerRawUsageStats groups container meta-data and raw CPU and memory usage stats.
type ContainerRawUsageStats struct {
	InstanceMetadata  *InstanceMetadata `json:"instanceMetadata"`
	ContainerMetadata ContainerMetadata `json:"containerMetaData"`
	UsageStats        []UsageStats      `json:"usageStats"`
}

// ContainerMetadata contains meta-data information for a container.
type ContainerMetadata struct {
	DockerID string
	TaskArn  string
}

// InstanceMetadata contains meta-data information for the container instance.
// ecs container agent.
type InstanceMetadata struct {
	ClusterArn           string
	ContainerInstanceArn string
}
