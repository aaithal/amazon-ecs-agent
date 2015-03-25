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
	"golang.org/x/net/context"
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
}

// DockerContainerMetadataResolver implements ContainerMetadataResolver for
// DockerTaskEngine.
type DockerContainerMetadataResolver struct {
	dockerTaskEngine *ecsengine.DockerTaskEngine
}

// DockerStatsEngine is used to monitor docker container events and to report
// utlization metrics of the same.
type DockerStatsEngine struct {
	cancel              context.CancelFunc
	client              ecsengine.DockerClient
	containers          map[string]*CronContainer
	containersLock      sync.RWMutex
	ctx                 context.Context
	dockerEventListener chan *docker.APIEvents
	events              <-chan ecsengine.DockerContainerChangeEvent
	instanceMetadata    *InstanceMetadata
	resolver            ContainerMetadataResolver
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

// NewDockerStatsEngine creates a new instance of the DockerStatsEngine object.
// MustInit() must be called to initialize the fields of the new event listener.
func NewDockerStatsEngine() *DockerStatsEngine {
	if dockerStatsEngine == nil {
		dockerStatsEngine = &DockerStatsEngine{
			client:     nil,
			resolver:   nil,
			containers: make(map[string]*CronContainer),
		}
	}

	return dockerStatsEngine
}

// MustInit initializes fields of the DockerStatsEngine object.
func (engine *DockerStatsEngine) MustInit(taskEngine ecsengine.TaskEngine, clusterArn string, containerInstanceArn string) error {
	log.Info("Initializing stats engine")
	err := engine.initDockerClient()
	if err != nil {
		return err
	}

	engine.instanceMetadata = newInstanceMetadata(clusterArn, containerInstanceArn)

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

	// Create a context to handle errors from the goroutine.
	// Canceling the context would remove the event listener as well.
	engine.ctx, engine.cancel = context.WithCancel(context.Background())
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
		// Cancel the context to cleanly stop the goroutine.
		engine.cancel()
	}

	go engine.handleDockerEvents()
}

// AddContainer adds a container to the map of containers being watched.
// It also starts the periodic usage data collection for the container.
func (engine *DockerStatsEngine) AddContainer(dockerID string) {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	_, exists := engine.containers[dockerID]
	if exists {
		log.Info("Container already being watched, ignoring", "id", dockerID)
		return
	}

	task, err := engine.resolver.ResolveTask(dockerID)
	if err != nil {
		log.Info("Could not map container to task, ignoring", "err", err, "id", dockerID)
		return
	}

	if task.KnownStatus.Terminal() {
		log.Info("Task is terminal, ignoring", "id", dockerID)
		return
	}

	// Watch the container only if it could be mapped to a task
	// and if the task is active.
	log.Debug("Adding container to stats watch list", "id", dockerID, "task", task.Arn)
	container := newCronContainer(dockerID, task.Arn)
	engine.containers[dockerID] = container
	container.StartStatsCron()
}

// RemoveContainer deletes the container from the map of containers being watched.
// It also stops the periodic usage data collection for the container.
func (engine *DockerStatsEngine) RemoveContainer(dockerID string) {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	container, exists := engine.containers[dockerID]
	if !exists {
		log.Debug("Container not being watched", "id", dockerID)
		return
	}

	container.StopStatsCron()
	delete(engine.containers, dockerID)
}

// GetContainersStatsSet returns stats-set object for all containers being watched.
func (engine *DockerStatsEngine) GetContainersStatsSet() []ContainerStatsSet {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	containersStats := make([]ContainerStatsSet, 0, len(engine.containers))
	for _, container := range engine.containers {
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

		// Create and append the ContainerStatsSet object.
		statsSet := ContainerStatsSet{
			InstanceMetadata:  engine.instanceMetadata,
			ContainerMetadata: container.containerMetadata,
			CPUStatsSet:       cpuStatsSet,
			MemoryStatsSet:    memoryStatsSet,
		}
		containersStats = append(containersStats, statsSet)
	}
	return containersStats
}

// GetContainersRawUsageStats returns raw usage stats for all containers being watched.
func (engine *DockerStatsEngine) GetContainersRawUsageStats(numStats int) []ContainerRawUsageStats {
	engine.containersLock.Lock()
	defer engine.containersLock.Unlock()

	containersRawUsageStats := make([]ContainerRawUsageStats, 0, len(engine.containers))
	for _, container := range engine.containers {
		usageStats, err := container.statsQueue.GetRawUsageStats(numStats)
		if err != nil {
			log.Warn("Error getting usage stats", "err", err, "container", container.containerMetadata)
			continue
		}

		containerRawUsageStat := ContainerRawUsageStats{
			InstanceMetadata:  engine.instanceMetadata,
			ContainerMetadata: container.containerMetadata,
			UsageStats:        usageStats,
		}
		containersRawUsageStats = append(containersRawUsageStats, containerRawUsageStat)
	}

	return containersRawUsageStats
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
		// engine.manager.AddContainer(containerID)
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
	for {
		select {
		case <-engine.ctx.Done():
			log.Info("stats engine context done. returning")
			// The context gets canceled when the docker client returns unexpected
			// error while listing containers.
			err := engine.client.UnsubscribeContainerEvents(engine.dockerEventListener)
			if err != nil {
				log.Warn("Error unsubscribing docker event listener")
			}
			// Reset event listener to indicate that it has benn unsubscribed.
			engine.dockerEventListener = nil
			return
		default:
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
	}
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

// newInstanceMetadata creates the singleton metadata object.
func newInstanceMetadata(clusterArn string, containerInstanceArn string) *InstanceMetadata {
	return &InstanceMetadata{
		ClusterArn:           clusterArn,
		ContainerInstanceArn: containerInstanceArn,
	}
}

// setMetricCollectionFlag reads the ECS_DISABLE_METRICS env variable and
// sets the isMetricCollectionDisabled flag appropriately.
func setMetricCollectionFlag() {
	disableStatsEnvVarValue := utils.DefaultIfBlank(os.Getenv(DisableStatsEnvVar), DefaultDisableStatsEnvVarValue)
	// Ignore any errors in parsing.
	isMetricCollectionDisabled, _ = strconv.ParseBool(disableStatsEnvVarValue)
}
