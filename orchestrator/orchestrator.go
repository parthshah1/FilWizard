package orchestrator

import "sync"

type Orchestrator struct {
	handlers map[string]TaskHandler
	mu       sync.RWMutex
}
