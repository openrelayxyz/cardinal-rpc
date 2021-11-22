package rpc

import (
	"context"
	"encoding/json"
	"time"
	log "github.com/inconshreveable/log15"
)

type HealthStatus int

const (
	Healthy HealthStatus = iota
	Warning
	Unavailable
)

type HealthCheck interface{
	Healthy() HealthStatus
}

type Check struct {
	Frequency int64 `yaml:"frequency"`
	Params string  `yaml:"params"`
	Method string   `yaml:"method"`
	MaxHeavyResponseTime int64 `yaml:"max_heavy_response_time_ms"`
	MaxNormalResponseTime int64 `yaml:"max_normal_response_time_ms"`
	heavyPassed bool
	normalPassed bool
}

type Checks []Check

func (c Checks) Healthy() HealthStatus {
	heavyFailed := false
	for _, check := range c {
		if !check.normalPassed {
			return Unavailable
		}
		if !check.heavyPassed {
			heavyFailed = true
		}
	}
	if heavyFailed {
		return Warning
	}
	return Healthy
}

func (c Checks) Start(r RegistryCallable) {
	for i := range c {
		go func(i int) {
			for {
				time.Sleep(time.Duration(c[i].Frequency) * time.Second)
				start := time.Now()
				params := []json.RawMessage{}
				if err := json.Unmarshal([]byte(c[i].Params), &params); err != nil {
					log.Error("Error loading healthcheck params", "params", c[i].Params, "err", err)
					return
				}
				if _, _, err := r.Call(context.Background(), c[i].Method, params); err != nil {
					c[i].heavyPassed = false
					c[i].normalPassed = false
				} else {
					duration := time.Since(start)
					if c[i].MaxHeavyResponseTime > 0 && duration > time.Duration(c[i].MaxHeavyResponseTime) * time.Millisecond {
						c[i].heavyPassed = false
						log.Debug("Check succeeded, but exceeded acceptable heavy time", "duration", duration, "target", c[i].MaxHeavyResponseTime)
					} else {
						c[i].heavyPassed = true
					}
					if c[i].MaxNormalResponseTime > 0 && duration > time.Duration(c[i].MaxNormalResponseTime) * time.Millisecond {
						c[i].normalPassed = false
						log.Debug("Check succeeded, but exceeded acceptable normal time", "duration", duration, "target", c[i].MaxNormalResponseTime)
					} else {
						c[i].normalPassed = true
					}
				}
			}
		}(i)
	}
}
