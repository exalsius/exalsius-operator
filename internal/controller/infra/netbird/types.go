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

// Network represents a NetBird network
type Network struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// Group represents a NetBird group
type Group struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Peers []PeerMinimum `json:"peers,omitempty"`
}

// PeerMinimum represents a minimal peer object in API responses
type PeerMinimum struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SetupKey represents a NetBird setup key (API response)
type SetupKey struct {
	ID         string   `json:"id"`
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Expires    string   `json:"expires"`
	Valid      bool     `json:"valid"`
	Revoked    bool     `json:"revoked"`
	UsedTimes  int      `json:"used_times"`
	LastUsed   string   `json:"last_used"`
	State      string   `json:"state"`
	AutoGroups []string `json:"auto_groups,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
	UsageLimit int      `json:"usage_limit"`
	Ephemeral  bool     `json:"ephemeral"`
}

// SetupKeyRequest represents a request to create a setup key
type SetupKeyRequest struct {
	AutoGroups []string `json:"auto_groups,omitempty"`
	Ephemeral  *bool    `json:"ephemeral,omitempty"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	ExpiresIn  int64    `json:"expires_in"`
	UsageLimit int      `json:"usage_limit"`
}

// GroupRef represents a group reference in API responses
type GroupRef struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	PeersCount     int    `json:"peers_count"`
	ResourcesCount int    `json:"resources_count"`
}

// Policy represents a NetBird policy (API response)
type Policy struct {
	ID          *string      `json:"id,omitempty"`
	Name        string       `json:"name"`
	Description *string      `json:"description,omitempty"`
	Enabled     bool         `json:"enabled"`
	Rules       []PolicyRule `json:"rules"`
}

// PolicyRule represents a rule within a policy (API response)
type PolicyRule struct {
	ID            *string     `json:"id,omitempty"`
	Name          string      `json:"name"`
	Description   *string     `json:"description,omitempty"`
	Enabled       bool        `json:"enabled"`
	Action        string      `json:"action"`
	Protocol      string      `json:"protocol"`
	Sources       *[]GroupRef `json:"sources,omitempty"`
	Destinations  *[]GroupRef `json:"destinations,omitempty"`
	Bidirectional bool        `json:"bidirectional"`
}

// PolicyRuleRequest represents a rule for create/update requests
type PolicyRuleRequest struct {
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Enabled       bool      `json:"enabled"`
	Action        string    `json:"action"`
	Protocol      string    `json:"protocol"`
	Sources       *[]string `json:"sources,omitempty"`
	Destinations  *[]string `json:"destinations,omitempty"`
	Bidirectional bool      `json:"bidirectional"`
}

// PolicyRequest represents a request to create or update a policy
type PolicyRequest struct {
	Name        string              `json:"name"`
	Description *string             `json:"description,omitempty"`
	Enabled     bool                `json:"enabled"`
	Rules       []PolicyRuleRequest `json:"rules"`
}

// NetworkResource represents a NetBird network resource (API response)
type NetworkResource struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Address     string     `json:"address"`
	Enabled     bool       `json:"enabled"`
	Groups      []GroupRef `json:"groups,omitempty"`
	Description *string    `json:"description,omitempty"`
	Type        *string    `json:"type,omitempty"`
}

// NetworkResourceRequest represents a request to create or update a network resource
type NetworkResourceRequest struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Enabled     bool     `json:"enabled"`
	Groups      []string `json:"groups,omitempty"`
	Description *string  `json:"description,omitempty"`
}

// NetworkRouter represents a NetBird network router
type NetworkRouter struct {
	ID         string    `json:"id"`
	Enabled    bool      `json:"enabled"`
	Masquerade bool      `json:"masquerade"`
	Metric     int       `json:"metric"`
	PeerGroups *[]string `json:"peer_groups,omitempty"`
	Peer       *string   `json:"peer,omitempty"`
}

// NetworkRouterRequest represents a request to create or update a network router
type NetworkRouterRequest struct {
	Enabled    bool      `json:"enabled"`
	Masquerade bool      `json:"masquerade"`
	Metric     int       `json:"metric"`
	PeerGroups *[]string `json:"peer_groups,omitempty"`
	Peer       *string   `json:"peer,omitempty"`
}

// Peer represents a NetBird peer
type Peer struct {
	ID       string     `json:"id"`
	Hostname string     `json:"hostname"`
	IP       string     `json:"ip"`
	Groups   []GroupRef `json:"groups,omitempty"`
}

// GroupRequest represents a request to update a group
type GroupRequest struct {
	Name  string    `json:"name"`
	Peers *[]string `json:"peers,omitempty"`
}

// NetworkRequest represents a request to create a network
type NetworkRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// GroupCreateRequest represents a request to create a group
type GroupCreateRequest struct {
	Name string `json:"name"`
}
