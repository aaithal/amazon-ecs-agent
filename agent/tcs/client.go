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
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecstcs"
	"github.com/aws/amazon-ecs-agent/agent/logger"
	"github.com/aws/amazon-ecs-agent/agent/stats"
	"github.com/aws/amazon-ecs-agent/agent/utils"
	wsclient "github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/gorilla/websocket"
)

//go:generate mockgen.sh github.com/aws/amazon-ecs-agent/agent/tcs WebsocketClient mock/$GOFILE

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

	// readBufSize is the size of the read buffer for the ws connection.
	readBufSize = 4096

	// writeBufSize is the size of the write buffer for the ws connection.
	writeBufSize = 4096
)

var log = logger.ForModule("tcs client")

// tcsPublishMetricInterval is used to get the inerval to publish metrics to
// the backend. This is present only for testing and should be deleted in a
// future commit.
// TODO: Disable the initialization from the environment variable for this.
var publishMetricsInterval time.Duration

// port is used to retreive the port to connect to the backend. This is present
// only for testing and should be deleted in a future commit.
// TODO: Disable the initialization from the environment variable for this.
var port string

// WebsocketClient captures functions related to websocket client.
type WebsocketClient interface {
	NewClient(net.Conn, *url.URL, http.Header, int, int) (*websocket.Conn, *http.Response, error)
}

// WSClient implements WebsocketClient.
type WSClient struct{}

// clientServer implements wsclient.ClientServer interface for metrics backend.
type clientServer struct {
	url          string
	statsEngine  stats.Engine
	publishTimer *timer
	ws           WebsocketClient
	wsclient.ClientServerImpl
}

// Connect opens a connection to the backend and upgrades it to a websocket.
// Calls to 'MakeRequest' can be made after calling this, but responss will
// not be receivable until 'Serve' is also called.
// TODO This code is copied from acsclient package as is with minor
// modifications. Package re-used code into functions and reduce duplicating
// code.
func (cs *clientServer) Connect() error {
	parsedURL, err := url.Parse(cs.url)
	if err != nil {
		return err
	}
	request, _ := http.NewRequest("GET", cs.url, nil)

	// url.Host might not have the port, but tls.Dial needs it
	dialHost := parsedURL.Host
	if !strings.Contains(dialHost, ":") {
		dialHost += ":" + port
	}

	// TODO Use TLS Dialer instead.
	timeoutDialer := &net.Dialer{Timeout: tcsConnectTimeout}
	log.Info("Creating poll dialer", "host", parsedURL.Host)
	conn, err := timeoutDialer.Dial("tcp", dialHost)

	if err != nil {
		return err
	}

	wsConn, httpResponse, err := cs.ws.NewClient(conn, parsedURL, request.Header, readBufSize, writeBufSize)
	if httpResponse != nil {
		defer httpResponse.Body.Close()
	}

	if err != nil {
		var resp []byte
		if httpResponse != nil {
			var readErr error
			resp, readErr = ioutil.ReadAll(httpResponse.Body)
			if readErr != nil {
				return errors.New("Unable to read websocket connection: " + readErr.Error() + ", " + err.Error())
			}
			// If there's a response, we can try to unmarshal it into one of the
			// modeled acs error types
			possibleError, _, decodeErr := wsclient.DecodeData(resp, cs.TypeDecoder)
			if decodeErr == nil {
				return NewTcsError(possibleError)
			}
		}
		log.Warn("Error creating a websocket client", "err", err)
		return errors.New(string(resp) + ", " + err.Error())
	}
	cs.Conn = wsConn
	return nil
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

// Close closes the underlying connection.
func (cs *clientServer) Close() error {
	cs.publishTimer.stop()
	return cs.Conn.Close()
}

// New returns a client/server to bidirectionally communicate with the backend.
// The returned struct should have both 'Connect' and 'Serve' called upon it
// before being used.
func New(url string, statsEngine stats.Engine) wsclient.ClientServer {
	cs := &clientServer{
		statsEngine:  statsEngine,
		publishTimer: newTimer(publishMetricsInterval, publishMetrics),
		url:          url,
		ws:           &WSClient{},
	}
	cs.TypeMappings = &typeMappings{}
	cs.RequestHandlers = make(map[string]wsclient.RequestHandler)
	cs.TypeDecoder = &decoder{}
	return cs
}

// NewClient creates a new client connection using the given net connection.
func (ws *WSClient) NewClient(conn net.Conn, u *url.URL, requestHeader http.Header, readBufSize, writeBufSize int) (c *websocket.Conn, response *http.Response, err error) {
	return websocket.NewClient(conn, u, requestHeader, readBufSize, writeBufSize)
}

func init() {
	// TODO: Delete me! Refer comments for port and publishMetricsInterval.
	setPort()
	setPublishMetricInterval()
}

// setPort sets backend port from an environment variable.
func setPort() {
	port = utils.DefaultIfBlank(os.Getenv(portEnvVar), defaultPort)
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

	return clsrv.MakeRequest(&ecstcs.PublishMetricsRequest{
		Metadata:    metadata,
		TaskMetrics: taskMetrics,
		Timestamp:   &timestamp,
	})
	return nil
}
