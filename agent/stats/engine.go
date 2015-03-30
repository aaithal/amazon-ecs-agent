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

//go:generate mockgen.sh $GOPACKAGE $GOFILE

import (
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/aws/amazon-ecs-agent/agent/api"
	ecsengine "github.com/aws/amazon-ecs-agent/agent/engine"
	"github.com/aws/amazon-ecs-agent/agent/logger"
	"github.com/aws/amazon-ecs-agent/agent/utils"
	"github.com/fsouza/go-dockerclient"
)

const (
	// DisableStatsEnvVar specifies the environment variable name to
	// be set to disable metric gathering.
	DisableStatsEnvVar = "ECS_DISABLE_METRICS"

	// DefaultDisableStatsEnvVarValue specifies the default environment
	// value for the DisableStatsEnvVar variable.
	DefaultDisableStatsEnvVarValue = "false"
)

var log = logger.ForModule("stats")

// ContainerMetadataResolver defines methods to resolve meta-data.
type ContainerMetadataResolver interface {
	ResolveTask(string) (*api.Task, error)
	ResolveName(string) (string, error)
}

// DockerContainerMetadataResolver implements ContainerMetadataResolver for
// DockerTaskEngine.
type DockerContainerMetadataResolver struct {
	dockerTaskEngine *ecsengine.DockerTaskEngine
}

// Engine defines methods to be implemented by the engine struct. It is
// defined to make testing easier.
type Engine interface {
	GetInstanceMetrics() (*InstanceMetrics, error)
}

// DockerStatsEngine is used to monitor docker container events and to report
// utlization metrics of the same.
type DockerStatsEngine struct {
	client              ecsengine.DockerClient
	containersLock      sync.RWMutex
	dockerEventListener chan *docker.APIEvents
	events              <-chan ecsengine.DockerContainerChangeEvent
	instanceMetadata    *InstanceMetadata
	resolver            ContainerMetadataResolver
	// tasksToContainers maps task arns to a map of container ids to CronContainer objects.
	tasksToContainers map[string]map[string]*CronContainer
}

// dockerStatsEngine is a singleton object of DockerStatsEngine.
var dockerStatsEngine *DockerStatsEngine

// isMetricCollectionDisabled stores the value of DisableStatsEnvVar.
var isMetricCollectionDisabled bool

func init() {
	setMetricCollectionFlag()
}

// IsMetricCollectionDisabled returns true if the ECS_DISABLE_METRICS is set to "false".
// Else, it returns true.
func IsMetricCollectionDisabled() bool {
	return isMetricCollectionDisabled
}

// ResolveTask resolves the task arn, given container id.
func (resolver *DockerContainerMetadataResolver) ResolveTask(dockerID string) (*api.Task, error) {
	if resolver.dockerTaskEngine == nil {
		return nil, errors.New("Docker task engine uninitialized.")
	}
	task, found := resolver.dockerTaskEngine.State().TaskById(dockerID)
	if !found {
		return nil, errors.New("Could not map docker id to task")
	}

	return task, nil
}

// ResolveName resolves the container name, given container id.
func (resolver *DockerContainerMetadataResolver) ResolveName(dockerID string) (string, error) {
	if resolver.dockerTaskEngine == nil {
		return "", errors.New("Docker task engine uninitialized.")
	}
	container, found := resolver.dockerTaskEngine.State().ContainerById(dockerID)
	if !found {
		return "", errors.New("Could not map docker id to container")
	}

	return container.DockerName, nil
}

// NewDockerStatsEngine creates a new instance of the DockerStatsEngine object.
// MustInit() must be called to initialize the fields of the new event listener.
func NewDockerStatsEngine() *DockerStatsEngine {
	if dockerStatsEngine == nil {
		dockerStatsEngine = &DockerStatsEngine{
			client:            nil,
			resolver:          nil,
			tasksToContainers: make(map[string]map[string]*CronContainer),
		}
	}

	return dockerStatsEngine
}

// MustInit initializes fields of the DockerStatsEngine object.
func (engine *DockerStatsEngine) MustInit(taskEngine ecsengine.TaskEngine, imd *InstanceMetadata) error {
	log.Info("Initializing stats engine")
	err := engine.initDockerClient()
	if err != nil {
		return err
	}

	engine.instanceMetadata = imd

	engine.resolver, err = newDockerContainerMetadataResolver(taskEngine)
	if err != nil {
		return err
	}

	return engine.Init()
}

// Init initializes the docker client's event engine. This must be called
// to subscribe to the docker's event stream.
func (engine *DockerStatsEngine) Init() error {
	err := engine.openEventStream()
	if err != nil {
		return err
	}

	go engine.listContainersAndStartEventHandler()
	return nil
}

// listContainersAndStartEventHandler adds existing containers to the watch-list
// and starts the docker event handler.
func (engine *DockerStatsEngine) listContainersAndStartEventHandler() {
	// List and add existing containers to the list of containers to watch.
	err := engine.addExistingContainers()
	if err != nil {
		log.Warn("Error listing existing containers", "err", err)
		err := engine.client.UnsubscribeContainerEvents(engine.dockerEventListener)
		if err != nil {
			log.Warn("Error unsubscribing docker event listener")
		}
		// Reset event listener to indicate that it has benn unsubscribed.
		engine.dockerEventListener = nil
		return
	}

	go engine.handleDockerEvents()
}

// AddContainer adds a container to the map of containers being watched.
// It also starts the periodic usage data collection for the container.
func (engine *DockerStatsEngine) AddContainer(dockerID string) {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	// Make sure that this container belongs to a task and that the task
	// is not terminal.
	task, err := engine.resolver.ResolveTask(dockerID)
	if err != nil {
		log.Info("Could not map container to task, ignoring", "err", err, "id", dockerID)
		return
	}

	if task.KnownStatus.Terminal() {
		log.Info("Task is terminal, ignoring", "id", dockerID)
		return
	}

	// Check if this containers is already being watched.
	_, taskExists := engine.tasksToContainers[task.Arn]
	if taskExists {
		// task arn exists in map.
		_, containerExists := engine.tasksToContainers[task.Arn][dockerID]
		if containerExists {
			// container arn exists in map.
			log.Info("Container already being watched, ignoring", "id", dockerID)
			return
		}
	}

	if !taskExists {
		// Create a map for the task arn if it doesn't exist yet.
		engine.tasksToContainers[task.Arn] = make(map[string]*CronContainer)
	}

	containerName, err := engine.resolver.ResolveName(dockerID)
	if err != nil {
		log.Info("Could not get name for container, ignoring", "err", err, "id", dockerID)
		return
	}
	log.Debug("Adding container to stats watch list", "id", dockerID, "task", task.Arn)
	container := newCronContainer(dockerID, containerName)
	engine.tasksToContainers[task.Arn][dockerID] = container
	container.StartStatsCron()
}

// RemoveContainer deletes the container from the map of containers being watched.
// It also stops the periodic usage data collection for the container.
func (engine *DockerStatsEngine) RemoveContainer(dockerID string) {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	// Make sure that this container belongs to a task.
	task, err := engine.resolver.ResolveTask(dockerID)
	if err != nil {
		log.Info("Could not map container to task, ignoring", "err", err, "id", dockerID)
		return
	}

	_, taskExists := engine.tasksToContainers[task.Arn]
	if !taskExists {
		log.Debug("Container not being watched", "id", dockerID)
		return
	}

	// task arn exists in map.
	container, containerExists := engine.tasksToContainers[task.Arn][dockerID]
	if !containerExists {
		// container arn exists in map.
		log.Debug("Container not being watched", "id", dockerID)
		return
	}

	container.StopStatsCron()
	delete(engine.tasksToContainers[task.Arn], dockerID)
	log.Debug("Deleted container from tasks", "id", dockerID)

	if len(engine.tasksToContainers[task.Arn]) == 0 {
		// No containers in task, delete task arn from map.
		delete(engine.tasksToContainers, task.Arn)
		log.Debug("Deleted task from tasks", "arn", task.Arn)
	}
}

// GetInstanceMetrics gets all task metrics and instance metadata from stats engine.
func (engine *DockerStatsEngine) GetInstanceMetrics() (*InstanceMetrics, error) {
	var taskMetrics []TaskMetric
	for taskArn := range engine.tasksToContainers {
		containerMetrics, err := engine.getContainerMetricsForTask(taskArn)
		if err != nil {
			log.Warn("Error getting container metrics for task", "err", err, "task", taskArn)
			continue
		}

		if len(containerMetrics) == 0 {
			log.Debug("Empty containerMetrics for task, ignoring", "task", taskArn)
			continue
		}

		taskMetrics = append(taskMetrics, TaskMetric{
			TaskArn:          taskArn,
			ContainerMetrics: containerMetrics,
		})
	}

	if len(taskMetrics) == 0 {
		return nil, errors.New("No tasks to report")
	}

	return &InstanceMetrics{
		Metadata:    engine.instanceMetadata,
		TaskMetrics: taskMetrics,
	}, nil
}

// initDockerClient initializes engine's docker client.
func (engine *DockerStatsEngine) initDockerClient() error {
	if engine.client == nil {
		client, err := ecsengine.NewDockerGoClient()
		if err != nil {
			return err
		}
		engine.client = client
	}

	return nil
}

// addExistingContainers lists existing containers and adds them to the engine.
func (engine *DockerStatsEngine) addExistingContainers() error {
	containerIDs, err := engine.client.ListContainers(false)
	if err != nil {
		return err
	}

	for _, containerID := range containerIDs {
		engine.AddContainer(containerID)
	}

	return nil
}

// openEventStream initializes the channel to receive events from docker client's
// event stream.
func (engine *DockerStatsEngine) openEventStream() error {
	events, listener, err := engine.client.ContainerEvents()
	if err != nil {
		return err
	}
	engine.events = events
	engine.dockerEventListener = listener
	return nil
}

// handleDockerEvents must be called after openEventstream; it processes each
// event that it reads from the docker event stream.
func (engine *DockerStatsEngine) handleDockerEvents() {
	for event := range engine.events {
		log.Debug("Handling an event: ", "container", event.DockerId, "status", event.Status.String())
		switch event.Status {
		case api.ContainerRunning:
			engine.AddContainer(event.DockerId)
		case api.ContainerStopped:
			engine.RemoveContainer(event.DockerId)
		case api.ContainerDead:
			engine.RemoveContainer(event.DockerId)
		default:
			log.Info("Ignoring event for container", "id", event.DockerId, "status", event.Status)
		}
	}
	log.Crit("Docker event stream closed unexpectedly")
}

// newDockerContainerMetadataResolver returns a new instance of DockerContainerMetadataResolver.
func newDockerContainerMetadataResolver(taskEngine ecsengine.TaskEngine) (*DockerContainerMetadataResolver, error) {
	dockerTaskEngine, ok := taskEngine.(*ecsengine.DockerTaskEngine)
	if !ok {
		// Error type casting docker task engine.
		return nil, errors.New("Could not load docker task engine")
	}

	resolver := &DockerContainerMetadataResolver{
		dockerTaskEngine: dockerTaskEngine,
	}

	return resolver, nil
}

// setMetricCollectionFlag reads the ECS_DISABLE_METRICS env variable and
// sets the isMetricCollectionDisabled flag appropriately.
func setMetricCollectionFlag() {
	disableStatsEnvVarValue := utils.DefaultIfBlank(os.Getenv(DisableStatsEnvVar), DefaultDisableStatsEnvVarValue)
	// Ignore any errors in parsing.
	isMetricCollectionDisabled, _ = strconv.ParseBool(disableStatsEnvVarValue)
}

// getContainerMetricsForTask gets all container metrics for a task arn.
func (engine *DockerStatsEngine) getContainerMetricsForTask(taskArn string) ([]ContainerMetric, error) {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	containerMap, taskExists := engine.tasksToContainers[taskArn]
	if !taskExists {
		return nil, errors.New("Task not found")
	}

	var containerMetrics []ContainerMetric
	for _, container := range containerMap {
		// Get CPU stats set.
		cpuStatsSet, err := container.statsQueue.GetCPUStatsSet()
		if err != nil {
			log.Warn("Error getting cpu stats", "err", err, "container", container.containerMetadata)
			continue
		}

		// Get memory stats set.
		memoryStatsSet, err := container.statsQueue.GetMemoryStatsSet()
		if err != nil {
			log.Warn("Error getting memory stats", "err", err, "container", container.containerMetadata)
			continue
		}

		cpuStatsSet.Unit = CPUUsageUnit
		memoryStatsSet.Unit = MemoryUsageUnit

		containerMetrics = append(containerMetrics, ContainerMetric{
			ContainerMetadata: container.containerMetadata,
			CPUStatsSet:       cpuStatsSet,
			MemoryStatsSet:    memoryStatsSet,
		})
	}

	return containerMetrics, nil
}

// newInstanceMetadata creates the singleton metadata object.
func newInstanceMetadata(clusterArn string, containerInstanceArn string) *InstanceMetadata {
	return &InstanceMetadata{
		ClusterArn:           clusterArn,
		ContainerInstanceArn: containerInstanceArn,
	}
}
