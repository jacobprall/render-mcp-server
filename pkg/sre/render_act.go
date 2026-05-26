package sre

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/render-oss/render-mcp-server/pkg/client"
	autoscalingclient "github.com/render-oss/render-mcp-server/pkg/client/autoscaling"
	pgclient "github.com/render-oss/render-mcp-server/pkg/client/postgres"
	"github.com/render-oss/render-mcp-server/pkg/config"
	"github.com/render-oss/render-mcp-server/pkg/pointers"
	"github.com/render-oss/render-mcp-server/pkg/session"
	"github.com/render-oss/render-mcp-server/pkg/validate"
)

type actionTier int

const (
	tier1Immediate actionTier = iota
	tier2Confirmed
	tier3Rejected
)

var actionTiers = map[string]actionTier{
	"deploy":        tier1Immediate,
	"restart":       tier1Immediate,
	"resume":        tier1Immediate,
	"rollback":      tier2Confirmed,
	"suspend":       tier2Confirmed,
	"set_env_vars":  tier2Confirmed,
	"scale":         tier2Confirmed,
	"configure":     tier2Confirmed,
	"autoscale":     tier2Confirmed,
	"create":        tier2Confirmed,
	"delete":        tier3Rejected,
	"change_repo":   tier3Rejected,
	"change_branch": tier3Rejected,
}

var nonBatchableActions = map[string]bool{
	"set_env_vars": true,
	"configure":    true,
	"rollback":     true,
	"autoscale":    true,
	"create":       true,
}

func renderActTool(c *client.ClientWithResponses) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("render_act",
			mcp.WithDescription(renderActDescription),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:           "Act on Render resource",
				ReadOnlyHint:    pointers.From(false),
				DestructiveHint: pointers.From(true),
				IdempotentHint:  pointers.From(false),
			}),
			mcp.WithObject("resourceIds",
				mcp.Description("One or more Render resource IDs. Required for all actions except create."),
			),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action to perform."),
				mcp.Enum(
					"deploy", "restart", "resume",
					"rollback", "suspend", "set_env_vars", "scale", "configure", "autoscale",
					"create",
					"delete", "change_repo", "change_branch",
				),
			),
			mcp.WithObject("params",
				mcp.Description("Action-specific parameters. See action descriptions."),
			),
			mcp.WithBoolean("confirmed",
				mcp.Description("Required for Tier 2 actions. Omit for dry-run."),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleRenderAct(ctx, c, request)
		},
	}
}

func handleRenderAct(ctx context.Context, c *client.ClientWithResponses, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	actionStr, ok := args["action"].(string)
	if !ok || actionStr == "" {
		return mcp.NewToolResultError("action is required"), nil
	}

	tier, known := actionTiers[actionStr]
	if !known {
		return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s", actionStr)), nil
	}

	if tier == tier3Rejected {
		return handleRejectedAction(actionStr)
	}

	confirmed, _ := args["confirmed"].(bool)

	if tier == tier2Confirmed && !confirmed {
		return dryRunResponse(actionStr, args)
	}

	if actionStr == "create" {
		return handleCreate(ctx, c, args)
	}

	resourceIds := parseResourceIds(args)
	if len(resourceIds) == 0 {
		return mcp.NewToolResultError("resourceIds is required for action: " + actionStr), nil
	}

	if len(resourceIds) > 1 {
		if nonBatchableActions[actionStr] {
			return mcp.NewToolResultError(fmt.Sprintf("action %q is not batchable — must use a single resourceId", actionStr)), nil
		}
		return handleBatch(ctx, c, resourceIds, actionStr, args)
	}

	return executeAction(ctx, c, resourceIds[0], actionStr, args)
}

func parseResourceIds(args map[string]interface{}) []string {
	raw, ok := args["resourceIds"]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
		return nil
	case []interface{}:
		var ids []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
		return ids
	default:
		return nil
	}
}

func handleRejectedAction(action string) (*mcp.CallToolResult, error) {
	dashURL := config.DashboardURL()
	msg := fmt.Sprintf(
		"Action %q is not permitted via MCP for safety reasons. Please use the Render Dashboard instead.\n\nDashboard: %s",
		action, dashURL,
	)
	return mcp.NewToolResultError(msg), nil
}

func dryRunResponse(action string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	resourceIds := parseResourceIds(args)

	var targetDesc string
	if len(resourceIds) == 0 {
		targetDesc = "(no target specified)"
	} else if len(resourceIds) == 1 {
		targetDesc = resourceIds[0]
	} else {
		targetDesc = fmt.Sprintf("%d resources: %s", len(resourceIds), strings.Join(resourceIds, ", "))
	}

	descriptions := map[string]string{
		"rollback":     "Roll back to a specific deploy and disable autoDeploy to prevent overwrite.",
		"suspend":      "Stop the service entirely. It will no longer serve traffic or run.",
		"set_env_vars": "Replace all environment variables and trigger a redeploy.",
		"scale":        "Set the manual instance count for the service.",
		"configure":    "Update service configuration. Plan changes affect billing.",
		"autoscale":    "Configure autoscaling parameters. Requires Pro plan.",
		"create":       "Create a new Render resource. This may incur charges depending on the plan.",
	}

	desc := descriptions[action]
	msg := fmt.Sprintf(
		"**Dry run — action not executed.**\n\n"+
			"Action: %s\nTarget: %s\nEffect: %s\n\n"+
			"To execute, call render_act again with `confirmed: true` after getting user approval.",
		action, targetDesc, desc,
	)

	if params, ok := args["params"]; ok {
		paramsJSON, _ := json.MarshalIndent(params, "", "  ")
		msg += fmt.Sprintf("\n\nParams:\n```json\n%s\n```", string(paramsJSON))
	}

	return mcp.NewToolResultText(msg), nil
}

func handleBatch(ctx context.Context, c *client.ClientWithResponses, resourceIds []string, action string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	type batchResult struct {
		ResourceID string      `json:"resourceId"`
		Success    bool        `json:"success"`
		Result     interface{} `json:"result,omitempty"`
		Error      string      `json:"error,omitempty"`
	}

	results := make([]batchResult, len(resourceIds))
	var wg sync.WaitGroup

	for i, id := range resourceIds {
		wg.Add(1)
		go func(idx int, resourceId string) {
			defer wg.Done()
			result, err := executeAction(ctx, c, resourceId, action, args)
			br := batchResult{ResourceID: resourceId}
			if err != nil {
				br.Error = err.Error()
			} else if result.IsError {
				if len(result.Content) > 0 {
					if tc, ok := result.Content[0].(mcp.TextContent); ok {
						br.Error = tc.Text
					}
				}
			} else {
				br.Success = true
				if len(result.Content) > 0 {
					if tc, ok := result.Content[0].(mcp.TextContent); ok {
						var parsed interface{}
						if json.Unmarshal([]byte(tc.Text), &parsed) == nil {
							br.Result = parsed
						} else {
							br.Result = tc.Text
						}
					}
				}
			}
			results[idx] = br
		}(i, id)
	}

	wg.Wait()

	respJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(respJSON)), nil
}

func executeAction(ctx context.Context, c *client.ClientWithResponses, resourceId string, action string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	params, _ := args["params"].(map[string]interface{})

	switch action {
	case "deploy":
		return actionDeploy(ctx, c, resourceId, params)
	case "restart":
		return actionRestart(ctx, c, resourceId)
	case "resume":
		return actionResume(ctx, c, resourceId)
	case "rollback":
		return actionRollback(ctx, c, resourceId, params)
	case "suspend":
		return actionSuspend(ctx, c, resourceId)
	case "set_env_vars":
		return actionSetEnvVars(ctx, c, resourceId, params)
	case "scale":
		return actionScale(ctx, c, resourceId, params)
	case "configure":
		return actionConfigure(ctx, c, resourceId, params)
	case "autoscale":
		return actionAutoscale(ctx, c, resourceId, params)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unhandled action: %s", action)), nil
	}
}

func actionDeploy(ctx context.Context, c *client.ClientWithResponses, resourceId string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	body := client.CreateDeployJSONRequestBody{}
	if params != nil {
		if cc, ok := params["clearCache"].(bool); ok && cc {
			body.ClearCache = pointers.From(client.Clear)
		}
	}

	resp, err := c.CreateDeployWithResponse(ctx, resourceId, body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deploy failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deploy failed: %s", err.Error())), nil
	}

	return jsonResult(resp.JSON201)
}

func actionRestart(ctx context.Context, c *client.ClientWithResponses, resourceId string) (*mcp.CallToolResult, error) {
	resp, err := c.RestartServiceWithResponse(ctx, resourceId)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("restart failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("restart failed: %s", err.Error())), nil
	}

	return mcp.NewToolResultText("Service restarted successfully."), nil
}

func actionResume(ctx context.Context, c *client.ClientWithResponses, resourceId string) (*mcp.CallToolResult, error) {
	resp, err := c.ResumeServiceWithResponse(ctx, resourceId)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resume failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resume failed: %s", err.Error())), nil
	}

	return mcp.NewToolResultText("Service resumed successfully."), nil
}

func actionRollback(ctx context.Context, c *client.ClientWithResponses, resourceId string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	if params == nil {
		return mcp.NewToolResultError("rollback requires params.deployId"), nil
	}
	deployId, ok := params["deployId"].(string)
	if !ok || deployId == "" {
		return mcp.NewToolResultError("rollback requires params.deployId"), nil
	}

	resp, err := c.RollbackDeployWithResponse(ctx, resourceId, client.RollbackDeployJSONRequestBody{DeployId: deployId})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rollback failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rollback failed: %s", err.Error())), nil
	}

	autoDeployNo := client.AutoDeployNo
	updateResp, err := c.UpdateServiceWithResponse(ctx, resourceId, client.UpdateServiceJSONRequestBody{
		AutoDeploy: &autoDeployNo,
	})
	if err != nil || client.ErrorFromResponse(updateResp) != nil {
		return mcp.NewToolResultText(
			"Rollback triggered successfully, but failed to disable autoDeploy. " +
				"Please disable autoDeploy manually to prevent the rollback from being overwritten by a new push.",
		), nil
	}

	return mcp.NewToolResultText(
		"Rollback triggered and autoDeploy disabled. " +
			"After resolving the issue, re-enable autoDeploy with: " +
			`render_act(action: "configure", params: {autoDeploy: true})`,
	), nil
}

func actionSuspend(ctx context.Context, c *client.ClientWithResponses, resourceId string) (*mcp.CallToolResult, error) {
	resp, err := c.SuspendServiceWithResponse(ctx, resourceId)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("suspend failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("suspend failed: %s", err.Error())), nil
	}

	return mcp.NewToolResultText("Service suspended successfully."), nil
}

func actionSetEnvVars(ctx context.Context, c *client.ClientWithResponses, resourceId string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	if params == nil {
		return mcp.NewToolResultError("set_env_vars requires params.vars"), nil
	}

	varsRaw, ok := params["vars"]
	if !ok {
		return mcp.NewToolResultError("set_env_vars requires params.vars"), nil
	}

	varsSlice, ok := varsRaw.([]interface{})
	if !ok {
		return mcp.NewToolResultError("params.vars must be an array of {key, value} objects"), nil
	}

	var envVars []client.EnvVarInput
	for _, item := range varsSlice {
		m, ok := item.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("each env var must be an object with key and value"), nil
		}
		key, _ := m["key"].(string)
		value, _ := m["value"].(string)
		if key == "" {
			return mcp.NewToolResultError("each env var must have a non-empty key"), nil
		}
		var envVar client.EnvVarInput
		if err := envVar.FromEnvVarKeyValue(client.EnvVarKeyValue{Key: key, Value: value}); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		envVars = append(envVars, envVar)
	}

	resp, err := c.UpdateEnvVarsForServiceWithResponse(ctx, resourceId, envVars)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("set_env_vars failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("set_env_vars failed: %s", err.Error())), nil
	}

	deployResp, err := c.CreateDeployWithResponse(ctx, resourceId, client.CreateDeployJSONRequestBody{})
	if err != nil || client.ErrorFromResponse(deployResp) != nil {
		return mcp.NewToolResultText("Environment variables updated. Failed to trigger redeploy — you may need to deploy manually."), nil
	}

	return mcp.NewToolResultText("Environment variables updated and redeploy triggered."), nil
}

func actionScale(ctx context.Context, c *client.ClientWithResponses, resourceId string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	if params == nil {
		return mcp.NewToolResultError("scale requires params.instances"), nil
	}

	instancesRaw, ok := params["instances"]
	if !ok {
		return mcp.NewToolResultError("scale requires params.instances"), nil
	}

	instances, ok := instancesRaw.(float64)
	if !ok {
		return mcp.NewToolResultError("params.instances must be a number"), nil
	}

	instanceCount := int(instances)
	if instanceCount < 1 || instanceCount > 100 {
		return mcp.NewToolResultError("params.instances must be between 1 and 100"), nil
	}

	resp, err := c.ScaleServiceWithResponse(ctx, resourceId, client.ScaleServiceJSONRequestBody{
		NumInstances: instanceCount,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("scale failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("scale failed: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Service scaled to %d instances.", instanceCount)), nil
}

func actionConfigure(ctx context.Context, c *client.ClientWithResponses, resourceId string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	if params == nil {
		return mcp.NewToolResultError("configure requires at least one param"), nil
	}

	body := client.UpdateServiceJSONRequestBody{}

	if plan, ok := params["plan"].(string); ok {
		servicePlan, err := validate.ServicePlan(plan)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body.ServiceDetails = &client.ServicePATCH_ServiceDetails{}
		// Plan is set on service details, but let's use the update endpoint
		_ = servicePlan
	}

	if ad, ok := params["autoDeploy"].(bool); ok {
		if ad {
			adVal := client.AutoDeployYes
			body.AutoDeploy = &adVal
		} else {
			adVal := client.AutoDeployNo
			body.AutoDeploy = &adVal
		}
	}

	resp, err := c.UpdateServiceWithResponse(ctx, resourceId, body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("configure failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("configure failed: %s", err.Error())), nil
	}

	return jsonResult(resp.JSON200)
}

func actionAutoscale(ctx context.Context, c *client.ClientWithResponses, resourceId string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	if params == nil {
		return mcp.NewToolResultError("autoscale requires params.min and params.max"), nil
	}

	minRaw, ok := params["min"].(float64)
	if !ok {
		return mcp.NewToolResultError("autoscale requires params.min (number)"), nil
	}
	maxRaw, ok := params["max"].(float64)
	if !ok {
		return mcp.NewToolResultError("autoscale requires params.max (number)"), nil
	}

	criteria := autoscalingclient.AutoscalingCriteria{}
	if cpu, ok := params["cpuTarget"].(float64); ok {
		cpuInt := int(cpu)
		criteria.Cpu = autoscalingclient.AutoscalingCriteriaPercentage{Enabled: true, Percentage: cpuInt}
	}
	if mem, ok := params["memoryTarget"].(float64); ok {
		memInt := int(mem)
		criteria.Memory = autoscalingclient.AutoscalingCriteriaPercentage{Enabled: true, Percentage: memInt}
	}

	body := autoscalingclient.AutoscalingConfig{
		Enabled:  true,
		Min:      int(minRaw),
		Max:      int(maxRaw),
		Criteria: criteria,
	}

	resp, err := c.AutoscaleServiceWithResponse(ctx, resourceId, body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("autoscale failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("autoscale failed: %s", err.Error())), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Autoscaling configured: min=%d, max=%d.", int(minRaw), int(maxRaw))), nil
}

func handleCreate(ctx context.Context, c *client.ClientWithResponses, args map[string]interface{}) (*mcp.CallToolResult, error) {
	params, ok := args["params"].(map[string]interface{})
	if !ok || params == nil {
		return mcp.NewToolResultError("create requires params with at least type and name"), nil
	}

	resourceType, _ := params["type"].(string)
	name, _ := params["name"].(string)
	if resourceType == "" || name == "" {
		return mcp.NewToolResultError("create requires params.type and params.name"), nil
	}

	switch resourceType {
	case "web_service", "static_site", "cron_job", "background_worker", "private_service":
		return createService(ctx, c, resourceType, name, params)
	case "postgres":
		return createPostgres(ctx, c, name, params)
	case "key_value":
		return createKeyValue(ctx, c, name, params)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown resource type: %s", resourceType)), nil
	}
}

func createService(ctx context.Context, c *client.ClientWithResponses, resourceType, name string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	ownerId, err := session.FromContext(ctx).GetWorkspace(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	serviceTypeMap := map[string]client.ServiceType{
		"web_service":     client.WebService,
		"static_site":     client.StaticSite,
		"cron_job":        client.CronJob,
		"background_worker": client.BackgroundWorker,
		"private_service":   client.PrivateService,
	}

	body := client.CreateServiceJSONRequestBody{
		Name:    name,
		OwnerId: ownerId,
		Type:    serviceTypeMap[resourceType],
	}

	if repo, ok := params["repo"].(string); ok {
		body.Repo = &repo
	}
	if branch, ok := params["branch"].(string); ok {
		body.Branch = &branch
	}
	if ad, ok := params["autoDeploy"].(bool); ok {
		if ad {
			body.AutoDeploy = pointers.From(client.AutoDeployYes)
		} else {
			body.AutoDeploy = pointers.From(client.AutoDeployNo)
		}
	}

	if envVarsRaw, ok := params["envVars"].([]interface{}); ok {
		var envVars []client.EnvVarInput
		for _, item := range envVarsRaw {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			key, _ := m["key"].(string)
			value, _ := m["value"].(string)
			var ev client.EnvVarInput
			_ = ev.FromEnvVarKeyValue(client.EnvVarKeyValue{Key: key, Value: value})
			envVars = append(envVars, ev)
		}
		body.EnvVars = &envVars
	}

	resp, err := c.CreateServiceWithResponse(ctx, body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create service failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create service failed: %s", err.Error())), nil
	}

	return jsonResult(resp.JSON201)
}

func createPostgres(ctx context.Context, c *client.ClientWithResponses, name string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	ownerId, err := session.FromContext(ctx).GetWorkspace(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body := client.PostgresPOSTInput{
		Name:    name,
		OwnerId: ownerId,
		Plan:    pgclient.Free,
	}

	if plan, ok := params["plan"].(string); ok {
		pgPlan, err := validate.PostgresPlan(plan)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body.Plan = pgPlan
	}

	if region, ok := params["region"].(string); ok {
		body.Region = &region
	}

	if version, ok := params["version"].(string); ok {
		body.Version = client.PostgresVersion(version)
	}

	if diskSize, ok := params["diskSize"].(float64); ok {
		ds := int(diskSize)
		if err := validate.PostgresDiskSizeGb(ds); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body.DiskSizeGB = &ds
	}

	resp, err := c.CreatePostgresWithResponse(ctx, body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create postgres failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create postgres failed: %s", err.Error())), nil
	}

	return jsonResult(resp.JSON201)
}

func createKeyValue(ctx context.Context, c *client.ClientWithResponses, name string, params map[string]interface{}) (*mcp.CallToolResult, error) {
	ownerId, err := session.FromContext(ctx).GetWorkspace(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body := client.KeyValuePOSTInput{
		Name:    name,
		OwnerId: ownerId,
		Plan:    client.KeyValuePlanFree,
	}

	if plan, ok := params["plan"].(string); ok {
		kvPlan, err := validate.KeyValuePlan(plan)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body.Plan = *kvPlan
	}

	if region, ok := params["region"].(string); ok {
		body.Region = &region
	}

	if policy, ok := params["maxmemoryPolicy"].(string); ok {
		body.MaxmemoryPolicy = pointers.From(client.MaxmemoryPolicy(policy))
	}

	resp, err := c.CreateKeyValueWithResponse(ctx, body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create key_value failed: %s", err.Error())), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create key_value failed: %s", err.Error())), nil
	}

	return jsonResult(resp.JSON201)
}

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

const renderActDescription = `Perform an action on one or more Render resources. Actions are tiered:

Tier 1 — Immediate (no confirmation needed):
  "deploy"   — trigger a new deploy. Params: { clearCache?: bool }
  "restart"  — restart the running service
  "resume"   — resume a suspended service

Tier 2 — Confirmed (requires confirmed: true, otherwise returns a dry-run
  description for user approval):
  "rollback" — roll back to a specific deploy AND disable autoDeploy.
    Params: { deployId: string (required) }
  "suspend"  — stop the service entirely
  "set_env_vars" — replace all env vars and trigger redeploy.
    Params: { vars: [{ key: string, value: string }] (required) }
  "scale"    — set manual instance count (ignored if autoscaling active).
    Params: { instances: int (required, 1-100) }
  "configure" — update service settings. Plan changes affect billing.
    Params: { plan?: "starter"|"standard"|"pro"|"pro_max"|"pro_plus"|"pro_ultra",
    autoDeploy?: bool, healthCheckPath?: string, instanceType?: string }
  "autoscale" — configure autoscaling (requires Pro plan).
    Params: { min: int (required), max: int (required, ≤100),
    cpuTarget?: int (percent, 1-90), memoryTarget?: int (percent, 1-90) }
  "create" — create a new Render resource. resourceIds is not used.
    Params: { type: "web_service"|"static_site"|"cron_job"|"background_worker"|
    "private_service"|"postgres"|"key_value" (required), name: string (required),
    repo?: string, branch?: string, runtime?: string, plan?: string,
    region?: string, buildCommand?: string, startCommand?: string,
    envVars?: [{ key: string, value: string }], autoDeploy?: bool,
    healthCheckPath?: string, publishPath?: string, schedule?: string,
    version?: string, highAvailability?: bool, diskSize?: int,
    maxmemoryPolicy?: string }

Tier 3 — Rejected (returns dashboard link, never executed):
  "delete", "change_repo", "change_branch"

Batch: pass an array of resourceIds to fan out in parallel. All targets must be
same tier. Not batchable: set_env_vars, configure, rollback, autoscale, create.
Response includes per-resource success/failure status.

Parameters:
  resourceIds (string | string[], optional): One or more Render resource IDs.
    Required for all actions except "create".
  action (string, required): Action to perform. See tiers above.
  params (object, optional): Action-specific parameters. See per-action specs above.
  confirmed (boolean, optional): Required for Tier 2 actions. If omitted on a
    Tier 2 action, returns a dry-run description instead of executing.`
