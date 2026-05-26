package sre

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/render-oss/render-mcp-server/pkg/client"
	logsclient "github.com/render-oss/render-mcp-server/pkg/client/logs"
	metricsclient "github.com/render-oss/render-mcp-server/pkg/client/metrics"
	"github.com/render-oss/render-mcp-server/pkg/pointers"
)

func renderGetTool(c *client.ClientWithResponses) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("render_get",
			mcp.WithDescription(renderGetDescription),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:           "Get Render resource",
				ReadOnlyHint:    pointers.From(true),
				DestructiveHint: pointers.From(false),
				IdempotentHint:  pointers.From(true),
			}),
			mcp.WithString("resourceId",
				mcp.Required(),
				mcp.Description("Render resource ID (e.g. srv-abc123, dpg-xyz789)"),
			),
			mcp.WithArray("include",
				mcp.Description("Data to include. Bare name or name?filters in URL query-string syntax. Valid names: env_vars, deploys, events, connections, domains, logs, metrics."),
				mcp.Items(map[string]interface{}{
					"type": "string",
				}),
			),
			mcp.WithString("window",
				mcp.Description("Time window for time-series includes. Default: 1h."),
				mcp.Enum("15m", "30m", "1h", "6h", "24h", "7d"),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleRenderGet(ctx, c, request)
		},
	}
}

func handleRenderGet(ctx context.Context, c *client.ClientWithResponses, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resourceId, err := request.RequireString("resourceId")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	includes := parseIncludes(request)
	window := parseWindow(request)

	response := make(map[string]interface{})

	base, err := fetchBaseConfig(ctx, c, resourceId)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to fetch resource %s: %s", resourceId, err.Error())), nil
	}
	response["config"] = base

	if len(includes) > 0 {
		ownerID := extractOwnerID(base)
		results := fetchIncludesParallel(ctx, c, resourceId, ownerID, includes, window)
		for k, v := range results {
			response[k] = v
		}
	}

	respJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(respJSON)), nil
}

type includeSpec struct {
	name    string
	filters url.Values
}

func parseIncludes(request mcp.CallToolRequest) []includeSpec {
	args := request.GetArguments()
	raw, ok := args["include"]
	if !ok || raw == nil {
		return nil
	}

	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var specs []includeSpec
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			continue
		}
		spec := includeSpec{}
		if idx := strings.IndexByte(s, '?'); idx >= 0 {
			spec.name = s[:idx]
			spec.filters, _ = url.ParseQuery(s[idx+1:])
		} else {
			spec.name = s
			spec.filters = url.Values{}
		}
		specs = append(specs, spec)
	}
	return specs
}

func parseWindow(request mcp.CallToolRequest) string {
	args := request.GetArguments()
	w, ok := args["window"].(string)
	if !ok || w == "" {
		return "1h"
	}
	return w
}

func windowToDuration(window string) time.Duration {
	switch window {
	case "15m":
		return 15 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	default:
		return time.Hour
	}
}

func fetchIncludesParallel(ctx context.Context, c *client.ClientWithResponses, resourceId, ownerID string, includes []includeSpec, window string) map[string]interface{} {
	var mu sync.Mutex
	results := make(map[string]interface{})
	var wg sync.WaitGroup

	for _, inc := range includes {
		wg.Add(1)
		go func(inc includeSpec) {
			defer wg.Done()
			data, err := fetchInclude(ctx, c, resourceId, ownerID, inc, window)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[inc.name] = map[string]interface{}{"error": err.Error()}
			} else {
				results[inc.name] = data
			}
		}(inc)
	}

	wg.Wait()
	return results
}

func fetchInclude(ctx context.Context, c *client.ClientWithResponses, resourceId, ownerID string, inc includeSpec, window string) (interface{}, error) {
	switch inc.name {
	case "env_vars":
		return fetchEnvVars(ctx, c, resourceId)
	case "deploys":
		return fetchDeploys(ctx, c, resourceId, window)
	case "events":
		return fetchEvents(ctx, c, resourceId, window)
	case "connections":
		return fetchConnections(ctx, c, resourceId)
	case "domains":
		return fetchDomains(ctx, c, resourceId)
	case "logs":
		return fetchLogs(ctx, c, resourceId, ownerID, inc.filters, window)
	case "metrics":
		return fetchMetrics(ctx, c, resourceId, ownerID, inc.filters, window)
	default:
		return nil, fmt.Errorf("unknown include: %s", inc.name)
	}
}

func fetchBaseConfig(ctx context.Context, c *client.ClientWithResponses, resourceId string) (interface{}, error) {
	prefix := resourcePrefix(resourceId)
	switch prefix {
	case "srv", "ssc", "crn":
		resp, err := c.RetrieveServiceWithResponse(ctx, resourceId)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		return resp.JSON200, nil
	case "dpg":
		resp, err := c.RetrievePostgresWithResponse(ctx, resourceId)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		return resp.JSON200, nil
	case "kv":
		resp, err := c.RetrieveKeyValueWithResponse(ctx, resourceId)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		return resp.JSON200, nil
	default:
		return nil, fmt.Errorf("unknown resource type for ID %s (prefix: %s)", resourceId, prefix)
	}
}

func fetchEnvVars(ctx context.Context, c *client.ClientWithResponses, resourceId string) (interface{}, error) {
	prefix := resourcePrefix(resourceId)
	switch prefix {
	case "srv", "ssc", "crn":
		resp, err := c.GetEnvVarsForServiceWithResponse(ctx, resourceId, &client.GetEnvVarsForServiceParams{})
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return []interface{}{}, nil
		}
		var envVars []map[string]string
		for _, ev := range *resp.JSON200 {
			envVars = append(envVars, map[string]string{
				"key":   ev.EnvVar.Key,
				"value": ev.EnvVar.Value,
			})
		}
		return envVars, nil
	default:
		return nil, fmt.Errorf("env_vars not supported for resource type %s", prefix)
	}
}

func fetchDeploys(ctx context.Context, c *client.ClientWithResponses, resourceId string, window string) (interface{}, error) {
	prefix := resourcePrefix(resourceId)
	if prefix != "srv" && prefix != "ssc" && prefix != "crn" {
		return nil, fmt.Errorf("deploys not supported for resource type %s", prefix)
	}

	limit := 20
	resp, err := c.ListDeploysWithResponse(ctx, resourceId, &client.ListDeploysParams{
		Limit: &limit,
	})
	if err != nil {
		return nil, err
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}
	return resp.JSON200, nil
}

func fetchEvents(ctx context.Context, c *client.ClientWithResponses, resourceId string, window string) (interface{}, error) {
	prefix := resourcePrefix(resourceId)
	if prefix != "srv" && prefix != "ssc" && prefix != "crn" {
		return nil, fmt.Errorf("events not supported for resource type %s", prefix)
	}

	dur := windowToDuration(window)
	from := time.Now().Add(-dur)
	fromParam := client.StartTimeParam(from)

	resp, err := c.ListEventsWithResponse(ctx, resourceId, &client.ListEventsParams{
		StartTime: &fromParam,
	})
	if err != nil {
		return nil, err
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}
	return resp.JSON200, nil
}

func fetchConnections(ctx context.Context, c *client.ClientWithResponses, resourceId string) (interface{}, error) {
	prefix := resourcePrefix(resourceId)
	switch prefix {
	case "dpg":
		resp, err := c.RetrievePostgresWithResponse(ctx, resourceId)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("no data returned")
		}
		pg := resp.JSON200
		return map[string]interface{}{
			"type":                    "postgres",
			"version":                 pg.Version,
			"plan":                    pg.Plan,
			"region":                  pg.Region,
			"highAvailabilityEnabled": pg.HighAvailabilityEnabled,
			"diskSizeGB":              pg.DiskSizeGB,
			"role":                    pg.Role,
			"status":                  pg.Status,
		}, nil
	case "kv":
		resp, err := c.RetrieveKeyValueWithResponse(ctx, resourceId)
		if err != nil {
			return nil, err
		}
		if err := client.ErrorFromResponse(resp); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return nil, fmt.Errorf("no data returned")
		}
		kv := resp.JSON200
		return map[string]interface{}{
			"type":   "key_value",
			"plan":   kv.Plan,
			"region": kv.Region,
			"status": kv.Status,
		}, nil
	default:
		return nil, fmt.Errorf("connections not supported for resource type %s", prefix)
	}
}

func fetchDomains(ctx context.Context, c *client.ClientWithResponses, resourceId string) (interface{}, error) {
	prefix := resourcePrefix(resourceId)
	if prefix != "srv" && prefix != "ssc" {
		return nil, fmt.Errorf("domains not supported for resource type %s", prefix)
	}

	resp, err := c.ListCustomDomainsWithResponse(ctx, resourceId, &client.ListCustomDomainsParams{})
	if err != nil {
		return nil, err
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}
	return resp.JSON200, nil
}

func fetchLogs(ctx context.Context, c *client.ClientWithResponses, resourceId, ownerID string, filters url.Values, window string) (interface{}, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("ownerId required for logs; could not determine owner from resource config")
	}

	dur := windowToDuration(window)
	startTime := client.StartTimeParam(time.Now().Add(-dur))
	endTime := client.EndTimeParam(time.Now())

	params := &client.ListLogsParams{
		OwnerId:   ownerID,
		Resource:  []string{resourceId},
		StartTime: &startTime,
		EndTime:   &endTime,
	}

	if v := filters.Get("level"); v != "" {
		params.Level = &[]string{v}
	}
	if v := filters.Get("text"); v != "" {
		params.Text = &[]string{v}
	}
	if v := filters.Get("path"); v != "" {
		params.Path = &[]string{v}
	}
	if v := filters.Get("method"); v != "" {
		params.Method = &[]string{v}
	}
	if v := filters.Get("statusCode"); v != "" {
		params.StatusCode = &[]string{v}
	}
	if v := filters.Get("host"); v != "" {
		params.Host = &[]string{v}
	}
	if v := filters.Get("type"); v != "" {
		params.Type = &[]string{v}
	}
	if v := filters.Get("limit"); v != "" {
		var limit int
		fmt.Sscanf(v, "%d", &limit)
		if limit > 0 {
			params.Limit = &limit
		}
	} else {
		defaultLimit := 100
		params.Limit = &defaultLimit
	}

	direction := logsclient.Backward
	params.Direction = &direction

	resp, err := c.ListLogsWithResponse(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}
	return resp.JSON200, nil
}

// metricsTimestampEditor rewrites startTime/endTime query params from RFC3339Nano
// with local timezone (as serialized by oapi-codegen) to RFC3339 in UTC with no
// fractional seconds, which is the format the Render metrics API accepts.
// See issue.md for details on the underlying client bug.
func metricsTimestampEditor(_ context.Context, req *http.Request) error {
	q := req.URL.Query()
	for _, key := range []string{"startTime", "endTime"} {
		if v := q.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				continue
			}
			q.Set(key, t.UTC().Truncate(time.Second).Format(time.RFC3339))
		}
	}
	req.URL.RawQuery = q.Encode()
	return nil
}

func fetchMetrics(ctx context.Context, c *client.ClientWithResponses, resourceId, _ string, filters url.Values, window string) (interface{}, error) {
	dur := windowToDuration(window)
	now := time.Now()
	startTime := client.StartTimeParam(now.Add(-dur))
	endTime := client.EndTimeParam(now)

	metricTypeStrs := []string{"cpu_usage", "memory_usage"}
	if v := filters.Get("types"); v != "" {
		metricTypeStrs = strings.Split(v, ",")
	}

	var resolution *metricsclient.ResolutionParam
	if v := filters.Get("resolution"); v != "" {
		var r float64
		fmt.Sscanf(v, "%f", &r)
		rp := metricsclient.ResolutionParam(float32(r))
		resolution = &rp
	}

	var aggregation *metricsclient.ApplicationMetricAggregationMethod
	if v := filters.Get("aggregation"); v != "" {
		agg := metricsclient.ApplicationMetricAggregationMethod(v)
		aggregation = &agg
	}

	var latencyQuantile *metricsclient.Quantile
	if v := filters.Get("latencyQuantile"); v != "" {
		var q float64
		fmt.Sscanf(v, "%f", &q)
		qt := metricsclient.Quantile(q)
		latencyQuantile = &qt
	} else {
		defaultQ := metricsclient.Quantile(0.95)
		latencyQuantile = &defaultQ
	}

	var httpHost *metricsclient.HostQueryParam
	if v := filters.Get("httpHost"); v != "" {
		h := metricsclient.HostQueryParam(v)
		httpHost = &h
	}

	var httpPath *metricsclient.PathQueryParam
	if v := filters.Get("httpPath"); v != "" {
		p := metricsclient.PathQueryParam(v)
		httpPath = &p
	}

	resource := metricsclient.ResourceQueryParam(resourceId)
	serviceResource := metricsclient.ServiceResourceQueryParam(resourceId)
	datastoreResource := metricsclient.DatastoreResourceQueryParam(resourceId)

	type metricResult struct {
		Type string                              `json:"type"`
		Data metricsclient.TimeSeriesCollection  `json:"data"`
	}

	response := struct {
		ResourceID string         `json:"resourceId"`
		TimeRange  struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		} `json:"timeRange"`
		Metrics []metricResult `json:"metrics"`
	}{
		ResourceID: resourceId,
		Metrics:    []metricResult{},
	}
	response.TimeRange.Start = startTime.Unix()
	response.TimeRange.End = endTime.Unix()

	for _, mtStr := range metricTypeStrs {
		mt := strings.TrimSpace(mtStr)
		data, err := fetchSingleMetric(ctx, c, mt, resource, serviceResource, datastoreResource,
			&startTime, &endTime, resolution, aggregation, latencyQuantile, httpHost, httpPath)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch %s metrics: %w", mt, err)
		}
		response.Metrics = append(response.Metrics, metricResult{Type: mt, Data: data})
	}

	return response, nil
}

func fetchSingleMetric(
	ctx context.Context,
	c *client.ClientWithResponses,
	metricType string,
	resource metricsclient.ResourceQueryParam,
	serviceResource metricsclient.ServiceResourceQueryParam,
	datastoreResource metricsclient.DatastoreResourceQueryParam,
	startTime *client.StartTimeParam,
	endTime *client.EndTimeParam,
	resolution *metricsclient.ResolutionParam,
	aggregation *metricsclient.ApplicationMetricAggregationMethod,
	latencyQuantile *metricsclient.Quantile,
	httpHost *metricsclient.HostQueryParam,
	httpPath *metricsclient.PathQueryParam,
) (metricsclient.TimeSeriesCollection, error) {
	ed := metricsTimestampEditor

	switch metricType {
	case "cpu_usage":
		resp, err := c.GetCpuWithResponse(ctx, &client.GetCpuParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
			AggregationMethod: aggregation,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "memory_usage":
		resp, err := c.GetMemoryWithResponse(ctx, &client.GetMemoryParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "cpu_limit":
		resp, err := c.GetCpuLimitWithResponse(ctx, &client.GetCpuLimitParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "cpu_target":
		resp, err := c.GetCpuTargetWithResponse(ctx, &client.GetCpuTargetParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "memory_limit":
		resp, err := c.GetMemoryLimitWithResponse(ctx, &client.GetMemoryLimitParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "memory_target":
		resp, err := c.GetMemoryTargetWithResponse(ctx, &client.GetMemoryTargetParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "instance_count":
		resp, err := c.GetInstanceCountWithResponse(ctx, &client.GetInstanceCountParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &resource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "http_request_count":
		resp, err := c.GetHttpRequestsWithResponse(ctx, &client.GetHttpRequestsParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &serviceResource,
			Host: httpHost, Path: httpPath,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "http_latency":
		resp, err := c.GetHttpLatencyWithResponse(ctx, &client.GetHttpLatencyParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &serviceResource,
			Quantile: latencyQuantile, Host: httpHost, Path: httpPath,
		}, ed)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode() == 400 {
			return metricsclient.TimeSeriesCollection{}, nil
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "bandwidth_usage":
		resp, err := c.GetBandwidthWithResponse(ctx, &client.GetBandwidthParams{
			StartTime: startTime, EndTime: endTime,
			Resource: &serviceResource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	case "active_connections":
		resp, err := c.GetActiveConnectionsWithResponse(ctx, &client.GetActiveConnectionsParams{
			StartTime: startTime, EndTime: endTime,
			ResolutionSeconds: resolution, Resource: &datastoreResource,
		}, ed)
		if err != nil {
			return nil, err
		}
		return extractTimeSeries(resp.StatusCode(), resp.Body, resp.JSON200)

	default:
		return nil, fmt.Errorf("unsupported metric type: %s", metricType)
	}
}

func extractTimeSeries(statusCode int, body []byte, data *metricsclient.TimeSeriesCollection) (metricsclient.TimeSeriesCollection, error) {
	if statusCode != 200 {
		return nil, fmt.Errorf("API returned status %d: %s", statusCode, string(body))
	}
	if data == nil {
		return metricsclient.TimeSeriesCollection{}, nil
	}
	return *data, nil
}

func resourcePrefix(id string) string {
	if idx := strings.IndexByte(id, '-'); idx > 0 {
		return id[:idx]
	}
	return ""
}

func extractOwnerID(baseConfig interface{}) string {
	m, ok := baseConfig.(map[string]interface{})
	if ok {
		if id, ok := m["ownerId"].(string); ok && id != "" {
			return id
		}
	}

	data, err := json.Marshal(baseConfig)
	if err != nil {
		return ""
	}
	var generic struct {
		OwnerID string `json:"ownerId"`
		Owner   struct {
			ID string `json:"id"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(data, &generic); err != nil {
		return ""
	}
	if generic.OwnerID != "" {
		return generic.OwnerID
	}
	return generic.Owner.ID
}

const renderGetDescription = `Get information about any Render resource. Always returns base config (name,
type, status, plan, region, runtime, URLs, scaling). Use include to fetch
additional data — each include can have URL query-string filters appended.

When multiple includes are requested, they are fetched in parallel server-side.

Parameters:
  resourceId (string, required): Render resource ID. Prefixes: srv- (web service),
    ssc- (static site), crn- (cron job), dpg- (postgres), kv- (key-value).
    Get IDs from the render://inventory resource.
  include (string[], optional): Data to include beyond base config. Each element
    is a bare name or name?filter=value&filter=value using URL query-string syntax.
    Valid include names:
      "env_vars"    — environment variables
      "deploys"     — recent deploy history (id, status, commit, timestamps, trigger)
      "events"      — recent events (scaling, config changes, suspend/resume)
      "connections"  — database/KV connection metadata (no credentials)
      "domains"     — custom domains and verification status
      "logs"        — application, request, and build logs
        Filters: level (info|warn|error), text (string match), path, method
        (GET|POST|PUT|PATCH|DELETE), statusCode (e.g. 500, 5xx, 4xx), host,
        type (app|request|build), limit (int, default 100)
      "metrics"     — time-series performance data
        Filters: types (comma-separated: cpu_usage, cpu_limit, cpu_target,
        memory_usage, memory_limit, memory_target, instance_count,
        http_request_count, http_latency, bandwidth_usage, active_connections),
        resolution (seconds, default 60, min 30), httpPath, httpHost,
        aggregation (AVG|MAX|MIN), latencyQuantile (0.0-1.0, default 0.95)
    Examples:
      "logs"
      "logs?level=error&limit=50"
      "metrics?types=cpu_usage,memory_usage,http_latency"
      "logs?level=error&statusCode=5xx&text=timeout"
  window (string, optional): Time window for logs, metrics, deploys, events.
    Valid values: "15m", "30m", "1h" (default), "6h", "24h", "7d"`
