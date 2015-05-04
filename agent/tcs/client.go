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

package tcs

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
	"github.com/aws/amazon-ecs-agent/agent/ecs_client/authv4"
	"github.com/aws/amazon-ecs-agent/agent/ecs_client/authv4/credentials"
	"github.com/aws/amazon-ecs-agent/agent/logger"
	"github.com/aws/amazon-ecs-agent/agent/stats"
	wsclient "github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/gorilla/websocket"
)

const (
	// defaultPort specifies the default port to connect to the backend.
	defaultPort = "443"

	// defaultPublishMetricsInterval is the interval at which utilization
	// metrics in stats engine are published to the backend.
	defaultPublishMetricsInterval = 30 * time.Second

	// portEnvVar specifies the port to connect to the backend. This is
	// present only for testing and should be deleted in a future commit.
	portEnvVar = "ECS_METRICS_BACKEND_PORT"

	// publishMetricIntervalEnvVar specifies the env variable name to be
	// set to specify the publish interval. This is present only for testing
	// and should be deleted in a future commit.
	publishMetricIntervalEnvVar = "ECS_METRICS_PUBLISH_INTERVAL"

	// tcsConnectTimeout specifies the default connection timeout to the backend.
	tcsConnectTimeout = 3 * time.Second
)

var log = logger.ForModule("tcs client")

// tcsPublishMetricInterval is used to get the inerval to publish metrics to
// the backend. This is present only for testing and should be deleted in a
// future commit.
// TODO: Disable the initialization from the environment variable for this.
var publishMetricsInterval time.Duration

// clientServer implements wsclient.ClientServer interface for metrics backend.
type clientServer struct {
	statsEngine  stats.Engine
	publishTimer *timer
	wsclient.ClientServerImpl
	signer authv4.HttpSigner
}

// New returns a client/server to bidirectionally communicate with the backend.
// The returned struct should have both 'Connect' and 'Serve' called upon it
// before being used.
func New(url string, region string, credentialProvider credentials.AWSCredentialProvider, acceptInvalidCert bool, statsEngine stats.Engine) wsclient.ClientServer {
	cs := &clientServer{
		statsEngine:  statsEngine,
		publishTimer: newTimer(publishMetricsInterval, publishMetrics),
		signer:       authv4.NewHttpSigner(region, wsclient.ServiceName, credentialProvider, nil),
	}
	cs.URL = url
	cs.Region = region
	cs.CredentialProvider = credentialProvider
	cs.AcceptInvalidCert = acceptInvalidCert
	cs.ServiceError = &tcsError{}
	cs.TypeMappings = &typeMappings{}
	cs.RequestHandlers = make(map[string]wsclient.RequestHandler)
	cs.TypeDecoder = &decoder{}
	return cs
}

// Serve begins serving requests using previously registered handlers (see
// AddRequestHandler). All request handlers should be added prior to making this
// call as unhandled requests will be discarded.
func (cs *clientServer) Serve() error {
	log.Debug("Starting websocket poll loop")
	if cs.Conn == nil {
		return errors.New("nil connection")
	}

	// Start the timer function to publish metrics to the backend.
	if cs.statsEngine != nil {
		cs.publishTimer.start(cs)
	}

	return cs.ConsumeMessages()
}

// MakeRequest makes a request using the given input. Note, the input *MUST* be
// a pointer to a valid backend type that this client recognises
func (cs *clientServer) MakeRequest(input interface{}) error {
	send, err := cs.CreateRequestMessage(input)
	if err != nil {
		return err
	}

	signer := authv4.NewHttpSigner(cs.Region, "ecs", cs.CredentialProvider, nil)
	// NewRequest never returns an error if the url parses and we just verified
	// it did above
	reqBody := bytes.NewBuffer(send)
	request, _ := http.NewRequest("GET", cs.URL, reqBody)
	signer.SignHttpRequest(request)

	var data []byte
	for k, vs := range request.Header {
		for _, v := range vs {
			data = append(data, k...)
			data = append(data, ": "...)
			data = append(data, v...)
			data = append(data, "\r\n"...)
		}
	}
	data = append(data, "\r\n"...)
	data = append(data, send...)
	// Over the wire we send something like
	// {"type":"AckRequest","message":{"messageId":"xyz"}}
	// return cs.Conn.WriteMessage(websocket.TextMessage, send)
	return cs.Conn.WriteMessage(websocket.TextMessage, data)
}

// Close closes the underlying connection.
func (cs *clientServer) Close() error {
	cs.publishTimer.stop()
	return cs.Conn.Close()
}

func init() {
	// TODO: Delete me! Refer comments for and publishMetricsInterval.
	setPublishMetricInterval()
}

// setPublishMetricInterval sets the publish interval from an environment variable.
func setPublishMetricInterval() {
	envPublishInterval := os.Getenv(publishMetricIntervalEnvVar)
	if len(envPublishInterval) != 0 {
		// Convert to Base 10 32 bit int.
		envPublishIntervalValue, err := strconv.ParseInt(envPublishInterval, 10, 32)
		if err == nil {
			publishMetricsInterval = time.Duration(envPublishIntervalValue) * time.Second
		}
	} else {
		publishMetricsInterval = defaultPublishMetricsInterval
	}
}

// publishMetrics invokes the PublishMetricsRequest on the clientserver object.
// The argument must be of the clientServer type.
func publishMetrics(cs interface{}) error {
	clsrv, ok := cs.(*clientServer)
	if !ok {
		return errors.New("Unexpected object type in publishMetric")
	}
	metadata, taskMetrics, err := clsrv.statsEngine.GetInstanceMetrics()
	if err != nil {
		return err
	}

	timestamp := time.Now()

	log.Debug("making PublishMetricsRequest", "timestamp", &timestamp)
	return clsrv.MakeRequest(&ecstcs.PublishMetricsRequest{
		Metadata:    metadata,
		TaskMetrics: taskMetrics,
		Timestamp:   &timestamp,
	})
	return nil
}
