package sre

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/render-oss/render-mcp-server/pkg/client"
)

const inventoryURI = "render://inventory"
const inventoryRefreshTTL = 30 * time.Second

type InventoryResource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type WorkspaceInventory struct {
	Workspace struct {
		Name    string `json:"name"`
		OwnerID string `json:"ownerId"`
	} `json:"workspace"`
	Resources []InventoryResource `json:"resources"`
}

type inventoryCache struct {
	mu          sync.RWMutex
	data        []WorkspaceInventory
	hash        string
	lastRefresh time.Time
	client      inventoryClient
}

type inventoryClient interface {
	ListOwnersWithResponse(ctx context.Context, params *client.ListOwnersParams, reqEditors ...client.RequestEditorFn) (*client.ListOwnersResponse, error)
	ListServicesWithResponse(ctx context.Context, params *client.ListServicesParams, reqEditors ...client.RequestEditorFn) (*client.ListServicesResponse, error)
	ListPostgresWithResponse(ctx context.Context, params *client.ListPostgresParams, reqEditors ...client.RequestEditorFn) (*client.ListPostgresResponse, error)
	ListKeyValueWithResponse(ctx context.Context, params *client.ListKeyValueParams, reqEditors ...client.RequestEditorFn) (*client.ListKeyValueResponse, error)
}

func newInventoryCache(c inventoryClient) *inventoryCache {
	return &inventoryCache{client: c}
}

func (ic *inventoryCache) get(ctx context.Context, typeFilter, workspaceFilter string) ([]WorkspaceInventory, error) {
	ic.mu.RLock()
	needsRefresh := time.Since(ic.lastRefresh) > inventoryRefreshTTL
	ic.mu.RUnlock()

	if needsRefresh {
		if err := ic.refresh(ctx); err != nil {
			ic.mu.RLock()
			if ic.data != nil {
				defer ic.mu.RUnlock()
				return ic.filter(typeFilter, workspaceFilter), nil
			}
			ic.mu.RUnlock()
			return nil, err
		}
	}

	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.filter(typeFilter, workspaceFilter), nil
}

func (ic *inventoryCache) filter(typeFilter, workspaceFilter string) []WorkspaceInventory {
	if typeFilter == "" && workspaceFilter == "" {
		result := make([]WorkspaceInventory, len(ic.data))
		copy(result, ic.data)
		return result
	}

	var filtered []WorkspaceInventory
	for _, ws := range ic.data {
		if workspaceFilter != "" && ws.Workspace.Name != workspaceFilter {
			continue
		}
		if typeFilter == "" {
			filtered = append(filtered, ws)
			continue
		}
		var resources []InventoryResource
		for _, r := range ws.Resources {
			if r.Type == typeFilter {
				resources = append(resources, r)
			}
		}
		if len(resources) > 0 {
			entry := ws
			entry.Resources = resources
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (ic *inventoryCache) refresh(ctx context.Context) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if time.Since(ic.lastRefresh) < inventoryRefreshTTL {
		return nil
	}

	owners, err := ic.fetchOwners(ctx)
	if err != nil {
		return fmt.Errorf("fetching owners: %w", err)
	}

	var inventories []WorkspaceInventory
	for _, owner := range owners {
		inv, err := ic.fetchWorkspaceInventory(ctx, owner)
		if err != nil {
			log.Printf("warning: failed to fetch inventory for workspace %s: %v", owner.Name, err)
			continue
		}
		inventories = append(inventories, inv)
	}

	data, _ := json.Marshal(inventories)
	newHash := fmt.Sprintf("%x", sha256.Sum256(data))

	ic.data = inventories
	ic.hash = newHash
	ic.lastRefresh = time.Now()

	return nil
}

type ownerInfo struct {
	ID   string
	Name string
}

func (ic *inventoryCache) fetchOwners(ctx context.Context) ([]ownerInfo, error) {
	resp, err := ic.client.ListOwnersWithResponse(ctx, &client.ListOwnersParams{})
	if err != nil {
		return nil, err
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}

	var owners []ownerInfo
	for _, o := range *resp.JSON200 {
		owners = append(owners, ownerInfo{
			ID:   o.Owner.Id,
			Name: o.Owner.Name,
		})
	}
	return owners, nil
}

func (ic *inventoryCache) fetchWorkspaceInventory(ctx context.Context, owner ownerInfo) (WorkspaceInventory, error) {
	inv := WorkspaceInventory{}
	inv.Workspace.Name = owner.Name
	inv.Workspace.OwnerID = owner.ID

	ownerFilter := []string{owner.ID}

	type result struct {
		resources []InventoryResource
		err       error
	}

	ch := make(chan result, 3)

	go func() {
		resources, err := ic.fetchServices(ctx, ownerFilter)
		ch <- result{resources, err}
	}()

	go func() {
		resources, err := ic.fetchPostgres(ctx, ownerFilter)
		ch <- result{resources, err}
	}()

	go func() {
		resources, err := ic.fetchKeyValue(ctx, ownerFilter)
		ch <- result{resources, err}
	}()

	for i := 0; i < 3; i++ {
		r := <-ch
		if r.err != nil {
			return inv, r.err
		}
		inv.Resources = append(inv.Resources, r.resources...)
	}

	return inv, nil
}

func (ic *inventoryCache) fetchServices(ctx context.Context, ownerFilter []string) ([]InventoryResource, error) {
	params := &client.ListServicesParams{
		OwnerId: &ownerFilter,
	}

	var resources []InventoryResource
	var cursor *string

	for {
		if cursor != nil {
			params.Cursor = cursor
		}

		resp, err := ic.client.ListServicesWithResponse(ctx, params)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil || len(*resp.JSON200) == 0 {
			break
		}

		for _, s := range *resp.JSON200 {
			resources = append(resources, InventoryResource{
				ID:     s.Service.Id,
				Name:   s.Service.Name,
				Type:   string(s.Service.Type),
				Status: serviceStatus(s.Service.Suspended),
			})
			c := s.Cursor
			cursor = &c
		}

		if len(*resp.JSON200) < 20 {
			break
		}
	}

	return resources, nil
}

func serviceStatus(suspended client.ServiceSuspended) string {
	if suspended == client.ServiceSuspendedSuspended {
		return "suspended"
	}
	return "running"
}

func (ic *inventoryCache) fetchPostgres(ctx context.Context, ownerFilter []string) ([]InventoryResource, error) {
	params := &client.ListPostgresParams{
		OwnerId: &ownerFilter,
	}

	var resources []InventoryResource
	var cursor *string

	for {
		if cursor != nil {
			params.Cursor = cursor
		}

		resp, err := ic.client.ListPostgresWithResponse(ctx, params)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil || len(*resp.JSON200) == 0 {
			break
		}

		for _, pg := range *resp.JSON200 {
			resources = append(resources, InventoryResource{
				ID:     pg.Postgres.Id,
				Name:   pg.Postgres.Name,
				Type:   "postgres",
				Status: string(pg.Postgres.Status),
			})
			c := pg.Cursor
			cursor = &c
		}

		if len(*resp.JSON200) < 20 {
			break
		}
	}

	return resources, nil
}

func (ic *inventoryCache) fetchKeyValue(ctx context.Context, ownerFilter []string) ([]InventoryResource, error) {
	params := &client.ListKeyValueParams{
		OwnerId: &ownerFilter,
	}

	var resources []InventoryResource
	var cursor *string

	for {
		if cursor != nil {
			params.Cursor = cursor
		}

		resp, err := ic.client.ListKeyValueWithResponse(ctx, params)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil || len(*resp.JSON200) == 0 {
			break
		}

		for _, kv := range *resp.JSON200 {
			resources = append(resources, InventoryResource{
				ID:     kv.KeyValue.Id,
				Name:   kv.KeyValue.Name,
				Type:   "key_value",
				Status: string(kv.KeyValue.Status),
			})
			c := kv.Cursor
			cursor = &c
		}

		if len(*resp.JSON200) < 20 {
			break
		}
	}

	return resources, nil
}

func addInventoryResource(s *server.MCPServer, c *client.ClientWithResponses) {
	cache := newInventoryCache(c)

	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			inventoryURI+"{?type,workspace}",
			"Render Infrastructure Inventory",
			mcp.WithTemplateDescription(
				"Live inventory of all Render resources accessible by the current API token. "+
					"Returns a JSON array grouped by workspace. Each resource includes: id, name, "+
					"type, status, workspace. Use resource IDs from this inventory when calling "+
					"render_get or render_act.\n\n"+
					"URI supports query-string filters for large accounts:\n"+
					"  render://inventory              — all resources\n"+
					"  render://inventory?type=postgres — filter by type\n"+
					"  render://inventory?type=web_service\n"+
					"  render://inventory?workspace=team-abc — filter by workspace\n\n"+
					"Valid type values: web_service, static_site, cron_job, background_worker, "+
					"private_service, postgres, key_value\n\n"+
					"Refreshes automatically. Lean by design — use render_get for full details "+
					"on any specific resource.",
			),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			typeFilter, workspaceFilter := parseInventoryFilters(request.Params.URI)

			data, err := cache.get(ctx, typeFilter, workspaceFilter)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch inventory: %w", err)
			}

			jsonData, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal inventory: %w", err)
			}

			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      request.Params.URI,
					MIMEType: "application/json",
					Text:     string(jsonData),
				},
			}, nil
		},
	)
}

func parseInventoryFilters(uri string) (typeFilter, workspaceFilter string) {
	parts := strings.SplitN(uri, "?", 2)
	if len(parts) < 2 {
		return "", ""
	}
	q, err := url.ParseQuery(parts[1])
	if err != nil {
		return "", ""
	}
	return q.Get("type"), q.Get("workspace")
}
