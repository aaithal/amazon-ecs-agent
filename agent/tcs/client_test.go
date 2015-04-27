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
	"math/rand"
	"testing"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
	"github.com/aws/amazon-ecs-agent/agent/auth"
	"github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/aws/amazon-ecs-agent/agent/websocket/client/mock/utils"
)

const (
	testClusterArn    = "arn:aws:ecs:us-east-1:123:cluster/default"
	testInstanceArn   = "arn:aws:ecs:us-east-1:123:container-instance/abc"
	testTaskArn       = "arn:aws:ecs:us-east-1:123:task/def"
	testContainerName = "c1"
	testUnit          = "Percent"
)

func createPublishMetricsRequest() *ecstcs.PublishMetricsRequest {
	cluster := testClusterArn
	ci := testInstanceArn
	taskArn := testTaskArn
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
				TaskArn: &taskArn,
			},
		},
		Timestamp: &ts,
	}
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
	_, responseType, err := wsclient.DecodeData([]byte(request), &decoder{})
	if err != nil {
		t.Fatal(err)
	}
	if responseType != "PublishMetricsRequest" {
		t.Fatal("Unexpected responseType: ", responseType)
	}
	closeWS <- true
	close(serverChan)
}
