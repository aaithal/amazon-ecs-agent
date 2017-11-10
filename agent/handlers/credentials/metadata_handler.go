package credentials

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/aws/amazon-ecs-agent/agent/api"
	"github.com/aws/amazon-ecs-agent/agent/engine/dockerstate"
)

const metadataPath = "/metadata/v1"

type taskResp struct {
	Cluster    string
	TaskARN    string
	Family     string
	Version    string
	Containers []containerResp
	Networks   []taskNetworkResponse
	Limits     limitsResponse
}

type containerResp struct {
	ID            string
	Name          string
	DockerName    string
	Image         string
	ImageID       string
	Ports         []portResponse
	Labels        map[string]string
	DesiredStatus string
	KnownStatus   string
	ExitCode      int `json:"omitempty"`
	Limits        limitsResponse
	StartedAt     time.Time
	Type          string
}

type taskNetworkResponse struct {
	NetworkMode   string
	IPv4Addresses []string
}

type limitsResponse struct {
	CPU    uint
	Memory uint
}

type portResponse struct {
	ContainerPort uint16
	Protocol      string
}

func taskToResponse(task *api.Task, state dockerstate.TaskEngineState, cluster string) taskResp {
	t := taskResp{
		Cluster: cluster,
		TaskARN: task.Arn,
		Family:  task.Family,
		Version: task.Version,
		Networks: []taskNetworkResponse{
			{
				NetworkMode:   "awsvpc",
				IPv4Addresses: []string{task.ENI.IPV4Addresses[0].Address},
			},
		},
		// TODO Limits
	}

	containerNameToDockerContainer, ok := state.ContainerMapByArn(task.Arn)
	if !ok {
		return t
	}

	for _, dockerContainer := range containerNameToDockerContainer {
		container := dockerContainer.Container
		cr := containerResp{
			ID:            dockerContainer.DockerID,
			Name:          container.Name,
			DockerName:    dockerContainer.DockerName,
			Image:         container.Image,
			ImageID:       container.ImageID,
			Labels:        make(map[string]string), // TODO
			DesiredStatus: container.GetDesiredStatus().String(),
			KnownStatus:   container.GetKnownStatus().String(),
			// TODO ExitCode:      0,
			Limits: limitsResponse{
				CPU:    container.CPU,
				Memory: container.Memory,
			},
			// TODO: StartedAt: time.Now(),
			Type: container.Type.String(),
		}
		for _, binding := range container.Ports {
			cr.Ports = append(cr.Ports, portResponse{
				ContainerPort: binding.ContainerPort,
				Protocol:      binding.Protocol.String(),
			})
		}
		t.Containers = append(t.Containers, cr)
	}

	return t
}

func metadataV1Handler(state dockerstate.TaskEngineState, cluster string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			jsonMsg, _ := json.Marshal("Unable to get request's ip address")
			writeJSONToResponse(w, http.StatusBadRequest, jsonMsg)
			return
		}

		taskARN, ok := state.GetTaskByIPAddress(ip)
		if !ok {
			jsonMsg, _ := json.Marshal("Unable to assoicate '" + ip + "' with a task")
			writeJSONToResponse(w, http.StatusBadRequest, jsonMsg)
			return
		}
		task, ok := state.TaskByArn(taskARN)
		if !ok {
			jsonMsg, _ := json.Marshal("Unable to find task '" + taskARN + "'for ip'" + ip + "'")
			writeJSONToResponse(w, http.StatusBadRequest, jsonMsg)
			return
		}
		jsonMsg, _ := json.Marshal(taskToResponse(task, state, cluster))
		writeJSONToResponse(w, http.StatusOK, jsonMsg)
	}
}
