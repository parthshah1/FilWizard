package orchestrator

import (
	"context"
	"time"
)

type Task struct {
	Name       string                 `json:",omitempty"`
	Type       string                 `json:",omitempty"`
	Params     map[string]interface{} `json:",omitempty"`
	DependsOn  []string               `json:",omitempty"`
	RetryCount int                    `json:",omitempty"`
	Timeout    time.Duration          `json:",omitempty"`
}

type Scenario struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Tasks       []Task            `yaml:"tasks"`
	Variables   map[string]string `yaml:"variables,omitempty"`
}

type TaskResult struct {
	TaskName string
	Output   map[string]interface{}
	Error    error
	Duration time.Duration
}

type TaskHandler interface {
	Execute(ctx context.Context, params map[string]interface{}) (map[string]interface{}, error)
}
