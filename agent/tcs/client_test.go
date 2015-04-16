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
	"bytes"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"code.google.com/p/gomock/gomock"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
	mock_tcs "github.com/aws/amazon-ecs-agent/agent/tcs/mock"
	wsclient "github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/gorilla/websocket"
)

// byteBufferCloser provides an implementation of io.ReadCloser for
// http.Response.Body
type byteBufferCloser struct {
	io.Reader
}

func TestConnect(t *testing.T) {
	// Start test server.
	closeWS := make(chan bool)
	server, serverChan, requestChan, serverErr, err := startMockAcsServer(t, closeWS)
	defer server.Close()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		err = <-serverErr
		t.Fatal(err)
	}()
	defer func() {
		closeWS <- true
		close(serverChan)
	}()

	// Created websocket client.
	client := New(server.URL, nil)
	err = client.Connect()
	if err != nil {
		t.Fatal(err)
	}

	// Send payload to the server.
	go func() {
		client.MakeRequest(createPublishMetricsRequest())
	}()

	// Read request channel to get the metric data published to the server.
	request := <-requestChan

	// Decode and verify the metric data.
	_, responseType, err := wsclient.DecodeData([]byte(request), &decoder{})
	if err != nil {
		t.Fatal(err)
	}
	if responseType != "PublishMetricsRequest" {
		t.Fatal("Unexpected responseType: ", responseType)
	}
}

func TestWebsocketErrorsWithConnect(t *testing.T) {
	// Start the test server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	wsClient := mock_tcs.NewMockWebsocketClient(mockCtrl)

	// websocket.NewClient returns an error.
	wsClient.EXPECT().NewClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, fakeHTTPResponse(), errors.New("client error"))
	client := &clientServer{
		statsEngine:  nil,
		publishTimer: newTimer(publishMetricsInterval, publishMetrics),
		url:          ts.URL,
		ws:           wsClient,
	}
	client.TypeDecoder = &decoder{}

	// Connect should return an error while creating the websocket client.
	err := client.Connect()
	if err == nil {
		t.Error("Creating mock ws client succeeded, expected to fail")
	}
}

func (byteBufferCloser) Close() error { return nil }

func fakeHTTPResponse() *http.Response {
	return &http.Response{
		Body: byteBufferCloser{bytes.NewBufferString("")},
	}
}

func startMockAcsServer(t *testing.T, closeWS <-chan bool) (*httptest.Server, chan<- string, <-chan string, <-chan error, error) {
	serverChan := make(chan string)
	requestsChan := make(chan string)
	errChan := make(chan error)

	upgrader := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		go func() {
			<-closeWS
			ws.Close()
		}()
		if err != nil {
			errChan <- err
		}
		go func() {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				errChan <- err
			} else {
				requestsChan <- string(msg)
			}
		}()
		for str := range serverChan {
			err := ws.WriteMessage(websocket.TextMessage, []byte(str))
			if err != nil {
				errChan <- err
			}
		}
	})

	server := httptest.NewServer(handler)
	return server, serverChan, requestsChan, errChan, nil
}

func createPublishMetricsRequest() *ecstcs.PublishMetricsRequest {
	cluster := "default"
	ci := "ci"
	taskArn := "t1"
	unit := "unit"
	containerName := "c1"
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
							Timestamp:   &ts,
							Unit:        &unit,
						},
						MemoryStatsSet: &ecstcs.CWStatsSet{
							Max:         &fval,
							Min:         &fval,
							SampleCount: &ival,
							Sum:         &fval,
							Timestamp:   &ts,
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
	}
}
