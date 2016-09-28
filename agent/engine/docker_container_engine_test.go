// +build !integration
// Copyright 2014-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
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

package engine

import (
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/golang/mock/gomock"
	"golang.org/x/net/context"

	"github.com/aws/amazon-ecs-agent/agent/api"
	"github.com/aws/amazon-ecs-agent/agent/config"
	"github.com/aws/amazon-ecs-agent/agent/ec2"
	"github.com/aws/amazon-ecs-agent/agent/ecr/mocks"
	ecrapi "github.com/aws/amazon-ecs-agent/agent/ecr/model/ecr"
	"github.com/aws/amazon-ecs-agent/agent/engine/dockerclient"
	"github.com/aws/amazon-ecs-agent/agent/engine/dockerclient/mocks"
	"github.com/aws/amazon-ecs-agent/agent/engine/dockeriface/mocks"
	"github.com/aws/amazon-ecs-agent/agent/engine/emptyvolume"
	"github.com/aws/amazon-ecs-agent/agent/utils/ttime/mocks"
)

// xContainerShortTimeout is a short duration intended to be used by the
// docker client APIs that test if the underlying context gets canceled
// upon the expiration of the timeout duration.
const xContainerShortTimeout = 1 * time.Millisecond

func defaultTestConfig() *config.Config {
	cfg, _ := config.NewConfig(ec2.NewBlackholeEC2MetadataClient())
	return cfg
}

func dockerClientSetup(t *testing.T) (*mock_dockeriface.MockClient, *dockerGoClient, *mock_ttime.MockTime, func()) {
	return dockerClientSetupWithConfig(t, config.DefaultConfig())
}

func dockerClientSetupWithConfig(t *testing.T, conf config.Config) (*mock_dockeriface.MockClient, *dockerGoClient, *mock_ttime.MockTime, func()) {
	ctrl := gomock.NewController(t)
	mockDocker := mock_dockeriface.NewMockClient(ctrl)
	mockDocker.EXPECT().Ping().AnyTimes().Return(nil)
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().AnyTimes().Return(mockDocker, nil)
	mockTime := mock_ttime.NewMockTime(ctrl)

	conf.EngineAuthData = config.NewSensitiveRawMessage([]byte{})
	client, _ := NewDockerGoClient(factory, false, &conf)
	goClient, _ := client.(*dockerGoClient)
	ecrClientFactory := mock_ecr.NewMockECRFactory(ctrl)
	goClient.ecrClientFactory = ecrClientFactory
	goClient._time = mockTime
	return mockDocker, goClient, mockTime, ctrl.Finish
}

type pullImageOptsMatcher struct {
	image string
}

func (matcher *pullImageOptsMatcher) String() string {
	return "matches " + matcher.image
}

func (matcher *pullImageOptsMatcher) Matches(x interface{}) bool {
	return matcher.image == x.(docker.PullImageOptions).Repository
}

func TestPullImageOutputTimeout(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	pullBeginTimeout := make(chan time.Time)
	testTime.EXPECT().After(dockerPullBeginTimeout).Return(pullBeginTimeout)
	testTime.EXPECT().After(pullImageTimeout)
	wait := sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().PullImage(&pullImageOptsMatcher{"image:latest"}, gomock.Any()).Do(func(x, y interface{}) {
		pullBeginTimeout <- time.Now()
		wait.Wait()
		// Don't return, verify timeout happens
	})

	metadata := client.PullImage("image", nil)
	if metadata.Error == nil {
		t.Error("Expected error for pull timeout")
	}
	if metadata.Error.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}

	// cleanup
	wait.Done()
}

func TestPullImageGlobalTimeout(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	pullBeginTimeout := make(chan time.Time, 1)
	testTime.EXPECT().After(dockerPullBeginTimeout).Return(pullBeginTimeout)
	pullTimeout := make(chan time.Time, 1)
	testTime.EXPECT().After(pullImageTimeout).Return(pullTimeout)
	wait := sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().PullImage(&pullImageOptsMatcher{"image:latest"}, gomock.Any()).Do(func(x, y interface{}) {
		opts, ok := x.(docker.PullImageOptions)
		if !ok {
			t.Error("Cannot cast argument to PullImageOptions")
		}
		io.WriteString(opts.OutputStream, "string\n")
		pullBeginTimeout <- time.Now()
		pullTimeout <- time.Now()
		wait.Wait()
		// Don't return, verify timeout happens
	})

	metadata := client.PullImage("image", nil)
	if metadata.Error == nil {
		t.Error("Expected error for pull timeout")
	}
	if metadata.Error.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}

	testTime.EXPECT().After(dockerPullBeginTimeout)
	testTime.EXPECT().After(pullImageTimeout)
	mockDocker.EXPECT().PullImage(&pullImageOptsMatcher{"image2:latest"}, gomock.Any())
	_ = client.PullImage("image2", nil)

	// cleanup
	wait.Done()
}

func TestPullImage(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	testTime.EXPECT().After(gomock.Any()).AnyTimes()
	mockDocker.EXPECT().PullImage(&pullImageOptsMatcher{"image:latest"}, gomock.Any()).Return(nil)

	metadata := client.PullImage("image", nil)
	if metadata.Error != nil {
		t.Error("Expected pull to succeed")
	}
}

func TestPullImageTag(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	testTime.EXPECT().After(gomock.Any()).AnyTimes()
	mockDocker.EXPECT().PullImage(&pullImageOptsMatcher{"image:mytag"}, gomock.Any()).Return(nil)

	metadata := client.PullImage("image:mytag", nil)
	if metadata.Error != nil {
		t.Error("Expected pull to succeed")
	}
}

func TestPullImageDigest(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	testTime.EXPECT().After(gomock.Any()).AnyTimes()
	mockDocker.EXPECT().PullImage(
		&pullImageOptsMatcher{"image@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb"},
		gomock.Any(),
	).Return(nil)

	metadata := client.PullImage("image@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb", nil)
	if metadata.Error != nil {
		t.Error("Expected pull to succeed")
	}
}

func TestPullEmptyvolumeImage(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	// The special emptyvolume image leads to a create, not pull
	testTime.EXPECT().After(gomock.Any()).AnyTimes()
	gomock.InOrder(
		mockDocker.EXPECT().InspectImage(emptyvolume.Image+":"+emptyvolume.Tag).Return(nil, errors.New("Does not exist")),
		mockDocker.EXPECT().ImportImage(gomock.Any()).Do(func(x interface{}) {
			req := x.(docker.ImportImageOptions)
			if req.Repository != emptyvolume.Image {
				t.Fatal("Expected empty volume repository")
			}
			if req.Tag != emptyvolume.Tag {
				t.Fatal("Expected empty volume repository")
			}
		}),
	)

	metadata := client.PullImage(emptyvolume.Image+":"+emptyvolume.Tag, nil)
	if metadata.Error != nil {
		t.Error(metadata.Error)
	}
}

func TestPullExistingEmptyvolumeImage(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	// The special emptyvolume image leads to a create only if it doesn't exist
	testTime.EXPECT().After(gomock.Any()).AnyTimes()
	gomock.InOrder(
		mockDocker.EXPECT().InspectImage(emptyvolume.Image+":"+emptyvolume.Tag).Return(&docker.Image{}, nil),
	)

	metadata := client.PullImage(emptyvolume.Image+":"+emptyvolume.Tag, nil)
	if metadata.Error != nil {
		t.Error(metadata.Error)
	}
}

func TestPullImageECRSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDocker := mock_dockeriface.NewMockClient(ctrl)
	mockDocker.EXPECT().Ping().AnyTimes().Return(nil)
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().AnyTimes().Return(mockDocker, nil)
	client, _ := NewDockerGoClient(factory, false, defaultTestConfig())
	goClient, _ := client.(*dockerGoClient)
	ecrClientFactory := mock_ecr.NewMockECRFactory(ctrl)
	ecrClient := mock_ecr.NewMockECRClient(ctrl)
	mockTime := mock_ttime.NewMockTime(ctrl)
	goClient.ecrClientFactory = ecrClientFactory
	goClient._time = mockTime

	mockTime.EXPECT().After(gomock.Any()).AnyTimes()

	registryId := "123456789012"
	region := "eu-west-1"
	endpointOverride := "my.endpoint"
	authData := &api.RegistryAuthenticationData{
		Type: "ecr",
		ECRAuthData: &api.ECRAuthData{
			RegistryId:       registryId,
			Region:           region,
			EndpointOverride: endpointOverride,
		},
	}
	imageEndpoint := "registry.endpoint"
	image := imageEndpoint + "/myimage:tag"
	username := "username"
	password := "password"
	dockerAuthConfiguration := docker.AuthConfiguration{
		Username:      username,
		Password:      password,
		ServerAddress: "https://" + imageEndpoint,
	}

	ecrClientFactory.EXPECT().GetClient(region, endpointOverride).Return(ecrClient)
	ecrClient.EXPECT().GetAuthorizationToken(registryId).Return(
		&ecrapi.AuthorizationData{
			ProxyEndpoint:      aws.String("https://" + imageEndpoint),
			AuthorizationToken: aws.String(base64.StdEncoding.EncodeToString([]byte(username + ":" + password))),
		}, nil)

	mockDocker.EXPECT().PullImage(
		&pullImageOptsMatcher{image},
		dockerAuthConfiguration,
	).Return(nil)

	metadata := client.PullImage(image, authData)
	if metadata.Error != nil {
		t.Error("Expected pull to succeed")
	}
}

func TestPullImageECRAuthFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDocker := mock_dockeriface.NewMockClient(ctrl)
	mockDocker.EXPECT().Ping().AnyTimes().Return(nil)
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().AnyTimes().Return(mockDocker, nil)
	client, _ := NewDockerGoClient(factory, false, defaultTestConfig())
	goClient, _ := client.(*dockerGoClient)
	ecrClientFactory := mock_ecr.NewMockECRFactory(ctrl)
	ecrClient := mock_ecr.NewMockECRClient(ctrl)
	mockTime := mock_ttime.NewMockTime(ctrl)
	goClient.ecrClientFactory = ecrClientFactory
	goClient._time = mockTime

	mockTime.EXPECT().After(gomock.Any()).AnyTimes()

	registryId := "123456789012"
	region := "eu-west-1"
	endpointOverride := "my.endpoint"
	authData := &api.RegistryAuthenticationData{
		Type: "ecr",
		ECRAuthData: &api.ECRAuthData{
			RegistryId:       registryId,
			Region:           region,
			EndpointOverride: endpointOverride,
		},
	}
	imageEndpoint := "registry.endpoint"
	image := imageEndpoint + "/myimage:tag"

	ecrClientFactory.EXPECT().GetClient(region, endpointOverride).Return(ecrClient)
	ecrClient.EXPECT().GetAuthorizationToken(gomock.Any()).Return(nil, errors.New("test error"))

	metadata := client.PullImage(image, authData)
	if metadata.Error == nil {
		t.Error("Expected pull to fail")
	}
}

func TestCreateContainerTimeout(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	warp := make(chan time.Time)
	wait := &sync.WaitGroup{}
	wait.Add(1)
	config := docker.CreateContainerOptions{Config: &docker.Config{Memory: 100}, Name: "containerName"}
	mockDocker.EXPECT().CreateContainer(gomock.Any()).Do(func(x interface{}) {
		warp <- time.Now()
		wait.Wait()
		// Don't return, verify timeout happens
	})
	metadata := client.CreateContainer(config.Config, nil, config.Name, xContainerShortTimeout)
	if metadata.Error == nil {
		t.Error("Expected error for pull timeout")
	}
	if metadata.Error.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}
	wait.Done()
}

func TestCreateContainerInspectTimeout(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	wait := &sync.WaitGroup{}
	wait.Add(1)
	config := docker.CreateContainerOptions{Config: &docker.Config{Memory: 100}, Name: "containerName"}
	gomock.InOrder(
		mockDocker.EXPECT().CreateContainer(gomock.Any()).Do(func(opts docker.CreateContainerOptions) {
			if !reflect.DeepEqual(opts.Config, config.Config) {
				t.Errorf("Mismatch in create container config, %v != %v", opts.Config, config.Config)
			}
			if opts.Name != config.Name {
				t.Errorf("Mismatch in create container options, %s != %s", opts.Name, config.Name)
			}
		}).Return(&docker.Container{ID: "id"}, nil),
		mockDocker.EXPECT().InspectContainerWithContext("id", gomock.Any()).Return(nil, &DockerTimeoutError{}),
	)
	metadata := client.CreateContainer(config.Config, nil, config.Name, 1*time.Second)
	if metadata.DockerId != "id" {
		t.Error("Expected ID to be set even if inspect failed; was " + metadata.DockerId)
	}
	if metadata.Error == nil {
		t.Error("Expected error for inspect timeout")
	}
	wait.Done()
}

func TestCreateContainer(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	config := docker.CreateContainerOptions{Config: &docker.Config{Memory: 100}, Name: "containerName"}
	gomock.InOrder(
		mockDocker.EXPECT().CreateContainer(gomock.Any()).Do(func(opts docker.CreateContainerOptions) {
			if !reflect.DeepEqual(opts.Config, config.Config) {
				t.Errorf("Mismatch in create container config, %v != %v", opts.Config, config.Config)
			}
			if opts.Name != config.Name {
				t.Errorf("Mismatch in create container options, %s != %s", opts.Name, config.Name)
			}
		}).Return(&docker.Container{ID: "id"}, nil),
		mockDocker.EXPECT().InspectContainerWithContext("id", gomock.Any()).Return(&docker.Container{ID: "id"}, nil),
	)
	metadata := client.CreateContainer(config.Config, nil, config.Name, 1*time.Second)
	if metadata.Error != nil {
		t.Error("Did not expect error")
	}
	if metadata.DockerId != "id" {
		t.Error("Wrong id")
	}
	if metadata.ExitCode != nil {
		t.Error("Expected a created container to not have an exit code")
	}
}

func TestStartContainerTimeout(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	wait := &sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().StartContainerWithContext("id", nil, gomock.Any()).Do(func(x, y, z interface{}) {
		wait.Wait()
		// Don't return, verify timeout happens
	})
	metadata := client.StartContainer("id", xContainerShortTimeout)
	if metadata.Error == nil {
		t.Error("Expected error for pull timeout")
	}
	if metadata.Error.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}
	wait.Done()
}

func TestStartContainer(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	gomock.InOrder(
		mockDocker.EXPECT().StartContainerWithContext("id", nil, gomock.Any()).Return(nil),
		mockDocker.EXPECT().InspectContainerWithContext("id", gomock.Any()).Return(&docker.Container{ID: "id"}, nil),
	)
	metadata := client.StartContainer("id", startContainerTimeout)
	if metadata.Error != nil {
		t.Error("Did not expect error")
	}
	if metadata.DockerId != "id" {
		t.Error("Wrong id")
	}
}

func TestStopContainerTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DockerStopTimeout = xContainerShortTimeout
	mockDocker, client, _, done := dockerClientSetupWithConfig(t, cfg)
	defer done()

	warp := make(chan time.Time)
	wait := &sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().StopContainerWithContext("id", uint(client.config.DockerStopTimeout/time.Second), gomock.Any()).Do(func(x, y, z interface{}) {
		warp <- time.Now()
		wait.Wait()
		// Don't return, verify timeout happens
	})
	metadata := client.StopContainer("id", xContainerShortTimeout)
	if metadata.Error == nil {
		t.Error("Expected error for pull timeout")
	}
	if metadata.Error.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}
	wait.Done()
}

func TestStopContainer(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	gomock.InOrder(
		mockDocker.EXPECT().StopContainerWithContext("id", uint(client.config.DockerStopTimeout/time.Second), gomock.Any()).Return(nil),
		mockDocker.EXPECT().InspectContainerWithContext("id", gomock.Any()).Return(&docker.Container{ID: "id", State: docker.State{ExitCode: 10}}, nil),
	)
	metadata := client.StopContainer("id", stopContainerTimeout)
	if metadata.Error != nil {
		t.Error("Did not expect error")
	}
	if metadata.DockerId != "id" {
		t.Error("Wrong id")
	}
}

func TestInspectContainerTimeout(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	warp := make(chan time.Time)
	wait := &sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().InspectContainerWithContext("id", gomock.Any()).Do(func(x, ctx interface{}) {
		warp <- time.Now()
		wait.Wait()
		// Don't return, verify timeout happens
	})
	_, err := client.InspectContainer("id", xContainerShortTimeout)
	if err == nil {
		t.Error("Expected error for inspect timeout")
	}
	if err.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}
	wait.Done()
}

func TestInspectContainer(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	containerOutput := docker.Container{ID: "id", State: docker.State{ExitCode: 10}}
	gomock.InOrder(
		mockDocker.EXPECT().InspectContainerWithContext("id", gomock.Any()).Return(&containerOutput, nil),
	)
	container, err := client.InspectContainer("id", inspectContainerTimeout)
	if err != nil {
		t.Error("Did not expect error")
	}
	if !reflect.DeepEqual(&containerOutput, container) {
		t.Fatal("Did not match expected output")
	}
}

func TestContainerEvents(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	var events chan<- *docker.APIEvents
	mockDocker.EXPECT().AddEventListener(gomock.Any()).Do(func(x interface{}) {
		events = x.(chan<- *docker.APIEvents)
	})

	dockerEvents, err := client.ContainerEvents(context.TODO())
	if err != nil {
		t.Fatal("Could not get container events")
	}

	mockDocker.EXPECT().InspectContainerWithContext("containerId", gomock.Any()).Return(
		&docker.Container{
			ID: "containerId",
		},
		nil)
	go func() {
		events <- &docker.APIEvents{Type: "container", ID: "containerId", Status: "create"}
	}()

	event := <-dockerEvents
	if event.DockerId != "containerId" {
		t.Error("Wrong docker id")
	}
	if event.Status != api.ContainerCreated {
		t.Error("Wrong status")
	}

	container := &docker.Container{
		ID: "cid2",
		NetworkSettings: &docker.NetworkSettings{
			Ports: map[docker.Port][]docker.PortBinding{
				"80/tcp": []docker.PortBinding{docker.PortBinding{HostPort: "9001"}},
			},
		},
		Volumes: map[string]string{"/host/path": "/container/path"},
	}
	mockDocker.EXPECT().InspectContainerWithContext("cid2", gomock.Any()).Return(container, nil)
	go func() {
		events <- &docker.APIEvents{Type: "container", ID: "cid2", Status: "start"}
	}()
	event = <-dockerEvents
	if event.DockerId != "cid2" {
		t.Error("Wrong docker id")
	}
	if event.Status != api.ContainerRunning {
		t.Error("Wrong status")
	}
	if event.PortBindings[0].ContainerPort != 80 || event.PortBindings[0].HostPort != 9001 {
		t.Error("Incorrect port bindings")
	}
	if event.Volumes["/host/path"] != "/container/path" {
		t.Error("Incorrect volume mapping")
	}

	for i := 0; i < 2; i++ {
		stoppedContainer := &docker.Container{
			ID: "cid3" + strconv.Itoa(i),
			State: docker.State{
				FinishedAt: time.Now(),
				ExitCode:   20,
			},
		}
		mockDocker.EXPECT().InspectContainerWithContext("cid3"+strconv.Itoa(i), gomock.Any()).Return(stoppedContainer, nil)
	}
	go func() {
		events <- &docker.APIEvents{Type: "container", ID: "cid30", Status: "stop"}
		events <- &docker.APIEvents{Type: "container", ID: "cid31", Status: "die"}
	}()

	for i := 0; i < 2; i++ {
		anEvent := <-dockerEvents
		if anEvent.DockerId != "cid3"+strconv.Itoa(i) {
			t.Error("Wrong container id: " + anEvent.DockerId)
		}
		if anEvent.Status != api.ContainerStopped {
			t.Error("Should be stopped")
		}
		if *anEvent.ExitCode != 20 {
			t.Error("Incorrect exit code")
		}
	}

	// Verify the following events do not translate into our event stream

	//
	// Docker 1.8.3 sends the full command appended to exec_create and exec_start
	// events. Test that we ignore there as well..
	//
	ignore := []string{
		"pause",
		"exec_create",
		"exec_create: /bin/bash",
		"exec_start",
		"exec_start: /bin/bash",
		"top",
		"attach",
		"export",
		"pull",
		"push",
		"tag",
		"untag",
		"import",
		"delete",
		"oom",
		"kill",
	}
	for _, eventStatus := range ignore {
		events <- &docker.APIEvents{Type: "container", ID: "123", Status: eventStatus}
		select {
		case <-dockerEvents:
			t.Error("No event should be available for " + eventStatus)
		default:
		}
	}

	// Verify only the container type event will translate to our event stream
	// Events type: network, image, volume, daemon, plugins won't be handled
	ignoreEventType := map[string]string{
		"network": "connect",
		"image":   "pull",
		"volume":  "create",
		"plugin":  "install",
		"daemon":  "reload",
	}

	for eventType, eventStatus := range ignoreEventType {
		events <- &docker.APIEvents{Type: eventType, ID: "123", Status: eventStatus}
		select {
		case <-dockerEvents:
			t.Error("No event should be available for %v", eventType)
		default:
		}
	}
}

func TestDockerVersion(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	mockDocker.EXPECT().Version().Return(&docker.Env{"Version=1.6.0"}, nil)

	str, err := client.Version()
	if err != nil {
		t.Error(err)
	}
	if str != "DockerVersion: 1.6.0" {
		t.Error("Got unexpected version string: " + str)
	}
}

func TestListContainers(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	containers := []docker.APIContainers{docker.APIContainers{ID: "id"}}
	mockDocker.EXPECT().ListContainers(gomock.Any()).Return(containers, nil)
	response := client.ListContainers(true, ListContainersTimeout)
	if response.Error != nil {
		t.Error("Did not expect error")
	}

	containerIds := response.DockerIds
	if len(containerIds) != 1 {
		t.Error("Unexpected number of containers in list: ", len(containerIds))
	}

	if containerIds[0] != "id" {
		t.Error("Unexpected container id in the list: ", containerIds[0])
	}
}

func TestListContainersTimeout(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()

	warp := make(chan time.Time)
	wait := &sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().ListContainers(gomock.Any()).Do(func(x interface{}) {
		warp <- time.Now()
		wait.Wait()
		// Don't return, verify timeout happens
	})
	response := client.ListContainers(true, xContainerShortTimeout)
	if response.Error == nil {
		t.Error("Expected error for pull timeout")
	}
	if response.Error.(api.NamedError).ErrorName() != "DockerTimeoutError" {
		t.Error("Wrong error type")
	}
	wait.Done()
}

func TestPingFailError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDocker := mock_dockeriface.NewMockClient(ctrl)
	mockDocker.EXPECT().Ping().Return(errors.New("err"))
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().Return(mockDocker, nil)
	_, err := NewDockerGoClient(factory, false, defaultTestConfig())
	if err == nil {
		t.Fatal("Expected ping error to result in constructor fail")
	}
}

func TestUsesVersionedClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDocker := mock_dockeriface.NewMockClient(ctrl)
	mockDocker.EXPECT().Ping().Return(nil)
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().Return(mockDocker, nil)
	client, err := NewDockerGoClient(factory, false, defaultTestConfig())
	if err != nil {
		t.Fatal(err)
	}

	vclient := client.WithVersion(dockerclient.DockerVersion("1.20"))

	factory.EXPECT().GetClient(dockerclient.DockerVersion("1.20")).Times(2).Return(mockDocker, nil)
	mockDocker.EXPECT().StartContainerWithContext(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockDocker.EXPECT().InspectContainerWithContext(gomock.Any(), gomock.Any()).Return(nil, errors.New("err"))

	vclient.StartContainer("foo", startContainerTimeout)
}

func TestUnavailableVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDocker := mock_dockeriface.NewMockClient(ctrl)
	mockDocker.EXPECT().Ping().Return(nil)
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().Return(mockDocker, nil)
	client, err := NewDockerGoClient(factory, false, defaultTestConfig())
	if err != nil {
		t.Fatal(err)
	}

	vclient := client.WithVersion(dockerclient.DockerVersion("1.21"))

	factory.EXPECT().GetClient(dockerclient.DockerVersion("1.21")).Times(1).Return(nil, errors.New("Cannot get client"))

	metadata := vclient.StartContainer("foo", startContainerTimeout)

	if metadata.Error == nil {
		t.Fatal("Expected error, didn't get one")
	}
	if namederr, ok := metadata.Error.(api.NamedError); ok {
		if namederr.ErrorName() != "CannotGetDockerclientError" {
			t.Fatal("Wrong error name, expected CannotGetDockerclientError but got " + namederr.ErrorName())
		}
	} else {
		t.Fatal("Error was not a named error")
	}
}

func TestStatsNormalExit(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()
	time1 := time.Now()
	time2 := time1.Add(1 * time.Second)
	mockDocker.EXPECT().Stats(gomock.Any()).Do(func(x interface{}) {
		opts := x.(docker.StatsOptions)
		defer close(opts.Stats)
		if opts.ID != "foo" {
			t.Fatalf("Expected ID foo, got %s", opts.ID)
		}
		if opts.Stream != true {
			t.Fatal("Expected stream to be true")
		}
		opts.Stats <- &docker.Stats{
			Read: time1,
		}
		opts.Stats <- &docker.Stats{
			Read: time2,
		}
	})
	ctx := context.TODO()
	stats, err := client.Stats("foo", ctx)
	if err != nil {
		t.Fatal(err)
	}
	stat := <-stats
	checkStatRead(t, stat, time1)
	stat = <-stats
	checkStatRead(t, stat, time2)
	stat = <-stats
	if stat != nil {
		t.Fatal("Expected stat to be nil")
	}
}

func checkStatRead(t *testing.T, stat *docker.Stats, read time.Time) {
	if stat.Read != read {
		t.Fatalf("Expected %v, but was %v", read, stat.Read)
	}
}

func TestStatsClosed(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()
	time1 := time.Now()
	mockDocker.EXPECT().Stats(gomock.Any()).Do(func(x interface{}) {
		opts := x.(docker.StatsOptions)
		defer close(opts.Stats)
		if opts.ID != "foo" {
			t.Fatalf("Expected ID foo, got %s", opts.ID)
		}
		if opts.Stream != true {
			t.Fatal("Expected stream to be true")
		}
		for i := 0; true; i++ {
			select {
			case <-opts.Context.Done():
				t.Logf("Received cancel after %d iterations", i)
				return
			default:
				opts.Stats <- &docker.Stats{
					Read: time1.Add(time.Duration(i) * time.Second),
				}
			}
		}
	})
	ctx, cancel := context.WithCancel(context.TODO())
	stats, err := client.Stats("foo", ctx)
	if err != nil {
		t.Fatal(err)
	}
	stat := <-stats
	checkStatRead(t, stat, time1)
	stat = <-stats
	checkStatRead(t, stat, time1.Add(time.Second))
	cancel()
	// drain
	for {
		stat = <-stats
		if stat == nil {
			break
		}
	}
}

func TestStatsErrorReading(t *testing.T) {
	mockDocker, client, _, done := dockerClientSetup(t)
	defer done()
	mockDocker.EXPECT().Stats(gomock.Any()).Do(func(x interface{}) error {
		opts := x.(docker.StatsOptions)
		close(opts.Stats)
		return errors.New("test error")
	})
	ctx := context.TODO()
	stats, err := client.Stats("foo", ctx)
	if err != nil {
		t.Fatal(err)
	}
	stat := <-stats
	if stat != nil {
		t.Fatal("Expected stat to be nil")
	}
}

func TestStatsClientError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	factory := mock_dockerclient.NewMockFactory(ctrl)
	factory.EXPECT().GetDefaultClient().Return(nil, errors.New("No client"))
	client := &dockerGoClient{
		clientFactory: factory,
	}
	ctx := context.TODO()
	_, err := client.Stats("foo", ctx)
	if err == nil {
		t.Fatal("Expected error with nil docker client")
	}
}

func TestRemoveImageTimeout(t *testing.T) {
	mockDocker, client, _, _ := dockerClientSetup(t)
	wait := sync.WaitGroup{}
	wait.Add(1)
	mockDocker.EXPECT().RemoveImage("image").Do(func(x interface{}) {
		wait.Wait()
	})
	err := client.RemoveImage("image", 2*time.Millisecond)
	if err == nil {
		t.Errorf("Expected error for remove image timeout")
	}
	wait.Done()
}

func TestRemoveImage(t *testing.T) {
	mockDocker, client, testTime, done := dockerClientSetup(t)
	defer done()

	testTime.EXPECT().After(gomock.Any()).AnyTimes()
	mockDocker.EXPECT().RemoveImage("image").Return(nil)
	err := client.RemoveImage("image", 2*time.Millisecond)
	if err != nil {
		t.Errorf("Did not expect error")
	}
}
