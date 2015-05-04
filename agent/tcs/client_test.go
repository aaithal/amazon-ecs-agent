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

// Package acsclient wraps the generated aws-sdk-go client to provide marshalling
// and unmarshalling of data over a websocket connection in the format expected
// by ACS. It allows for bidirectional communication and acts as both a
// client-and-server in terms of requests, but only as a client in terms of
// connecting.

package tcs

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
	"github.com/aws/amazon-ecs-agent/agent/auth"
	"github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/aws/amazon-ecs-agent/agent/websocket/client/mock/utils"
	"github.com/gorilla/websocket"
)

const (
	testClusterArn           = "arn:aws:ecs:us-east-1:123:cluster/default"
	testInstanceArn          = "arn:aws:ecs:us-east-1:123:container-instance/abc"
	testTaskArn              = "arn:aws:ecs:us-east-1:123:task/def"
	testTaskDefinitionFamily = "task-def"
	testContainerName        = "c1"
	testUnit                 = "Percent"
)

type messageLogger struct {
	writes [][]byte
	reads  [][]byte
	closed bool
}

func (ml *messageLogger) WriteMessage(_ int, data []byte) error {
	if ml.closed {
		return errors.New("can't write to closed ws")
	}
	ml.writes = append(ml.writes, data)
	return nil
}

func (ml *messageLogger) Close() error {
	ml.closed = true
	return nil
}

func (ml *messageLogger) ReadMessage() (int, []byte, error) {
	for len(ml.reads) == 0 && !ml.closed {
		time.Sleep(1 * time.Millisecond)
	}
	if ml.closed {
		return 0, []byte{}, errors.New("can't read from a closed websocket")
	}
	read := ml.reads[len(ml.reads)-1]
	ml.reads = ml.reads[0 : len(ml.reads)-1]
	return websocket.TextMessage, read, nil
}

func TestConnectPublishMetricsRequest(t *testing.T) {
	closeWS := make(chan bool)
	server, serverChan, requestChan, serverErr, err := mockwsutils.StartMockServer(t, closeWS)
	defer server.Close()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		t.Fatal(<-serverErr)
	}()

	cs := New(server.URL, "us-east-1", auth.TestCredentialProvider{}, true, nil)
	// Wait for up to a second for the mock server to launch
	for i := 0; i < 100; i++ {
		err = cs.Connect()
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = cs.Serve()
	}()

	go func() {
		cs.MakeRequest(createPublishMetricsRequest())
	}()

	request := <-requestChan

	// Decode and verify the metric data.
	payload, err := getPayloadFromRequest(request)
	if err != nil {
		t.Fatal("Error decoding payload: ", err)
	}

	_, responseType, err := wsclient.DecodeData([]byte(payload), &decoder{})
	if err != nil {
		t.Fatal("error decoding data: ", err)
	}
	if responseType != "PublishMetricsRequest" {
		t.Fatal("Unexpected responseType: ", responseType)
	}
	closeWS <- true
	close(serverChan)
}

func TestPublishMetricsRequest(t *testing.T) {
	cs, _ := testCS()
	err := cs.MakeRequest(createPublishMetricsRequest())
	if err != nil {
		t.Fatal(err)
	}

	//cs.Close()
}

func createPublishMetricsRequest() *ecstcs.PublishMetricsRequest {
	cluster := testClusterArn
	ci := testInstanceArn
	taskArn := testTaskArn
	taskDefinitionFamily := testTaskDefinitionFamily
	unit := testUnit
	containerName := testContainerName
	var fval float64
	fval = rand.Float64()
	var ival int64
	ival = rand.Int63n(10)
	ts := time.Now()
	return &ecstcs.PublishMetricsRequest{
		Metadata: &ecstcs.MetricsMetadata{
			Cluster:           &cluster,
			ContainerInstance: &ci,
		},
		TaskMetrics: []*ecstcs.TaskMetric{
			&ecstcs.TaskMetric{
				ContainerMetrics: []*ecstcs.ContainerMetric{
					&ecstcs.ContainerMetric{
						CpuStatsSet: &ecstcs.CWStatsSet{
							Max:         &fval,
							Min:         &fval,
							SampleCount: &ival,
							Sum:         &fval,
							Unit:        &unit,
						},
						MemoryStatsSet: &ecstcs.CWStatsSet{
							Max:         &fval,
							Min:         &fval,
							SampleCount: &ival,
							Sum:         &fval,
							Unit:        &unit,
						},
						Metadata: &ecstcs.ContainerMetadata{
							Name: &containerName,
						},
					},
				},
				TaskArn:              &taskArn,
				TaskDefinitionFamily: &taskDefinitionFamily,
			},
		},
		Timestamp: &ts,
	}
}

func testCS() (wsclient.ClientServer, *messageLogger) {
	testCreds := auth.TestCredentialProvider{}
	cs := New("localhost:443", "us-east-1", testCreds, true, nil).(*clientServer)
	ml := &messageLogger{make([][]byte, 0), make([][]byte, 0), false}
	cs.Conn = ml
	return cs, ml
}

func getPayloadFromRequest(request string) (string, error) {
	lines := strings.Split(request, "\r\n")
	if len(lines) > 0 {
		return lines[len(lines)-1], nil
	}

	return "", errors.New("Could not get payload")
}
