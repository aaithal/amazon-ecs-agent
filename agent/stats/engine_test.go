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
	"os"
	"testing"
	"time"

	"code.google.com/p/gomock/gomock"
	"github.com/aws/amazon-ecs-agent/agent/api"
	ecsengine "github.com/aws/amazon-ecs-agent/agent/engine"
	"github.com/aws/amazon-ecs-agent/agent/statemanager"
)

const defaultClusterArn = "default"
const defaultContainerInstanceArn = "ci"

type MockTaskEngine struct {
}

func (engine *MockTaskEngine) Init() error {
	return nil
}
func (engine *MockTaskEngine) MustInit() {
}

func (engine *MockTaskEngine) TaskEvents() <-chan api.ContainerStateChange {
	return make(chan api.ContainerStateChange)
}

func (engine *MockTaskEngine) SetSaver(statemanager.Saver) {
}

func (engine *MockTaskEngine) AddTask(*api.Task) {
}

func (engine *MockTaskEngine) ListTasks() ([]*api.Task, error) {
	return nil, nil
}

func (engine *MockTaskEngine) UnmarshalJSON([]byte) error {
	return nil
}

func (engine *MockTaskEngine) MarshalJSON() ([]byte, error) {
	return make([]byte, 0), nil
}

func (engine *MockTaskEngine) Version() (string, error) {
	return "", nil
}

func TestStatsEngineAddRemoveContainers(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	resolver := NewMockContainerMetadataResolver(mockCtrl)
	resolver.EXPECT().ResolveTask("c1").Return(&api.Task{Arn: "t1"}, nil)
	resolver.EXPECT().ResolveTask("c2").Return(&api.Task{Arn: "t2"}, nil)
	resolver.EXPECT().ResolveTask("c3").Return(&api.Task{Arn: "t3"}, nil)
	resolver.EXPECT().ResolveTask("c4").Return(nil, errors.New("unmapped container"))

	engine := NewDockerStatsEngine()
	engine.resolver = resolver

	engine.AddContainer("c1")
	engine.AddContainer("c1")
	if len(engine.containers) != 1 {
		t.Error("Adding duplicate containers failed.")
	}
	_, exists := engine.containers["c1"]
	if !exists {
		t.Error("Container c1 not found in engine")
	}
	_, exists = engine.containers["c2"]
	if exists {
		t.Error("Unexpected container c2 found in engine")
	}
	engine.AddContainer("c2")
	_, exists = engine.containers["c2"]
	if !exists {
		t.Error("Container c2 not found in engine")
	}

	containerStats := []*ContainerStats{
		CreateContainerStats(22400432, 1839104, ParseNanoTime("2015-02-12T21:22:05.131117533Z")),
		CreateContainerStats(116499979, 3649536, ParseNanoTime("2015-02-12T21:22:05.232291187Z")),
	}
	for _, cronContainer := range engine.containers {
		for i := 0; i < 2; i++ {
			cronContainer.statsQueue.Add(containerStats[i])
		}
	}

	statsSets := engine.GetContainersStatsSet()
	if len(statsSets) != 2 {
		t.Error("Mismatch in number of ContainerStatsSet elements. Expected: 2, Got: ", len(statsSets))
	}
	for _, statsSet := range statsSets {
		if statsSet.CPUStatsSet == nil {
			t.Error("CPUStatsSet is nil")
		}
		if statsSet.MemoryStatsSet == nil {
			t.Error("MemoryStatsSet is nil")
		}
	}

	rawStatsSets := engine.GetContainersRawUsageStats(1)
	if len(rawStatsSets) != 2 {
		t.Error("Mismatch in number of ContainerStatsSet elements. Expected: 2, Got: ", len(rawStatsSets))
	}
	for _, rawStatsSet := range rawStatsSets {
		if len(rawStatsSet.UsageStats) != 1 {
			t.Error("Incorrect number of raw stats set retrieved. Expected: 1, Got: ", len(rawStatsSet.UsageStats))
		}
	}

	engine.RemoveContainer("c1")
	_, exists = engine.containers["c1"]
	if exists {
		t.Error("Container c1 not removed from engine")
	}
	engine.RemoveContainer("c2")
	_, exists = engine.containers["c2"]
	if exists {
		t.Error("Container c2 not removed from engine")
	}
	engine.AddContainer("c3")
	_, exists = engine.containers["c3"]
	if !exists {
		t.Error("Container c3 not found in engine")
	}

	// Container stats haven't been added for 'c3'.
	statsSets = engine.GetContainersStatsSet()
	if len(statsSets) != 0 {
		t.Error("Expected to get empty stats. Got: ", len(statsSets))
	}
	rawStatsSets = engine.GetContainersRawUsageStats(1)
	if len(rawStatsSets) != 0 {
		t.Error("Expected to get empty stats. Got: ", len(rawStatsSets))
	}
	engine.RemoveContainer("c3")

	// Should get an error while adding this container.
	engine.AddContainer("c4")
	_, exists = engine.containers["c4"]
	if exists {
		t.Error("Container c4 found in engine")
	}

}

func TestStatsEngineMetadataInStatsSets(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	resolver := NewMockContainerMetadataResolver(mockCtrl)
	resolver.EXPECT().ResolveTask("c1").Return(&api.Task{Arn: "t1"}, nil)

	engine := NewDockerStatsEngine()
	engine.resolver = resolver
	engine.instanceMetadata = newInstanceMetadata(defaultClusterArn, defaultContainerInstanceArn)
	engine.AddContainer("c1")
	_, exists := engine.containers["c1"]
	if !exists {
		t.Error("Container c1 not found in engine")
	}
	containerStats := []*ContainerStats{
		CreateContainerStats(22400432, 1839104, ParseNanoTime("2015-02-12T21:22:05.131117533Z")),
		CreateContainerStats(116499979, 3649536, ParseNanoTime("2015-02-12T21:22:05.232291187Z")),
	}
	for _, cronContainer := range engine.containers {
		for i := 0; i < 2; i++ {
			cronContainer.statsQueue.Add(containerStats[i])
		}
	}
	statsSets := engine.GetContainersStatsSet()
	if len(statsSets) != 1 {
		t.Error("Mismatch in number of ContainerStatsSet elements. Expected: 1, Got: ", len(statsSets))
	}
	for _, statsSet := range statsSets {
		if statsSet.CPUStatsSet == nil {
			t.Error("CPUStatsSet is nil")
		}
		if statsSet.MemoryStatsSet == nil {
			t.Error("MemoryStatsSet is nil")
		}

		if statsSet.InstanceMetadata.ClusterArn != defaultClusterArn {
			t.Errorf("Cluster Arn not set in metadata. Expected: %s, got: %s", defaultClusterArn, statsSet.InstanceMetadata.ClusterArn)
		}
		if statsSet.InstanceMetadata.ContainerInstanceArn != defaultContainerInstanceArn {
			t.Errorf("Container Instance Arn not set in metadata. Expected: %s, got: %s", defaultContainerInstanceArn, statsSet.InstanceMetadata.ContainerInstanceArn)
		}
	}

	rawStatsSets := engine.GetContainersRawUsageStats(1)
	if len(rawStatsSets) != 1 {
		t.Error("Mismatch in number of ContainerStatsSet elements. Expected: 1, Got: ", len(rawStatsSets))
	}
	for _, rawStatsSet := range rawStatsSets {
		if len(rawStatsSet.UsageStats) != 1 {
			t.Error("Incorrect number of raw stats set retrieved. Expected: 1, Got: ", len(rawStatsSet.UsageStats))
		}
	}

	engine.RemoveContainer("c1")
	_, exists = engine.containers["c1"]
	if exists {
		t.Error("Container c1 not removed from engine")
	}
}

func TestStatsEngineInvalidTaskEngine(t *testing.T) {
	statsEngine := NewDockerStatsEngine()
	taskEngine := &MockTaskEngine{}
	err := statsEngine.MustInit(taskEngine, "", "")
	if err == nil {
		t.Error("Expected error in engine initialization, got nil")
	}
}

func TestStatsEngineUninitialized(t *testing.T) {
	engine := NewDockerStatsEngine()
	engine.resolver = &DockerContainerMetadataResolver{}
	engine.AddContainer("c1")
	_, exists := engine.containers["c1"]
	if exists {
		t.Error("Container c1 found in engine")
	}
}

func TestStatsEngineTerminalTask(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	resolver := NewMockContainerMetadataResolver(mockCtrl)
	resolver.EXPECT().ResolveTask("c1").Return(&api.Task{Arn: "t1", KnownStatus: api.TaskStopped}, nil)
	engine := NewDockerStatsEngine()
	engine.resolver = resolver

	engine.AddContainer("c1")
	_, exists := engine.containers["c1"]
	if exists {
		t.Error("Container c1 found in engine")
	}
}

func TestStatsEngineClientErrorListingContainers(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	engine := NewDockerStatsEngine()
	mockDockerClient := NewMockDockerClient(mockCtrl)
	// Mock client will return error while listing images.
	mockDockerClient.EXPECT().ListContainers(false).Return(nil, errors.New("could not list containers"))
	engine.client = mockDockerClient
	mockChannel := make(chan ecsengine.DockerContainerChangeEvent)
	mockDockerClient.EXPECT().ContainerEvents().Return(mockChannel, nil, nil)
	mockDockerClient.EXPECT().UnsubscribeContainerEvents(gomock.Any()).Return(nil)
	engine.client = mockDockerClient
	engine.Init()

	time.Sleep(waitForCleanupSleep)
	// Make sure that the stats engine deregisters the event listener when it fails to
	// list images.
	if engine.dockerEventListener != nil {
		t.Error("Event listener hasn't been reset")
	}
}

func TestStatsEngineDisabledEnvVar(t *testing.T) {
	os.Unsetenv("ECS_DISABLE_METRICS")
	setMetricCollectionFlag()
	if IsMetricCollectionDisabled() {
		t.Error("Stats engine disabled when ECS_DISABLE_METRICS is not set")
	}
	os.Setenv("ECS_DISABLE_METRICS", "opinion")
	setMetricCollectionFlag()
	if IsMetricCollectionDisabled() {
		t.Error("Stats engine disabled when ECS_DISABLE_METRICS is neither true nor false")
	}
	os.Setenv("ECS_DISABLE_METRICS", "false")
	setMetricCollectionFlag()
	if IsMetricCollectionDisabled() {
		t.Error("Stats engine disabled when ECS_DISABLE_METRICS is false")
	}
	os.Setenv("ECS_DISABLE_METRICS", "true")
	setMetricCollectionFlag()
	if !IsMetricCollectionDisabled() {
		t.Error("Stats engine enabled when ECS_DISABLE_METRICS is true")
	}
}
