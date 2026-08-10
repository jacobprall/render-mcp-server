package service

import (
	"context"

	"github.com/render-oss/render-mcp-server/pkg/client"
	"github.com/render-oss/render-mcp-server/pkg/pointers"
	"github.com/render-oss/render-mcp-server/pkg/session"
	"github.com/render-oss/render-mcp-server/pkg/validate"
)

// serviceRepoClient is the minimal set of generated Render API client methods
// that the service Repo depends on. Defining it as an interface allows a
// counterfeiter-generated fake to be used in tests.
//go:generate go tool counterfeiter -o ../fakes/fakeservicerepoclient_gen.go . serviceRepoClient
type serviceRepoClient interface {
	ListServicesWithResponse(ctx context.Context, params *client.ListServicesParams, reqEditors ...client.RequestEditorFn) (*client.ListServicesResponse, error)
	GetEnvVarsForServiceWithResponse(ctx context.Context, serviceId string, params *client.GetEnvVarsForServiceParams, reqEditors ...client.RequestEditorFn) (*client.GetEnvVarsForServiceResponse, error)
	UpdateEnvVarsForServiceWithResponse(ctx context.Context, serviceId string, body []client.EnvVarInput, reqEditors ...client.RequestEditorFn) (*client.UpdateEnvVarsForServiceResponse, error)
	CreateDeployWithResponse(ctx context.Context, serviceId string, body client.CreateDeployJSONRequestBody, reqEditors ...client.RequestEditorFn) (*client.CreateDeployResponse, error)
	CreateServiceWithResponse(ctx context.Context, data client.CreateServiceJSONRequestBody, reqEditors ...client.RequestEditorFn) (*client.CreateServiceResponse, error)
	RetrieveServiceWithResponse(ctx context.Context, id string, reqEditors ...client.RequestEditorFn) (*client.RetrieveServiceResponse, error)
}

// Repo wraps the Render API client to provide service-related operations
// scoped to the current workspace.
type Repo struct {
	client serviceRepoClient
}

// NewRepo returns a Repo that wraps the given Render API client to provide
// service-related operations scoped to the current workspace.
func NewRepo(c serviceRepoClient) *Repo {
	return &Repo{
		client: c,
	}
}

// ListServices scopes the query to the caller's workspace (taken from the
// session context) via OwnerId, then uses client.ListAll to page through all
// results.
func (s *Repo) ListServices(ctx context.Context, params *client.ListServicesParams) ([]*client.Service, error) {
	workspace, err := session.FromContext(ctx).GetWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	if workspace != "" {
		params.OwnerId = pointers.From([]string{workspace})
	}

	return client.ListAll(ctx, params, s.listPage)
}

// listPage fetches a single page of services and returns the last item's
// cursor for pagination, returning a nil cursor when there are no more results.
func (s *Repo) listPage(ctx context.Context, params *client.ListServicesParams) ([]*client.Service, *client.Cursor, error) {
	resp, err := s.client.ListServicesWithResponse(ctx, params)
	if err != nil {
		return nil, nil, err
	}

	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, nil, err
	}
	if resp.JSON200 == nil || len(*resp.JSON200) == 0 {
		return nil, nil, nil
	}

	res := *resp.JSON200
	services := make([]*client.Service, 0, len(res))
	for _, serviceWithCursor := range res {
		services = append(services, &serviceWithCursor.Service)
	}

	return services, &res[len(res)-1].Cursor, nil
}

type ListEnvParams struct {
	*client.GetEnvVarsForServiceParams
	serviceId string
}

// ListEnvVars uses client.ListAll to page through and return all environment
// variables for the given service.
func (s *Repo) ListEnvVars(ctx context.Context, serviceId string, params *client.GetEnvVarsForServiceParams) ([]*client.EnvVar, error) {
	listEnvParams := &ListEnvParams{
		GetEnvVarsForServiceParams: params,
		serviceId:                  serviceId,
	}
	return client.ListAll(ctx, listEnvParams, s.listEnvVarsPage)
}

// listEnvVarsPage fetches a single page of environment variables and returns
// the last item's cursor for pagination, returning a nil cursor when there are
// no more results.
func (s *Repo) listEnvVarsPage(ctx context.Context, params *ListEnvParams) ([]*client.EnvVar, *client.Cursor, error) {
	resp, err := s.client.GetEnvVarsForServiceWithResponse(ctx, params.serviceId, params.GetEnvVarsForServiceParams)
	if err != nil {
		return nil, nil, err
	}

	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, nil, err
	}
	if resp.JSON200 == nil || len(*resp.JSON200) == 0 {
		return nil, nil, nil
	}

	res := *resp.JSON200
	envVars := make([]*client.EnvVar, 0, len(res))
	for _, envVarsWithCursor := range res {
		envVars = append(envVars, &envVarsWithCursor.EnvVar)
	}

	return envVars, &res[len(res)-1].Cursor, nil
}

// UpdateEnvVars first calls GetService to validate that the service belongs to
// the current workspace before updating its environment variables.
func (s *Repo) UpdateEnvVars(ctx context.Context, serviceId string, envVars []client.EnvVarInput) (*client.UpdateEnvVarsForServiceResponse, error) {
	// validate that the service belongs to the workspace
	_, err := s.GetService(ctx, serviceId)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.UpdateEnvVarsForServiceWithResponse(ctx, serviceId, envVars)
	if err != nil {
		return nil, err
	}

	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// DeployService triggers a new deploy for the given service. Workspace
// validation is intentionally skipped here because it is expected to happen
// before this call.
func (s *Repo) DeployService(ctx context.Context, serviceId string) (*client.CreateDeployResponse, error) {
	// Skip validation of the service belongs to the workspace because it should be done before the
	// call to DeployService.
	resp, err := s.client.CreateDeployWithResponse(ctx, serviceId, client.CreateDeployJSONRequestBody{
		ClearCache: nil,
		CommitId:   nil,
		ImageUrl:   nil,
	})
	if err != nil {
		return nil, err
	}

	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// CreateService creates a new service, validating that the requested OwnerId
// matches the current workspace via validate.WorkspaceMatches.
func (s *Repo) CreateService(ctx context.Context, data client.CreateServiceJSONRequestBody) (*client.ServiceAndDeploy, error) {
	if err := validate.WorkspaceMatches(ctx, data.OwnerId); err != nil {
		return nil, err
	}

	resp, err := s.client.CreateServiceWithResponse(ctx, data)
	if err != nil {
		return nil, err
	}

	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}

	return resp.JSON201, nil
}

// GetService retrieves a single service by its ID.
func (s *Repo) GetService(ctx context.Context, id string) (*client.Service, error) {
	resp, err := s.client.RetrieveServiceWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := client.ErrorFromResponse(resp); err != nil {
		return nil, err
	}

	return resp.JSON200, nil
}
