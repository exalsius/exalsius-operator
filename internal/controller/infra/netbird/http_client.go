/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package netbird

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// NetBirdClient is an HTTP client for the NetBird Management API
type NetBirdClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewNetBirdClient creates a new NetBird HTTP client
func NewNetBirdClient(baseURL, apiKey string) *NetBirdClient {
	return &NetBirdClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// doRequest performs an HTTP request with authentication
func (c *NetBirdClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	return resp, nil
}

// handleResponse handles the HTTP response and unmarshals into result
func (c *NetBirdClient) handleResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if result != nil && len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w (body: %s)", err, string(bodyBytes))
		}
	}

	return nil
}

// Networks API

// ListNetworks lists all networks
func (c *NetBirdClient) ListNetworks(ctx context.Context) ([]Network, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/networks", nil)
	if err != nil {
		return nil, err
	}

	var networks []Network
	if err := c.handleResponse(resp, &networks); err != nil {
		return nil, err
	}

	return networks, nil
}

// CreateNetwork creates a new network
func (c *NetBirdClient) CreateNetwork(ctx context.Context, name, description string) (Network, error) {
	req := NetworkRequest{
		Name:        name,
		Description: &description,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/networks", req)
	if err != nil {
		return Network{}, err
	}

	var network Network
	if err := c.handleResponse(resp, &network); err != nil {
		return Network{}, err
	}

	return network, nil
}

// GetNetwork gets a network by ID
func (c *NetBirdClient) GetNetwork(ctx context.Context, networkID string) (Network, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/networks/%s", networkID), nil)
	if err != nil {
		return Network{}, err
	}

	var network Network
	if err := c.handleResponse(resp, &network); err != nil {
		return Network{}, err
	}

	return network, nil
}

// Groups API

// ListGroups lists all groups
func (c *NetBirdClient) ListGroups(ctx context.Context) ([]Group, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/groups", nil)
	if err != nil {
		return nil, err
	}

	var groups []Group
	if err := c.handleResponse(resp, &groups); err != nil {
		return nil, err
	}

	return groups, nil
}

// CreateGroup creates a new group
func (c *NetBirdClient) CreateGroup(ctx context.Context, name string) (Group, error) {
	req := GroupCreateRequest{
		Name: name,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/groups", req)
	if err != nil {
		return Group{}, err
	}

	var group Group
	if err := c.handleResponse(resp, &group); err != nil {
		return Group{}, err
	}

	return group, nil
}

// GetGroup gets a group by ID
func (c *NetBirdClient) GetGroup(ctx context.Context, groupID string) (Group, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/groups/%s", groupID), nil)
	if err != nil {
		return Group{}, err
	}

	var group Group
	if err := c.handleResponse(resp, &group); err != nil {
		return Group{}, err
	}

	return group, nil
}

// UpdateGroup updates a group
func (c *NetBirdClient) UpdateGroup(ctx context.Context, groupID, name string, peerIDs []string) (Group, error) {
	req := GroupRequest{
		Name:  name,
		Peers: &peerIDs,
	}

	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/groups/%s", groupID), req)
	if err != nil {
		return Group{}, err
	}

	var group Group
	if err := c.handleResponse(resp, &group); err != nil {
		return Group{}, err
	}

	return group, nil
}

// DeleteGroup deletes a group
func (c *NetBirdClient) DeleteGroup(ctx context.Context, groupID string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/groups/%s", groupID), nil)
	if err != nil {
		return err
	}

	return c.handleResponse(resp, nil)
}

// Setup Keys API

// ListSetupKeys lists all setup keys
func (c *NetBirdClient) ListSetupKeys(ctx context.Context) ([]SetupKey, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/setup-keys", nil)
	if err != nil {
		return nil, err
	}

	var keys []SetupKey
	if err := c.handleResponse(resp, &keys); err != nil {
		return nil, err
	}

	return keys, nil
}

// CreateSetupKey creates a new setup key
func (c *NetBirdClient) CreateSetupKey(ctx context.Context, request SetupKeyRequest) (SetupKey, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/setup-keys", request)
	if err != nil {
		return SetupKey{}, err
	}

	var key SetupKey
	if err := c.handleResponse(resp, &key); err != nil {
		return SetupKey{}, err
	}

	return key, nil
}

// DeleteSetupKey deletes a setup key
func (c *NetBirdClient) DeleteSetupKey(ctx context.Context, keyID string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/setup-keys/%s", keyID), nil)
	if err != nil {
		return err
	}

	return c.handleResponse(resp, nil)
}

// Policies API

// ListPolicies lists all policies
func (c *NetBirdClient) ListPolicies(ctx context.Context) ([]Policy, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/policies", nil)
	if err != nil {
		return nil, err
	}

	var policies []Policy
	if err := c.handleResponse(resp, &policies); err != nil {
		return nil, err
	}

	return policies, nil
}

// CreatePolicy creates a new policy
func (c *NetBirdClient) CreatePolicy(ctx context.Context, request PolicyRequest) (Policy, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/policies", request)
	if err != nil {
		return Policy{}, err
	}

	var policy Policy
	if err := c.handleResponse(resp, &policy); err != nil {
		return Policy{}, err
	}

	return policy, nil
}

// UpdatePolicy updates a policy
func (c *NetBirdClient) UpdatePolicy(ctx context.Context, policyID string, request PolicyRequest) (Policy, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/policies/%s", policyID), request)
	if err != nil {
		return Policy{}, err
	}

	var policy Policy
	if err := c.handleResponse(resp, &policy); err != nil {
		return Policy{}, err
	}

	return policy, nil
}

// DeletePolicy deletes a policy
func (c *NetBirdClient) DeletePolicy(ctx context.Context, policyID string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/policies/%s", policyID), nil)
	if err != nil {
		return err
	}

	return c.handleResponse(resp, nil)
}

// Network Resources API

// ListNetworkResources lists all resources for a network
func (c *NetBirdClient) ListNetworkResources(ctx context.Context, networkID string) ([]NetworkResource, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/networks/%s/resources", networkID), nil)
	if err != nil {
		return nil, err
	}

	var resources []NetworkResource
	if err := c.handleResponse(resp, &resources); err != nil {
		return nil, err
	}

	return resources, nil
}

// CreateNetworkResource creates a new network resource
func (c *NetBirdClient) CreateNetworkResource(ctx context.Context, networkID string, request NetworkResourceRequest) (NetworkResource, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/networks/%s/resources", networkID), request)
	if err != nil {
		return NetworkResource{}, err
	}

	var resource NetworkResource
	if err := c.handleResponse(resp, &resource); err != nil {
		return NetworkResource{}, err
	}

	return resource, nil
}

// UpdateNetworkResource updates a network resource
func (c *NetBirdClient) UpdateNetworkResource(ctx context.Context, networkID, resourceID string, request NetworkResourceRequest) (NetworkResource, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/networks/%s/resources/%s", networkID, resourceID), request)
	if err != nil {
		return NetworkResource{}, err
	}

	var resource NetworkResource
	if err := c.handleResponse(resp, &resource); err != nil {
		return NetworkResource{}, err
	}

	return resource, nil
}

// GetNetworkResource gets a network resource by ID
func (c *NetBirdClient) GetNetworkResource(ctx context.Context, networkID, resourceID string) (NetworkResource, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/networks/%s/resources/%s", networkID, resourceID), nil)
	if err != nil {
		return NetworkResource{}, err
	}

	var resource NetworkResource
	if err := c.handleResponse(resp, &resource); err != nil {
		return NetworkResource{}, err
	}

	return resource, nil
}

// DeleteNetworkResource deletes a network resource
func (c *NetBirdClient) DeleteNetworkResource(ctx context.Context, networkID, resourceID string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/networks/%s/resources/%s", networkID, resourceID), nil)
	if err != nil {
		return err
	}

	return c.handleResponse(resp, nil)
}

// Network Routers API

// ListNetworkRouters lists all routers for a network
func (c *NetBirdClient) ListNetworkRouters(ctx context.Context, networkID string) ([]NetworkRouter, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/networks/%s/routers", networkID), nil)
	if err != nil {
		return nil, err
	}

	var routers []NetworkRouter
	if err := c.handleResponse(resp, &routers); err != nil {
		return nil, err
	}

	return routers, nil
}

// CreateNetworkRouter creates a new network router
func (c *NetBirdClient) CreateNetworkRouter(ctx context.Context, networkID string, request NetworkRouterRequest) (NetworkRouter, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/networks/%s/routers", networkID), request)
	if err != nil {
		return NetworkRouter{}, err
	}

	var router NetworkRouter
	if err := c.handleResponse(resp, &router); err != nil {
		return NetworkRouter{}, err
	}

	return router, nil
}

// UpdateNetworkRouter updates a network router
func (c *NetBirdClient) UpdateNetworkRouter(ctx context.Context, networkID, routerID string, request NetworkRouterRequest) (NetworkRouter, error) {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/networks/%s/routers/%s", networkID, routerID), request)
	if err != nil {
		return NetworkRouter{}, err
	}

	var router NetworkRouter
	if err := c.handleResponse(resp, &router); err != nil {
		return NetworkRouter{}, err
	}

	return router, nil
}

// Peers API

// ListPeers lists all peers
func (c *NetBirdClient) ListPeers(ctx context.Context) ([]Peer, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/peers", nil)
	if err != nil {
		return nil, err
	}

	var peers []Peer
	if err := c.handleResponse(resp, &peers); err != nil {
		return nil, err
	}

	return peers, nil
}

