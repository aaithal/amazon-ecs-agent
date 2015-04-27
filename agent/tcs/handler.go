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
	"strings"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/config"
	"github.com/aws/amazon-ecs-agent/agent/ecs_client/authv4/credentials"
	"github.com/aws/amazon-ecs-agent/agent/stats"
	"github.com/aws/amazon-ecs-agent/agent/utils"
)

const (
	endpointEnvVar = "ECS_METRICS_BACKEND_HOST"
)

var endpoint string

// sessionWrapper encapsulates the parameters required to start a session
// with the backend. It defines the startSession method for this purpose.
type sessionWrapper struct {
	acceptInvalidCert  bool
	credentialProvider credentials.AWSCredentialProvider
	region             string
	statsEngine        stats.Engine
	url                string
}

// StartSession creates a session with the backend and handles requests
// using the passed in arguments.
// The engine is expected to initialized and gathering container metrics by
// the time the websocket client starts using it.
func StartSession(containerInstance string, credentialProvider credentials.AWSCredentialProvider, cfg *config.Config, acceptInvalidCert bool, statsEngine stats.Engine) error {
	backoff := utils.NewSimpleBackoff(time.Second, 1*time.Minute, 0.2, 2)
	setEndpoint()
	sw := &sessionWrapper{
		acceptInvalidCert:  acceptInvalidCert,
		credentialProvider: credentialProvider,
		region:             cfg.AWSRegion,
		url:                formatURL(endpoint, cfg.Cluster, containerInstance),
		statsEngine:        statsEngine,
	}
	return utils.RetryWithBackoff(backoff, sw.startSession)
}

// startSession creates a session with the backend.
func (sw *sessionWrapper) startSession() error {
	log.Info("Creating ws client", "url", sw.url)
	client := New(sw.url, sw.region, sw.credentialProvider, sw.acceptInvalidCert, sw.statsEngine)
	err := client.Connect()
	if err != nil {
		return err
	}

	return client.Serve()
}

// formatURL returns formatted url for tcs endpoint.
func formatURL(endpoint string, cluster string, containerInstance string) string {
	tcsURL := endpoint
	if !strings.HasSuffix(tcsURL, "/") {
		tcsURL += "/"
	}
	query := url.Values{}
	query.Set("cluster", cluster)
	query.Set("containerInstance", containerInstance)
	return tcsURL + "ws?" + query.Encode()
}

// setEndpoint sets the backend endpoint to connect to.
func setEndpoint() {
	// TODO Delete Me! The code should be querying ECS for an endpoint.
	// This should be changed to reflect that when ECS supports such an API.
	endpoint = os.Getenv(endpointEnvVar)
}
