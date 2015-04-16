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
package acsclient

import (
	"crypto/tls"
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/ecs_client/authv4"
	"github.com/aws/amazon-ecs-agent/agent/ecs_client/authv4/credentials"
	"github.com/aws/amazon-ecs-agent/agent/logger"
	wsclient "github.com/aws/amazon-ecs-agent/agent/websocket/client"
	"github.com/gorilla/websocket"
)

var log = logger.ForModule("acs client")

const (
	acsService = "ecs"

	// acsConnectTimeout specifies the default connection timeout to the backend.
	acsConnectTimeout = 3 * time.Second
)

// default implementation of ClientServer
type clientServer struct {
	// url is the full url to ACS, including path, querystring, and so on.
	url                string
	region             string
	credentialProvider credentials.AWSCredentialProvider
	acceptInvalidCert  bool

	wsclient.ClientServerImpl
}

// New returns a client/server to bidirectionally communicate with ACS
// The returned struct should have both 'Connect' and 'Serve' called upon it
// before being used.
func New(url string, region string, credentialProvider credentials.AWSCredentialProvider, acceptInvalidCert bool) wsclient.ClientServer {
	cs := &clientServer{
		url:                url,
		region:             region,
		credentialProvider: credentialProvider,
		acceptInvalidCert:  acceptInvalidCert,
	}
	cs.TypeMappings = &typeMappings{}
	cs.RequestHandlers = make(map[string]wsclient.RequestHandler)
	cs.TypeDecoder = &decoder{}
	return cs
}

// Connect opens a connection to ACS and upgrades it to a websocket. Calls to
// 'MakeRequest' can be made after calling this, but responss will not be
// receivable until 'Serve' is also called.
func (cs *clientServer) Connect() error {
	parsedAcsURL, err := url.Parse(cs.url)
	if err != nil {
		return err
	}

	signer := authv4.NewHttpSigner(cs.region, acsService, cs.credentialProvider, nil)

	// NewRequest never returns an error if the url parses and we just verified
	// it did above
	request, _ := http.NewRequest("GET", cs.url, nil)
	signer.SignHttpRequest(request)

	// url.Host might not have the port, but tls.Dial needs it
	dialHost := parsedAcsURL.Host
	if !strings.Contains(dialHost, ":") {
		dialHost += ":443"
	}

	timeoutDialer := &net.Dialer{Timeout: acsConnectTimeout}
	log.Info("Creating poll dialer", "host", parsedAcsURL.Host)
	acsConn, err := tls.DialWithDialer(timeoutDialer, "tcp", dialHost, &tls.Config{InsecureSkipVerify: cs.acceptInvalidCert})
	if err != nil {
		return err
	}

	websocketConn, httpResponse, err := websocket.NewClient(acsConn, parsedAcsURL, request.Header, 1024, 1024)
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
				return NewACSError(possibleError)
			}
		}
		log.Warn("Error creating a websocket client", "err", err)
		return errors.New(string(resp) + ", " + err.Error())
	}
	cs.Conn = websocketConn
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

	return cs.ConsumeMessages()
}

// Close closes the underlying connection
func (cs *clientServer) Close() error {
	return cs.Conn.Close()
}
