// Copyright 2014-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
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

package timer

import (
	"encoding/json"

	"github.com/cihub/seelog"
)

type TimerLogger struct {
	logger seelog.LoggerInterface
}

func newTimerLogger() *TimerLogger {
	config := `
	<seelog type="asyncloop" minlevel="info">
		<outputs formatid="main">
			<console />
			<file path="/log/timer.log"/>
		</outputs>
		<formats>
			<format id="main" format="%Msg%n" />
		</formats>
	</seelog>
`
	l, err := seelog.LoggerFromConfigAsString(config)
	if err != nil {
		seelog.Errorf("Error creating timer logger: %v", err)
	}
	return &TimerLogger{
		logger: l,
	}
}

var TLogger = newTimerLogger()

type TimerEntry struct {
	Operation string `json:"Operation"`
	Duration  int64  `json:"Duration"`
	Result    int    `json:"Result"`
}

func (tLgr *TimerLogger) Log(entry TimerEntry) {
	bytes, _ := json.Marshal(entry)
	tLgr.logger.Info(string(bytes))
}
