package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/api/client"
	"github.com/parthshah1/mpool-tx/config"
)

// Client wraps the Filecoin API client
type Client struct {
	api    api.FullNode
	cfg    *config.Config
	closer func()
}

// New creates a new client instance
func New(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Prepare headers with JWT token
	var headers http.Header
	if cfg.Token != "" {
		headers = http.Header{}
		headers.Add("Authorization", "Bearer "+cfg.Token)
	}

	// Connect to Filecoin node with authentication
	fullNodeAPI, closer, err := client.NewFullNodeRPCV1(context.Background(), cfg.RPC, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Lotus node at %s: %w", cfg.RPC, err)
	}

	return &Client{
		api:    fullNodeAPI,
		cfg:    cfg,
		closer: closer,
	}, nil
}

// Close closes the client connection
func (c *Client) Close() {
	if c.closer != nil {
		c.closer()
	}
}

// GetConfig returns the client configuration
func (c *Client) GetConfig() *config.Config {
	return c.cfg
}

// GetAPI returns the Filecoin API client
func (c *Client) GetAPI() api.FullNode {
	return c.api
}
