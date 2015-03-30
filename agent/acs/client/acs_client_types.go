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

package acsclient

import (
	"reflect"

	"github.com/aws/amazon-ecs-agent/agent/acs/model/ecsacs"
)

// typeMappings is a map of type-strings (as passed in acs messages as the
// 'type' field) to a pointer to the corresponding struct type this type should
// be marshalled/unmarshalled to/from.
// This list is currently *manually updated* and assumes that the generated
// struct type-names within the package *exactly match* the type sent by ACS
// (true so far; careful with inflections)
var typeMappings map[string]reflect.Type

func init() {
	// TODO, this list should be autogenerated
	// I couldn't figure out how to get a list of all structs in a package via
	// reflection, but that would solve this. The alternative is to either parse
	// the .json model or the generated struct names.
	recognizedTypes := []interface{}{
		ecsacs.HeartbeatMessage{}, ecsacs.PayloadMessage{},

		ecsacs.CloseMessage{}, ecsacs.AckRequest{},

		ecsacs.ServerException{},
		ecsacs.BadRequestException{}, ecsacs.InvalidClusterException{},
		ecsacs.InvalidInstanceException{}, ecsacs.AccessDeniedException{},
		ecsacs.InactiveInstanceException{},
	}

	// This produces a map of:
	// "MyMessage": TypeOf(ecsacs.MyMessage)
	typeMappings = make(map[string]reflect.Type)
	for _, recognizedType := range recognizedTypes {
		typeMappings[reflect.TypeOf(recognizedType).Name()] = reflect.TypeOf(recognizedType)
	}
}

func NewOfType(acsType string) (interface{}, bool) {
	rtype, ok := typeMappings[acsType]
	if !ok {
		return nil, false
	}
	return reflect.New(rtype).Interface(), true
}
