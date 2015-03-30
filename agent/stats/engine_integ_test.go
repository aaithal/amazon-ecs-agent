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

	"github.com/aws/amazon-ecs-agent/agent/api"
	"github.com/aws/amazon-ecs-agent/agent/config"
	"github.com/aws/amazon-ecs-agent/agent/engine"
	"github.com/aws/amazon-ecs-agent/agent/utils"
	docker "github.com/fsouza/go-dockerclient"
)

const testImageName = "amazon/amazon-ecs-gremlin:make"

// defaultDockerTimeoutSeconds is the timeout for dialing the docker remote API.
const defaultDockerTimeoutSeconds uint = 10

// waitForCleanupSleep is the sleep duration in milliseconds
// for the waiting after container cleanup before checking the state of the manager.
const waitForCleanupSleep = 1 * time.Millisecond

var endpoint = utils.DefaultIfBlank(os.Getenv(engine.DOCKER_ENDPOINT_ENV_VARIABLE), engine.DOCKER_DEFAULT_ENDPOINT)

var client, _ = docker.NewClient(endpoint)

// createGremlin creates the gremlin container using the docker client.
// It is used only in the test code.
func createGremlin(client *docker.Client) (*docker.Container, error) {
	container, err := client.CreateContainer(docker.CreateContainerOptions{
		Config: &docker.Config{
			Image: testImageName,
		},
	})

	return container, err
}

type IntegContainerMetadataResolver struct {
	containerIDToTask map[string]*api.Task
	containerIDToName map[string]string
}

func newIntegContainerMetadataResolver() *IntegContainerMetadataResolver {
	resolver := IntegContainerMetadataResolver{
		containerIDToTask: make(map[string]*api.Task),
		containerIDToName: make(map[string]string),
	}

	return &resolver
}

func (resolver *IntegContainerMetadataResolver) ResolveTask(containerID string) (*api.Task, error) {
	task, exists := resolver.containerIDToTask[containerID]
	if !exists {
		return nil, errors.New("unmapped container")
	}

	return task, nil
}

func (resolver *IntegContainerMetadataResolver) ResolveName(dockerID string) (string, error) {
	name, exists := resolver.containerIDToName[dockerID]
	if !exists {
		return "", errors.New("unmapped container")
	}

	return name, nil
}
func (resolver *IntegContainerMetadataResolver) addToMap(containerID string, taskArn string, name string) {
	resolver.containerIDToTask[containerID] = &api.Task{Arn: taskArn}
	resolver.containerIDToName[containerID] = name
}

func TestStatsEngineWithExistingContainers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integ test in short mode")
	}
	engine := NewDockerStatsEngine()
	err := engine.initDockerClient()
	if err != nil {
		t.Error("Error initializing stats engine: ", err)
	}

	// Create a container to get the container id.
	container, err := createGremlin(client)
	if err != nil {
		t.Error("Error creating container", err)
	}
	defer client.RemoveContainer(docker.RemoveContainerOptions{
		ID:    container.ID,
		Force: true,
	})
	resolver := newIntegContainerMetadataResolver()
	// Initialize mock interface so that task id is resolved only for the container
	// that was launched during the test.
	resolver.addToMap(container.ID, "gremlin", "docker-gremlin")

	// Wait for containers from previous tests to transition states.
	time.Sleep(checkPointSleep)

	engine.resolver = resolver

	err = client.StartContainer(container.ID, nil)
	if err != nil {
		t.Error("Error starting container: ", container.ID, " error: ", err)
	}
	defer client.StopContainer(container.ID, defaultDockerTimeoutSeconds)

	// Simulate container start prior to listener initialization.
	time.Sleep(checkPointSleep)
	err = engine.Init()
	if err != nil {
		t.Error("Error initializing stats engine: ", err)
	}

	// Wait for the stats collection go routine to start.
	time.Sleep(checkPointSleep)

	instanceMetrics, err := engine.GetInstanceMetrics()
	if err != nil {
		t.Error("Error gettting instance metrics: ", err)
	}

	taskMetrics := instanceMetrics.TaskMetrics
	if len(taskMetrics) != 1 {
		t.Error("Incorrect number of tasks. Expected: 1, got: ", len(taskMetrics))
	}
	err = validateContainerMetrics(taskMetrics[0].ContainerMetrics, 1)
	if err != nil {
		t.Error("Error validating container metrics: ", err)
	}

	err = client.StopContainer(container.ID, defaultDockerTimeoutSeconds)
	if err != nil {
		t.Error("Error stopping container: ", container.ID, " error: ", err)
	}

	time.Sleep(waitForCleanupSleep)

	// Should not contain any metrics after cleanup.
	_, err = engine.GetInstanceMetrics()
	if err == nil {
		t.Error("Expected non-empty error for empty stats.")
	}
}

func TestStatsEngineWithNewContainers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integ test in short mode")
	}
	engine := NewDockerStatsEngine()
	err := engine.initDockerClient()
	if err != nil {
		t.Error("Error initializing stats engine: ", err)
	}
	container, err := createGremlin(client)
	if err != nil {
		t.Error("Error creating container", err)
	}
	defer client.RemoveContainer(docker.RemoveContainerOptions{
		ID:    container.ID,
		Force: true,
	})

	resolver := newIntegContainerMetadataResolver()
	// Initialize mock interface so that task id is resolved only for the container
	// that was launched during the test.
	resolver.addToMap(container.ID, "gremlin", "docker-gremlin")

	// Wait for containers from previous tests to transition states.
	time.Sleep(checkPointSleep)
	engine.resolver = resolver

	err = engine.Init()
	if err != nil {
		t.Error("Error initializing stats engine: ", err)
	}

	err = client.StartContainer(container.ID, nil)
	defer client.StopContainer(container.ID, defaultDockerTimeoutSeconds)
	if err != nil {
		t.Error("Error starting container: ", container.ID, " error: ", err)
	}

	// Wait for the stats collection go routine to start.
	time.Sleep(checkPointSleep)

	instanceMetrics, err := engine.GetInstanceMetrics()
	if err != nil {
		t.Error("Error gettting instance metrics: ", err)
	}

	taskMetrics := instanceMetrics.TaskMetrics
	if len(taskMetrics) != 1 {
		t.Error("Incorrect number of tasks. Expected: 1, got: ", len(taskMetrics))
	}
	err = validateContainerMetrics(taskMetrics[0].ContainerMetrics, 1)
	if err != nil {
		t.Error("Error validating container metrics: ", err)
	}

	err = client.StopContainer(container.ID, defaultDockerTimeoutSeconds)
	if err != nil {
		t.Error("Error stopping container: ", container.ID, " error: ", err)
	}

	time.Sleep(waitForCleanupSleep)

	// Should not contain any metrics after cleanup.
	_, err = engine.GetInstanceMetrics()
	if err == nil {
		t.Error("Expected non-empty error for empty stats.")
	}
}

func TestStatsEngineWithDockerTaskEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integ test in short mode")
	}
	taskEngine := engine.NewTaskEngine(&config.Config{})
	container, err := createGremlin(client)
	if err != nil {
		t.Error("Error creating container", err)
	}
	defer client.RemoveContainer(docker.RemoveContainerOptions{
		ID:    container.ID,
		Force: true,
	})
	unmappedContainer, err := createGremlin(client)
	if err != nil {
		t.Error("Error creating container", err)
	}
	defer client.RemoveContainer(docker.RemoveContainerOptions{
		ID:    unmappedContainer.ID,
		Force: true,
	})
	containers := []*api.Container{
		&api.Container{
			Name: "gremlin",
		},
	}
	testTask := api.Task{
		Arn:           "gremlin-task",
		DesiredStatus: api.TaskRunning,
		KnownStatus:   api.TaskRunning,
		Family:        "test",
		Version:       "1",
		Containers:    containers,
	}
	// Populate Tasks and Container map in the engine.
	dockerTaskEngine, _ := taskEngine.(*engine.DockerTaskEngine)
	dockerTaskEngine.State().AddOrUpdateTask(&testTask)
	dockerTaskEngine.State().AddContainer(
		&api.DockerContainer{
			DockerId:   container.ID,
			DockerName: "gremlin",
			Container:  containers[0],
		},
		&testTask)
	statsEngine := NewDockerStatsEngine()
	err = statsEngine.MustInit(taskEngine, defaultClusterArn, defaultContainerInstanceArn)
	if err != nil {
		t.Error("Error initializing stats engine: ", err)
	}

	err = client.StartContainer(container.ID, nil)
	defer client.StopContainer(container.ID, defaultDockerTimeoutSeconds)
	if err != nil {
		t.Error("Error starting container: ", container.ID, " error: ", err)
	}

	err = client.StartContainer(unmappedContainer.ID, nil)
	defer client.StopContainer(unmappedContainer.ID, defaultDockerTimeoutSeconds)
	if err != nil {
		t.Error("Error starting container: ", unmappedContainer.ID, " error: ", err)
	}

	// Wait for the stats collection go routine to start.
	time.Sleep(checkPointSleep)

	instanceMetrics, err := statsEngine.GetInstanceMetrics()
	if err != nil {
		t.Error("Error gettting instance metrics: ", err)
	}

	taskMetrics := instanceMetrics.TaskMetrics
	if len(taskMetrics) != 1 {
		t.Error("Incorrect number of tasks. Expected: 1, got: ", len(taskMetrics))
	}
	err = validateContainerMetrics(taskMetrics[0].ContainerMetrics, 1)
	if err != nil {
		t.Error("Error validating container metrics: ", err)
	}

	err = client.StopContainer(container.ID, defaultDockerTimeoutSeconds)
	if err != nil {
		t.Error("Error stopping container: ", container.ID, " error: ", err)
	}

	time.Sleep(waitForCleanupSleep)

	// Should not contain any metrics after cleanup.
	_, err = statsEngine.GetInstanceMetrics()
	if err == nil {
		t.Error("Expected non-empty error for empty stats.")
	}
}
