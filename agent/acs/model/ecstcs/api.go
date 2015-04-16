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

package ecstcs

import "time"

type AckRequest struct {
	MessageId *string `locationName:"messageId" type:"string"`

	metadataAckRequest `json:"-", xml:"-"`
}

type metadataAckRequest struct {
	SDKShapeTraits bool `type:"structure"`
}

type CWStatsSet struct {
	Max *float64 `locationName:"max" type:"double"`

	Min *float64 `locationName:"min" type:"double"`

	SampleCount *int64 `locationName:"sampleCount" type:"integer"`

	Sum *float64 `locationName:"sum" type:"double"`

	Timestamp *time.Time `locationName:"timestamp" type:"timestamp" timestampFormat:"unix"`

	Unit *string `locationName:"unit" type:"string"`

	metadataCWStatsSet `json:"-", xml:"-"`
}

type metadataCWStatsSet struct {
	SDKShapeTraits bool `type:"structure"`
}

type ClientException struct {
	Message *string `locationName:"message" type:"string"`

	metadataClientException `json:"-", xml:"-"`
}

type metadataClientException struct {
	SDKShapeTraits bool `type:"structure"`
}

type ContainerMetadata struct {
	Name *string `locationName:"name" type:"string"`

	metadataContainerMetadata `json:"-", xml:"-"`
}

type metadataContainerMetadata struct {
	SDKShapeTraits bool `type:"structure"`
}

type ContainerMetric struct {
	CpuStatsSet *CWStatsSet `locationName:"cpuStatsSet" type:"structure"`

	MemoryStatsSet *CWStatsSet `locationName:"memoryStatsSet" type:"structure"`

	Metadata *ContainerMetadata `locationName:"metadata" type:"structure"`

	metadataContainerMetric `json:"-", xml:"-"`
}

type metadataContainerMetric struct {
	SDKShapeTraits bool `type:"structure"`
}

type MetricsMetadata struct {
	Cluster *string `locationName:"cluster" type:"string"`

	ContainerInstance *string `locationName:"containerInstance" type:"string"`

	metadataMetricsMetadata `json:"-", xml:"-"`
}

type metadataMetricsMetadata struct {
	SDKShapeTraits bool `type:"structure"`
}

type PublishMetricsRequest struct {
	Metadata *MetricsMetadata `locationName:"metadata" type:"structure"`

	TaskMetrics []*TaskMetric `locationName:"taskMetrics" type:"list"`

	metadataPublishMetricsRequest `json:"-", xml:"-"`
}

type metadataPublishMetricsRequest struct {
	SDKShapeTraits bool `type:"structure"`
}

type ServerException struct {
	Message *string `locationName:"message" type:"string"`

	metadataServerException `json:"-", xml:"-"`
}

type metadataServerException struct {
	SDKShapeTraits bool `type:"structure"`
}

type StartSessionRequest struct {
	Cluster *string `locationName:"cluster" type:"string"`

	ContainerInstance *string `locationName:"containerInstance" type:"string"`

	metadataStartSessionRequest `json:"-", xml:"-"`
}

type metadataStartSessionRequest struct {
	SDKShapeTraits bool `type:"structure"`
}

type StopSessionMessage struct {
	Message *string `locationName:"message" type:"string"`

	metadataStopSessionMessage `json:"-", xml:"-"`
}

type metadataStopSessionMessage struct {
	SDKShapeTraits bool `type:"structure"`
}

type TaskMetric struct {
	ContainerMetrics []*ContainerMetric `locationName:"containerMetrics" type:"list"`

	TaskArn *string `locationName:"taskArn" type:"string"`

	metadataTaskMetric `json:"-", xml:"-"`
}

type metadataTaskMetric struct {
	SDKShapeTraits bool `type:"structure"`
}
