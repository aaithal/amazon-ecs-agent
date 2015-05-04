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
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
	"github.com/aws/amazon-ecs-agent/agent/auth"
	"github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/aws/amazon-ecs-agent/agent/websocket/client/mock/utils"
)

type mockStatsEngine struct{}

func TestFormatURL(t *testing.T) {
	endpoint := "http://127.0.0.0.1/"
	wsurl := formatURL(endpoint, testClusterArn, testInstanceArn)

	parsed, err := url.Parse(wsurl)
	if err != nil {
		t.Fatal("Should be able to parse url")
	}

	if parsed.Path != "/ws" {
		t.Fatal("Wrong path")
	}

	if parsed.Query().Get("cluster") != testClusterArn {
		t.Fatal("Wrong cluster")
	}
	if parsed.Query().Get("containerInstance") != testInstanceArn {
		t.Fatal("Wrong cluster")
	}
}

func TestStartSession(t *testing.T) {
	// Set metric publish interval to 1 second.
	err := os.Setenv(publishMetricIntervalEnvVar, "1")
	if err != nil {
		t.Error("Error setting metric publish interval", err)
	}
	setPublishMetricInterval()
	if publishMetricsInterval != 1*time.Second {
		t.Error("Incorrect publishMetricsInterval")
	}

	// Start test server.
	closeWS := make(chan bool)
	server, serverChan, requestChan, serverErr, err := mockwsutils.StartMockServer(t, closeWS)
	defer server.Close()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		t.Error(<-serverErr)
	}()
	defer func() {
		closeWS <- true
		close(serverChan)
	}()

	// Start a session with the test server.
	sw := &sessionWrapper{
		acceptInvalidCert:  true,
		credentialProvider: auth.TestCredentialProvider{},
		region:             "us-east-1",
		url:                server.URL,
		statsEngine:        &mockStatsEngine{},
	}
	go sw.startSession()

	// startSession internally starts publishing metrics from the mockStatsEngine
	// object.
	time.Sleep(publishMetricsInterval + 1*time.Second)

	// Read request channel to get the metric data published to the server.
	request := <-requestChan

	// Decode and verify the metric data.
	payload, err := getPayloadFromRequest(request)
	if err != nil {
		t.Fatal("Error decoding payload: ", err)
	}

	// Decode and verify the metric data.
	_, responseType, err := wsclient.DecodeData([]byte(payload), &decoder{})
	if err != nil {
		t.Fatal("error decoding data: ", err)
	}
	if responseType != "PublishMetricsRequest" {
		t.Fatal("Unexpected responseType: ", responseType)
	}
}

func (engine *mockStatsEngine) GetInstanceMetrics() (*ecstcs.MetricsMetadata, []*ecstcs.TaskMetric, error) {
	req := createPublishMetricsRequest()
	return req.Metadata, req.TaskMetrics, nil
}
